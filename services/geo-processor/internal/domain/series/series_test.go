package series

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func gridRun(ds Dataset) Run {
	return Run{
		Site:    Site{ID: "grid:-7.00:107.50", Kind: SiteGrid, Location: Point{Lat: -7, Lon: 107.5}},
		Dataset: ds, Source: SourceOpenMeteo, Model: "best_match",
		Cell: Point{Lat: -6.99, Lon: 107.47}, Elevation: f(838),
		IssuedAt: now.Add(-20 * time.Minute), FetchedAt: now.Add(-20 * time.Minute), ArchiveKey: "openmeteo-cuaca/2026/09/24/124000Z-abc.json.gz",
	}
}

func weatherRun() WeatherRun {
	code := 3
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	return WeatherRun{Run: gridRun(Weather), Steps: []WeatherStep{
		{
			ValidTime: day, TemperatureC: f(21.8), HumidityPct: f(70), PrecipitationMM: f(0), WeatherCode: &code, CloudCoverPct: f(93),
			WindSpeedKmh: f(5.1), WindFromDeg: f(135), WindGustKmh: f(14), PressureHPa: f(922.5), BoundaryLayerM: f(280),
		},
		{ValidTime: day.Add(time.Hour), TemperatureC: f(23.9)},
	}}
}

func bmkgRun() WeatherRun {
	code := 61
	loc := Point{Lat: -6.9, Lon: 107.61}
	return WeatherRun{Run: Run{
		Site:    Site{ID: "adm4:32.73.01.1001", Kind: SiteRegion, Name: "Cihapit", Location: loc, RegionCode: "32.73.01.1001"},
		Dataset: Weather, Source: SourceBMKG, Model: ModelBMKG, Cell: loc,
		IssuedAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), FetchedAt: now,
	}, Steps: []WeatherStep{{ValidTime: now, TemperatureC: f(24), HumidityPct: f(90), WeatherCode: &code, VisibilityM: f(8000)}}}
}

func airRun() AirQualityRun {
	r := gridRun(AirQuality)
	r.Model = "cams_global"
	return AirQualityRun{Run: r, Steps: []AirQualityStep{{
		ValidTime: now, PM25: f(135), PM10: f(135), CO: f(999), NO2: f(54.1),
		SO2: f(29.5), O3: f(23), AerosolOpticalDepth: f(0.57), Dust: f(0),
	}}}
}

func dischargeRun() DischargeRun {
	r := gridRun(Discharge)
	r.Site = Site{ID: "river:citarum-dayeuhkolot", Kind: SiteRiver, Name: "Dayeuhkolot", River: "Citarum", Location: Point{Lat: -6.975, Lon: 107.625}}
	r.Model, r.Cell = "glofas_v4", Point{Lat: -6.975, Lon: 107.625}
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	return DischargeRun{Run: r, Steps: []DischargeStep{
		{ValidDate: day, Discharge: f(8.79), Mean: f(9.36), Median: f(8.77), Max: f(17.79), Min: f(5.3), P25: f(7.53), P75: f(10.4)},
		{ValidDate: day.AddDate(0, 0, 1), Discharge: f(4.09)},
	}}
}

func TestValidRunsPass(t *testing.T) {
	for name, err := range map[string]error{
		"cuaca grid": weatherRun().Validate(now), "cuaca BMKG": bmkgRun().Validate(now),
		"udara": airRun().Validate(now), "debit": dischargeRun().Validate(now),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRunInvariants(t *testing.T) {
	for name, mut := range map[string]func(*Run){
		"ID":                   func(r *Run) { r.Site.ID = "grid:-7:107.5" },
		"jenis vs ID":          func(r *Run) { r.Site.Kind = SiteRiver },
		"lokasi":               func(r *Run) { r.Site.Location = Point{Lat: 52, Lon: 13} },
		"nama spasi":           func(r *Run) { r.Site.Name = "x " },
		"nama panjang":         func(r *Run) { r.Site.Name = strings.Repeat("x", 101) },
		"UTF-8":                func(r *Run) { r.Site.Name = "\xff" },
		"sungai di grid":       func(r *Run) { r.Site.River = "Citarum" },
		"kode wilayah grid":    func(r *Run) { r.Site.RegionCode = "32.73.01.1001" },
		"model":                func(r *Run) { r.Model = "Best-Match" },
		"sumber":               func(r *Run) { r.Source = "nasa" },
		"sel":                  func(r *Run) { r.Cell = Point{Lat: math.NaN(), Lon: 107} },
		"elevasi":              func(r *Run) { r.Elevation = f(-600) },
		"waktu terbit kosong":  func(r *Run) { r.IssuedAt = time.Time{} },
		"bukan UTC":            func(r *Run) { r.FetchedAt = r.FetchedAt.In(time.FixedZone("WIB", 7*3600)) },
		"terbit setelah ambil": func(r *Run) { r.IssuedAt = r.FetchedAt.Add(2 * time.Hour) },
		"ambil di masa depan":  func(r *Run) { r.FetchedAt, r.IssuedAt = now.Add(3*time.Hour), now.Add(3*time.Hour) },
		"kunci arsip":          func(r *Run) { r.ArchiveKey = "a b" },
		"Open-Meteo per desa": func(r *Run) {
			r.Site = Site{ID: "adm4:32.73.01.1001", Kind: SiteRegion, Location: r.Site.Location, RegionCode: "32.73.01.1001"}
		},
	} {
		w := weatherRun()
		mut(&w.Run)
		if err := w.Validate(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, mut := range map[string]func(*WeatherRun){
		"BMKG model":           func(w *WeatherRun) { w.Model = "best_match" },
		"BMKG kode wilayah":    func(w *WeatherRun) { w.Site.RegionCode = "32.73.01.1002" },
		"BMKG kode tidak adm4": func(w *WeatherRun) { w.Site.ID, w.Site.RegionCode = "adm4:32.73.01.1001", "" },
		"BMKG untuk grid": func(w *WeatherRun) {
			w.Site = Site{ID: "grid:-7.00:107.50", Kind: SiteGrid, Location: w.Site.Location}
		},
	} {
		w := bmkgRun()
		mut(&w)
		if err := w.Validate(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestStepInvariants(t *testing.T) {
	for name, mut := range map[string]func(*WeatherRun){
		"tanpa langkah":  func(w *WeatherRun) { w.Steps = nil },
		"langkah kosong": func(w *WeatherRun) { w.Steps[1] = WeatherStep{ValidTime: w.Steps[1].ValidTime} },
		"tidak urut":     func(w *WeatherRun) { w.Steps[1].ValidTime = w.Steps[0].ValidTime },
		"bukan UTC":      func(w *WeatherRun) { w.Steps[0].ValidTime = w.Steps[0].ValidTime.Local() },
		"jangkauan":      func(w *WeatherRun) { w.Steps[1].ValidTime = w.IssuedAt.AddDate(0, 0, 25) },
		"kelembapan":     func(w *WeatherRun) { w.Steps[0].HumidityPct = f(100.5) },
		"kode cuaca":     func(w *WeatherRun) { c := 1000; w.Steps[0].WeatherCode = &c },
		"NaN":            func(w *WeatherRun) { w.Steps[0].TemperatureC = f(math.NaN()) },
		"jenis data":     func(w *WeatherRun) { w.Dataset = AirQuality },
		"terlalu banyak": func(w *WeatherRun) { w.Steps = make([]WeatherStep, maxSteps+1) },
	} {
		w := weatherRun()
		mut(&w)
		if err := w.Validate(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	a := airRun()
	a.Steps[0].PM25 = f(-1)
	a.Dataset = Weather
	if err := a.Validate(now); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "PM2,5") {
		t.Errorf("udara: %v", err)
	}
	a = airRun()
	a.Steps[0] = AirQualityStep{ValidTime: now}
	if err := a.Validate(now); !errors.Is(err, ErrInvalid) {
		t.Errorf("udara kosong: %v", err)
	}
}

func TestDischargeInvariants(t *testing.T) {
	for name, mut := range map[string]func(*DischargeRun){
		"bukan tengah malam": func(d *DischargeRun) { d.Steps[0].ValidDate = d.Steps[0].ValidDate.Add(time.Hour) },
		"min > maks":         func(d *DischargeRun) { d.Steps[0].Min = f(20) },
		"median > P75":       func(d *DischargeRun) { d.Steps[0].Median = f(11) },
		"rata-rata > maks":   func(d *DischargeRun) { d.Steps[0].Mean = f(18) },
		"negatif":            func(d *DischargeRun) { d.Steps[1].Discharge = f(-0.1) },
		"kosong":             func(d *DischargeRun) { d.Steps[1] = DischargeStep{ValidDate: d.Steps[1].ValidDate} },
		"titik grid":         func(d *DischargeRun) { d.Site = gridRun(Discharge).Site },
		"jenis data":         func(d *DischargeRun) { d.Dataset = Weather },
		"sungai tanpa nama":  func(d *DischargeRun) { d.Site.Name = "" },
	} {
		d := dischargeRun()
		mut(&d)
		if err := d.Validate(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Selisih yang hilang di presisi real tidak dianggap pelanggaran urutan.
	d := dischargeRun()
	d.Steps[0].P75, d.Steps[0].Max = f(17.790000001), f(17.79)
	if err := d.Validate(now); err != nil {
		t.Errorf("presisi real: %v", err)
	}
}

func TestKindOf(t *testing.T) {
	for id, want := range map[string]SiteKind{
		"adm4:32.73.01.1001": SiteRegion, "grid:-6.75:107.50": SiteGrid, "river:citarum-nanjung": SiteRiver,
	} {
		if got, ok := KindOf(id); !ok || got != want {
			t.Errorf("%s: %s %v", id, got, ok)
		}
	}
	for _, bad := range []string{"", "adm4:32.73.01", "grid:x", "river:A", "sensor:1", strings.Repeat("river:a", 20)} {
		if _, ok := KindOf(bad); ok {
			t.Errorf("%q seharusnya tidak valid", bad)
		}
	}
	if RegionSiteID("32.73.01.1001") != "adm4:32.73.01.1001" {
		t.Error("RegionSiteID")
	}
}

// FuzzWeatherValidate: Validate tidak pernah panik, dan deret yang lolos
// memenuhi constraint database yang paling mudah dilanggar (urutan waktu,
// batas kelembapan) sehingga tidak berakhir di DLQ karena ditolak PostgreSQL.
func FuzzWeatherValidate(f *testing.F) {
	f.Add(int64(0), int64(3600), 70.0, 21.8, 1)
	f.Add(int64(3600), int64(3600), 100.0, -40.0, 0)
	f.Add(int64(-86400*11), int64(1), math.NaN(), 0.0, 999)
	f.Fuzz(func(t *testing.T, off1, off2 int64, hum, temp float64, code int) {
		w := weatherRun()
		w.Steps[0].ValidTime = w.IssuedAt.Add(time.Duration(off1%(40*86400)) * time.Second)
		w.Steps[1].ValidTime = w.IssuedAt.Add(time.Duration(off2%(40*86400)) * time.Second)
		w.Steps[0].HumidityPct, w.Steps[0].TemperatureC, w.Steps[0].WeatherCode = &hum, &temp, &code
		if err := w.Validate(now); err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("galat tanpa ErrInvalid: %v", err)
			}
			return
		}
		if !w.Steps[1].ValidTime.After(w.Steps[0].ValidTime) || !(float32(hum) >= 0 && float32(hum) <= 100) || code < 0 || code > 999 ||
			w.Steps[0].ValidTime.Before(w.IssuedAt.AddDate(0, 0, -10)) || w.Steps[1].ValidTime.After(w.IssuedAt.AddDate(0, 0, 20)) {
			t.Fatalf("lolos padahal melanggar constraint: %+v", w.Steps)
		}
	})
}
