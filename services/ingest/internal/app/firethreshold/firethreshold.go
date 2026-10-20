// Package firethreshold menganalisis arsip titik api FIRMS untuk kalibrasi
// hazard.fire.* (ADR 0023, make fire-threshold): inventaris deteksi per
// wahana, sumber panas tetap yang harus disaring, jumlah kejadian per tahun
// menurut aturan tingkat PRD dan variannya, dan uji terhadap kebakaran
// tercatat di berita.
package firethreshold

import (
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/firecluster"
)

// WIB dipakai untuk tanggal kebakaran tercatat dan tampilan laporan.
var WIB = time.FixedZone("WIB", 7*3600)

// RecordedFire adalah kebakaran yang tercatat di berita, untuk uji.
type RecordedFire struct {
	ID string
	// From dan To adalah tanggal WIB inklusif.
	From, To time.Time
	Lat, Lon float64
	RadiusKm float64
	// AreaHa nol bila luas tidak diketahui.
	AreaHa float64
	Source string
	Note   string
}

// window adalah rentang waktu UTC uji: sehari sebelum From sampai dua hari
// setelah To (WIB), karena tanggal berita sering tanggal laporan, bukan
// awal api.
func (f RecordedFire) window() (time.Time, time.Time) {
	from := time.Date(f.From.Year(), f.From.Month(), f.From.Day(), 0, 0, 0, 0, WIB).AddDate(0, 0, -1)
	to := time.Date(f.To.Year(), f.To.Month(), f.To.Day(), 0, 0, 0, 0, WIB).AddDate(0, 0, 3)
	return from.UTC(), to.UTC()
}

// recordedColumns adalah header kebakaran-tercatat.csv.
var recordedColumns = []string{"id", "mulai", "selesai", "lintang", "bujur", "radius_km", "luas_ha", "sumber", "catatan"}

// ReadRecorded membaca docs/calibration/kebakaran-tercatat.csv (baris
// berawalan # adalah catatan).
func ReadRecorded(r io.Reader) ([]RecordedFire, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.FieldsPerRecord = len(recordedColumns)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("kebakaran tercatat: %w", err)
	}
	if !slices.Equal(header, recordedColumns) {
		return nil, fmt.Errorf("kebakaran tercatat: header %v, ingin %v", header, recordedColumns)
	}
	var out []RecordedFire
	ids := map[string]bool{}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("kebakaran tercatat: %w", err)
		}
		f, err := parseRecorded(rec)
		if err == nil && ids[f.ID] {
			err = errors.New("id ganda")
		}
		if err != nil {
			return nil, fmt.Errorf("kebakaran tercatat baris %d: %w", line, err)
		}
		ids[f.ID] = true
		out = append(out, f)
	}
	return out, nil
}

func parseRecorded(rec []string) (RecordedFire, error) {
	f := RecordedFire{ID: strings.TrimSpace(rec[0]), Source: strings.TrimSpace(rec[7]), Note: strings.TrimSpace(rec[8])}
	var errs []error
	date := func(s string) time.Time {
		t, err := time.Parse(time.DateOnly, strings.TrimSpace(s))
		errs = append(errs, err)
		return t
	}
	num := func(s string) float64 {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0
		}
		v, err := strconv.ParseFloat(s, 64)
		errs = append(errs, err)
		return v
	}
	f.From, f.To = date(rec[1]), date(rec[2])
	f.Lat, f.Lon, f.RadiusKm, f.AreaHa = num(rec[3]), num(rec[4]), num(rec[5]), num(rec[6])
	if err := errors.Join(errs...); err != nil {
		return f, err
	}
	switch {
	case f.ID == "" || strings.ContainsAny(f.ID, " ,"):
		return f, fmt.Errorf("id %q kosong atau berisi spasi/koma", f.ID)
	case f.To.Before(f.From):
		return f, errors.New("selesai sebelum mulai")
	case !(f.Lat >= -8.5 && f.Lat <= -5.5 && f.Lon >= 105.5 && f.Lon <= 109.5):
		return f, fmt.Errorf("lokasi (%v, %v) di luar Jawa Barat", f.Lat, f.Lon)
	case !(f.RadiusKm > 0 && f.RadiusKm <= 20):
		return f, fmt.Errorf("radius %v km harus 0..20", f.RadiusKm)
	case f.AreaHa < 0:
		return f, errors.New("luas negatif")
	case !strings.HasPrefix(f.Source, "https://"):
		return f, fmt.Errorf("sumber %q harus URL https", f.Source)
	}
	return f, nil
}

// Variant adalah satu cara menilai kejadian yang dibandingkan di laporan.
type Variant struct {
	Name string
	// Exclude berisi nama wahana yang dibuang sebelum dikelompokkan (misal
	// "S-NPP", yang berhenti 2 Nov 2026).
	Exclude []string
	// NoMask true: sumber panas tetap tidak disaring.
	NoMask  bool
	Cluster firecluster.ClusterRule
	Level   firecluster.LevelRule
}

// DefaultVariants mengembalikan aturan PRD apa adanya dan varian yang menguji tiap
// pilihannya.
func DefaultVariants() []Variant {
	nominal := firecluster.PRDRule
	nominal.Name, nominal.WaspadaMin = "Waspada nominal", fire.Nominal
	passes := firecluster.LevelRule{Name: "lintasan", SiagaCount: 3, Unit: firecluster.Overpasses, WaspadaMin: fire.Nominal, Window: 24 * time.Hour}
	return []Variant{
		{Name: "PRD", Cluster: firecluster.DefaultClusterRule, Level: firecluster.PRDRule},
		{Name: "PRD tanpa S-NPP", Exclude: []string{"S-NPP"}, Cluster: firecluster.DefaultClusterRule, Level: firecluster.PRDRule},
		{Name: "PRD tanpa saringan", NoMask: true, Cluster: firecluster.DefaultClusterRule, Level: firecluster.PRDRule},
		{Name: "Waspada nominal", Cluster: firecluster.DefaultClusterRule, Level: nominal},
		{Name: "Waspada nominal, Siaga ≥ 3 lintasan, tanpa S-NPP", Exclude: []string{"S-NPP"}, Cluster: firecluster.DefaultClusterRule, Level: passes},
	}
}

// InventoryRow adalah jumlah deteksi satu wahana satu tahun.
type InventoryRow struct {
	Satellite  string
	Year       int
	Detections int
	// ByConfidence diindeks fire.Low..fire.High (indeks 0 tidak dipakai).
	ByConfidence [4]int
	Night        int
	// Typed diindeks fire.SourceType 0..3; Untyped untuk NRT.
	Typed   [4]int
	Untyped int
}

// Event adalah satu kejadian hasil pengelompokan beserta tingkatnya.
type Event struct {
	firecluster.Outcome
	Lat, Lon   float64
	Satellites []string
	// members adalah indeks anggota di VariantResult.points.
	members []int
}

// VariantResult adalah hasil satu varian.
type VariantResult struct {
	Variant Variant
	// Points adalah deteksi yang dinilai (setelah buang wahana dan saringan).
	Points int
	// ByYear: jumlah kejadian per tingkat tertinggi (indeks firecluster.Level).
	ByYear map[int][4]int
	Events []Event
	points []firecluster.Point
}

// Total menjumlahkan kejadian per tingkat semua tahun.
func (v VariantResult) Total() [4]int {
	var t [4]int
	for _, y := range v.ByYear {
		for l := range t {
			t[l] += y[l]
		}
	}
	return t
}

// Top mengembalikan n kejadian dengan puncak terbesar.
func (v VariantResult) Top(n int) []Event {
	out := slices.Clone(v.Events)
	slices.SortFunc(out, func(a, b Event) int {
		return cmp.Or(cmp.Compare(b.Peak, a.Peak), cmp.Compare(b.Detections, a.Detections), a.Start.Compare(b.Start))
	})
	return out[:min(n, len(out))]
}

// CheckResult adalah hasil uji satu kebakaran tercatat di satu varian.
type CheckResult struct {
	Found bool
	Level firecluster.Level
	// Detections di dalam radius dan jendela uji.
	Detections int
	FirstAt    time.Time
	// Masked adalah deteksi di radius dan jendela yang tersaring sumber tetap.
	Masked int
}

// Check adalah uji satu kebakaran tercatat di semua varian.
type Check struct {
	Fire    RecordedFire
	Results []CheckResult
}

// Result adalah hasil Analyze.
type Result struct {
	From, To   time.Time
	Inventory  []InventoryRow
	StaticRule firecluster.StaticRule
	Static     []firecluster.Source
	// Masked adalah deteksi di dalam sumber panas tetap.
	Masked int
	// Monthly adalah deteksi bukan sumber tetap per bulan kalender (WIB).
	Monthly  [12]int
	Variants []VariantResult
	Checks   []Check
}

// Analyze menjalankan semua analisis atas ps (boleh tidak urut).
func Analyze(ps []firecluster.Point, fires []RecordedFire, rule firecluster.StaticRule, variants []Variant) (Result, error) {
	ps = slices.Clone(ps)
	firecluster.SortPoints(ps)
	ps = slices.CompactFunc(ps, func(a, b firecluster.Point) bool { return a.ID == b.ID })
	if len(ps) == 0 {
		return Result{}, errors.New("arsip titik api kosong")
	}
	res := Result{From: ps[0].At, To: ps[len(ps)-1].At, StaticRule: rule, Inventory: inventory(ps)}
	static, err := firecluster.FindStatic(ps, rule)
	if err != nil {
		return res, err
	}
	res.Static = static
	mask := firecluster.Mask(static)
	masked := make([]bool, len(ps))
	for i, p := range ps {
		if _, ok := mask.Match(p.Lat, p.Lon); ok {
			masked[i] = true
			res.Masked++
			continue
		}
		res.Monthly[p.At.In(WIB).Month()-1]++
	}
	for _, v := range variants {
		vr, err := runVariant(ps, masked, v)
		if err != nil {
			return res, fmt.Errorf("varian %s: %w", v.Name, err)
		}
		res.Variants = append(res.Variants, vr)
	}
	for _, f := range fires {
		res.Checks = append(res.Checks, check(ps, masked, f, res.Variants))
	}
	return res, nil
}

func inventory(ps []firecluster.Point) []InventoryRow {
	type key struct {
		sat  string
		year int
	}
	rows := map[key]*InventoryRow{}
	for _, p := range ps {
		k := key{p.Satellite, p.At.Year()}
		r := rows[k]
		if r == nil {
			r = &InventoryRow{Satellite: k.sat, Year: k.year}
			rows[k] = r
		}
		r.Detections++
		if p.Confidence >= fire.Low && p.Confidence <= fire.High {
			r.ByConfidence[p.Confidence]++
		}
		if !p.Daytime {
			r.Night++
		}
		if p.Type >= fire.VegetationFire && p.Type <= fire.Offshore {
			r.Typed[p.Type]++
		} else {
			r.Untyped++
		}
	}
	order := map[string]int{}
	for i, s := range fire.Satellites {
		order[s.Name] = i
	}
	out := make([]InventoryRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b InventoryRow) int {
		return cmp.Or(cmp.Compare(order[a.Satellite], order[b.Satellite]), cmp.Compare(a.Satellite, b.Satellite), cmp.Compare(a.Year, b.Year))
	})
	return out
}

// selected mengembalikan deteksi yang dinilai varian v (urut waktu).
func selected(ps []firecluster.Point, masked []bool, v Variant) []firecluster.Point {
	var out []firecluster.Point
	for i, p := range ps {
		if (masked[i] && !v.NoMask) || slices.Contains(v.Exclude, p.Satellite) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func runVariant(ps []firecluster.Point, masked []bool, v Variant) (VariantResult, error) {
	sel := selected(ps, masked, v)
	vr := VariantResult{Variant: v, Points: len(sel), ByYear: map[int][4]int{}, points: sel}
	groups, err := firecluster.Cluster(sel, v.Cluster)
	if err != nil {
		return vr, err
	}
	for _, idx := range groups {
		members := make([]firecluster.Point, len(idx))
		sats := map[string]bool{}
		var sumLat, sumLon float64
		for j, i := range idx {
			members[j] = sel[i]
			sats[sel[i].Satellite] = true
			sumLat += sel[i].Lat
			sumLon += sel[i].Lon
		}
		o, err := firecluster.Evaluate(members, v.Level)
		if err != nil {
			return vr, err
		}
		n := float64(len(idx))
		ev := Event{
			Outcome: o, Lat: math.Round(sumLat/n*1e4) / 1e4, Lon: math.Round(sumLon/n*1e4) / 1e4,
			Satellites: slices.Sorted(maps.Keys(sats)), members: idx,
		}
		vr.Events = append(vr.Events, ev)
		y := vr.ByYear[o.Start.In(WIB).Year()]
		y[o.Level]++
		vr.ByYear[o.Start.In(WIB).Year()] = y
	}
	return vr, nil
}

// check menguji satu kebakaran tercatat: kejadian yang punya anggota di
// dalam radius dan jendela waktu, dan tingkat tertinggi kejadian itu.
func check(ps []firecluster.Point, masked []bool, f RecordedFire, variants []VariantResult) Check {
	from, to := f.window()
	c := Check{Fire: f}
	inside := func(p firecluster.Point) bool {
		return !p.At.Before(from) && p.At.Before(to) && firecluster.DistanceKm(p.Lat, p.Lon, f.Lat, f.Lon) <= f.RadiusKm
	}
	for _, vr := range variants {
		var r CheckResult
		for i, p := range ps {
			if masked[i] && !vr.Variant.NoMask && !slices.Contains(vr.Variant.Exclude, p.Satellite) && inside(p) {
				r.Masked++
			}
		}
		for _, ev := range vr.Events {
			if ev.End.Before(from) || !ev.Start.Before(to) {
				continue
			}
			n := 0
			for _, i := range ev.members {
				p := vr.points[i]
				if !inside(p) {
					continue
				}
				n++
				if r.FirstAt.IsZero() || p.At.Before(r.FirstAt) {
					r.FirstAt = p.At
				}
			}
			if n > 0 {
				r.Found = true
				r.Detections += n
				r.Level = max(r.Level, ev.Level)
			}
		}
		c.Results = append(c.Results, r)
	}
	return c
}
