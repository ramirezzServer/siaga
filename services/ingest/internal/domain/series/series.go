// Package series berisi aturan murni untuk deret waktu keluaran model grid
// (cuaca, kualitas udara, debit sungai) di satu titik pantau: format ID
// titik, spesifikasi variabel per jenis data, dan invarian nilai.
package series

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalid menandai deret yang melanggar invarian domain.
var ErrInvalid = errors.New("deret waktu model tidak valid")

// Dataset adalah jenis data deret.
type Dataset string

// Jenis data yang dikenal.
const (
	Weather    Dataset = "weather"
	AirQuality Dataset = "air_quality"
	Discharge  Dataset = "discharge"
)

// Var adalah satu variabel sumber beserta batas nilainya.
type Var struct {
	// Name adalah nama variabel di sumber, misal "temperature_2m".
	Name string
	// Unit adalah satuan persis seperti ditulis sumber; satuan lain berarti
	// sumber mengubah format dan payload ditolak.
	Unit string
	// Batas nilai yang masih masuk akal. Di luar ini hampir pasti data rusak.
	Min, Max float64
	// Integer berarti nilai wajib bilangan bulat (misal kode cuaca).
	Integer bool
}

// Spec adalah spesifikasi satu jenis deret.
type Spec struct {
	Dataset Dataset
	// Step adalah resolusi waktu (per jam atau harian).
	Step time.Duration
	Vars []Var
	// MaxSteps membatasi jumlah langkah per deret.
	MaxSteps int
	// Langkah harus berada di [fetchedAt-MaxPast, fetchedAt+MaxFuture].
	MaxPast, MaxFuture time.Duration
	// MaxCellOffset adalah selisih lintang/bujur terbesar (derajat) antara
	// titik yang diminta dan pusat sel yang dijawab sumber.
	MaxCellOffset float64
}

// Index mengembalikan posisi variabel name di Vars, atau -1.
func (s Spec) Index(name string) int {
	for i, v := range s.Vars {
		if v.Name == name {
			return i
		}
	}
	return -1
}

// Spesifikasi per jenis data. Urutan Vars adalah urutan kolom di seluruh
// ingest; nama dan satuannya mengikuti Open-Meteo (satuan bawaan).
var (
	WeatherSpec = Spec{
		Dataset: Weather, Step: time.Hour, MaxSteps: 24 * 7,
		MaxPast: 2 * day, MaxFuture: 6 * day, MaxCellOffset: 0.5,
		Vars: []Var{
			{Name: "temperature_2m", Unit: "°C", Min: -30, Max: 50},
			{Name: "relative_humidity_2m", Unit: "%", Min: 0, Max: 100},
			{Name: "precipitation", Unit: "mm", Min: 0, Max: 300},
			{Name: "weather_code", Unit: "wmo code", Min: 0, Max: 99, Integer: true},
			{Name: "cloud_cover", Unit: "%", Min: 0, Max: 100},
			{Name: "wind_speed_10m", Unit: "km/h", Min: 0, Max: 400},
			{Name: "wind_direction_10m", Unit: "°", Min: 0, Max: 360},
			{Name: "wind_gusts_10m", Unit: "km/h", Min: 0, Max: 500},
			{Name: "surface_pressure", Unit: "hPa", Min: 500, Max: 1100},
			{Name: "boundary_layer_height", Unit: "m", Min: 0, Max: 10000},
		},
	}
	AirQualitySpec = Spec{
		Dataset: AirQuality, Step: time.Hour, MaxSteps: 24 * 7,
		MaxPast: 2 * day, MaxFuture: 6 * day, MaxCellOffset: 0.5,
		Vars: []Var{
			{Name: "pm2_5", Unit: "μg/m³", Min: 0, Max: 5000},
			{Name: "pm10", Unit: "μg/m³", Min: 0, Max: 10000},
			{Name: "carbon_monoxide", Unit: "μg/m³", Min: 0, Max: 200000},
			{Name: "nitrogen_dioxide", Unit: "μg/m³", Min: 0, Max: 5000},
			{Name: "sulphur_dioxide", Unit: "μg/m³", Min: 0, Max: 10000},
			{Name: "ozone", Unit: "μg/m³", Min: 0, Max: 5000},
			{Name: "aerosol_optical_depth", Unit: "", Min: 0, Max: 20},
			{Name: "dust", Unit: "μg/m³", Min: 0, Max: 50000},
		},
	}
	DischargeSpec = Spec{
		Dataset: Discharge, Step: day, MaxSteps: 31,
		MaxPast: 10 * day, MaxFuture: 20 * day, MaxCellOffset: 0.1,
		Vars: []Var{
			{Name: "river_discharge", Unit: "m³/s", Min: 0, Max: 200000},
			{Name: "river_discharge_mean", Unit: "m³/s", Min: 0, Max: 200000},
			{Name: "river_discharge_median", Unit: "m³/s", Min: 0, Max: 200000},
			{Name: "river_discharge_max", Unit: "m³/s", Min: 0, Max: 200000},
			{Name: "river_discharge_min", Unit: "m³/s", Min: 0, Max: 200000},
			{Name: "river_discharge_p25", Unit: "m³/s", Min: 0, Max: 200000},
			{Name: "river_discharge_p75", Unit: "m³/s", Min: 0, Max: 200000},
		},
	}
)

const (
	day        = 24 * time.Hour
	maxTextLen = 100
	// GridDecimals adalah jumlah desimal koordinat di ID simpul grid.
	GridDecimals = 2
)

// LatLon adalah titik WGS84.
type LatLon struct {
	Lat, Lon float64
}

// Site adalah titik pantau.
type Site struct {
	ID string
	// Name dan River hanya untuk titik pantau sungai.
	Name, River string
	Requested   LatLon
}

var (
	gridID  = regexp.MustCompile(`^grid:(-?[0-9]{1,2}\.[0-9]{2}):(-?[0-9]{1,3}\.[0-9]{2})$`)
	riverID = regexp.MustCompile(`^river:[a-z0-9]+(-[a-z0-9]+)*$`)
	model   = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
)

// GridID membentuk ID simpul grid, misal "grid:-6.75:107.50".
func GridID(p LatLon) string {
	return "grid:" + strconv.FormatFloat(p.Lat, 'f', GridDecimals, 64) + ":" + strconv.FormatFloat(p.Lon, 'f', GridDecimals, 64)
}

// RiverID membentuk ID titik pantau sungai dari slug, misal "river:citarum-dayeuhkolot".
func RiverID(slug string) string { return "river:" + slug }

// ValidSiteID melaporkan apakah id berformat ID titik yang dikenal. ID grid
// harus cocok persis dengan koordinat yang dibulatkan ke GridDecimals.
func ValidSiteID(id string) bool {
	if len(id) > 64 {
		return false
	}
	if m := gridID.FindStringSubmatch(id); m != nil {
		lat, err1 := strconv.ParseFloat(m[1], 64)
		lon, err2 := strconv.ParseFloat(m[2], 64)
		return err1 == nil && err2 == nil && GridID(LatLon{lat, lon}) == id
	}
	return riverID.MatchString(id)
}

// Series adalah deret waktu satu titik dari satu model. Values[v][i] adalah
// nilai variabel Spec.Vars[v] pada Times[i]; nil berarti kosong.
type Series struct {
	Site      Site
	Cell      LatLon
	Elevation *float64
	Model     string
	Times     []time.Time
	Values    [][]*float64
}

// Compact membuang langkah yang semua nilainya kosong (misal di luar
// jangkauan model). Series asli tidak diubah.
func (s Series) Compact() Series {
	out := s
	out.Times = nil
	out.Values = make([][]*float64, len(s.Values))
	for i, t := range s.Times {
		empty := true
		for _, col := range s.Values {
			if i < len(col) && col[i] != nil {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		out.Times = append(out.Times, t)
		for v, col := range s.Values {
			var x *float64
			if i < len(col) {
				x = col[i]
			}
			out.Values[v] = append(out.Values[v], x)
		}
	}
	return out
}

// Validate memeriksa semua invarian terhadap spec. fetchedAt dipakai untuk
// menolak langkah di luar jendela waktu yang masuk akal. Semua pelanggaran
// dilaporkan sekaligus.
func (s Series) Validate(spec Spec, fetchedAt time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !ValidSiteID(s.Site.ID) {
		bad("ID titik %q tidak valid", s.Site.ID)
	}
	if !model.MatchString(s.Model) {
		bad("nama model %q tidak valid", s.Model)
	}
	for _, t := range []struct{ name, v string }{{"name", s.Site.Name}, {"river", s.Site.River}} {
		if err := checkText(t.v); err != nil {
			bad("%s: %w", t.name, err)
		}
	}
	for _, p := range []struct {
		name string
		p    LatLon
	}{{"titik diminta", s.Site.Requested}, {"sel model", s.Cell}} {
		if !InIndonesia(p.p) {
			bad("%s (%v, %v) di luar Indonesia", p.name, p.p.Lat, p.p.Lon)
		}
	}
	if d := math.Max(math.Abs(s.Cell.Lat-s.Site.Requested.Lat), math.Abs(s.Cell.Lon-s.Site.Requested.Lon)); !(d <= spec.MaxCellOffset) {
		bad("sel model (%v, %v) berjarak %.3f° dari titik diminta, batas %v°", s.Cell.Lat, s.Cell.Lon, d, spec.MaxCellOffset)
	}
	if s.Elevation != nil && !inRange(*s.Elevation, -500, 6000) {
		bad("elevasi %v m tidak masuk akal", *s.Elevation)
	}
	errs = append(errs, s.validateTimes(spec, fetchedAt)...)
	errs = append(errs, s.validateValues(spec)...)
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s %s: %w", ErrInvalid, spec.Dataset, s.Site.ID, errors.Join(errs...))
}

func (s Series) validateTimes(spec Spec, fetchedAt time.Time) []error {
	var errs []error
	if len(s.Times) == 0 || len(s.Times) > spec.MaxSteps {
		return append(errs, fmt.Errorf("%d langkah, harus 1..%d", len(s.Times), spec.MaxSteps))
	}
	lo, hi := fetchedAt.Add(-spec.MaxPast), fetchedAt.Add(spec.MaxFuture)
	step := int64(spec.Step / time.Second)
	for i, t := range s.Times {
		switch {
		case t.IsZero() || t.Location() != time.UTC:
			errs = append(errs, fmt.Errorf("langkah %d: waktu kosong atau bukan UTC", i))
		case t.Unix()%step != 0 || t.Nanosecond() != 0:
			errs = append(errs, fmt.Errorf("langkah %d: %s tidak sejajar dengan langkah %v", i, t.Format(time.RFC3339), spec.Step))
		case t.Before(lo) || t.After(hi):
			errs = append(errs, fmt.Errorf("langkah %d: %s di luar jendela %s..%s", i, t.Format(time.RFC3339), lo.Format(time.RFC3339), hi.Format(time.RFC3339)))
		case i > 0 && !t.After(s.Times[i-1]):
			errs = append(errs, fmt.Errorf("langkah %d: %s tidak urut atau ganda", i, t.Format(time.RFC3339)))
		}
	}
	return errs
}

func (s Series) validateValues(spec Spec) []error {
	if len(s.Values) != len(spec.Vars) {
		return []error{fmt.Errorf("%d variabel, spesifikasi %s punya %d", len(s.Values), spec.Dataset, len(spec.Vars))}
	}
	var errs []error
	filled := 0
	for v, col := range s.Values {
		vs := spec.Vars[v]
		if len(col) != len(s.Times) {
			errs = append(errs, fmt.Errorf("%s: %d nilai untuk %d langkah", vs.Name, len(col), len(s.Times)))
			continue
		}
		for i, x := range col {
			if x == nil {
				continue
			}
			filled++
			switch {
			case !inRange(*x, vs.Min, vs.Max):
				errs = append(errs, fmt.Errorf("%s langkah %d: %v di luar %v..%v", vs.Name, i, *x, vs.Min, vs.Max))
			case vs.Integer && *x != math.Trunc(*x):
				errs = append(errs, fmt.Errorf("%s langkah %d: %v bukan bilangan bulat", vs.Name, i, *x))
			}
		}
	}
	if filled == 0 && len(errs) == 0 {
		errs = append(errs, errors.New("semua nilai kosong"))
	}
	return errs
}

// InIndonesia melaporkan apakah p berada di kotak Indonesia yang longgar
// (lintang -12..7, bujur 94..142).
func InIndonesia(p LatLon) bool {
	return inRange(p.Lat, -12, 7) && inRange(p.Lon, 94, 142)
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
	}
	return nil
}
