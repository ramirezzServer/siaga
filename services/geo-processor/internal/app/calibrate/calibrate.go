// Package calibrate mengukur ambang deduplikasi gempa terhadap katalog
// historis BMKG dan USGS, memakai fungsi domain yang sama dengan layanan
// (quake.Rule.Match), sehingga angka laporan kalibrasi berasal dari kode yang
// benar-benar berjalan.
//
// Metode (lihat docs/calibration/dedup-gempa.md):
//   - Recall: untuk setiap gempa USGS yang punya pasangan BMKG di katalog
//     (kandidat dalam jendela longgar 300 dtk / 300 km), apakah ambang
//     menemukan setidaknya satu gempa BMKG.
//   - Gabungan salah: waktu semua gempa USGS digeser (±1–13 jam untuk laju
//     kebetulan, ±2–10 menit untuk gempa susulan yang berdekatan); setiap
//     kecocokan setelah digeser pasti salah.
//   - Pasangan sumber sama: dua gempa berbeda dari satu katalog yang lolos
//     ambang revisi (akan dianggap revisi satu kejadian).
package calibrate

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Event adalah satu gempa di katalog.
type Event = ports.CatalogEvent

// Box adalah kotak lintang-bujur.
type Box struct{ MinLat, MaxLat, MinLon, MaxLon float64 }

// Contains melaporkan apakah titik ada di dalam kotak.
func (b Box) Contains(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}

// Filter menyaring katalog ke kotak dan rentang waktu [from, to), membuang
// baris kembar (katalog BMKG kadang memuat solusi yang sama dua kali), lalu
// mengurutkan menurut waktu.
func Filter(events []Event, box Box, from, to time.Time) []Event {
	var out []Event
	for _, e := range events {
		if box.Contains(e.Latitude, e.Longitude) && !e.OccurredAt.Before(from) && e.OccurredAt.Before(to) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Event) int {
		if c := a.OccurredAt.Compare(b.OccurredAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return slices.CompactFunc(out, func(a, b Event) bool { return a.Origin == b.Origin })
}

// Candidate adalah jendela longgar untuk menentukan gempa USGS mana yang
// punya pasangan BMKG di katalog. Gempa di luar jendela ini dianggap tidak
// tercatat BMKG (celah katalog), bukan kegagalan ambang.
var Candidate = quake.Rule{MaxTimeDelta: 300 * time.Second, MaxDistanceKm: 300, MaxMagnitudeDelta: 3}

// Offsets adalah persentil selisih pasangan terbaik BMKG–USGS.
type Offsets struct {
	Seconds, Km, Magnitude Percentiles
}

// Percentiles merangkum sebaran.
type Percentiles struct{ P50, P90, P95, P99, Max float64 }

// RuleResult adalah hasil satu ambang.
type RuleResult struct {
	Rule quake.Rule
	// Matched adalah jumlah gempa USGS berkandidat yang cocok dengan BMKG.
	Matched int
	// Recall = Matched / Report.WithCandidate.
	Recall float64
	// FalsePerThousandHours adalah kecocokan per 1.000 gempa USGS setelah
	// waktunya digeser ±1–13 jam (kebetulan murni).
	FalsePerThousandHours float64
	// FalsePerThousandMinutes sama, dengan geseran ±2–10 menit (gempa susulan).
	FalsePerThousandMinutes float64
	// SameSourceBMKG dan SameSourceUSGS adalah pasangan gempa berbeda di satu
	// katalog yang lolos ambang ini.
	SameSourceBMKG int
	SameSourceUSGS int
}

// Report adalah hasil kalibrasi.
type Report struct {
	BMKG, USGS    int
	WithCandidate int
	Offsets       Offsets
	Rules         []RuleResult
	// Misses adalah contoh gempa USGS berkandidat yang tidak cocok dengan
	// ambang pertama, beserta selisih ke kandidat terbaiknya.
	Misses []Miss
}

// Miss adalah satu gempa yang terlewat ambang.
type Miss struct {
	USGS              Event
	Seconds, Km, DMag float64
}

// HourShifts dan MinuteShifts adalah geseran waktu untuk uji gabungan salah.
var (
	HourShifts   = shifts(time.Hour, 1, 2, 3, 5, 8, 13)
	MinuteShifts = shifts(time.Minute, 2, 3, 5, 7, 10)
)

func shifts(unit time.Duration, ks ...int) []time.Duration {
	var out []time.Duration
	for _, k := range ks {
		out = append(out, time.Duration(k)*unit, -time.Duration(k)*unit)
	}
	return out
}

// index mencari kejadian dalam rentang waktu dengan pencarian biner.
type index []Event

func (ix index) within(t time.Time, d time.Duration) []Event {
	lo := sort.Search(len(ix), func(i int) bool { return !ix[i].OccurredAt.Before(t.Add(-d)) })
	hi := sort.Search(len(ix), func(i int) bool { return ix[i].OccurredAt.After(t.Add(d)) })
	return ix[lo:hi]
}

func (ix index) any(o quake.Origin, r quake.Rule) bool {
	for _, e := range ix.within(o.OccurredAt, r.MaxTimeDelta) {
		if ok, _ := r.Match(o, e.Origin); ok {
			return true
		}
	}
	return false
}

// best mengembalikan kandidat dengan skor terbaik menurut rule.
func (ix index) best(o quake.Origin, r quake.Rule) (Event, bool) {
	var best Event
	bestScore := math.Inf(1)
	for _, e := range ix.within(o.OccurredAt, r.MaxTimeDelta) {
		if ok, s := r.Match(o, e.Origin); ok && s < bestScore {
			best, bestScore = e, s
		}
	}
	return best, !math.IsInf(bestScore, 1)
}

// Evaluate menjalankan kalibrasi untuk setiap ambang. bmkg dan usgs harus
// sudah difilter (Filter). maxMisses membatasi contoh gempa yang terlewat
// ambang pertama.
func Evaluate(bmkg, usgs []Event, rules []quake.Rule, maxMisses int) Report {
	ix := index(bmkg)
	rep := Report{BMKG: len(bmkg), USGS: len(usgs)}

	var withCand []Event
	var dts, kms, dms []float64
	for _, u := range usgs {
		b, ok := ix.best(u.Origin, Candidate)
		if !ok {
			continue
		}
		withCand = append(withCand, u)
		dt, km, dm := deltas(u.Origin, b.Origin)
		dts, kms, dms = append(dts, dt), append(kms, km), append(dms, dm)
	}
	rep.WithCandidate = len(withCand)
	rep.Offsets = Offsets{Seconds: percentiles(dts), Km: percentiles(kms), Magnitude: percentiles(dms)}

	for i, r := range rules {
		res := RuleResult{Rule: r}
		for _, u := range withCand {
			if ix.any(u.Origin, r) {
				res.Matched++
				continue
			}
			if i == 0 && len(rep.Misses) < maxMisses {
				b, _ := ix.best(u.Origin, Candidate)
				dt, km, dm := deltas(u.Origin, b.Origin)
				rep.Misses = append(rep.Misses, Miss{USGS: u, Seconds: dt, Km: km, DMag: dm})
			}
		}
		if len(withCand) > 0 {
			res.Recall = float64(res.Matched) / float64(len(withCand))
		}
		res.FalsePerThousandHours = falseRate(ix, usgs, r, HourShifts)
		res.FalsePerThousandMinutes = falseRate(ix, usgs, r, MinuteShifts)
		res.SameSourceBMKG = sameSourcePairs(bmkg, r)
		res.SameSourceUSGS = sameSourcePairs(usgs, r)
		rep.Rules = append(rep.Rules, res)
	}
	return rep
}

func deltas(a, b quake.Origin) (seconds, km, dmag float64) {
	return a.OccurredAt.Sub(b.OccurredAt).Abs().Seconds(),
		quake.DistanceKm(a.Latitude, a.Longitude, b.Latitude, b.Longitude),
		math.Abs(a.Magnitude - b.Magnitude)
}

func falseRate(ix index, usgs []Event, r quake.Rule, shifts []time.Duration) float64 {
	if len(usgs) == 0 || len(shifts) == 0 {
		return 0
	}
	n := 0
	for _, s := range shifts {
		for _, u := range usgs {
			o := u.Origin
			o.OccurredAt = o.OccurredAt.Add(s)
			if ix.any(o, r) {
				n++
			}
		}
	}
	return float64(n) / float64(len(shifts)) / float64(len(usgs)) * 1000
}

func sameSourcePairs(events []Event, r quake.Rule) int {
	n := 0
	for i, a := range events {
		for _, b := range events[i+1:] {
			if b.OccurredAt.Sub(a.OccurredAt) > r.MaxTimeDelta {
				break
			}
			if ok, _ := r.Match(a.Origin, b.Origin); ok {
				n++
			}
		}
	}
	return n
}

// percentiles memakai metode nearest-rank.
func percentiles(xs []float64) Percentiles {
	if len(xs) == 0 {
		return Percentiles{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	at := func(q float64) float64 {
		i := int(math.Ceil(q*float64(len(s)))) - 1
		return s[min(max(i, 0), len(s)-1)]
	}
	return Percentiles{P50: at(0.5), P90: at(0.9), P95: at(0.95), P99: at(0.99), Max: s[len(s)-1]}
}

// Meta adalah keterangan asal data untuk laporan.
type Meta struct {
	Box         Box
	From, To    time.Time
	BMKGSources []string
	USGSSources []string
	Command     string
}

// Markdown menulis laporan kalibrasi sebagai Markdown berbahasa Indonesia.
func Markdown(rep Report, m Meta) string {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	f("# Kalibrasi deduplikasi gempa BMKG–USGS\n\n")
	f("Dihasilkan otomatis oleh `%s`. Jangan diedit manual; jalankan ulang perintahnya.\n\n", m.Command)
	f("## Data\n\n")
	f("| | |\n| --- | --- |\n")
	f("| Wilayah | lintang %s..%s, bujur %s..%s (Jawa Barat dan laut sekitarnya) |\n",
		num(m.Box.MinLat, 2), num(m.Box.MaxLat, 2), num(m.Box.MinLon, 2), num(m.Box.MaxLon, 2))
	f("| Periode | %s s.d. %s (UTC) |\n", m.From.Format("2006-01-02"), m.To.Format("2006-01-02"))
	f("| Gempa BMKG | %d |\n| Gempa USGS | %d |\n", rep.BMKG, rep.USGS)
	f("| USGS dengan kandidat BMKG (≤ %s, ≤ %s km) | %d |\n\n",
		Candidate.MaxTimeDelta, num(Candidate.MaxDistanceKm, 0), rep.WithCandidate)
	f("Sumber BMKG: %s. Sumber USGS: %s.\n\n", strings.Join(m.BMKGSources, ", "), strings.Join(m.USGSSources, ", "))

	f("## Selisih pasangan terbaik BMKG–USGS\n\n")
	f("| Selisih | p50 | p90 | p95 | p99 | maks |\n| --- | --- | --- | --- | --- | --- |\n")
	row := func(name string, p Percentiles, digits int) {
		f("| %s | %s | %s | %s | %s | %s |\n", name, num(p.P50, digits), num(p.P90, digits), num(p.P95, digits), num(p.P99, digits), num(p.Max, digits))
	}
	row("Waktu (detik)", rep.Offsets.Seconds, 1)
	row("Jarak episenter (km)", rep.Offsets.Km, 1)
	row("Magnitudo", rep.Offsets.Magnitude, 2)
	f("\n## Ambang\n\n")
	f("Recall dihitung atas gempa USGS yang punya kandidat BMKG. Gabungan salah adalah kecocokan per 1.000 gempa USGS setelah waktunya digeser: ±1–13 jam mengukur kebetulan murni, ±2–10 menit mengukur gempa susulan yang berdekatan (paling berisiko). Pasangan sumber sama adalah dua gempa berbeda di satu katalog yang lolos ambang.\n\n")
	f("| Ambang (waktu, jarak, magnitudo) | Recall | Gabungan salah /1.000 (jam) | Gabungan salah /1.000 (menit) | Pasangan BMKG–BMKG | Pasangan USGS–USGS |\n")
	f("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rep.Rules {
		f("| %s, %s km, %s | %s%% (%d/%d) | %s | %s | %d | %d |\n",
			r.Rule.MaxTimeDelta, num(r.Rule.MaxDistanceKm, 0), num(r.Rule.MaxMagnitudeDelta, 1),
			num(r.Recall*100, 1), r.Matched, rep.WithCandidate,
			num(r.FalsePerThousandHours, 2), num(r.FalsePerThousandMinutes, 2), r.SameSourceBMKG, r.SameSourceUSGS)
	}
	if len(rep.Misses) > 0 && len(rep.Rules) > 0 {
		r0 := rep.Rules[0].Rule
		f("\n## Contoh gempa yang terlewat ambang %s, %s km, %s\n\n", r0.MaxTimeDelta, num(r0.MaxDistanceKm, 0), num(r0.MaxMagnitudeDelta, 1))
		f("| USGS | Waktu (UTC) | M | Selisih waktu (dtk) | Jarak (km) | Selisih M |\n| --- | --- | --- | --- | --- | --- |\n")
		for _, mi := range rep.Misses {
			f("| %s | %s | %s | %s | %s | %s |\n", mi.USGS.ID, mi.USGS.OccurredAt.Format("2006-01-02 15:04:05"),
				num(mi.USGS.Magnitude, 1), num(mi.Seconds, 1), num(mi.Km, 1), num(mi.DMag, 2))
		}
	}
	return b.String()
}

// num menulis angka gaya Indonesia (koma desimal).
func num(v float64, digits int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', digits, 64), ".", ",", 1)
}
