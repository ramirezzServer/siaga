// Package hotspot berisi aturan murni untuk deteksi titik panas satelit
// (NASA FIRMS) yang disimpan di ts.hotspot. Invarian di sini sama dengan
// constraint migrasi 00005_ts_observation.sql (ADR 0013).
package hotspot

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalid menandai deteksi yang melanggar invarian. Pesan dengan galat
// ini tidak akan berhasil bila dicoba ulang.
var ErrInvalid = errors.New("deteksi titik panas tidak valid")

// Confidence adalah kelas keyakinan (kolom ts.hotspot.confidence).
type Confidence string

// Kelas keyakinan.
const (
	Low     Confidence = "low"
	Nominal Confidence = "nominal"
	High    Confidence = "high"
)

// ConfidenceFromPct memetakan persentase MODIS (< 30 rendah, 30–79 nominal,
// >= 80 tinggi).
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

// instruments memetakan produk FIRMS ke instrumennya.
var instruments = map[string]string{
	"VIIRS_SNPP_NRT":   "VIIRS",
	"VIIRS_NOAA20_NRT": "VIIRS",
	"VIIRS_NOAA21_NRT": "VIIRS",
	"MODIS_NRT":        "MODIS",
}

// Batas waktu.
const (
	// MaxClockSkew adalah selisih jam terbesar yang ditoleransi.
	MaxClockSkew = time.Hour
	// MaxAge adalah umur deteksi terbesar terhadap waktu ambil.
	MaxAge = 10 * 24 * time.Hour
)

var (
	idFormat  = regexp.MustCompile(`^([A-Z0-9_]+):([0-9]{8}T[0-9]{4}):(-?[0-9]{1,2}\.[0-9]{5}):(-?[0-9]{1,3}\.[0-9]{5})$`)
	satellite = regexp.MustCompile(`^[A-Za-z0-9]{1,10}$`)
	version   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,16}$`)
)

// Detection adalah satu deteksi titik panas: satu event raw.fire.firms.
type Detection struct {
	ID            string
	Product       string
	Satellite     string
	Instrument    string
	Lat, Lon      float64
	DetectedAt    time.Time
	Confidence    Confidence
	ConfidencePct *int
	BrightnessK   float64
	BackgroundK   *float64
	FRPMW         *float64
	ScanKm        float64
	TrackKm       float64
	Daytime       bool
	Version       string
	FetchedAt     time.Time
	ArchiveKey    string
}

// Validate memeriksa semua invarian; now adalah jam geo-processor.
func (d Detection) Validate(now time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	inst, ok := instruments[d.Product]
	switch {
	case !ok:
		bad("produk %q tidak dikenal", d.Product)
	case inst != d.Instrument:
		bad("instrumen %q tidak cocok dengan produk %s", d.Instrument, d.Product)
	}
	if m := idFormat.FindStringSubmatch(d.ID); m == nil || len(d.ID) > 80 {
		bad("ID deteksi %q tidak valid", d.ID)
	} else if want := fmt.Sprintf("%s:%s:%.5f:%.5f", d.Product, d.DetectedAt.UTC().Format("20060102T1504"), d.Lat, d.Lon); m[0] != want {
		bad("ID deteksi %q tidak cocok dengan isinya (%s)", d.ID, want)
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
	case t.IsZero() || t.Location() != time.UTC || d.FetchedAt.IsZero() || d.FetchedAt.Location() != time.UTC:
		bad("waktu deteksi dan waktu ambil wajib terisi dan UTC")
	case t.Second() != 0 || t.Nanosecond() != 0:
		bad("waktu deteksi %s bukan kelipatan menit", t.Format(time.RFC3339))
	case t.After(d.FetchedAt.Add(MaxClockSkew)) || t.Before(d.FetchedAt.Add(-MaxAge)):
		bad("waktu deteksi %s di luar jendela waktu ambil", t.Format(time.RFC3339))
	case d.FetchedAt.After(now.Add(MaxClockSkew)):
		bad("waktu ambil %s di masa depan", d.FetchedAt.Format(time.RFC3339))
	}
	switch d.Confidence {
	case Low, Nominal, High:
	default:
		bad("kelas keyakinan %q tidak dikenal", d.Confidence)
	}
	switch {
	case (inst == "MODIS") != (d.ConfidencePct != nil):
		bad("persentase keyakinan wajib untuk MODIS dan hanya untuk MODIS")
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
	if len(d.ArchiveKey) > 512 || !utf8.ValidString(d.ArchiveKey) || strings.ContainsAny(d.ArchiveKey, " \t\r\n") {
		bad("kunci arsip %q tidak valid", d.ArchiveKey)
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, d.ID, errors.Join(errs...))
}

func inRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}
