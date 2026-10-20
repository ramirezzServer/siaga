// Package firecluster berisi aturan murni kalibrasi titik api (ADR 0023):
// sumber panas tetap dari arsip FIRMS (industri, panas bumi, kawah) yang
// harus disaring, pengelompokan deteksi menjadi kejadian kebakaran, dan
// tingkat peringatan kejadian menurut aturan PRD beserta variannya.
//
// Paket ini dipakai alat kalibrasi ingest (make fire-threshold). Aturan
// yang terpilih disalin ke geo-processor saat hazard.fire.* dibuat, dengan
// test yang memastikan hasilnya sama.
package firecluster

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
)

// EarthRadiusKm adalah jari-jari bumi rata-rata (IUGG).
const EarthRadiusKm = 6371.0088

// DistanceKm adalah jarak lingkaran besar (haversine) dua titik.
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * EarthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// kmPerDegLat adalah panjang satu derajat lintang (untuk saringan kasar).
const kmPerDegLat = EarthRadiusKm * math.Pi / 180

// Point adalah satu deteksi titik panas untuk kalibrasi.
type Point struct {
	ID string
	// Satellite adalah nama wahana (fire.SatelliteOf), misal "S-NPP".
	Satellite  string
	Instrument fire.Instrument
	Lat, Lon   float64
	At         time.Time
	Confidence fire.Confidence
	Type       fire.SourceType
	FRPMW      float64
	Daytime    bool
}

// FromArchived mengubah deteksi arsip menjadi Point.
func FromArchived(a fire.Archived) Point {
	p := Point{
		ID: a.ID(), Satellite: fire.SatelliteOf(a.Product), Instrument: a.Instrument,
		Lat: a.Lat, Lon: a.Lon, At: a.DetectedAt, Confidence: a.Confidence, Type: a.Type, Daytime: a.Daytime,
	}
	if a.FRPMW != nil {
		p.FRPMW = *a.FRPMW
	}
	return p
}

// SortPoints mengurutkan menurut waktu lalu ID (urutan tetap untuk
// Cluster dan Evaluate).
func SortPoints(ps []Point) {
	slices.SortFunc(ps, func(a, b Point) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID)) })
}

// ErrRule menandai parameter aturan yang tidak sah.
var ErrRule = errors.New("aturan titik api tidak sah")

// ClusterRule menentukan kapan dua deteksi termasuk satu kejadian: jarak
// paling jauh LinkKm dan selisih waktu paling lama Gap. Kejadian berantai:
// deteksi yang terhubung ke salah satu anggota ikut masuk, jadi kebakaran
// yang menyala berhari-hari tetap satu kejadian selama tidak ada jeda lebih
// lama dari Gap (sama dengan aturan kedaluwarsa 24 jam PRD).
type ClusterRule struct {
	LinkKm float64
	Gap    time.Duration
}

// DefaultClusterRule adalah 2 km (±5 piksel VIIRS, 2 piksel MODIS) dan 24 jam.
var DefaultClusterRule = ClusterRule{LinkKm: 2, Gap: 24 * time.Hour}

// Validate memeriksa batas parameter.
func (r ClusterRule) Validate() error {
	if !(r.LinkKm > 0 && r.LinkKm <= 20) || r.Gap <= 0 || r.Gap > 7*24*time.Hour {
		return fmt.Errorf("%w: jarak %v km harus 0..20, jeda %v harus 0..7 hari", ErrRule, r.LinkKm, r.Gap)
	}
	return nil
}

// Cluster mengelompokkan ps (wajib urut waktu, lihat SortPoints) menjadi
// kejadian. Hasilnya indeks anggota per kejadian, setiap kejadian urut
// waktu, kejadian urut menurut anggota pertamanya.
func Cluster(ps []Point, r ClusterRule) ([][]int, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !slices.IsSortedFunc(ps, func(a, b Point) int { return a.At.Compare(b.At) }) {
		return nil, fmt.Errorf("%w: deteksi belum urut waktu", ErrRule)
	}
	parent := make([]int, len(ps))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	dLat := r.LinkKm / kmPerDegLat
	lo := 0
	for i, p := range ps {
		for ps[lo].At.Before(p.At.Add(-r.Gap)) {
			lo++
		}
		// Derajat bujur menyempit ke kutub; cos lintang Indonesia ≥ 0,97.
		dLon := dLat / math.Max(math.Cos(p.Lat*math.Pi/180), 0.1)
		for j := lo; j < i; j++ {
			q := ps[j]
			if math.Abs(q.Lat-p.Lat) > dLat || math.Abs(q.Lon-p.Lon) > dLon {
				continue
			}
			if DistanceKm(p.Lat, p.Lon, q.Lat, q.Lon) <= r.LinkKm {
				a, b := find(i), find(j)
				if a != b {
					parent[max(a, b)] = min(a, b)
				}
			}
		}
	}
	groups := map[int][]int{}
	var roots []int
	for i := range ps {
		root := find(i)
		if _, ok := groups[root]; !ok {
			roots = append(roots, root)
		}
		groups[root] = append(groups[root], i)
	}
	out := make([][]int, 0, len(roots))
	for _, root := range roots {
		out = append(out, groups[root])
	}
	return out, nil
}

// Level adalah tingkat peringatan titik api. Bahaya tidak dipakai sendiri
// (PRD, Aturan bisnis).
type Level int8

// Tingkat titik api.
const (
	None Level = iota
	Info
	Waspada
	Siaga
)

// String mengembalikan nama tingkat.
func (l Level) String() string {
	switch l {
	case Info:
		return "Info"
	case Waspada:
		return "Waspada"
	case Siaga:
		return "Siaga"
	case None:
	}
	return "-"
}

// Unit adalah satuan hitungan ambang Siaga.
type Unit int8

// Satuan hitungan.
const (
	// Detections menghitung setiap piksel deteksi (PRD: "≥ 5 deteksi").
	Detections Unit = iota + 1
	// Overpasses menghitung lintasan berbeda (wahana + waktu) yang melihat
	// kejadian, jadi satu lintasan yang melihat kebakaran besar sebagai
	// sepuluh piksel dihitung sekali.
	Overpasses
)

// String mengembalikan nama satuan.
func (u Unit) String() string {
	if u == Overpasses {
		return "lintasan"
	}
	return "deteksi"
}

// LevelRule adalah aturan tingkat satu kejadian. PRD: Info untuk deteksi
// keyakinan rendah, Waspada bila ada ≥ 1 deteksi keyakinan tinggi dalam
// Window, Siaga bila ≥ SiagaCount deteksi dalam Window.
type LevelRule struct {
	Name       string
	SiagaCount int
	Unit       Unit
	// WaspadaMin adalah keyakinan terendah yang menaikkan ke Waspada (PRD:
	// fire.High).
	WaspadaMin fire.Confidence
	Window     time.Duration
}

// PRDRule adalah aturan PRD apa adanya.
var PRDRule = LevelRule{Name: "PRD", SiagaCount: 5, Unit: Detections, WaspadaMin: fire.High, Window: 24 * time.Hour}

// Validate memeriksa batas parameter.
func (r LevelRule) Validate() error {
	if r.SiagaCount < 1 || r.SiagaCount > 1000 || (r.Unit != Detections && r.Unit != Overpasses) ||
		r.WaspadaMin < fire.Low || r.WaspadaMin > fire.High || r.Window <= 0 || r.Window > 7*24*time.Hour {
		return fmt.Errorf("%w: %+v", ErrRule, r)
	}
	return nil
}

// Outcome adalah hasil Evaluate untuk satu kejadian.
type Outcome struct {
	Level Level
	// ReachedAt adalah waktu deteksi pertama yang mencapai Level.
	ReachedAt time.Time
	// Peak adalah hitungan terbesar (dalam Unit aturan) di satu Window.
	Peak       int
	Detections int
	Start, End time.Time
}

// Evaluate menghitung tingkat tertinggi kejadian ps (urut waktu) menurut
// aturan r: di setiap deteksi, jendela (t − Window, t] dinilai.
func Evaluate(ps []Point, r LevelRule) (Outcome, error) {
	if err := r.Validate(); err != nil {
		return Outcome{}, err
	}
	if len(ps) == 0 {
		return Outcome{}, nil
	}
	o := Outcome{Detections: len(ps), Start: ps[0].At, End: ps[len(ps)-1].At}
	lo := 0
	if !slices.IsSortedFunc(ps, func(a, b Point) int { return a.At.Compare(b.At) }) {
		return Outcome{}, fmt.Errorf("%w: deteksi belum urut waktu", ErrRule)
	}
	for i, p := range ps {
		for !ps[lo].At.After(p.At.Add(-r.Window)) {
			lo++
		}
		win := ps[lo : i+1]
		n := count(win, r.Unit)
		lvl := Info
		switch {
		case n >= r.SiagaCount:
			lvl = Siaga
		case slices.ContainsFunc(win, func(q Point) bool { return q.Confidence >= r.WaspadaMin }):
			lvl = Waspada
		}
		o.Peak = max(o.Peak, n)
		if lvl > o.Level {
			o.Level, o.ReachedAt = lvl, p.At
		}
	}
	return o, nil
}

func count(win []Point, u Unit) int {
	if u == Detections {
		return len(win)
	}
	type pass struct {
		sat string
		at  time.Time
	}
	seen := map[pass]bool{}
	for _, q := range win {
		seen[pass{q.Satellite, q.At}] = true
	}
	return len(seen)
}
