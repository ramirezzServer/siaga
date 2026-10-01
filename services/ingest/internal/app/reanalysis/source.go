// Package reanalysis mengambil deret historis Open-Meteo per sel untuk alat
// kalibrasi ambang: debit harian reanalisis GloFAS (make flood-threshold) dan
// hujan per jam model ECMWF IFS dari arsip (make rain-threshold). Polanya sama
// dengan river-snap (ADR 0019): cache per sel, titik uji saat cache kosong,
// batas kuota berbobot, dan lanjut dari cache bila berhenti di tengah
// (ADR 0020).
package reanalysis

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// Resolution adalah langkah waktu deret.
type Resolution string

// Langkah waktu yang didukung.
const (
	Daily  Resolution = "daily"
	Hourly Resolution = "hourly"
)

// Source menjelaskan satu deret historis yang diminta: endpoint, model,
// satu variabel, satuan yang diharapkan, dan periode tanggal UTC inklusif.
type Source struct {
	Endpoint   string
	Model      string
	Variable   string
	Unit       string
	Resolution Resolution
	From, To   time.Time
}

var (
	name     = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)
	endpoint = regexp.MustCompile(`^https?://[^?#\s]+$`)
)

// Validate memeriksa nama model dan variabel, endpoint, dan periode.
func (s Source) Validate() error {
	switch {
	case !endpoint.MatchString(s.Endpoint):
		return fmt.Errorf("endpoint %q tidak valid", s.Endpoint)
	case !name.MatchString(s.Model):
		return fmt.Errorf("nama model %q tidak valid", s.Model)
	case !name.MatchString(s.Variable):
		return fmt.Errorf("nama variabel %q tidak valid", s.Variable)
	case strings.TrimSpace(s.Unit) == "":
		return errors.New("satuan yang diharapkan wajib diisi")
	case s.Resolution != Daily && s.Resolution != Hourly:
		return fmt.Errorf("langkah waktu %q tidak dikenal", s.Resolution)
	case !isDate(s.From) || !isDate(s.To):
		return errors.New("tanggal periode harus pukul 00.00 UTC")
	case s.To.Before(s.From):
		return fmt.Errorf("periode %s..%s terbalik", Date(s.From), Date(s.To))
	}
	return nil
}

func isDate(t time.Time) bool {
	return t.Location() == time.UTC && t.Equal(t.Truncate(24*time.Hour))
}

// Days adalah jumlah hari periode (inklusif).
func (s Source) Days() int {
	return int(s.To.Sub(s.From)/(24*time.Hour)) + 1
}

// Steps adalah jumlah nilai per sel: hari, atau hari × 24 untuk deret per jam.
func (s Source) Steps() int {
	if s.Resolution == Hourly {
		return s.Days() * 24
	}
	return s.Days()
}

// Weight adalah bobot kuota Open-Meteo untuk satu lokasi dengan satu
// variabel: max(1, hari/14 × variabel/10) (calculateQueryWeight Open-Meteo,
// HANDOFF temuan 1e-1, terbukti di jalan pertama river-snap 1e-3b-1).
func (s Source) Weight() float64 {
	return math.Max(1, float64(s.Days())/14/10)
}

// Key adalah nama unik sumber untuk file cache.
func (s Source) Key() string {
	return fmt.Sprintf("%s-%s-%s-%s", s.Model, s.Variable, Date(s.From), Date(s.To))
}

func (s Source) String() string {
	return fmt.Sprintf("%s %s (%s) %s..%s", s.Model, s.Variable, s.Resolution, Date(s.From), Date(s.To))
}

// Date menulis tanggal UTC sebagai YYYY-MM-DD.
func Date(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// ParseDate membaca tanggal YYYY-MM-DD sebagai pukul 00.00 UTC.
func ParseDate(s string) (time.Time, error) {
	return time.Parse(time.DateOnly, s)
}
