// Package forecast berisi aturan murni untuk prakiraan cuaca per
// kelurahan/desa (adm4): format kode wilayah, invarian nilai, dan urutan
// sapuan yang mendahulukan zona fokus.
package forecast

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalid menandai prakiraan yang melanggar invarian domain.
var ErrInvalid = errors.New("prakiraan cuaca tidak valid")

// Batas nilai yang masih masuk akal untuk Indonesia. Di luar ini hampir pasti data rusak.
const (
	MaxSteps       = 200
	MaxHorizon     = 10 * 24 * time.Hour
	MaxClockSkew   = time.Hour
	MaxAnalysisAge = 7 * 24 * time.Hour
	maxTextLen     = 256
)

var adm4 = regexp.MustCompile(`^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$`)

// ValidCode melaporkan apakah code adalah kode Kemendagri adm4 bertitik
// (misal "32.73.01.1001").
func ValidCode(code string) bool { return adm4.MatchString(code) }

// Step adalah satu langkah prakiraan.
type Step struct {
	ValidTime       time.Time
	TemperatureC    float64
	HumidityPct     float64
	CloudCoverPct   float64
	PrecipitationMM float64
	WeatherCode     int
	WeatherDesc     string
	WeatherDescEN   string
	WindSpeedKmh    float64
	WindFromDeg     float64
	WindFrom        string
	VisibilityM     *float64 // nil bila sumber tidak mengisi
	VisibilityText  string
}

// Forecast adalah prakiraan satu kelurahan/desa. Waktu selalu UTC.
type Forecast struct {
	RegionCode   string
	Province     string
	Regency      string
	District     string
	Village      string
	Latitude     float64
	Longitude    float64
	AnalysisTime time.Time
	Steps        []Step
}

// Validate memeriksa semua invarian. fetchedAt dipakai untuk menolak waktu
// analisis di masa depan atau yang sudah terlalu tua. Semua pelanggaran
// dilaporkan sekaligus.
func (f Forecast) Validate(fetchedAt time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !ValidCode(f.RegionCode) {
		bad("kode wilayah %q bukan adm4", f.RegionCode)
	}
	// Kotak Indonesia yang longgar: titik di luar ini pasti salah.
	if !inRange(f.Latitude, -12, 7) || !inRange(f.Longitude, 94, 142) {
		bad("titik (%v, %v) di luar Indonesia", f.Latitude, f.Longitude)
	}
	for _, s := range []struct{ name, value string }{
		{"province", f.Province}, {"regency", f.Regency}, {"district", f.District}, {"village", f.Village},
	} {
		if err := checkText(s.value); err != nil {
			bad("%s: %w", s.name, err)
		}
	}
	switch {
	case f.AnalysisTime.IsZero():
		bad("waktu analisis kosong")
	case f.AnalysisTime.Location() != time.UTC:
		bad("waktu analisis harus UTC")
	case f.AnalysisTime.After(fetchedAt.Add(MaxClockSkew)):
		bad("waktu analisis %s di masa depan", f.AnalysisTime.Format(time.RFC3339))
	case fetchedAt.Sub(f.AnalysisTime) > MaxAnalysisAge:
		bad("waktu analisis %s lebih tua dari %v", f.AnalysisTime.Format(time.RFC3339), MaxAnalysisAge)
	}
	if len(f.Steps) == 0 || len(f.Steps) > MaxSteps {
		bad("%d langkah prakiraan, harus 1..%d", len(f.Steps), MaxSteps)
	}
	for i, s := range f.Steps {
		if err := s.validate(f.AnalysisTime); err != nil {
			bad("langkah %d: %w", i, err)
		}
		if i > 0 && !s.ValidTime.After(f.Steps[i-1].ValidTime) {
			bad("langkah %d tidak urut atau ganda (%s)", i, s.ValidTime.Format(time.RFC3339))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, f.RegionCode, errors.Join(errs...))
}

func (s Step) validate(analysis time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	switch {
	case s.ValidTime.IsZero() || s.ValidTime.Location() != time.UTC:
		bad("waktu kosong atau bukan UTC")
	case !analysis.IsZero() && (s.ValidTime.Before(analysis.Add(-24*time.Hour)) || s.ValidTime.After(analysis.Add(MaxHorizon))):
		bad("waktu %s di luar jangkauan analisis", s.ValidTime.Format(time.RFC3339))
	}
	for _, v := range []struct {
		name   string
		v      float64
		lo, hi float64
	}{
		{"suhu", s.TemperatureC, -20, 50},
		{"kelembapan", s.HumidityPct, 0, 100},
		{"tutupan awan", s.CloudCoverPct, 0, 100},
		{"curah hujan", s.PrecipitationMM, 0, 500},
		{"kecepatan angin", s.WindSpeedKmh, 0, 400},
		{"arah angin", s.WindFromDeg, 0, 360},
	} {
		if !inRange(v.v, v.lo, v.hi) {
			bad("%s %v di luar %v..%v", v.name, v.v, v.lo, v.hi)
		}
	}
	if s.VisibilityM != nil && !inRange(*s.VisibilityM, 0, 1_000_000) {
		bad("jarak pandang %v m tidak masuk akal", *s.VisibilityM)
	}
	if s.WeatherCode < 0 || s.WeatherCode > 999 {
		bad("kode cuaca %d di luar 0..999", s.WeatherCode)
	}
	for _, t := range []string{s.WeatherDesc, s.WeatherDescEN, s.WindFrom, s.VisibilityText} {
		if err := checkText(t); err != nil {
			bad("teks: %w", err)
		}
	}
	return errors.Join(errs...)
}

// Order mengurutkan kode untuk satu sapuan: kode yang diawali salah satu
// prefix fokus lebih dulu (urut sesuai daftar fokus), sisanya menyusul;
// di dalam kelompok urut kode. Kode ganda dibuang. Hasilnya deterministik.
func Order(codes []string, focus []string) []string {
	rank := func(code string) int {
		for i, p := range focus {
			if p != "" && (code == p || strings.HasPrefix(code, p+".")) {
				return i
			}
		}
		return len(focus)
	}
	out := slices.Clone(codes)
	slices.SortFunc(out, func(a, b string) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(a, b)
	})
	return slices.Compact(out)
}

func inRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}

func checkText(s string) error {
	if len(s) > maxTextLen {
		return fmt.Errorf("panjang %d melebihi %d", len(s), maxTextLen)
	}
	if !utf8.ValidString(s) {
		return errors.New("bukan UTF-8 valid")
	}
	return nil
}
