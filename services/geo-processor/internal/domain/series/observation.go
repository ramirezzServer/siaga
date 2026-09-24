package series

import (
	"fmt"
	"regexp"
	"time"
)

// Parameter pengukuran stasiun dan satuannya (sama dengan domain airquality
// ingest dan constraint ts.aq_observation).
const (
	UnitMicrogram = "µg/m³"
	UnitPPM       = "ppm"
	UnitPPB       = "ppb"
)

// obsLimits adalah batas atas nilai per (parameter, satuan).
var obsLimits = map[string]map[string]float64{
	"pm25": {UnitMicrogram: 5000},
	"pm10": {UnitMicrogram: 10000},
	"no2":  {UnitMicrogram: 5000, UnitPPM: 2.7, UnitPPB: 2700},
	"o3":   {UnitMicrogram: 5000, UnitPPM: 2.6, UnitPPB: 2600},
	"so2":  {UnitMicrogram: 10000, UnitPPM: 3.9, UnitPPB: 3900},
	"co":   {UnitMicrogram: 200000, UnitPPM: 175, UnitPPB: 175000},
}

// ObservationLimit mengembalikan batas atas nilai parameter dalam satuan
// unit; ok false bila kombinasinya tidak diterima.
func ObservationLimit(parameter, unit string) (float64, bool) {
	hi, ok := obsLimits[parameter][unit]
	return hi, ok
}

// Batas pengukuran.
const (
	maxReadings = 64
	// MaxObservationAge adalah umur nilai terbesar yang diterima terhadap
	// waktu ambil (ingest sudah membuang nilai lebih tua dari 1 hari).
	MaxObservationAge = 7 * 24 * time.Hour
)

var timezone = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+-]+){0,2}$`)

// Station adalah keterangan stasiun pengukur.
type Station struct {
	Provider  string
	Owner     string
	Locality  string
	IsMonitor bool
	Timezone  string
}

// Reading adalah nilai satu sensor.
type Reading struct {
	SensorID   int64
	Parameter  string
	Unit       string
	Value      float64
	ObservedAt time.Time
}

// StationObservation adalah nilai terbaru semua sensor di satu stasiun:
// satu event raw.aq.openaq. Run.IssuedAt adalah waktu ukur terbaru.
type StationObservation struct {
	Run
	Station  Station
	Readings []Reading
}

// Validate memeriksa semua invarian; now adalah jam geo-processor.
func (o StationObservation) Validate(now time.Time) error {
	errs := o.validate(now)
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if o.Dataset != AirQualityObs {
		bad("jenis data %q, harus %q", o.Dataset, AirQualityObs)
	}
	st := o.Station
	for _, f := range []struct{ name, v string }{
		{"penyedia", st.Provider}, {"pemilik", st.Owner}, {"daerah", st.Locality},
	} {
		if err := checkText(f.v); err != nil {
			bad("%s stasiun: %v", f.name, err)
		}
	}
	if st.Provider == "" || o.Site.Name == "" {
		bad("nama dan penyedia stasiun wajib diisi")
	}
	if st.Timezone != "" && (len(st.Timezone) > 64 || !timezone.MatchString(st.Timezone)) {
		bad("zona waktu %q tidak valid", st.Timezone)
	}
	if len(o.Readings) == 0 || len(o.Readings) > maxReadings {
		bad("%d nilai sensor, harus 1..%d", len(o.Readings), maxReadings)
	}
	var newest time.Time
	for i, r := range o.Readings {
		hi, ok := ObservationLimit(r.Parameter, r.Unit)
		switch {
		case r.SensorID <= 0:
			bad("ID sensor %d tidak valid", r.SensorID)
		case i > 0 && r.SensorID <= o.Readings[i-1].SensorID:
			bad("sensor %d tidak urut atau ganda", r.SensorID)
		case !ok:
			bad("sensor %d: %s dalam %q tidak diterima", r.SensorID, r.Parameter, r.Unit)
		case !inRange(r.Value, 0, hi):
			bad("sensor %d: %s %v %s di luar 0..%v", r.SensorID, r.Parameter, r.Value, r.Unit, hi)
		}
		switch t := r.ObservedAt; {
		case !isUTC(t):
			bad("sensor %d: waktu ukur kosong atau bukan UTC", r.SensorID)
		case t.After(o.FetchedAt.Add(MaxClockSkew)) || t.Before(o.FetchedAt.Add(-MaxObservationAge)):
			bad("sensor %d: waktu ukur %s di luar jendela waktu ambil", r.SensorID, t.Format(time.RFC3339))
		case t.After(newest):
			newest = t
		}
	}
	if len(o.Readings) > 0 && !newest.IsZero() && !o.IssuedAt.Equal(newest) {
		bad("waktu terbit %s harus waktu ukur terbaru %s", o.IssuedAt.Format(time.RFC3339), newest.Format(time.RFC3339))
	}
	return wrap(AirQualityObs, o.Site.ID, errs)
}
