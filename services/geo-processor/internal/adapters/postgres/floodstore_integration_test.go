//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/calibration"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/hazardpb"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/floods"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

// Titik uji. Database uji bisa sama dengan database dev (make
// test-integration), jadi test hanya menyentuh titik ini dan ambang asli
// dipulihkan.
const (
	testRiver = "river:uji-banjir"
	testBasin = "catchment:uji-banjir"
)

// floodPool menyiapkan database dan menghapus sisa kejadian titik uji.
func floodPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := pool(t)
	clean := func() {
		for _, q := range []string{
			`DELETE FROM hazard.flood_site WHERE site_id IN ('` + testRiver + `', '` + testBasin + `')`,
			`DELETE FROM hazard.event WHERE kind = 'flood' AND source_event_id IN ('` + testRiver + `', '` + testBasin + `')`,
			`DELETE FROM ts.weather_forecast WHERE site_id = '` + testBasin + `'`,
			`DELETE FROM ts.series WHERE site_id = '` + testBasin + `'`,
			`DELETE FROM ts.site WHERE id = '` + testBasin + `'`,
		} {
			if _, err := p.Exec(context.Background(), q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	return p
}

// testCalibration adalah ambang asli ditambah titik uji (ambang Nanjung dan
// Cikapundung asli).
func testCalibration(t *testing.T) flood.Calibration {
	t.Helper()
	p := flood.DefaultPolicy()
	cal, err := calibration.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	river := cal.Discharge["river:citarum-nanjung"]
	river.SiteID, river.River, river.Name = testRiver, "Sungai Uji", "Titik Uji"
	cal.Discharge[testRiver] = river
	basin := cal.Rainfall["catchment:cikapundung"]
	basin.SiteID, basin.Name = testBasin, "Sub-DAS Uji"
	cal.Rainfall[testBasin] = basin
	return cal
}

func pf(v float64) *float64 { return &v }

// Waktu uji 2001 seperti test lain: putaran kedaluwarsa di test tidak menyentuh
// kejadian asli di database dev.
var floodT0 = time.Date(2001, 10, 2, 0, 30, 0, 0, time.UTC)

// nanjungRun adalah keluaran debit titik uji (ambang Nanjung: efektif ≈
// 187,7 / 236,6 / 329,5 / 417,3 m³/s).
func nanjungRun(at time.Time, median, p75 float64) series.DischargeRun {
	day := at.Truncate(24 * time.Hour)
	return series.DischargeRun{Run: series.Run{
		Site:    series.Site{ID: testRiver, Kind: series.SiteRiver, Name: "Titik Uji", River: "Sungai Uji", Location: series.Point{Lat: -6.941, Lon: 107.537}},
		Dataset: series.Discharge, Source: series.SourceOpenMeteo, Model: "glofas_v4", Cell: series.Point{Lat: -6.975, Lon: 107.525},
		IssuedAt: at, FetchedAt: at,
	}, Steps: []series.DischargeStep{
		{ValidDate: day.AddDate(0, 0, -1), Discharge: pf(150)},
		{ValidDate: day, Discharge: pf(200), Mean: pf(median), Median: pf(median), Max: pf(p75 + 100), Min: pf(median / 2), P25: pf(median - 1), P75: pf(p75)},
		{ValidDate: day.AddDate(0, 0, 1), Discharge: pf(180)},
	}}
}

func TestFloodStoreSyncThresholds(t *testing.T) {
	p := floodPool(t)
	ctx := context.Background()
	store := postgres.NewFloodStore(p)
	cal, err := calibration.Load(flood.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	// Ambang asli dipulihkan setelah test mengubahnya.
	t.Cleanup(func() {
		if _, err := store.SyncThresholds(ctx, cal); err != nil {
			t.Error(err)
		}
	})
	res, err := store.SyncThresholds(ctx, cal)
	if err != nil || res.Inserted+res.Updated+res.Unchanged != 38+21 {
		t.Fatalf("%+v %v", res, err)
	}
	var rivers, rains int
	if err := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM ref.discharge_threshold), (SELECT count(*) FROM ref.rainfall_threshold)`).Scan(&rivers, &rains); err != nil || rivers != 38 || rains != 21 {
		t.Fatalf("%d titik, %d baris hujan, %v", rivers, rains, err)
	}
	if res, err := store.SyncThresholds(ctx, cal); err != nil || res.Unchanged != 59 || res.Inserted+res.Updated+res.Deleted != 0 {
		t.Fatalf("jalan kedua %+v %v", res, err)
	}
	var siaga, corr float64
	var maxLevel int
	if err := p.QueryRow(ctx, `SELECT siaga_m3s, correction, max_level FROM ref.discharge_threshold WHERE site_id = 'river:citarum-nanjung'`).Scan(&siaga, &corr, &maxLevel); err != nil {
		t.Fatal(err)
	}
	if math.Abs(siaga-378.73*0.87) > 1e-9 || corr != 0.87 || maxLevel != 4 {
		t.Fatalf("Nanjung %v %v %d", siaga, corr, maxLevel)
	}
	if err := p.QueryRow(ctx, `SELECT max_level FROM ref.discharge_threshold WHERE site_id = 'river:citarum-muara'`).Scan(&maxLevel); err != nil || maxLevel != 3 {
		t.Fatalf("muara %d %v", maxLevel, err)
	}
	var rain int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM ref.rainfall_threshold WHERE max_level = 3 AND period_from = '2017-01-01'`).Scan(&rain); err != nil || rain != 21 {
		t.Fatalf("%d %v", rain, err)
	}

	// Kalibrasi baru: satu titik hilang, satu ambang berubah.
	next, _ := calibration.Load(flood.DefaultPolicy())
	delete(next.Discharge, "river:cibuni-muara")
	th := next.Discharge["river:cikaso-muara"]
	th.Climatology[3] = 330
	next.Discharge["river:cikaso-muara"] = th
	delete(next.Rainfall, "catchment:cihaur")
	if res, err := store.SyncThresholds(ctx, next); err != nil || res.Deleted != 1+3 || res.Updated != 1 || res.Unchanged != 36+18 {
		t.Fatalf("kalibrasi baru %+v %v", res, err)
	}
	// Ambang yang tidak naik ditolak database (constraint kedua), dan
	// transaksi batal utuh.
	th.Climatology[3] = th.Climatology[2]
	next.Discharge["river:cikaso-muara"] = th
	if _, err := store.SyncThresholds(ctx, next); !errors.Is(err, flood.ErrInvalid) {
		t.Fatalf("ambang rusak: %v", err)
	}
}

func TestFloodStoreLifecycle(t *testing.T) {
	p := floodPool(t)
	ctx := context.Background()
	now := floodT0
	svc, err := floods.New(postgres.NewFloodStore(p), hazardpb.FloodEncoder{}, flood.DefaultPolicy(), testCalibration(t), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	// Median 240 (Waspada, ambang efektif 236,6), P75 340 (Siaga): kemungkinan Siaga.
	res, err := svc.Discharge(ctx, nanjungRun(floodT0, 240, 340))
	if err != nil || res.Created != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	id := flood.EventIDFor(testRiver, floodT0)
	var (
		level, outlook, maxLevel int
		possible                 bool
		title, summary, src      string
		thresholds               []float64
		expires, peak            time.Time
	)
	if err := p.QueryRow(ctx, `SELECT e.level, e.title, e.summary, e.source_event_id, e.expires_at, f.outlook_level, f.possible,
		f.max_level, f.thresholds, f.peak_date FROM hazard.event e JOIN hazard.flood f ON f.event_id = e.id WHERE e.id = $1`, id.String()).
		Scan(&level, &title, &summary, &src, &expires, &outlook, &possible, &maxLevel, &thresholds, &peak); err != nil {
		t.Fatal(err)
	}
	if level != 3 || outlook != 3 || !possible || maxLevel != 4 || src != testRiver || len(thresholds) != 4 ||
		!expires.Equal(floodT0.Add(24*time.Hour)) || !peak.Equal(floodT0.Truncate(24*time.Hour)) || !strings.Contains(summary, "kemungkinan Siaga") {
		t.Fatalf("%d %d %v %d %s %v %v %v\n%s", level, outlook, possible, maxLevel, src, thresholds, expires, peak, summary)
	}
	var days, ensembleDays int
	if err := p.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE from_ensemble) FROM hazard.flood_day WHERE event_id = $1`, id.String()).Scan(&days, &ensembleDays); err != nil || days != 4 || ensembleDays != 1 {
		t.Fatalf("%d hari, %d ensemble, %v", days, ensembleDays, err)
	}

	// Keluaran berikutnya di bawah Info: tetap aktif sebagai Info.
	t1 := floodT0.Add(6 * time.Hour)
	now = t1
	if res, err := svc.Discharge(ctx, nanjungRun(t1, 100, 120)); err != nil || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if err := p.QueryRow(ctx, `SELECT e.level, f.outlook_level, f.peak_date IS NULL, e.revision FROM hazard.event e JOIN hazard.flood f ON f.event_id = e.id WHERE e.id = $1`, id.String()).
		Scan(&level, &outlook, &possible, &maxLevel); err != nil || level != 1 || outlook != 0 || !possible || maxLevel != 2 {
		t.Fatalf("%d %d %v rev %d %v", level, outlook, possible, maxLevel, err)
	}
	// Keluaran ulangan dan yang lebih tua tidak mengubah apa pun.
	if res, err := svc.Discharge(ctx, nanjungRun(floodT0, 500, 500)); err != nil || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}

	// Kedaluwarsa 24 jam setelah keluaran terakhir di atas ambang.
	if n, err := svc.Expire(ctx, floodT0.Add(24*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	var status string
	var site string
	if err := p.QueryRow(ctx, `SELECT e.status, s.site_id FROM hazard.flood_site s JOIN hazard.event e ON e.id = s.event_id WHERE s.site_id = $1`, testRiver).Scan(&status, &site); err != nil || status != "expired" {
		t.Fatalf("%s %v", status, err)
	}
	rows, err := p.Query(ctx, `SELECT subject FROM hazard.outbox WHERE msg_id LIKE $1 ORDER BY id`, id.String()+":%")
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		subjects = append(subjects, s)
	}
	if strings.Join(subjects, " ") != "hazard.flood.created hazard.flood.updated hazard.flood.expired" {
		t.Fatalf("%v", subjects)
	}
}

// TestFloodRainfallInDatabase menyimpan hujan sub-DAS sebagai deret cuaca
// titik catchment dan membuka kejadian indeks hujan.
func TestFloodRainfallInDatabase(t *testing.T) {
	p := floodPool(t)
	ctx := context.Background()
	start := floodT0.Truncate(24 * time.Hour)
	run := series.WeatherRun{Run: series.Run{
		Site:    series.Site{ID: testBasin, Kind: series.SiteCatchment, Name: "Sub-DAS Uji", Location: series.Point{Lat: -6.82, Lon: 107.62}},
		Dataset: series.Weather, Source: series.SourceOpenMeteo, Model: "ecmwf_ifs", Cell: series.Point{Lat: -6.82, Lon: 107.62},
		IssuedAt: floodT0, FetchedAt: floodT0, ArchiveKey: "openmeteo-hujan/x.json.gz",
	}}
	for h := range 24 {
		v := 0.0
		if h >= 10 && h < 13 {
			v = 7 // 3 jam 21 mm: Siaga (≥ 17,53), 24 jam 21 mm: Waspada
		}
		run.Steps = append(run.Steps, series.WeatherStep{ValidTime: start.Add(time.Duration(h) * time.Hour), PrecipitationMM: pf(v)})
	}
	if res, err := postgres.NewSeriesStore(p).SaveWeather(ctx, run); err != nil || res.Inserted != 24 {
		t.Fatalf("%+v %v", res, err)
	}
	var kind, name, river string
	if err := p.QueryRow(ctx, `SELECT kind, name, river FROM ts.site WHERE id = $1`, testBasin).Scan(&kind, &name, &river); err != nil ||
		kind != "catchment" || name != "Sub-DAS Uji" || river != "" {
		t.Fatalf("%s %s %q %v", kind, name, river, err)
	}

	svc, err := floods.New(postgres.NewFloodStore(p), hazardpb.FloodEncoder{}, flood.DefaultPolicy(), testCalibration(t), func() time.Time { return floodT0 })
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Rainfall(ctx, run)
	if err != nil || res.Created != 1 || res.Level != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	var window, maxLevel, n int
	if err := p.QueryRow(ctx, `SELECT d.window_hours, f.max_level, cardinality(f.thresholds) FROM hazard.flood f
		JOIN hazard.flood_day d ON d.event_id = f.event_id AND d.level = 3 WHERE f.site_id = $1`, testBasin).Scan(&window, &maxLevel, &n); err != nil ||
		window != 3 || maxLevel != 3 || n != 12 {
		t.Fatalf("%d %d %d %v", window, maxLevel, n, err)
	}
}

func TestFloodConstraints(t *testing.T) {
	p := floodPool(t)
	ctx := context.Background()
	svc, _ := floods.New(postgres.NewFloodStore(p), hazardpb.FloodEncoder{}, flood.DefaultPolicy(), testCalibration(t), func() time.Time { return floodT0 })
	if _, err := svc.Discharge(ctx, nanjungRun(floodT0, 240, 340)); err != nil {
		t.Fatal(err)
	}
	// Baris ambang asli untuk uji constraint ref (sinkron sama dengan saat start).
	actual, _ := calibration.Load(flood.DefaultPolicy())
	if _, err := postgres.NewFloodStore(p).SyncThresholds(ctx, actual); err != nil {
		t.Fatal(err)
	}
	id := flood.EventIDFor(testRiver, floodT0).String()
	for name, q := range map[string]string{
		"indeks hujan Bahaya":        `UPDATE hazard.flood SET indicator = 'rainfall', site_id = 'catchment:x', river = '', max_level = 4 WHERE event_id = '` + id + `'`,
		"tingkat di atas batas":      `UPDATE hazard.flood SET max_level = 2 WHERE event_id = '` + id + `'`,
		"puncak tanpa tingkat":       `UPDATE hazard.flood SET peak_date = NULL WHERE event_id = '` + id + `'`,
		"bentuk ambang":              `UPDATE hazard.flood SET thresholds = '{1,2,3}' WHERE event_id = '` + id + `'`,
		"sungai di sub-DAS":          `UPDATE hazard.flood SET site_id = 'catchment:x' WHERE event_id = '` + id + `'`,
		"hari kemungkinan":           `UPDATE hazard.flood_day SET level = 0, window_hours = 0 WHERE event_id = '` + id + `' AND possible`,
		"hari bukan tengah malam":    `UPDATE hazard.flood_day SET date = date + interval '1 hour' WHERE event_id = '` + id + `'`,
		"jendela tanpa tingkat":      `UPDATE hazard.flood_day SET window_hours = 3 WHERE event_id = '` + id + `' AND level = 0`,
		"ambang hujan Bahaya":        `INSERT INTO ref.rainfall_threshold (site_id, window_hours, name, p50, p80, p90, p98, p99_5, max_level, period_from, period_to, source_sha256) VALUES ('catchment:x', 3, 'X', 1, 2, 3, 4, 5, 4, '2017-01-01', '2024-12-31', decode(repeat('00', 32), 'hex'))`,
		"jendela tak dikenal":        `INSERT INTO ref.rainfall_threshold (site_id, window_hours, name, p50, p80, p90, p98, p99_5, max_level, period_from, period_to, source_sha256) VALUES ('catchment:x', 12, 'X', 1, 2, 3, 4, 5, 3, '2017-01-01', '2024-12-31', decode(repeat('00', 32), 'hex'))`,
		"koreksi di atas 1":          `UPDATE ref.discharge_threshold SET correction = 1.1 WHERE site_id = 'river:citarum-nanjung'`,
		"titik catchment tanpa nama": `INSERT INTO ts.site (id, kind, name, river, location, first_seen_at) VALUES ('catchment:x', 'catchment', '', '', ST_SetSRID(ST_MakePoint(107, -7), 4326), now())`,
		"catchment dengan sungai":    `INSERT INTO ts.site (id, kind, name, river, location, first_seen_at) VALUES ('catchment:x', 'catchment', 'X', 'Citarum', ST_SetSRID(ST_MakePoint(107, -7), 4326), now())`,
	} {
		_, err := p.Exec(ctx, q)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23514" {
			t.Errorf("%s harus ditolak CHECK: %v", name, err)
		}
	}
}
