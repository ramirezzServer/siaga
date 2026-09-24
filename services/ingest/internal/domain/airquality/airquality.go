// Package airquality berisi aturan murni untuk pengukuran kualitas udara
// stasiun permukaan (OpenAQ): stasiun, parameter dan satuan yang diterima,
// batas nilai, dan pembersihan nilai sensor yang basi atau rusak.
//
// Nilai tidak dikonversi antarsatuan: satuan sumber ikut disimpan, dan
// konversi ppm/ppb ke µg/m³ (butuh suhu dan tekanan) dikerjakan pemakai data.
package airquality

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalid menandai stasiun atau nilai yang melanggar invarian.
var ErrInvalid = errors.New("pengukuran kualitas udara tidak valid")

// Parameter adalah besaran yang diukur, memakai nama parameter OpenAQ.
type Parameter string

// Parameter yang dipakai SIAGA (ISPU memakai PM2,5, PM10, NO2, O3, SO2, CO).
const (
	PM25 Parameter = "pm25"
	PM10 Parameter = "pm10"
	NO2  Parameter = "no2"
	O3   Parameter = "o3"
	SO2  Parameter = "so2"
	CO   Parameter = "co"
)

// Satuan yang diterima, persis seperti ditulis sumber.
const (
	UnitMicrogram = "µg/m³"
	UnitPPM       = "ppm"
	UnitPPB       = "ppb"
)

// limits adalah batas atas nilai per (parameter, satuan). Batas µg/m³ sama
// dengan constraint ts.aq_observation; batas ppm setara kira-kira pada 25 °C,
// ppb = ppm × 1000. Partikulat hanya sah dalam µg/m³.
var limits = map[Parameter]map[string]float64{
	PM25: {UnitMicrogram: 5000},
	PM10: {UnitMicrogram: 10000},
	NO2:  {UnitMicrogram: 5000, UnitPPM: 2.7, UnitPPB: 2700},
	O3:   {UnitMicrogram: 5000, UnitPPM: 2.6, UnitPPB: 2600},
	SO2:  {UnitMicrogram: 10000, UnitPPM: 3.9, UnitPPB: 3900},
	CO:   {UnitMicrogram: 200000, UnitPPM: 175, UnitPPB: 175000},
}

// Known melaporkan apakah p dipakai SIAGA.
func Known(p Parameter) bool {
	_, ok := limits[p]
	return ok
}

// Limit mengembalikan batas atas nilai p dalam satuan unit; ok false bila
// kombinasinya tidak diterima.
func Limit(p Parameter, unit string) (float64, bool) {
	hi, ok := limits[p][unit]
	return hi, ok
}

// Batas lain.
const (
	maxTextLen = 100
	// MaxClockSkew adalah selisih jam terbesar yang ditoleransi terhadap sumber.
	MaxClockSkew = 15 * time.Minute
	// MaxReadings membatasi jumlah sensor per stasiun.
	MaxReadings = 64
)

var (
	stationID = regexp.MustCompile(`^openaq:[1-9][0-9]{0,11}$`)
	timezone  = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+-]+){0,2}$`)
)

// StationID membentuk ID stasiun SIAGA dari ID lokasi OpenAQ.
func StationID(locationID int64) string { return "openaq:" + strconv.FormatInt(locationID, 10) }

// Station adalah stasiun pengukur.
type Station struct {
	ID        string
	Name      string
	Locality  string
	Lat, Lon  float64
	Provider  string
	Owner     string
	IsMonitor bool
	Timezone  string
}

func (s Station) validate() []error {
	var errs []error
	if !stationID.MatchString(s.ID) {
		errs = append(errs, fmt.Errorf("ID stasiun %q tidak valid", s.ID))
	}
	if !InIndonesia(s.Lat, s.Lon) {
		errs = append(errs, fmt.Errorf("lokasi stasiun (%v, %v) di luar Indonesia", s.Lat, s.Lon))
	}
	for _, f := range []struct{ name, v string }{
		{"nama", s.Name}, {"daerah", s.Locality}, {"penyedia", s.Provider}, {"pemilik", s.Owner},
	} {
		if err := checkText(f.v); err != nil {
			errs = append(errs, fmt.Errorf("%s stasiun: %w", f.name, err))
		}
	}
	if s.Name == "" || s.Provider == "" {
		errs = append(errs, errors.New("nama dan penyedia stasiun wajib diisi"))
	}
	if s.Timezone != "" && (len(s.Timezone) > 64 || !timezone.MatchString(s.Timezone)) {
		errs = append(errs, fmt.Errorf("zona waktu %q tidak valid", s.Timezone))
	}
	return errs
}

// Reading adalah nilai terakhir satu sensor.
type Reading struct {
	SensorID   int64
	Parameter  Parameter
	Unit       string
	Value      float64
	ObservedAt time.Time
}

// check memeriksa satu nilai terhadap waktu ambil dan umur maksimum.
func (r Reading) check(fetchedAt time.Time, maxAge time.Duration) error {
	hi, ok := Limit(r.Parameter, r.Unit)
	switch {
	case r.SensorID <= 0:
		return fmt.Errorf("ID sensor %d tidak valid", r.SensorID)
	case !Known(r.Parameter):
		return fmt.Errorf("sensor %d: parameter %q tidak dipakai", r.SensorID, r.Parameter)
	case !ok:
		return fmt.Errorf("sensor %d: satuan %q tidak diterima untuk %s", r.SensorID, r.Unit, r.Parameter)
	case math.IsNaN(r.Value) || r.Value < 0 || r.Value > hi:
		return fmt.Errorf("sensor %d: %s %v %s di luar 0..%v", r.SensorID, r.Parameter, r.Value, r.Unit, hi)
	case r.ObservedAt.IsZero() || r.ObservedAt.Location() != time.UTC:
		return fmt.Errorf("sensor %d: waktu ukur kosong atau bukan UTC", r.SensorID)
	case r.ObservedAt.After(fetchedAt.Add(MaxClockSkew)):
		return fmt.Errorf("sensor %d: waktu ukur %s di masa depan", r.SensorID, r.ObservedAt.Format(time.RFC3339))
	case r.ObservedAt.Before(fetchedAt.Add(-maxAge)):
		return fmt.Errorf("%w: sensor %d terakhir mengukur %s", ErrStale, r.SensorID, r.ObservedAt.Format(time.RFC3339))
	}
	return nil
}

// ErrStale menandai nilai yang lebih tua dari umur maksimum. Bukan galat
// sumber: sensor yang berhenti tetap muncul di daftar nilai terbaru.
var ErrStale = errors.New("nilai basi")

// Observation adalah nilai terbaru semua sensor di satu stasiun.
type Observation struct {
	Station  Station
	Readings []Reading
}

// Clean membuang nilai yang rusak atau basi, mengurutkan per ID sensor, dan
// menolak ID sensor ganda (keduanya dibuang). Galat yang dikembalikan
// menjelaskan setiap nilai yang dibuang; basi dibungkus ErrStale.
func Clean(readings []Reading, fetchedAt time.Time, maxAge time.Duration) ([]Reading, []error) {
	var kept []Reading
	var dropped []error
	count := map[int64]int{}
	for _, r := range readings {
		count[r.SensorID]++
	}
	for _, r := range readings {
		if count[r.SensorID] > 1 {
			dropped = append(dropped, fmt.Errorf("sensor %d muncul %d kali", r.SensorID, count[r.SensorID]))
			continue
		}
		if err := r.check(fetchedAt, maxAge); err != nil {
			dropped = append(dropped, err)
			continue
		}
		kept = append(kept, r)
	}
	slices.SortFunc(kept, func(a, b Reading) int { return cmp.Compare(a.SensorID, b.SensorID) })
	return kept, dropped
}

// Validate memeriksa stasiun dan nilai yang sudah dibersihkan Clean.
func (o Observation) Validate(fetchedAt time.Time, maxAge time.Duration) error {
	errs := o.Station.validate()
	if len(o.Readings) == 0 || len(o.Readings) > MaxReadings {
		errs = append(errs, fmt.Errorf("%d nilai sensor, harus 1..%d", len(o.Readings), MaxReadings))
	}
	for i, r := range o.Readings {
		if err := r.check(fetchedAt, maxAge); err != nil {
			errs = append(errs, err)
		}
		if i > 0 && r.SensorID <= o.Readings[i-1].SensorID {
			errs = append(errs, fmt.Errorf("sensor %d tidak urut atau ganda", r.SensorID))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, o.Station.ID, errors.Join(errs...))
}

// Newest mengembalikan waktu ukur terbaru; nol bila tidak ada nilai.
func (o Observation) Newest() time.Time {
	var t time.Time
	for _, r := range o.Readings {
		if r.ObservedAt.After(t) {
			t = r.ObservedAt
		}
	}
	return t
}

// InIndonesia melaporkan apakah titik berada di kotak Indonesia yang longgar
// (sama dengan constraint ts.site).
func InIndonesia(lat, lon float64) bool {
	return inRange(lat, -12, 7) && inRange(lon, 94, 142)
}

func inRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}

func checkText(s string) error {
	switch {
	case len(s) > maxTextLen:
		return fmt.Errorf("panjang %d melebihi %d", len(s), maxTextLen)
	case !utf8.ValidString(s):
		return errors.New("bukan UTF-8 valid")
	case s != strings.TrimSpace(s):
		return errors.New("diawali atau diakhiri spasi")
	case strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 }):
		return errors.New("berisi karakter kontrol")
	}
	return nil
}
