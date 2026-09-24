package series

import (
	"fmt"
	"math"
	"time"
)

// WeatherStep adalah satu langkah prakiraan cuaca. Nilai nil berarti kosong.
type WeatherStep struct {
	ValidTime       time.Time
	TemperatureC    *float64
	HumidityPct     *float64
	PrecipitationMM *float64
	WeatherCode     *int
	CloudCoverPct   *float64
	WindSpeedKmh    *float64
	WindFromDeg     *float64
	WindGustKmh     *float64
	PressureHPa     *float64
	BoundaryLayerM  *float64
	VisibilityM     *float64
}

// WeatherRun adalah satu keluaran prakiraan cuaca di satu titik.
type WeatherRun struct {
	Run
	Steps []WeatherStep
}

// Validate memeriksa semua invarian; now adalah jam geo-processor.
func (w WeatherRun) Validate(now time.Time) error {
	errs := w.validate(now)
	if w.Dataset != Weather {
		errs = append(errs, fmt.Errorf("jenis data %q, harus %q", w.Dataset, Weather))
	}
	times := make([]time.Time, len(w.Steps))
	for i, s := range w.Steps {
		times[i] = s.ValidTime
		var code *float64
		if s.WeatherCode != nil {
			c := float64(*s.WeatherCode)
			code = &c
		}
		filled, e := checkFields(i, []field{
			{"suhu", s.TemperatureC, -30, 50},
			{"kelembapan", s.HumidityPct, 0, 100},
			{"curah hujan", s.PrecipitationMM, 0, 500},
			{"kode cuaca", code, 0, 999},
			{"tutupan awan", s.CloudCoverPct, 0, 100},
			{"kecepatan angin", s.WindSpeedKmh, 0, 400},
			{"arah angin", s.WindFromDeg, 0, 360},
			{"hembusan angin", s.WindGustKmh, 0, 500},
			{"tekanan", s.PressureHPa, 500, 1100},
			{"lapisan batas", s.BoundaryLayerM, 0, 10000},
			{"jarak pandang", s.VisibilityM, 0, 1_000_000},
		})
		errs = append(errs, e...)
		if filled == 0 {
			errs = append(errs, fmt.Errorf("langkah %d: semua nilai kosong", i))
		}
	}
	errs = append(errs, checkTimes(times, w.IssuedAt, gridHorizon, false)...)
	return wrap(Weather, w.Site.ID, errs)
}

// AirQualityStep adalah satu langkah prakiraan kualitas udara (µg/m³).
type AirQualityStep struct {
	ValidTime           time.Time
	PM25                *float64
	PM10                *float64
	CO                  *float64
	NO2                 *float64
	SO2                 *float64
	O3                  *float64
	AerosolOpticalDepth *float64
	Dust                *float64
}

// AirQualityRun adalah satu keluaran prakiraan kualitas udara di satu titik.
type AirQualityRun struct {
	Run
	Steps []AirQualityStep
}

// Validate memeriksa semua invarian; now adalah jam geo-processor.
func (a AirQualityRun) Validate(now time.Time) error {
	errs := a.validate(now)
	if a.Dataset != AirQuality {
		errs = append(errs, fmt.Errorf("jenis data %q, harus %q", a.Dataset, AirQuality))
	}
	times := make([]time.Time, len(a.Steps))
	for i, s := range a.Steps {
		times[i] = s.ValidTime
		filled, e := checkFields(i, []field{
			{"PM2,5", s.PM25, 0, 5000},
			{"PM10", s.PM10, 0, 10000},
			{"CO", s.CO, 0, 200000},
			{"NO2", s.NO2, 0, 5000},
			{"SO2", s.SO2, 0, 10000},
			{"O3", s.O3, 0, 5000},
			{"AOD", s.AerosolOpticalDepth, 0, 20},
			{"debu", s.Dust, 0, 50000},
		})
		errs = append(errs, e...)
		if filled == 0 {
			errs = append(errs, fmt.Errorf("langkah %d: semua nilai kosong", i))
		}
	}
	errs = append(errs, checkTimes(times, a.IssuedAt, gridHorizon, false)...)
	return wrap(AirQuality, a.Site.ID, errs)
}

// DischargeStep adalah debit satu hari (m³/s). Statistik ensemble kosong
// untuk hari yang sudah lewat.
type DischargeStep struct {
	ValidDate time.Time
	Discharge *float64
	Mean      *float64
	Median    *float64
	Max       *float64
	Min       *float64
	P25       *float64
	P75       *float64
}

// DischargeRun adalah satu keluaran prakiraan debit di satu titik pantau sungai.
type DischargeRun struct {
	Run
	Steps []DischargeStep
}

// Validate memeriksa semua invarian; now adalah jam geo-processor.
func (d DischargeRun) Validate(now time.Time) error {
	errs := d.validate(now)
	if d.Dataset != Discharge {
		errs = append(errs, fmt.Errorf("jenis data %q, harus %q", d.Dataset, Discharge))
	}
	if d.Site.Kind != SiteRiver {
		errs = append(errs, fmt.Errorf("debit hanya untuk titik pantau sungai, bukan %q", d.Site.Kind))
	}
	times := make([]time.Time, len(d.Steps))
	for i, s := range d.Steps {
		times[i] = s.ValidDate
		fs := []field{
			{"debit", s.Discharge, 0, 200000},
			{"rata-rata ensemble", s.Mean, 0, 200000},
			{"median ensemble", s.Median, 0, 200000},
			{"maksimum ensemble", s.Max, 0, 200000},
			{"minimum ensemble", s.Min, 0, 200000},
			{"P25 ensemble", s.P25, 0, 200000},
			{"P75 ensemble", s.P75, 0, 200000},
		}
		filled, e := checkFields(i, fs)
		errs = append(errs, e...)
		if filled == 0 {
			errs = append(errs, fmt.Errorf("langkah %d: semua nilai kosong", i))
		}
		errs = append(errs, ensembleOrder(i, s)...)
	}
	errs = append(errs, checkTimes(times, d.IssuedAt, dischargeHorizon, true)...)
	return wrap(Discharge, d.Site.ID, errs)
}

// ensembleOrder memastikan min ≤ P25 ≤ median ≤ P75 ≤ maks dan min ≤ rata-rata
// ≤ maks, dilewati bila salah satu kosong. Pembandingan dilakukan dalam
// presisi real (float32), sama dengan kolom database, supaya dua nilai yang
// hampir sama tidak lolos di sini lalu ditolak constraint.
func ensembleOrder(step int, s DischargeStep) []error {
	var errs []error
	for _, p := range []struct {
		lo, hi         *float64
		loName, hiName string
	}{
		{s.Min, s.Max, "minimum", "maksimum"},
		{s.Min, s.P25, "minimum", "P25"},
		{s.P25, s.Median, "P25", "median"},
		{s.Median, s.P75, "median", "P75"},
		{s.P75, s.Max, "P75", "maksimum"},
		{s.Min, s.Mean, "minimum", "rata-rata"},
		{s.Mean, s.Max, "rata-rata", "maksimum"},
	} {
		if p.lo == nil || p.hi == nil || math.IsNaN(*p.lo) || math.IsNaN(*p.hi) {
			continue
		}
		if float32(*p.lo) > float32(*p.hi) {
			errs = append(errs, fmt.Errorf("langkah %d: %s ensemble %v > %s %v", step, p.loName, *p.lo, p.hiName, *p.hi))
		}
	}
	return errs
}
