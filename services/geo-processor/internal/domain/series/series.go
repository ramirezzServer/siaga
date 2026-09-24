// Package series berisi aturan murni untuk deret waktu yang disimpan di
// schema ts: prakiraan cuaca (BMKG per kelurahan/desa, Open-Meteo per simpul
// grid), prakiraan kualitas udara model, debit sungai, dan pengukuran stasiun
// kualitas udara. Invarian di sini sama dengan constraint migrasi
// 00004_ts_series.sql dan 00005_ts_observation.sql (ADR 0012–0013).
package series

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalid menandai deret yang melanggar invarian domain. Pesan dengan
// galat ini tidak akan berhasil bila dicoba ulang.
var ErrInvalid = errors.New("deret waktu tidak valid")

// Dataset adalah jenis data deret.
type Dataset string

// Jenis data (kolom ts.series.dataset).
const (
	Weather    Dataset = "weather"
	AirQuality Dataset = "air_quality"
	Discharge  Dataset = "discharge"
	// AirQualityObs adalah pengukuran stasiun (bukan model).
	AirQualityObs Dataset = "aq_observation"
)

// Source adalah penerbit data.
type Source string

// Sumber yang dikenal (kolom ts.series.source).
const (
	SourceBMKG      Source = "bmkg"
	SourceOpenMeteo Source = "openmeteo"
	SourceOpenAQ    Source = "openaq"
)

// SiteKind adalah jenis titik pantau.
type SiteKind string

// Jenis titik (kolom ts.site.kind).
const (
	SiteRegion SiteKind = "region"
	SiteGrid   SiteKind = "grid"
	SiteRiver  SiteKind = "river"
	// SiteStation adalah stasiun pengukur, ID "openaq:<id lokasi>".
	SiteStation SiteKind = "station"
)

// ModelBMKG adalah nama model untuk prakiraan BMKG per kelurahan/desa.
const ModelBMKG = "bmkg"

// ModelSensor adalah nama "model" deret pengukuran stasiun: nilainya dari
// alat ukur, bukan keluaran model.
const ModelSensor = "sensor"

// Batas yang sama dengan constraint database.
const (
	maxTextLen = 100
	// MaxClockSkew adalah selisih jam terbesar yang ditoleransi antara ingest
	// dan geo-processor.
	MaxClockSkew = time.Hour
	maxSteps     = 400
)

var (
	adm4ID  = regexp.MustCompile(`^adm4:([0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4})$`)
	gridID  = regexp.MustCompile(`^grid:-?[0-9]{1,2}\.[0-9]{2}:-?[0-9]{1,3}\.[0-9]{2}$`)
	riverID = regexp.MustCompile(`^river:[a-z0-9]+(-[a-z0-9]+)*$`)
	stnID   = regexp.MustCompile(`^openaq:[1-9][0-9]{0,11}$`)
	adm4    = regexp.MustCompile(`^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$`)
	model   = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
)

// RegionSiteID membentuk ID titik kelurahan/desa, misal "adm4:32.73.01.1001".
func RegionSiteID(code string) string { return "adm4:" + code }

// KindOf mengembalikan jenis titik dari ID-nya; ok false bila ID tidak valid.
func KindOf(id string) (SiteKind, bool) {
	switch {
	case len(id) > 64:
		return "", false
	case adm4ID.MatchString(id):
		return SiteRegion, true
	case gridID.MatchString(id):
		return SiteGrid, true
	case riverID.MatchString(id):
		return SiteRiver, true
	case stnID.MatchString(id):
		return SiteStation, true
	default:
		return "", false
	}
}

// Point adalah titik WGS84.
type Point struct {
	Lat, Lon float64
}

// InIndonesia melaporkan apakah p berada di kotak Indonesia yang longgar.
func (p Point) InIndonesia() bool { return inRange(p.Lat, -12, 7) && inRange(p.Lon, 94, 142) }

// Site adalah titik pantau.
type Site struct {
	ID       string
	Kind     SiteKind
	Name     string
	River    string
	Location Point
	// RegionCode hanya untuk titik kelurahan/desa (sama dengan kode di ID).
	// Untuk grid dan sungai dihitung penyimpanan dari ref.region.
	RegionCode string
}

func (s Site) validate() []error {
	var errs []error
	kind, ok := KindOf(s.ID)
	switch {
	case !ok:
		errs = append(errs, fmt.Errorf("ID titik %q tidak valid", s.ID))
	case kind != s.Kind:
		errs = append(errs, fmt.Errorf("jenis titik %q tidak cocok dengan ID %s", s.Kind, s.ID))
	}
	if !s.Location.InIndonesia() {
		errs = append(errs, fmt.Errorf("lokasi titik (%v, %v) di luar Indonesia", s.Location.Lat, s.Location.Lon))
	}
	for _, t := range []struct{ name, v string }{{"nama", s.Name}, {"sungai", s.River}} {
		if err := checkText(t.v); err != nil {
			errs = append(errs, fmt.Errorf("%s titik: %w", t.name, err))
		}
	}
	if (s.Kind == SiteRiver) != (s.River != "") || (s.Kind == SiteRiver && s.Name == "") {
		errs = append(errs, errors.New("titik pantau sungai wajib bernama dan menyebut sungai; titik lain tidak"))
	}
	switch {
	case s.Kind == SiteRegion && "adm4:"+s.RegionCode != s.ID:
		errs = append(errs, fmt.Errorf("kode wilayah %q tidak cocok dengan ID %s", s.RegionCode, s.ID))
	case s.Kind != SiteRegion && s.RegionCode != "":
		errs = append(errs, errors.New("kode wilayah titik grid, sungai, dan stasiun dihitung penyimpanan, bukan diisi"))
	case s.RegionCode != "" && !adm4.MatchString(s.RegionCode):
		errs = append(errs, fmt.Errorf("kode wilayah %q bukan adm4", s.RegionCode))
	}
	return errs
}

// Run adalah kepala satu keluaran model di satu titik: satu event raw.
type Run struct {
	Site    Site
	Dataset Dataset
	Source  Source
	Model   string
	// Cell adalah pusat sel model yang dijawab sumber (BMKG: titik acuan desa).
	Cell      Point
	Elevation *float64
	// IssuedAt adalah waktu terbit keluaran (BMKG: waktu analisis; Open-Meteo:
	// waktu ingest pertama kali menerima isi ini).
	IssuedAt   time.Time
	FetchedAt  time.Time
	ArchiveKey string
}

func (r Run) validate(now time.Time) []error {
	errs := r.Site.validate()
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !model.MatchString(r.Model) {
		bad("nama model %q tidak valid", r.Model)
	}
	switch r.Source {
	case SourceBMKG:
		if r.Dataset != Weather || r.Site.Kind != SiteRegion || r.Model != ModelBMKG {
			bad("BMKG hanya prakiraan cuaca per kelurahan/desa dengan model %q", ModelBMKG)
		}
	case SourceOpenMeteo:
		if r.Site.Kind == SiteRegion || r.Site.Kind == SiteStation || r.Dataset == AirQualityObs {
			bad("Open-Meteo hanya keluaran model per simpul grid atau titik sungai")
		}
	case SourceOpenAQ:
		if r.Dataset != AirQualityObs || r.Site.Kind != SiteStation || r.Model != ModelSensor {
			bad("OpenAQ hanya pengukuran stasiun dengan model %q", ModelSensor)
		}
	default:
		bad("sumber %q tidak dikenal", r.Source)
	}
	if !r.Cell.InIndonesia() {
		bad("sel model (%v, %v) di luar Indonesia", r.Cell.Lat, r.Cell.Lon)
	}
	if r.Elevation != nil && !inRange(*r.Elevation, -500, 6000) {
		bad("elevasi %v m tidak masuk akal", *r.Elevation)
	}
	switch {
	case !isUTC(r.IssuedAt) || !isUTC(r.FetchedAt):
		bad("waktu terbit dan waktu ambil wajib terisi dan UTC")
	case r.IssuedAt.After(r.FetchedAt.Add(MaxClockSkew)):
		bad("waktu terbit %s setelah waktu ambil %s", r.IssuedAt.Format(time.RFC3339), r.FetchedAt.Format(time.RFC3339))
	case r.FetchedAt.After(now.Add(MaxClockSkew)):
		bad("waktu ambil %s di masa depan", r.FetchedAt.Format(time.RFC3339))
	}
	if len(r.ArchiveKey) > 512 || !utf8.ValidString(r.ArchiveKey) || strings.ContainsAny(r.ArchiveKey, " \t\r\n") {
		bad("kunci arsip %q tidak valid", r.ArchiveKey)
	}
	return errs
}

// horizon adalah jangkauan waktu berlaku terhadap waktu terbit (sama dengan
// constraint *_horizon di database).
type horizon struct{ before, after time.Duration }

var (
	gridHorizon      = horizon{before: 10 * 24 * time.Hour, after: 20 * 24 * time.Hour}
	dischargeHorizon = horizon{before: 31 * 24 * time.Hour, after: 31 * 24 * time.Hour}
)

// checkTimes memeriksa urutan dan jangkauan waktu langkah.
func checkTimes(times []time.Time, issued time.Time, h horizon, midnight bool) []error {
	var errs []error
	if len(times) == 0 || len(times) > maxSteps {
		return []error{fmt.Errorf("%d langkah, harus 1..%d", len(times), maxSteps)}
	}
	for i, t := range times {
		switch {
		case !isUTC(t):
			errs = append(errs, fmt.Errorf("langkah %d: waktu kosong atau bukan UTC", i))
		case t.Before(issued.Add(-h.before)) || t.After(issued.Add(h.after)):
			errs = append(errs, fmt.Errorf("langkah %d: %s di luar jangkauan waktu terbit", i, t.Format(time.RFC3339)))
		case midnight && (t.Unix()%86400 != 0 || t.Nanosecond() != 0):
			errs = append(errs, fmt.Errorf("langkah %d: %s bukan pukul 00.00 UTC", i, t.Format(time.RFC3339)))
		case i > 0 && !t.After(times[i-1]):
			errs = append(errs, fmt.Errorf("langkah %d: %s tidak urut atau ganda", i, t.Format(time.RFC3339)))
		}
	}
	return errs
}

// field adalah satu nilai opsional beserta batasnya.
type field struct {
	name   string
	v      *float64
	lo, hi float64
}

// checkFields memeriksa batas setiap nilai yang terisi dan menghitung isinya.
func checkFields(step int, fs []field) (filled int, errs []error) {
	for _, f := range fs {
		if f.v == nil {
			continue
		}
		filled++
		if !inRange(*f.v, f.lo, f.hi) {
			errs = append(errs, fmt.Errorf("langkah %d: %s %v di luar %v..%v", step, f.name, *f.v, f.lo, f.hi))
		}
	}
	return filled, errs
}

func wrap(dataset Dataset, id string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s %s: %w", ErrInvalid, dataset, id, errors.Join(errs...))
}

func isUTC(t time.Time) bool { return !t.IsZero() && t.Location() == time.UTC }

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
