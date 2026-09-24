//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Stasiun dan titik panas uji berada di desa uji 97.01.01.2002 (lihat
// seedProvince97), dengan waktu relatif terhadap sekarang.
const (
	stationSite = "openaq:970001"
	hotspotLat  = -11.65
	hotspotLon  = 141.25
)

func observationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := pool(t)
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM ts.aq_observation WHERE site_id = 'openaq:970001'`,
		`DELETE FROM ts.station WHERE site_id = 'openaq:970001'`,
		`DELETE FROM ts.series WHERE site_id = 'openaq:970001'`,
		`DELETE FROM ts.site WHERE id = 'openaq:970001'`,
		`DELETE FROM ts.hotspot WHERE ST_X(location) BETWEEN 141 AND 142 AND ST_Y(location) BETWEEN -12 AND -11`,
	} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func stationObservation(fetched time.Time, values ...float64) series.StationObservation {
	loc := series.Point{Lat: hotspotLat, Lon: hotspotLon}
	obs := series.StationObservation{
		Run: series.Run{
			Site:    series.Site{ID: stationSite, Kind: series.SiteStation, Name: "Stasiun Uji", Location: loc},
			Dataset: series.AirQualityObs, Source: series.SourceOpenAQ, Model: series.ModelSensor, Cell: loc,
			FetchedAt: fetched, ArchiveKey: "openaq-stasiun-latest/k.json.gz",
		},
		Station: series.Station{Provider: "AirGradient", Owner: "Uji", IsMonitor: false, Timezone: "Asia/Jakarta"},
	}
	for i, v := range values {
		at := fetched.Add(-time.Duration(len(values)-i) * time.Hour)
		obs.Readings = append(obs.Readings, series.Reading{
			SensorID: int64(970001*10 + i), Parameter: "pm25", Unit: series.UnitMicrogram, Value: v, ObservedAt: at,
		})
		obs.IssuedAt = at
	}
	return obs
}

func TestObservationStoreLifecycle(t *testing.T) {
	p := observationPool(t)
	ctx := context.Background()
	seedProvince97(ctx, t, p)
	store := postgres.NewSeriesStore(p)
	fetched := time.Now().UTC().Truncate(time.Minute)

	obs := stationObservation(fetched, 41.5, 38)
	if err := obs.Validate(fetched); err != nil {
		t.Fatal(err)
	}
	res, err := store.SaveObservation(ctx, obs)
	if err != nil || res != (ports.SeriesResult{Inserted: 2}) {
		t.Fatalf("%+v %v", res, err)
	}
	// Ulangan tidak mengubah apa pun; koreksi nilai hanya mengubah satu baris.
	if res, err = store.SaveObservation(ctx, obs); err != nil || res != (ports.SeriesResult{}) {
		t.Fatalf("ulangan %+v %v", res, err)
	}
	obs.Readings[1].Value = 39
	obs.Station.Owner = "Pemilik Baru"
	if res, err = store.SaveObservation(ctx, obs); err != nil || res != (ports.SeriesResult{Updated: 1}) {
		t.Fatalf("koreksi %+v %v", res, err)
	}

	var region, owner, provider string
	var issued time.Time
	var current int
	if err := p.QueryRow(ctx, `SELECT s.region_code, st.owner, st.provider, se.last_issued_at,
		(SELECT count(*) FROM ts.aq_observation_current WHERE site_id = s.id)
		FROM ts.site s JOIN ts.station st ON st.site_id = s.id
		JOIN ts.series se ON se.site_id = s.id AND se.dataset = 'aq_observation'
		WHERE s.id = $1`, stationSite).Scan(&region, &owner, &provider, &issued, &current); err != nil {
		t.Fatal(err)
	}
	if region != "97.01.01.2002" || owner != "Pemilik Baru" || provider != "AirGradient" || !issued.Equal(obs.IssuedAt) || current != 2 {
		t.Fatalf("region %s, pemilik %s, terbit %s, current %d", region, owner, issued, current)
	}
}

func detection(at, fetched time.Time, pct int) hotspot.Detection {
	d := hotspot.Detection{
		Product: "MODIS_NRT", Satellite: "Aqua", Instrument: "MODIS", Lat: hotspotLat, Lon: hotspotLon,
		DetectedAt: at, ConfidencePct: &pct, Confidence: hotspot.ConfidenceFromPct(pct),
		BrightnessK: 321.5, BackgroundK: fp(298.3), FRPMW: fp(12.4), ScanKm: 1.1, TrackKm: 1,
		Daytime: true, Version: "6.1NRT", FetchedAt: fetched, ArchiveKey: "firms-modis-nrt/k.csv.gz",
	}
	d.ID = fmt.Sprintf("%s:%s:%.5f:%.5f", d.Product, at.Format("20060102T1504"), d.Lat, d.Lon)
	return d
}

func TestHotspotStoreLifecycle(t *testing.T) {
	p := observationPool(t)
	ctx := context.Background()
	seedProvince97(ctx, t, p)
	store := postgres.NewSeriesStore(p)
	fetched := time.Now().UTC().Truncate(time.Minute)
	d := detection(fetched.Add(-3*time.Hour), fetched, 72)
	if err := d.Validate(fetched); err != nil {
		t.Fatal(err)
	}
	if res, err := store.SaveHotspot(ctx, d); err != nil || res != (ports.SeriesResult{Inserted: 1}) {
		t.Fatalf("%+v %v", res, err)
	}
	later := d
	later.FetchedAt, later.ArchiveKey = fetched.Add(30*time.Minute), "firms-modis-nrt/lain.csv.gz"
	if res, err := store.SaveHotspot(ctx, later); err != nil || res != (ports.SeriesResult{}) {
		t.Fatalf("ulangan %+v %v", res, err)
	}
	var region, key, conf string
	var first time.Time
	var recent int
	if err := p.QueryRow(ctx, `SELECT region_code, archive_key, confidence, first_fetched_at,
		(SELECT count(*) FROM ts.hotspot_recent WHERE id = $1)
		FROM ts.hotspot WHERE id = $1`, d.ID).Scan(&region, &key, &conf, &first, &recent); err != nil {
		t.Fatal(err)
	}
	if region != "97.01.01.2002" || key != d.ArchiveKey || conf != "nominal" || !first.Equal(fetched) || recent != 1 {
		t.Fatalf("region %s, arsip %s, kelas %s, pertama %s, recent %d", region, key, conf, first, recent)
	}
}

func TestObservationConstraints(t *testing.T) {
	p := observationPool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `INSERT INTO ts.site (id, kind, name, location, first_seen_at)
		VALUES ($1, 'station', 'Uji', ST_SetSRID(ST_MakePoint(141.25, -11.65), 4326), now())`, stationSite); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"stasiun berawalan salah": `INSERT INTO ts.site (id, kind, name, location, first_seen_at)
			VALUES ('grid:-11.25:141.25', 'station', 'X', ST_SetSRID(ST_MakePoint(141.25, -11.25), 4326), now())`,
		"PM2,5 dalam ppm": `INSERT INTO ts.aq_observation (site_id, sensor_id, parameter, unit, observed_at, value, fetched_at)
			VALUES ('openaq:970001', 1, 'pm25', 'ppm', now(), 1, now())`,
		"CO ppm di atas batas": `INSERT INTO ts.aq_observation (site_id, sensor_id, parameter, unit, observed_at, value, fetched_at)
			VALUES ('openaq:970001', 1, 'co', 'ppm', now(), 200, now())`,
		"nilai negatif": `INSERT INTO ts.aq_observation (site_id, sensor_id, parameter, unit, observed_at, value, fetched_at)
			VALUES ('openaq:970001', 1, 'no2', 'ppb', now(), -1, now())`,
		"ukur setelah ambil": `INSERT INTO ts.aq_observation (site_id, sensor_id, parameter, unit, observed_at, value, fetched_at)
			VALUES ('openaq:970001', 1, 'pm10', 'µg/m³', now() + interval '2 hours', 1, now())`,
		"OpenAQ untuk prakiraan": `INSERT INTO ts.series (site_id, dataset, model, source, cell, last_issued_at, last_fetched_at)
			VALUES ('openaq:970001', 'air_quality', 'sensor', 'openaq', ST_SetSRID(ST_MakePoint(141.25, -11.65), 4326), now(), now())`,
		"stasiun tanpa penyedia": `INSERT INTO ts.station (site_id, source, provider, is_monitor) VALUES ('openaq:970001', 'openaq', '', false)`,
		"ID titik panas tidak cocok": `INSERT INTO ts.hotspot (id, detected_at, product, satellite, instrument, location, confidence,
			brightness_k, scan_km, track_km, daytime, version, first_fetched_at)
			VALUES ('VIIRS_SNPP_NRT:20260924T0612:-11.65000:141.25000', '2026-09-24 06:13Z', 'VIIRS_SNPP_NRT', 'N', 'VIIRS',
			ST_SetSRID(ST_MakePoint(141.25, -11.65), 4326), 'low', 330, 0.4, 0.4, true, '2.0NRT', now())`,
		"VIIRS dengan persentase": `INSERT INTO ts.hotspot (id, detected_at, product, satellite, instrument, location, confidence,
			confidence_pct, brightness_k, scan_km, track_km, daytime, version, first_fetched_at)
			VALUES ('VIIRS_SNPP_NRT:20260924T0612:-11.65000:141.25000', '2026-09-24 06:12Z', 'VIIRS_SNPP_NRT', 'N', 'VIIRS',
			ST_SetSRID(ST_MakePoint(141.25, -11.65), 4326), 'low', 10, 330, 0.4, 0.4, true, '2.0NRT', now())`,
		"kelas tidak cocok persentase": `INSERT INTO ts.hotspot (id, detected_at, product, satellite, instrument, location, confidence,
			confidence_pct, brightness_k, scan_km, track_km, daytime, version, first_fetched_at)
			VALUES ('MODIS_NRT:20260924T0612:-11.65000:141.25000', '2026-09-24 06:12Z', 'MODIS_NRT', 'T', 'MODIS',
			ST_SetSRID(ST_MakePoint(141.25, -11.65), 4326), 'high', 50, 330, 1, 1, true, '6.1NRT', now())`,
	} {
		if _, err := p.Exec(ctx, q); err == nil {
			t.Errorf("%s: tidak ditolak database", name)
		}
	}
	// Kontrol: baris yang sama dengan nilai benar diterima.
	if _, err := p.Exec(ctx, `INSERT INTO ts.hotspot (id, detected_at, product, satellite, instrument, location, confidence,
		confidence_pct, brightness_k, scan_km, track_km, daytime, version, first_fetched_at)
		VALUES ('MODIS_NRT:20260924T0612:-11.65000:141.25000', '2026-09-24 06:12Z', 'MODIS_NRT', 'T', 'MODIS',
		ST_SetSRID(ST_MakePoint(141.25, -11.65), 4326), 'nominal', 50, 330, 1, 1, true, '6.1NRT', '2026-09-24 08:00Z')`); err != nil {
		t.Fatalf("baris benar ditolak: %v", err)
	}
}
