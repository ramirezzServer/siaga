package main

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/nopub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestConfig(t *testing.T) {
	s, err := config(lookup(map[string]string{"INGEST_CONNECTORS": " bmkg-autogempa, ,usgs-2.5-day ", "LOG_LEVEL": "debug"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.connectors) != 2 || s.connectors[1] != "usgs-2.5-day" || s.logLevel != slog.LevelDebug || s.natsURL == "" {
		t.Fatalf("%+v", s)
	}
	if _, err := config(lookup(map[string]string{"LOG_LEVEL": "cerewet"})); err == nil {
		t.Fatal("level log salah harus ditolak")
	}
}

func TestConnectorsAndBudgets(t *testing.T) {
	s, _ := config(lookup(nil))
	lim, err := budgets()
	if err != nil {
		t.Fatal(err)
	}
	specs := connectors(s)
	names := map[string]bool{}
	for _, c := range specs {
		if names[c.conn.Name()] {
			t.Fatalf("nama konektor ganda %s", c.conn.Name())
		}
		names[c.conn.Name()] = true
		if lim[c.budget] == nil || c.interval <= 0 {
			t.Fatalf("%s tanpa anggaran atau interval", c.conn.Name())
		}
	}
	if len(specs) != 4 {
		t.Fatalf("ingin 4 konektor, dapat %d", len(specs))
	}
	// Anggaran BMKG harus cukup untuk semua konektor BMKG pada intervalnya.
	var perMinute float64
	for _, c := range specs {
		if c.budget == "bmkg" {
			perMinute += float64(60e9) / float64(c.interval)
		}
	}
	if perMinute > 55 {
		t.Fatalf("konektor BMKG butuh %.1f req/menit, melebihi anggaran", perMinute)
	}
}

func TestConfigWeatherDefaultsAndErrors(t *testing.T) {
	s, err := config(lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.forecastInterval != 6*time.Hour || s.forecastLimit != 0 || !slices.Equal(s.capProvinces, []string{"32"}) ||
		!slices.Equal(s.capLanguages, []string{"en"}) || len(s.forecastFocus) != 4 || s.capBaseURL == "" || s.forecastURL == "" {
		t.Fatalf("%+v", s)
	}
	for _, bad := range []map[string]string{
		{"INGEST_FORECAST_INTERVAL": "sebentar"},
		{"INGEST_FORECAST_INTERVAL": "30s"},
		{"INGEST_FORECAST_LIMIT": "-1"},
		{"INGEST_FORECAST_LIMIT": "banyak"},
	} {
		if _, err := config(lookup(bad)); err == nil {
			t.Errorf("config(%v) seharusnya gagal", bad)
		}
	}
}

func TestPlan(t *testing.T) {
	lim, err := budgets()
	if err != nil {
		t.Fatal(err)
	}
	d := deps{clock: sysclock.Clock{}, log: slog.New(slog.DiscardHandler), limiters: lim, pub: &nopub.Publisher{}}
	s, _ := config(lookup(nil))
	jobs, sweepers, err := plan(s, d)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]time.Duration{}
	for _, j := range jobs {
		byName[j.Poller.Name()] = j.Interval
	}
	if len(jobs) != 8 || len(sweepers) != 1 || byName["bmkg-cap"] != 2*time.Minute ||
		byName["openmeteo-cuaca"] != time.Hour || byName["openmeteo-udara"] != time.Hour || byName["openmeteo-sungai"] != 6*time.Hour {
		t.Fatalf("%d job, %d sapuan: %v", len(jobs), len(sweepers), byName)
	}
	st := sweepers[0].Snapshot()
	if st.Codes != 5957 || st.Interval != 6*time.Hour {
		t.Fatalf("%+v", st)
	}
	for name, want := range map[string][2]int{
		"bmkg-cap":                      {1, 0},
		"bmkg-prakiraan":                {0, 1},
		"bmkg-autogempa,bmkg-prakiraan": {1, 1},
	} {
		s, _ := config(lookup(map[string]string{"INGEST_CONNECTORS": name, "INGEST_FORECAST_LIMIT": "10"}))
		jobs, sw, err := plan(s, d)
		if err != nil || len(jobs) != want[0] || len(sw) != want[1] {
			t.Fatalf("%s: %d job, %d sapuan, %v", name, len(jobs), len(sw), err)
		}
		if len(sw) == 1 && sw[0].Snapshot().Codes != 10 {
			t.Fatalf("limit tidak dipakai: %+v", sw[0].Snapshot())
		}
	}
	for _, env := range []map[string]string{
		{"INGEST_CONNECTORS": "tidak-ada"},
		{"INGEST_FORECAST_PROVINCES": "99"},
		{"INGEST_OPENMETEO_PROVINCES": "99"},
	} {
		s, _ := config(lookup(env))
		if _, _, err := plan(s, d); err == nil {
			t.Errorf("plan(%v) seharusnya gagal", env)
		}
	}
	// Tanpa provinsi prakiraan: tidak ada sapuan.
	s, _ = config(lookup(map[string]string{"INGEST_FORECAST_PROVINCES": ","}))
	if _, sw, err := plan(s, d); err != nil || len(sw) != 0 {
		t.Fatalf("%d sapuan, %v", len(sw), err)
	}
}

// Sapuan prakiraan hanya memakai sisa anggaran BMKG: request biasa (gempa +
// RSS peringatan dini) harus muat di anggaran bersama dikurangi cadangan
// burst, dan cadangannya valid.
func TestForecastBudgetLeavesRoomForAlerts(t *testing.T) {
	lim, _ := budgets()
	if forecastHeadroom < 1 || forecastHeadroom > lim["bmkg"].MaxHeadroom() {
		t.Fatalf("cadangan %d di luar 1..%d", forecastHeadroom, lim["bmkg"].MaxHeadroom())
	}
	s, _ := config(lookup(nil))
	perMinute := float64(time.Minute) / float64(capInterval)
	for _, c := range connectors(s) {
		if c.budget == "bmkg" {
			perMinute += float64(time.Minute) / float64(c.interval)
		}
	}
	// 55/menit bersama − 50/menit prakiraan = 5/menit untuk request biasa.
	if perMinute > 55-50 {
		t.Fatalf("request biasa BMKG %.1f/menit tidak muat di sisa anggaran", perMinute)
	}
}

// Kuota Open-Meteo gratis dihitung per lokasi: 600/menit, 5.000/jam,
// 10.000/hari. Polling rutin harus menyisakan kuota untuk river-snap,
// backfill, dan percobaan ulang.
func TestOpenMeteoQuota(t *testing.T) {
	s, _ := config(lookup(nil))
	specs, err := openMeteoConnectors(s, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var perDay, burst float64
	for _, c := range specs {
		sites := float64(c.conn.(interface{ Sites() int }).Sites())
		perDay += sites * float64(24*time.Hour) / float64(c.interval)
		burst += sites
		if c.budget != "openmeteo" {
			t.Fatalf("%s memakai anggaran %s", c.conn.Name(), c.budget)
		}
	}
	if perDay > 6000 || burst > 300 {
		t.Fatalf("Open-Meteo %.0f lokasi/hari, %.0f lokasi sekaligus", perDay, burst)
	}
	s, _ = config(lookup(map[string]string{"INGEST_OPENMETEO_PROVINCES": ","}))
	if specs, err := openMeteoConnectors(s, time.Now); err != nil || len(specs) != 0 {
		t.Fatalf("tanpa provinsi: %d konektor, %v", len(specs), err)
	}
}

// Konektor ber-key hanya jalan bila key diisi; diminta eksplisit tanpa key
// adalah galat konfigurasi.
func TestPlanKeyedSources(t *testing.T) {
	lim, err := budgets()
	if err != nil {
		t.Fatal(err)
	}
	d := deps{clock: sysclock.Clock{}, log: slog.New(slog.DiscardHandler), limiters: lim, pub: &nopub.Publisher{}}
	keys := map[string]string{"OPENAQ_API_KEY": "KUNCIUJIKUNCIUJIKUNC", "FIRMS_MAP_KEY": "KUNCIUJIKUNCIUJIKUNCIUJIKUNCIUJI"}
	s, err := config(lookup(keys))
	if err != nil {
		t.Fatal(err)
	}
	jobs, _, err := plan(s, d)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]time.Duration{}
	for _, j := range jobs {
		byName[j.Poller.Name()] = j.Interval
	}
	if len(jobs) != 13 || byName["openaq-stasiun"] != 15*time.Minute || byName["firms-viirs-snpp-nrt"] != 30*time.Minute ||
		byName["firms-modis-nrt"] != 30*time.Minute {
		t.Fatalf("%d job: %v", len(jobs), byName)
	}
	if s.firmsBox.String() != "106.3,-7.9,108.9,-5.7" || s.openAQBox != s.firmsBox {
		t.Fatalf("kotak bawaan %v %v", s.firmsBox, s.openAQBox)
	}

	only := func(env map[string]string) []string {
		s, err := config(lookup(env))
		if err != nil {
			t.Fatal(err)
		}
		jobs, _, err := plan(s, d)
		if err != nil {
			return nil
		}
		var names []string
		for _, j := range jobs {
			names = append(names, j.Poller.Name())
		}
		return names
	}
	if got := only(map[string]string{"INGEST_CONNECTORS": "openaq-stasiun", "OPENAQ_API_KEY": keys["OPENAQ_API_KEY"]}); !slices.Equal(got, []string{"openaq-stasiun"}) {
		t.Fatalf("hanya openaq: %v", got)
	}
	if got := only(map[string]string{"INGEST_CONNECTORS": "firms-modis-nrt"}); got != nil {
		t.Fatalf("FIRMS tanpa key harus gagal, dapat %v", got)
	}
	if got := only(map[string]string{"INGEST_CONNECTORS": "openaq-stasiun"}); got != nil {
		t.Fatalf("OpenAQ tanpa key harus gagal, dapat %v", got)
	}
	for _, bad := range []map[string]string{
		{"INGEST_FIRMS_BBOX": "1,2,3"},
		{"INGEST_OPENAQ_BBOX": "108,-6,107,-7"},
	} {
		if _, err := config(lookup(bad)); err == nil {
			t.Errorf("config(%v) seharusnya gagal", bad)
		}
	}
	s, _ = config(lookup(map[string]string{"FIRMS_MAP_KEY": "pendek"}))
	if _, _, err := plan(s, d); err == nil {
		t.Error("MAP_KEY berformat salah harus ditolak")
	}
}
