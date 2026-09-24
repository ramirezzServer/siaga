// Package fire berisi aturan murni untuk deteksi titik panas satelit near
// real-time (NASA FIRMS): produk yang diterima, ID deteksi yang stabil,
// pemetaan keyakinan, dan batas nilai fisis.
package fire

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// ErrInvalid menandai deteksi yang melanggar invarian.
var ErrInvalid = errors.New("deteksi titik panas tidak valid")

// Instrument adalah sensor satelit.
type Instrument string

// Instrumen FIRMS yang dipakai.
const (
	VIIRS Instrument = "VIIRS"
	MODIS Instrument = "MODIS"
)

// Product adalah produk FIRMS near real-time.
type Product string

// Produk yang dipakai SIAGA. Landsat NRT hanya untuk Amerika Utara.
const (
	VIIRSSNPP    Product = "VIIRS_SNPP_NRT"
	VIIRSNOAA20  Product = "VIIRS_NOAA20_NRT"
	VIIRSNOAA21  Product = "VIIRS_NOAA21_NRT"
	MODISProduct Product = "MODIS_NRT"
)

// Products adalah semua produk yang diterima, dalam urutan polling.
var Products = []Product{VIIRSSNPP, VIIRSNOAA20, VIIRSNOAA21, MODISProduct}

// Instrument mengembalikan instrumen produk; ok false bila produk tidak dikenal.
func (p Product) Instrument() (Instrument, bool) {
	switch p {
	case VIIRSSNPP, VIIRSNOAA20, VIIRSNOAA21:
		return VIIRS, true
	case MODISProduct:
		return MODIS, true
	}
	return "", false
}

// Confidence adalah kelas keyakinan deteksi.
type Confidence int

// Kelas keyakinan. Nol berarti tidak diketahui (tidak valid).
const (
	Low Confidence = iota + 1
	Nominal
	High
)

// String mengembalikan nama kelas dalam huruf kecil.
func (c Confidence) String() string {
	switch c {
	case Low:
		return "low"
	case Nominal:
		return "nominal"
	case High:
		return "high"
	}
	return "unknown"
}

// ConfidenceFromPct memetakan persentase MODIS ke kelas (panduan FIRMS:
// < 30 rendah, 30–79 nominal, >= 80 tinggi).
func ConfidenceFromPct(pct int) Confidence {
	switch {
	case pct >= 80:
		return High
	case pct >= 30:
		return Nominal
	default:
		return Low
	}
}

// ParseVIIRSConfidence membaca kelas VIIRS ("l", "n", "h", atau nama lengkap).
func ParseVIIRSConfidence(s string) (Confidence, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "l", "low":
		return Low, true
	case "n", "nominal":
		return Nominal, true
	case "h", "high":
		return High, true
	}
	return 0, false
}

// Batas nilai.
const (
	// MaxClockSkew adalah selisih jam terbesar yang ditoleransi terhadap sumber.
	MaxClockSkew = 15 * time.Minute
	// MaxAge adalah umur deteksi terbesar yang diterima. Request memakai
	// jendela 2 hari; NRT kadang tertunda, jadi diberi kelonggaran.
	MaxAge = 10 * 24 * time.Hour
)

var (
	satellite = regexp.MustCompile(`^[A-Za-z0-9]{1,10}$`)
	version   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,16}$`)
)

// Detection adalah satu deteksi titik panas.
type Detection struct {
	Product    Product
	Satellite  string
	Instrument Instrument
	Lat, Lon   float64
	// DetectedAt adalah waktu lintasan satelit, UTC, presisi menit.
	DetectedAt time.Time
	Confidence Confidence
	// ConfidencePct hanya untuk MODIS.
	ConfidencePct *int
	BrightnessK   float64
	BackgroundK   *float64
	FRPMW         *float64
	ScanKm        float64
	TrackKm       float64
	Daytime       bool
	Version       string
}

// ID membentuk ID deteksi yang stabil: produk, menit lintasan, dan pusat
// piksel lima desimal (±1 m). Dua baris dengan ID sama adalah deteksi yang sama.
func (d Detection) ID() string {
	return fmt.Sprintf("%s:%s:%.5f:%.5f", d.Product, d.DetectedAt.UTC().Format("20060102T1504"), d.Lat, d.Lon)
}

// Validate memeriksa semua invarian; fetchedAt adalah waktu respons diterima.
func (d Detection) Validate(fetchedAt time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	inst, ok := d.Product.Instrument()
	switch {
	case !ok:
		bad("produk %q tidak dikenal", d.Product)
	case inst != d.Instrument:
		bad("instrumen %q tidak cocok dengan produk %s", d.Instrument, d.Product)
	}
	if !satellite.MatchString(d.Satellite) {
		bad("kode satelit %q tidak valid", d.Satellite)
	}
	if !version.MatchString(d.Version) {
		bad("versi %q tidak valid", d.Version)
	}
	if !inRange(d.Lat, -12, 7) || !inRange(d.Lon, 94, 142) {
		bad("lokasi (%v, %v) di luar Indonesia", d.Lat, d.Lon)
	}
	switch t := d.DetectedAt; {
	case t.IsZero() || t.Location() != time.UTC:
		bad("waktu deteksi kosong atau bukan UTC")
	case t.Second() != 0 || t.Nanosecond() != 0:
		bad("waktu deteksi %s bukan kelipatan menit", t.Format(time.RFC3339))
	case t.After(fetchedAt.Add(MaxClockSkew)):
		bad("waktu deteksi %s di masa depan", t.Format(time.RFC3339))
	case t.Before(fetchedAt.Add(-MaxAge)):
		bad("waktu deteksi %s lebih tua dari %v", t.Format(time.RFC3339), MaxAge)
	}
	if d.Confidence < Low || d.Confidence > High {
		bad("kelas keyakinan %d tidak dikenal", d.Confidence)
	}
	switch {
	case d.Instrument == MODIS && d.ConfidencePct == nil:
		bad("MODIS wajib membawa persentase keyakinan")
	case d.Instrument != MODIS && d.ConfidencePct != nil:
		bad("persentase keyakinan hanya untuk MODIS")
	case d.ConfidencePct != nil && (*d.ConfidencePct < 0 || *d.ConfidencePct > 100):
		bad("persentase keyakinan %d di luar 0..100", *d.ConfidencePct)
	case d.ConfidencePct != nil && ConfidenceFromPct(*d.ConfidencePct) != d.Confidence:
		bad("kelas %s tidak cocok dengan persentase %d", d.Confidence, *d.ConfidencePct)
	}
	if !inRange(d.BrightnessK, 200, 700) {
		bad("suhu kecerahan %v K di luar 200..700", d.BrightnessK)
	}
	if d.BackgroundK != nil && !inRange(*d.BackgroundK, 150, 450) {
		bad("suhu kecerahan latar %v K di luar 150..450", *d.BackgroundK)
	}
	if d.FRPMW != nil && !inRange(*d.FRPMW, 0, 50000) {
		bad("FRP %v MW di luar 0..50000", *d.FRPMW)
	}
	if !inRange(d.ScanKm, 0.1, 10) || !inRange(d.TrackKm, 0.1, 10) {
		bad("ukuran piksel %v × %v km di luar 0,1..10", d.ScanKm, d.TrackKm)
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, d.ID(), errors.Join(errs...))
}

func inRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}
