//go:build integration

package postgres_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Data deret waktu uji memakai titik di pojok tenggara kotak Indonesia
// (-11,5; 141,5) dan provinsi 97, dengan waktu relatif terhadap sekarang:
// kebijakan retensi TimescaleDB membuang chunk yang lebih tua dari setahun.
const (
	gridSite  = "grid:-11.50:141.50"
	riverSite = "river:uji-sungai"
	adm4Site  = "adm4:97.01.01.2001"
)

func seriesPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := pool(t)
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM ts.weather_forecast WHERE site_id IN ('grid:-11.50:141.50', 'adm4:97.01.01.2001')`,
		`DELETE FROM ts.aq_forecast WHERE site_id = 'grid:-11.50:141.50'`,
		`DELETE FROM ts.river_discharge WHERE site_id = 'river:uji-sungai'`,
		`DELETE FROM ts.series WHERE site_id IN ('grid:-11.50:141.50', 'adm4:97.01.01.2001', 'river:uji-sungai')`,
		`DELETE FROM ts.site WHERE id IN ('grid:-11.50:141.50', 'adm4:97.01.01.2001', 'river:uji-sungai')`,
	} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// seedProvince97 menyiapkan satu desa uji yang memuat titik sungai uji.
func seedProvince97(ctx context.Context, t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	conn, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, q := range []string{
		`DELETE FROM ref.region WHERE code = '97' OR code LIKE '97.%'`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	box := func(code string, b [4]float64) region.Region {
		ring := region.Ring{{b[0], b[1]}, {b[2], b[1]}, {b[2], b[3]}, {b[0], b[3]}, {b[0], b[1]}}
		r, err := region.New(region.MustParseCode(code), "Wilayah Uji "+code, region.MultiPolygon{{ring}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	all := [4]float64{141.0, -11.9, 141.9, -11.0}
	regions := []region.Region{
		box("97", all), box("97.01", all), box("97.01.01", all),
		box("97.01.01.2002", [4]float64{141.20, -11.70, 141.30, -11.60}),
	}
	if _, err := postgres.NewRegionStore(conn.Conn()).UpsertAll(ctx, regions, "test", "series"); err != nil {
		t.Fatal(err)
	}
}

func fp(v float64) *float64 { return &v }

func TestSeriesStoreLifecycle(t *testing.T) {
	p := seriesPool(t)
	ctx := context.Background()
	seedProvince97(ctx, t, p)
	store := postgres.NewSeriesStore(p)
	now := time.Now().UTC().Truncate(time.Hour)

	grid := series.Run{
		Site:    series.Site{ID: gridSite, Kind: series.SiteGrid, Location: series.Point{Lat: -11.5, Lon: 141.5}},
		Dataset: series.Weather, Source: series.SourceOpenMeteo, Model: "best_match",
		Cell: series.Point{Lat: -11.49, Lon: 141.51}, Elevation: fp(12),
		IssuedAt: now, FetchedAt: now, ArchiveKey: "openmeteo-cuaca/a.json.gz",
	}
	wx := func(issued time.Time, temp float64) series.WeatherRun {
		r := grid
		r.IssuedAt, r.FetchedAt = issued, issued
		code := 3
		run := series.WeatherRun{Run: r}
		for h := range 3 {
			run.Steps = append(run.Steps, series.WeatherStep{
				ValidTime: now.Add(time.Duration(h) * time.Hour), TemperatureC: fp(temp + float64(h)),
				HumidityPct: fp(80), PrecipitationMM: fp(0.4), WeatherCode: &code, BoundaryLayerM: fp(500),
			})
		}
		return run
	}
	save := func(run series.WeatherRun, want ports.SeriesResult) {
		t.Helper()
		if err := run.Validate(now); err != nil {
			t.Fatal(err)
		}
		got, err := store.SaveWeather(ctx, run)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("SaveWeather = %+v, ingin %+v", got, want)
		}
	}

	first := wx(now, 20)
	save(first, ports.SeriesResult{Inserted: 3})
	save(first, ports.SeriesResult{})
	changed := wx(now, 20)
	changed.Steps[1].TemperatureC = fp(25.5)
	save(changed, ports.SeriesResult{Updated: 1})
	later := wx(now.Add(time.Hour), 22)
	later.ArchiveKey = "openmeteo-cuaca/b.json.gz"
	save(later, ports.SeriesResult{Inserted: 3})
	// Keluaran lama yang datang terlambat tidak memundurkan kesegaran deret.
	save(wx(now.Add(-time.Hour), 19), ports.SeriesResult{Inserted: 3})

	var issued, fetched time.Time
	var key string
	var elev float64
	if err := p.QueryRow(ctx, `SELECT last_issued_at, last_fetched_at, last_archive_key, elevation_m FROM ts.series
		WHERE site_id = $1 AND dataset = 'weather' AND model = 'best_match'`, gridSite).Scan(&issued, &fetched, &key, &elev); err != nil {
		t.Fatal(err)
	}
	if !issued.Equal(now.Add(time.Hour)) || !fetched.Equal(now.Add(time.Hour)) || key != "openmeteo-cuaca/b.json.gz" || elev != 12 {
		t.Fatalf("ts.series = %v %v %q %v", issued, fetched, key, elev)
	}

	// Tampilan current memilih keluaran terbaru per waktu berlaku.
	var temp float64
	if err := p.QueryRow(ctx, `SELECT temperature_c FROM ts.weather_forecast_current
		WHERE site_id = $1 AND valid_time = $2`, gridSite, now.Add(time.Hour)).Scan(&temp); err != nil {
		t.Fatal(err)
	}
	if temp != 23 {
		t.Fatalf("current temperature_c = %v, ingin 23 (keluaran terbaru)", temp)
	}
	var region *string
	if err := p.QueryRow(ctx, `SELECT region_code FROM ts.site WHERE id = $1`, gridSite).Scan(&region); err != nil {
		t.Fatal(err)
	}
	if region != nil {
		t.Fatalf("simpul grid di luar desa uji mendapat region_code %q", *region)
	}

	// Prakiraan BMKG per desa: kode wilayah dari ID, jarak pandang tersimpan.
	loc := series.Point{Lat: -11.65, Lon: 141.25}
	bmkg := series.WeatherRun{Run: series.Run{
		Site:    series.Site{ID: adm4Site, Kind: series.SiteRegion, Name: "Desa Uji", Location: loc, RegionCode: "97.01.01.2001"},
		Dataset: series.Weather, Source: series.SourceBMKG, Model: series.ModelBMKG, Cell: loc,
		IssuedAt: now.Add(-6 * time.Hour), FetchedAt: now,
	}}
	code := 61
	bmkg.Steps = []series.WeatherStep{{
		ValidTime: now, TemperatureC: fp(24), HumidityPct: fp(90), CloudCoverPct: fp(100),
		PrecipitationMM: fp(2.5), WeatherCode: &code, WindSpeedKmh: fp(5), WindFromDeg: fp(270), VisibilityM: fp(8000),
	}}
	save(bmkg, ports.SeriesResult{Inserted: 1})
	var vis float64
	if err := p.QueryRow(ctx, `SELECT s.region_code, w.visibility_m FROM ts.site s JOIN ts.weather_forecast w ON w.site_id = s.id
		WHERE s.id = $1`, adm4Site).Scan(&region, &vis); err != nil {
		t.Fatal(err)
	}
	if region == nil || *region != "97.01.01.2001" || vis != 8000 {
		t.Fatalf("titik BMKG: region %v, jarak pandang %v", region, vis)
	}

	// Kualitas udara.
	aq := series.AirQualityRun{Run: grid}
	aq.Dataset, aq.Model = series.AirQuality, "cams_global"
	aq.Steps = []series.AirQualityStep{
		{ValidTime: now, PM25: fp(35.2), PM10: fp(40), AerosolOpticalDepth: fp(0.3)},
		{ValidTime: now.Add(time.Hour), PM25: fp(37.9), Dust: fp(0)},
	}
	if err := aq.Validate(now); err != nil {
		t.Fatal(err)
	}
	if got, err := store.SaveAirQuality(ctx, aq); err != nil || got != (ports.SeriesResult{Inserted: 2}) {
		t.Fatalf("SaveAirQuality = %+v, %v", got, err)
	}

	// Debit sungai: titik di dalam desa uji mendapat region_code dari ref.region.
	day := now.Truncate(24 * time.Hour)
	river := series.DischargeRun{Run: series.Run{
		Site:    series.Site{ID: riverSite, Kind: series.SiteRiver, Name: "Titik Uji", River: "Sungai Uji", Location: series.Point{Lat: -11.625, Lon: 141.275}},
		Dataset: series.Discharge, Source: series.SourceOpenMeteo, Model: "glofas_v4",
		Cell: series.Point{Lat: -11.625, Lon: 141.275}, IssuedAt: now, FetchedAt: now,
	}}
	river.Steps = []series.DischargeStep{
		{ValidDate: day.AddDate(0, 0, -1), Discharge: fp(8.79), Mean: fp(9.36), Median: fp(8.77), Max: fp(17.79), Min: fp(5.3), P25: fp(7.53), P75: fp(10.4)},
		{ValidDate: day, Discharge: fp(4.09)},
	}
	if err := river.Validate(now); err != nil {
		t.Fatal(err)
	}
	if got, err := store.SaveDischarge(ctx, river); err != nil || got != (ports.SeriesResult{Inserted: 2}) {
		t.Fatalf("SaveDischarge = %+v, %v", got, err)
	}
	if err := p.QueryRow(ctx, `SELECT region_code FROM ts.site WHERE id = $1`, riverSite).Scan(&region); err != nil {
		t.Fatal(err)
	}
	if region == nil || *region != "97.01.01.2002" {
		t.Fatalf("region_code titik sungai = %v, ingin 97.01.01.2002", region)
	}
	var max float64
	if err := p.QueryRow(ctx, `SELECT ensemble_max_m3s FROM ts.river_discharge_current WHERE site_id = $1 AND valid_date = $2`,
		riverSite, day.AddDate(0, 0, -1)).Scan(&max); err != nil {
		t.Fatal(err)
	}
	if math.Abs(max-17.79) > 1e-4 {
		t.Fatalf("ensemble_max_m3s = %v", max)
	}
}

// Constraint database menolak nilai yang lolos dari domain karena bug.
func TestSeriesConstraints(t *testing.T) {
	p := seriesPool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO ts.site (id, kind, location, first_seen_at)
		VALUES ($1, 'grid', ST_SetSRID(ST_MakePoint(141.5, -11.5), 4326), now())`, gridSite); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"kelembapan > 100": `INSERT INTO ts.weather_forecast (site_id, model, issued_at, valid_time, humidity_pct)
			VALUES ('grid:-11.50:141.50', 'best_match', now(), now(), 101)`,
		"horizon": `INSERT INTO ts.weather_forecast (site_id, model, issued_at, valid_time, temperature_c)
			VALUES ('grid:-11.50:141.50', 'best_match', now(), now() + interval '30 days', 20)`,
		"titik sungai tanpa nama sungai": `INSERT INTO ts.site (id, kind, name, location, first_seen_at)
			VALUES ('river:tanpa-sungai', 'river', 'X', ST_SetSRID(ST_MakePoint(141.5, -11.5), 4326), now())`,
		"jenis tidak cocok dengan ID": `INSERT INTO ts.site (id, kind, location, first_seen_at)
			VALUES ('grid:-11.25:141.50', 'river', ST_SetSRID(ST_MakePoint(141.5, -11.25), 4326), now())`,
		"urutan ensemble": `INSERT INTO ts.river_discharge (site_id, model, issued_at, valid_date, ensemble_min_m3s, ensemble_max_m3s)
			VALUES ('grid:-11.50:141.50', 'glofas_v4', now(), date_trunc('day', now(), 'UTC'), 5, 4)`,
		"BMKG untuk kualitas udara": `INSERT INTO ts.series (site_id, dataset, model, source, cell, last_issued_at, last_fetched_at)
			VALUES ('grid:-11.50:141.50', 'air_quality', 'bmkg', 'bmkg', ST_SetSRID(ST_MakePoint(141.5, -11.5), 4326), now(), now())`,
	} {
		if _, err := p.Exec(ctx, q); err == nil {
			t.Errorf("%s: tidak ditolak database", name)
		}
	}
}
