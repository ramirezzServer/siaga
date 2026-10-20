package firecluster

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
)

// StaticRule menentukan sumber panas tetap dari arsip: sel grid yang
// menyala di banyak hari, bertahun-tahun, dan di banyak bulan kalender
// (kebakaran vegetasi Jawa Barat musiman, Juli–Oktober), atau yang ditandai
// FIRMS sebagai gunung api/sumber tetap (kolom type arsip SP). Sel kandidat
// yang bersebelahan digabung menjadi satu sumber.
type StaticRule struct {
	// CellDeg adalah ukuran sel grid dalam derajat.
	CellDeg float64
	// Kriteria persisten: hari UTC berbeda, tahun berbeda, dan bulan
	// kalender berbeda paling sedikit sebanyak ini, dan bagian deteksi malam
	// paling sedikit MinNightShare. Pembakaran jerami dan tebu di dataran
	// rendah juga berulang tiap tahun di banyak bulan, tetapi hampir selalu
	// siang; nyala industri dan suar gas terlihat siang dan malam.
	MinDays, MinYears, MinMonths int
	MinNightShare                float64
	// Kriteria FIRMS: paling sedikit MinTyped deteksi SP di sel dan bagian
	// type 1/2 di antaranya paling sedikit TypedShare.
	MinTyped   int
	TypedShare float64
	// MarginKm ditambahkan ke jarak anggota terjauh dari pusat sumber
	// (setengah piksel MODIS); MinRadiusKm adalah jari-jari terkecil.
	MarginKm, MinRadiusKm float64
}

// DefaultStaticRule adalah titik awal kalibrasi (ADR 0023).
var DefaultStaticRule = StaticRule{
	CellDeg: 0.005, MinDays: 12, MinYears: 3, MinMonths: 6, MinNightShare: 0.2,
	MinTyped: 3, TypedShare: 0.5, MarginKm: 0.5, MinRadiusKm: 0.75,
}

// Validate memeriksa batas parameter.
func (r StaticRule) Validate() error {
	if !(r.CellDeg >= 0.001 && r.CellDeg <= 0.1) || r.MinDays < 1 || r.MinYears < 1 || r.MinMonths < 1 || r.MinMonths > 12 ||
		!(r.MinNightShare >= 0 && r.MinNightShare <= 1) ||
		r.MinTyped < 1 || !(r.TypedShare > 0 && r.TypedShare <= 1) || !(r.MarginKm >= 0 && r.MarginKm <= 5) ||
		!(r.MinRadiusKm > 0 && r.MinRadiusKm <= 10) {
		return fmt.Errorf("%w: %+v", ErrRule, r)
	}
	return nil
}

// Reason adalah alasan sumber dianggap tetap.
type Reason string

// Alasan sumber tetap.
const (
	ReasonPersistent Reason = "persisten"
	ReasonFIRMS      Reason = "type-firms"
	ReasonBoth       Reason = "keduanya"
)

// Source adalah satu sumber panas tetap.
type Source struct {
	Lat, Lon   float64
	RadiusKm   float64
	Reason     Reason
	Cells      int
	Detections int
	Days       int
	Years      int
	Months     int
	// Typed adalah jumlah deteksi per kolom type (indeks fire.SourceType 0..3).
	Typed       [4]int
	Night       int
	MeanFRPMW   float64
	First, Last time.Time
}

// ID stabil dari koordinat pusat, misal "-6.38510,108.37020".
func (s Source) ID() string { return fmt.Sprintf("%.5f,%.5f", s.Lat, s.Lon) }

type cellKey struct{ lat, lon int }

type cellStat struct {
	points  []int
	night   int
	days    map[time.Time]bool
	years   map[int]bool
	months  map[time.Month]bool
	typed   [4]int
	special int
}

// block menilai sel k bersama 8 tetangganya: geolokasi piksel satu sumber
// tetap bergeser ratusan meter antar-lintasan, jadi deteksinya tersebar di
// beberapa sel yang masing-masing belum memenuhi kriteria.
func block(cells map[cellKey]*cellStat, k cellKey, r StaticRule) (persistent, flagged bool) {
	days, years, months := map[time.Time]bool{}, map[int]bool{}, map[time.Month]bool{}
	var typed, special, n, night int
	for dl := -1; dl <= 1; dl++ {
		for dn := -1; dn <= 1; dn++ {
			c := cells[cellKey{k.lat + dl, k.lon + dn}]
			if c == nil {
				continue
			}
			for d := range c.days {
				days[d] = true
			}
			for y := range c.years {
				years[y] = true
			}
			for m := range c.months {
				months[m] = true
			}
			typed += c.typed[0] + c.typed[1] + c.typed[2] + c.typed[3]
			special += c.special
			n += len(c.points)
			night += c.night
		}
	}
	persistent = len(days) >= r.MinDays && len(years) >= r.MinYears && len(months) >= r.MinMonths &&
		float64(night) >= r.MinNightShare*float64(n)
	flagged = typed >= r.MinTyped && float64(special) >= r.TypedShare*float64(typed)
	return persistent, flagged
}

// FindStatic mencari sumber panas tetap di ps. Hasil urut jumlah deteksi
// terbanyak, lalu ID.
func FindStatic(ps []Point, r StaticRule) ([]Source, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	cells := map[cellKey]*cellStat{}
	for i, p := range ps {
		k := cellKey{int(math.Floor(p.Lat / r.CellDeg)), int(math.Floor(p.Lon / r.CellDeg))}
		c := cells[k]
		if c == nil {
			c = &cellStat{days: map[time.Time]bool{}, years: map[int]bool{}, months: map[time.Month]bool{}}
			cells[k] = c
		}
		c.points = append(c.points, i)
		if !p.Daytime {
			c.night++
		}
		day := p.At.UTC().Truncate(24 * time.Hour)
		c.days[day], c.years[day.Year()], c.months[day.Month()] = true, true, true
		if p.Type >= fire.VegetationFire && p.Type <= fire.Offshore {
			c.typed[p.Type]++
			if p.Type == fire.ActiveVolcano || p.Type == fire.StaticSource {
				c.special++
			}
		}
	}
	type flags struct{ persistent, flagged bool }
	candidate := map[cellKey]flags{}
	for k := range cells {
		if p, f := block(cells, k, r); p || f {
			candidate[k] = flags{p, f}
		}
	}
	// Gabungkan kandidat yang bersebelahan (8 tetangga), urutan kunci tetap.
	keys := make([]cellKey, 0, len(candidate))
	for k := range candidate {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b cellKey) int { return cmp.Or(cmp.Compare(a.lat, b.lat), cmp.Compare(a.lon, b.lon)) })
	seen := map[cellKey]bool{}
	var out []Source
	for _, start := range keys {
		if seen[start] {
			continue
		}
		var group []cellKey
		stack := []cellKey{start}
		seen[start] = true
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			group = append(group, k)
			for dl := -1; dl <= 1; dl++ {
				for dn := -1; dn <= 1; dn++ {
					n := cellKey{k.lat + dl, k.lon + dn}
					if _, ok := candidate[n]; ok && !seen[n] {
						seen[n] = true
						stack = append(stack, n)
					}
				}
			}
		}
		var persistent, flagged bool
		for _, k := range group {
			persistent = persistent || candidate[k].persistent
			flagged = flagged || candidate[k].flagged
		}
		out = append(out, source(ps, cells, group, persistent, flagged, r))
	}
	slices.SortFunc(out, func(a, b Source) int {
		return cmp.Or(cmp.Compare(b.Detections, a.Detections), cmp.Compare(a.ID(), b.ID()))
	})
	return out, nil
}

func source(ps []Point, cells map[cellKey]*cellStat, group []cellKey, persistent, flagged bool, r StaticRule) Source {
	s := Source{Cells: len(group)}
	days, years, months := map[time.Time]bool{}, map[int]bool{}, map[time.Month]bool{}
	var members []int
	for _, k := range group {
		c := cells[k]
		members = append(members, c.points...)
		for d := range c.days {
			days[d] = true
		}
		for y := range c.years {
			years[y] = true
		}
		for m := range c.months {
			months[m] = true
		}
		for t, n := range c.typed {
			s.Typed[t] += n
		}
	}
	var sumLat, sumLon, sumFRP float64
	for _, i := range members {
		p := ps[i]
		sumLat += p.Lat
		sumLon += p.Lon
		sumFRP += p.FRPMW
		if !p.Daytime {
			s.Night++
		}
		if s.First.IsZero() || p.At.Before(s.First) {
			s.First = p.At
		}
		if p.At.After(s.Last) {
			s.Last = p.At
		}
	}
	n := float64(len(members))
	s.Detections = len(members)
	s.Lat, s.Lon = round5(sumLat/n), round5(sumLon/n)
	s.MeanFRPMW = math.Round(sumFRP/n*10) / 10
	far := 0.0
	for _, i := range members {
		far = math.Max(far, DistanceKm(s.Lat, s.Lon, ps[i].Lat, ps[i].Lon))
	}
	s.RadiusKm = math.Max(r.MinRadiusKm, math.Ceil((far+r.MarginKm)*100)/100)
	s.Days, s.Years, s.Months = len(days), len(years), len(months)
	switch {
	case persistent && flagged:
		s.Reason = ReasonBoth
	case flagged:
		s.Reason = ReasonFIRMS
	default:
		s.Reason = ReasonPersistent
	}
	return s
}

func round5(v float64) float64 {
	r := math.Round(v*1e5) / 1e5
	if r == 0 {
		return 0
	}
	return r
}

// Mask adalah daftar sumber panas tetap untuk menyaring deteksi.
type Mask []Source

// Match mengembalikan sumber pertama yang jari-jarinya memuat (lat, lon).
func (m Mask) Match(lat, lon float64) (Source, bool) {
	for _, s := range m {
		if DistanceKm(lat, lon, s.Lat, s.Lon) <= s.RadiusKm {
			return s, true
		}
	}
	return Source{}, false
}
