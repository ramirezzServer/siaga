package fire

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

// Assessment adalah kejadian titik api lengkap yang disimpan dan
// diterbitkan.
type Assessment struct {
	Outcome
	ID hazard.EventID
	// OpenedBy adalah ID deteksi yang membuka kejadian (dasar ID kejadian).
	OpenedBy string
	// Lat, Lon adalah rata-rata posisi deteksi (lima desimal).
	Lat, Lon float64
	// Products adalah produk FIRMS yang melihat kejadian, urut nama.
	Products []string
	MaxFRPMW *float64
	// FirstFetchedAt adalah waktu terima deteksi pertama yang diterima;
	// LastFetchedAt yang terakhir. Kejadian berakhir Linger setelah
	// LastFetchedAt (PRD: 24 jam sejak data terakhir).
	FirstFetchedAt, LastFetchedAt time.Time
	ExpiresAt                     time.Time
	// Members adalah semua deteksi kejadian, urut waktu lalu ID.
	Members []Detection
	// SiagaCount dan WaspadaPasses dari aturan yang dipakai.
	SiagaCount, WaspadaPasses int
	Title, Summary            string
}

// Assess membentuk kejadian id dari anggotanya (minimal satu).
func (p Policy) Assess(id hazard.EventID, openedBy string, members []Detection) (Assessment, error) {
	if len(members) == 0 {
		return Assessment{}, fmt.Errorf("%w: kejadian %s tanpa deteksi", ErrInvalid, id)
	}
	ms := slices.Clone(members)
	Sort(ms)
	a := Assessment{
		Outcome: p.Evaluate(ms), ID: id, OpenedBy: openedBy, Members: ms,
		SiagaCount: p.SiagaCount, WaspadaPasses: p.WaspadaPasses,
	}
	var sumLat, sumLon float64
	products := map[string]bool{}
	for _, d := range ms {
		sumLat += d.Lat
		sumLon += d.Lon
		products[d.Product] = true
		if d.FRPMW != nil && (a.MaxFRPMW == nil || *d.FRPMW > *a.MaxFRPMW) {
			v := *d.FRPMW
			a.MaxFRPMW = &v
		}
		if a.FirstFetchedAt.IsZero() || d.FetchedAt.Before(a.FirstFetchedAt) {
			a.FirstFetchedAt = d.FetchedAt
		}
		if d.FetchedAt.After(a.LastFetchedAt) {
			a.LastFetchedAt = d.FetchedAt
		}
	}
	n := float64(len(ms))
	a.Lat, a.Lon = round5(sumLat/n), round5(sumLon/n)
	for pr := range products {
		a.Products = append(a.Products, pr)
	}
	slices.Sort(a.Products)
	a.ExpiresAt = a.LastFetchedAt.Add(p.Linger)
	a.Title = title(a)
	a.Summary = summary(a)
	return a, nil
}

func round5(v float64) float64 {
	r := math.Round(v*1e5) / 1e5
	if r == 0 {
		return 0
	}
	return r
}

var levelLabel = [...]string{"-", "Info", "Waspada", "Siaga", "Bahaya"}

// Label mengembalikan nama tingkat.
func Label(l hazard.Level) string {
	if l < 0 || int(l) >= len(levelLabel) {
		return l.String()
	}
	return levelLabel[l]
}

func title(a Assessment) string {
	if a.Detections == 1 {
		return "Titik api terdeteksi satelit"
	}
	return fmt.Sprintf("Titik api: %d deteksi satelit", a.Detections)
}

func summary(a Assessment) string {
	var parts []string
	when := weather.FormatWIB(a.First)
	if !a.Last.Equal(a.First) {
		when += " s.d. " + weather.FormatWIB(a.Last)
	}
	parts = append(parts, fmt.Sprintf("%d deteksi dari %d lintasan satelit (%s), %s.", a.Detections, a.Overpasses, satellites(a.Products), when))
	switch a.Level {
	case hazard.LevelSiaga:
		parts = append(parts, fmt.Sprintf("Siaga: %d deteksi dalam 24 jam (ambang %d).", a.PeakDetections, a.SiagaCount))
	case hazard.LevelWaspada:
		if a.HighConfidence > 0 {
			parts = append(parts, "Waspada: ada deteksi keyakinan tinggi.")
		} else {
			parts = append(parts, fmt.Sprintf("Waspada: api terlihat di %d lintasan dalam 24 jam.", a.PeakOverpasses))
		}
	case hazard.LevelInfo, hazard.LevelBahaya:
	}
	parts = append(parts, "Deteksi satelit bisa juga pembakaran lahan, bukan laporan lapangan. Sumber: NASA FIRMS.")
	return strings.Join(parts, " ")
}

var satelliteNames = map[string]string{
	"VIIRS_SNPP_NRT": "VIIRS S-NPP", "VIIRS_NOAA20_NRT": "VIIRS NOAA-20", "VIIRS_NOAA21_NRT": "VIIRS NOAA-21", "MODIS_NRT": "MODIS",
}

func satellites(products []string) string {
	names := make([]string, 0, len(products))
	for _, p := range products {
		names = append(names, cmp.Or(satelliteNames[p], p))
	}
	return strings.Join(names, ", ")
}

// Digest adalah SHA-256 semua isi yang diterbitkan; kejadian hanya
// diterbitkan ulang bila digest berubah.
func (a Assessment) Digest() [32]byte {
	d := hazard.NewDigester("siaga/fire-assessment/v1")
	d.Str(a.ID.String())
	d.Str(a.OpenedBy)
	d.Int(int64(a.Level))
	d.Time(a.ReachedAt)
	d.Int(int64(a.PeakDetections))
	d.Int(int64(a.PeakOverpasses))
	d.Int(int64(a.Overpasses))
	d.Int(int64(a.HighConfidence))
	d.F64(a.Lat)
	d.F64(a.Lon)
	d.Time(a.FirstFetchedAt)
	d.Time(a.LastFetchedAt)
	d.Time(a.ExpiresAt)
	d.Int(int64(a.SiagaCount))
	d.Int(int64(a.WaspadaPasses))
	d.Str(a.Title)
	d.Str(a.Summary)
	d.Int(int64(len(a.Members)))
	for _, m := range a.Members {
		d.Str(m.ID)
		d.Str(string(m.Confidence))
		d.Time(m.FetchedAt)
		d.Bool(m.FRPMW != nil)
		if m.FRPMW != nil {
			d.F64(*m.FRPMW)
		}
	}
	return d.Sum()
}
