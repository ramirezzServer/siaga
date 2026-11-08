// Package fire berisi aturan murni kejadian titik api (hazard.fire.*, ADR
// 0023): saringan sumber panas tetap, pengelompokan deteksi satelit menjadi
// kejadian, dan tingkat peringatan. Aturannya salinan aturan terpilih di
// domain firecluster ingest (alat kalibrasi make fire-threshold); test
// kecocokan di internal/app/fires memastikan hasil keduanya sama untuk
// deteksi asli yang sama.
package fire

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
)

// ErrInvalid menandai kejadian atau kalibrasi titik api yang melanggar
// invarian. Pesan dengan galat ini tidak akan berhasil bila dicoba ulang.
var ErrInvalid = errors.New("titik api tidak valid")

// EarthRadiusKm adalah jari-jari bumi rata-rata (IUGG), sama dengan
// firecluster ingest.
const EarthRadiusKm = 6371.0088

// DistanceKm adalah jarak lingkaran besar (haversine) dua titik.
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * EarthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Detection adalah satu deteksi titik panas yang dinilai.
type Detection struct {
	ID         string
	Product    string
	Lat, Lon   float64
	DetectedAt time.Time
	Confidence hotspot.Confidence
	FRPMW      *float64
	// FetchedAt adalah waktu ingest menerima payload deteksi ini.
	FetchedAt time.Time
}

// FromHotspot mengambil bagian deteksi yang dipakai penilaian.
func FromHotspot(d hotspot.Detection) Detection {
	return Detection{
		ID: d.ID, Product: d.Product, Lat: d.Lat, Lon: d.Lon, DetectedAt: d.DetectedAt.UTC(),
		Confidence: d.Confidence, FRPMW: d.FRPMW, FetchedAt: d.FetchedAt.UTC(),
	}
}

// Sort mengurutkan deteksi menurut waktu deteksi lalu ID (urutan
// firecluster.SortPoints).
func Sort(ds []Detection) {
	slices.SortFunc(ds, func(a, b Detection) int { return cmp.Or(a.DetectedAt.Compare(b.DetectedAt), cmp.Compare(a.ID, b.ID)) })
}

// Policy adalah aturan kejadian titik api. DefaultPolicy sama dengan
// firecluster.DefaultClusterRule + firecluster.SelectedRule ingest.
type Policy struct {
	// LinkKm dan Gap: dua deteksi satu kejadian bila berjarak ≤ LinkKm dan
	// berselang ≤ Gap (berantai).
	LinkKm float64
	Gap    time.Duration
	// Window adalah jendela hitungan tingkat (ke belakang dari tiap deteksi).
	Window time.Duration
	// SiagaCount: Siaga bila ≥ SiagaCount deteksi dalam Window.
	SiagaCount int
	// Waspada bila ada deteksi berkeyakinan WaspadaMin, atau kejadian
	// terlihat di ≥ WaspadaPasses lintasan (produk + waktu) dalam Window.
	WaspadaMin    hotspot.Confidence
	WaspadaPasses int
	// Linger: kejadian berakhir Linger setelah data terakhirnya diterima.
	Linger time.Duration
	// Excluded adalah produk yang tidak dinilai (S-NPP berhenti 2 Nov 2026;
	// aturan dikalibrasi tanpa wahana itu).
	Excluded []string
}

// DefaultPolicy adalah aturan terpilih kalibrasi 1e-3c-1b (ADR 0023).
func DefaultPolicy() Policy {
	return Policy{
		LinkKm: 2, Gap: 24 * time.Hour, Window: 24 * time.Hour, SiagaCount: 5,
		WaspadaMin: hotspot.High, WaspadaPasses: 2, Linger: 24 * time.Hour,
		Excluded: []string{"VIIRS_SNPP_NRT"},
	}
}

// Validate memeriksa batas parameter.
func (p Policy) Validate() error {
	switch {
	case !(p.LinkKm > 0 && p.LinkKm <= 20), p.Gap <= 0 || p.Gap > 7*24*time.Hour, p.Window <= 0 || p.Window > 7*24*time.Hour,
		p.SiagaCount < 1 || p.SiagaCount > 1000, p.WaspadaPasses < 0 || p.WaspadaPasses > 100,
		p.Linger < time.Hour || p.Linger > 7*24*time.Hour:
		return fmt.Errorf("%w: aturan %+v", ErrInvalid, p)
	}
	if rank(p.WaspadaMin) == 0 {
		return fmt.Errorf("%w: keyakinan Waspada %q", ErrInvalid, p.WaspadaMin)
	}
	return nil
}

// Excludes true bila produk tidak dinilai.
func (p Policy) Excludes(product string) bool { return slices.Contains(p.Excluded, product) }

// Linked true bila a dan b termasuk satu kejadian.
func (p Policy) Linked(a, b Detection) bool {
	dt := a.DetectedAt.Sub(b.DetectedAt)
	return dt <= p.Gap && dt >= -p.Gap && DistanceKm(a.Lat, a.Lon, b.Lat, b.Lon) <= p.LinkKm
}

func rank(c hotspot.Confidence) int {
	switch c {
	case hotspot.Low:
		return 1
	case hotspot.Nominal:
		return 2
	case hotspot.High:
		return 3
	}
	return 0
}

// Outcome adalah hasil Evaluate.
type Outcome struct {
	// Level adalah tingkat tertinggi selama kejadian (Info..Siaga); tidak
	// pernah turun selama kejadian aktif.
	Level hazard.Level
	// ReachedAt adalah waktu deteksi pertama yang mencapai Level.
	ReachedAt time.Time
	// PeakDetections dan PeakOverpasses adalah hitungan terbesar di satu Window.
	PeakDetections, PeakOverpasses int
	Detections, Overpasses         int
	HighConfidence                 int
	First, Last                    time.Time
}

type pass struct {
	product string
	at      time.Time
}

// Evaluate menghitung tingkat kejadian ms (urutan bebas): di setiap deteksi,
// jendela (t − Window, t] dinilai, sama dengan firecluster.Evaluate.
func (p Policy) Evaluate(ms []Detection) Outcome {
	if len(ms) == 0 {
		return Outcome{}
	}
	ds := slices.Clone(ms)
	Sort(ds)
	o := Outcome{Detections: len(ds), First: ds[0].DetectedAt, Last: ds[len(ds)-1].DetectedAt}
	all := map[pass]bool{}
	lo := 0
	for i, d := range ds {
		all[pass{d.Product, d.DetectedAt}] = true
		if d.Confidence == hotspot.High {
			o.HighConfidence++
		}
		for !ds[lo].DetectedAt.After(d.DetectedAt.Add(-p.Window)) {
			lo++
		}
		win := ds[lo : i+1]
		passes := map[pass]bool{}
		confident := false
		for _, q := range win {
			passes[pass{q.Product, q.DetectedAt}] = true
			confident = confident || rank(q.Confidence) >= rank(p.WaspadaMin)
		}
		lvl := hazard.LevelInfo
		switch {
		case len(win) >= p.SiagaCount:
			lvl = hazard.LevelSiaga
		case confident, p.WaspadaPasses > 0 && len(passes) >= p.WaspadaPasses:
			lvl = hazard.LevelWaspada
		}
		o.PeakDetections = max(o.PeakDetections, len(win))
		o.PeakOverpasses = max(o.PeakOverpasses, len(passes))
		if lvl > o.Level {
			o.Level, o.ReachedAt = lvl, d.DetectedAt
		}
	}
	o.Overpasses = len(all)
	return o
}

// EventIDFor membentuk ID kejadian dari deteksi yang membukanya.
func EventIDFor(openedBy string) hazard.EventID {
	return hazard.NewEventID("siaga/hazard/fire", 0, openedBy)
}
