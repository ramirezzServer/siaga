//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/calibration"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/hazardpb"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/fires"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Kejadian titik api uji: deteksi tahun 2001 (sintetis) dan 18 Agu–7 Sep
// 2023 (rekaman arsip asli di internal/app/fires/testdata). Database uji bisa
// sama dengan database dev, jadi hanya rentang itu yang dihapus.
func firePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := pool(t)
	clean := func() {
		for _, q := range []string{
			`UPDATE hazard.event SET merged_into = NULL, status = 'expired' WHERE kind = 'fire' AND status = 'merged' AND (occurred_at < '2002-01-01' OR occurred_at BETWEEN '2023-08-17' AND '2023-09-09')`,
			`DELETE FROM hazard.event WHERE kind = 'fire' AND (occurred_at < '2002-01-01' OR occurred_at BETWEEN '2023-08-17' AND '2023-09-09')`,
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

var fireT0 = time.Date(2001, 8, 20, 6, 30, 0, 0, time.UTC)

func fireDetection(product string, lat, lon float64, h time.Duration, c hotspot.Confidence) hotspot.Detection {
	at := fireT0.Add(h)
	frp := 4.5
	d := hotspot.Detection{
		ID:      product + ":" + at.Format("20060102T1504") + ":" + strconv.FormatFloat(lat, 'f', 5, 64) + ":" + strconv.FormatFloat(lon, 'f', 5, 64),
		Product: product, Satellite: "N20", Instrument: "VIIRS", Lat: lat, Lon: lon, DetectedAt: at, Confidence: c,
		BrightnessK: 340, FRPMW: &frp, ScanKm: 0.4, TrackKm: 0.4, Daytime: true, Version: "2.0NRT", FetchedAt: at.Add(3 * time.Hour),
	}
	if product == "MODIS_NRT" {
		pct := 60
		d.Instrument, d.Satellite, d.ConfidencePct, d.ScanKm, d.TrackKm, d.FRPMW = "MODIS", "Aqua", &pct, 1, 1, nil
	}
	return d
}

func TestFireStoreSyncStatic(t *testing.T) {
	p := firePool(t)
	ctx := t.Context()
	store := postgres.NewFireStore(p)
	cal, err := calibration.LoadFire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.SyncStatic(context.Background(), cal) })
	if _, err := store.SyncStatic(ctx, cal); err != nil {
		t.Fatal(err)
	}
	if res, err := store.SyncStatic(ctx, cal); err != nil || res.Unchanged != len(cal.Static) || res.Inserted+res.Updated+res.Deleted != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	changed := cal
	changed.Static = slices.Clone(cal.Static[1:])
	changed.Static[0].RadiusKm += 0.1
	if res, err := store.SyncStatic(ctx, changed); err != nil || res.Updated != 1 || res.Deleted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	var radius float64
	if err := p.QueryRow(ctx, `SELECT radius_km FROM ref.fire_static_source WHERE id = $1`, changed.Static[0].ID).Scan(&radius); err != nil || radius != changed.Static[0].RadiusKm {
		t.Fatal(radius, err)
	}
	if res, err := store.SyncStatic(ctx, cal); err != nil || res.Inserted != 1 || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	bad := cal
	bad.Static = []fire.StaticSource{{ID: "x"}}
	if _, err := store.SyncStatic(ctx, bad); !errors.Is(err, fire.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestFireStoreLifecycle(t *testing.T) {
	p := firePool(t)
	ctx := t.Context()
	cal, err := calibration.LoadFire()
	if err != nil {
		t.Fatal(err)
	}
	var clock time.Time
	svc, err := fires.New(postgres.NewFireStore(p), hazardpb.FireEncoder{}, fire.DefaultPolicy(), cal, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	run := func(d hotspot.Detection) fires.Result {
		t.Helper()
		clock = d.FetchedAt
		res, err := svc.Process(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	const n20, n21 = "VIIRS_NOAA20_NRT", "VIIRS_NOAA21_NRT"
	a := fireDetection(n20, -6.80, 107.35, 0, hotspot.Nominal)
	first := run(a)
	if first.Created != 1 || first.Level != hazard.LevelInfo {
		t.Fatalf("%+v", first)
	}
	if res := run(a); res.Skipped != fires.SkipSeen {
		t.Fatalf("ulangan %+v", res)
	}
	if res := run(fireDetection(n21, -6.809, 107.35, 50*time.Minute, hotspot.Nominal)); res.Level != hazard.LevelWaspada || res.Updated != 1 {
		t.Fatalf("%+v", res)
	}
	second := run(fireDetection(n20, -6.80, 107.383, 2*time.Hour, hotspot.Nominal))
	if second.Created != 1 {
		t.Fatalf("%+v", second)
	}
	if res := run(fireDetection(n20, -6.80, 107.3665, 12*time.Hour, hotspot.Low)); res.Merged != 1 || res.Event != first.Event {
		t.Fatalf("jembatan %+v", res)
	}
	if res := run(fireDetection("MODIS_NRT", -6.80, 107.36, 13*time.Hour, hotspot.Nominal)); res.Level != hazard.LevelSiaga {
		t.Fatalf("%+v", res)
	}
	var (
		status, mergedInto string
		n, peak, passes    int
		products           []string
		maxFRP             *float64
	)
	if err := p.QueryRow(ctx, `SELECT status, merged_into::text FROM hazard.event WHERE id = $1`, second.Event.String()).Scan(&status, &mergedInto); err != nil ||
		status != "merged" || mergedInto != first.Event.String() {
		t.Fatalf("%s %s %v", status, mergedInto, err)
	}
	if err := p.QueryRow(ctx, `SELECT f.detection_count, f.peak_detections, f.overpass_count, f.products, f.max_frp_mw
		FROM hazard.fire f WHERE f.event_id = $1`, first.Event.String()).Scan(&n, &peak, &passes, &products, &maxFRP); err != nil ||
		n != 5 || peak != 5 || passes != 5 || len(products) != 3 || maxFRP == nil || *maxFRP != 4.5 {
		t.Fatalf("%d %d %d %v %v %v", n, peak, passes, products, maxFRP, err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM hazard.fire_detection WHERE event_id = $1`, first.Event.String()).Scan(&n); err != nil || n != 5 {
		t.Fatal(n, err)
	}
	var level int
	if err := p.QueryRow(ctx, `SELECT level, expires_at = $2 FROM hazard.event WHERE id = $1`, first.Event.String(), fireT0.Add(40*time.Hour)).Scan(&level, new(bool)); err != nil || level != 3 {
		t.Fatal(level, err)
	}
	if got, err := svc.Expire(ctx, fireT0.Add(100*time.Hour), 100); err != nil || got != 1 {
		t.Fatal(got, err)
	}
	rows, err := p.Query(ctx, `SELECT subject FROM hazard.outbox WHERE subject LIKE 'hazard.fire.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		subjects = append(subjects, s[len("hazard.fire."):])
	}
	want := []string{"created", "updated", "created", "expired", "updated", "updated", "expired"}
	if !slices.Equal(subjects, want) {
		t.Fatalf("outbox %v", subjects)
	}
}

func TestFireConstraints(t *testing.T) {
	p := firePool(t)
	ctx := t.Context()
	store := postgres.NewFireStore(p)
	d := fire.FromHotspot(fireDetection("VIIRS_NOAA20_NRT", -6.8, 107.35, 0, hotspot.Nominal))
	a, err := fire.DefaultPolicy().Assess(fire.EventIDFor(d.ID), d.ID, []fire.Detection{d})
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func(tx ports.FireTx) error{
		"ID tidak cocok": func(tx ports.FireTx) error {
			if err := tx.SaveEvent(ctx, ports.FireEventRecord{Assessment: a, Revision: 1}); err != nil {
				return err
			}
			bad := d
			bad.Lat = -6.9
			return tx.AddDetection(ctx, a.ID, bad)
		},
		"hitungan": func(tx ports.FireTx) error {
			bad := a
			bad.PeakOverpasses = 2
			return tx.SaveEvent(ctx, ports.FireEventRecord{Assessment: bad, Revision: 1})
		},
		"produk": func(tx ports.FireTx) error {
			bad := a
			bad.Products = []string{"LANDSAT"}
			return tx.SaveEvent(ctx, ports.FireEventRecord{Assessment: bad, Revision: 1})
		},
	} {
		err := store.InTx(ctx, f)
		if !errors.Is(err, fire.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestFireMatchesFireclusterInDatabase memutar deteksi asli Agustus 2023 ke
// PostgreSQL asli dan membandingkan kejadian dengan berkas acuan firecluster
// ingest (sama dengan internal/app/fires TestMatchesFirecluster, tetapi
// lewat SQL Nearby dan Members).
func TestFireMatchesFireclusterInDatabase(t *testing.T) {
	p := firePool(t)
	ctx := t.Context()
	cal, err := calibration.LoadFire()
	if err != nil {
		t.Fatal(err)
	}
	var clock time.Time
	svc, err := fires.New(postgres.NewFireStore(p), hazardpb.FireEncoder{}, fire.DefaultPolicy(), cal, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../app/fires/testdata/firms-2023-08.csv")
	if err != nil {
		t.Fatal(err)
	}
	cr := csv.NewReader(bytes.NewReader(b))
	cr.Comment = '#'
	recs, err := cr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs[1:] {
		lat, _ := strconv.ParseFloat(r[2], 64)
		lon, _ := strconv.ParseFloat(r[3], 64)
		at, _ := time.Parse(time.RFC3339, r[4])
		d := hotspot.Detection{
			ID: r[0], Product: r[1], Satellite: "N20", Instrument: "VIIRS", Lat: lat, Lon: lon, DetectedAt: at, Confidence: hotspot.Confidence(r[5]),
			BrightnessK: 340, ScanKm: 0.4, TrackKm: 0.4, Version: "2.0NRT", FetchedAt: at.Add(3 * time.Hour),
		}
		if r[1] == "MODIS_NRT" {
			pct := map[hotspot.Confidence]int{hotspot.Low: 20, hotspot.Nominal: 60, hotspot.High: 90}[d.Confidence]
			d.Instrument, d.Satellite, d.ConfidencePct = "MODIS", "Aqua", &pct
		}
		clock = d.FetchedAt
		if _, err := svc.Process(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	var want struct {
		Events []struct {
			Members []string `json:"members"`
			Level   string   `json:"level"`
		} `json:"events"`
	}
	b, err = os.ReadFile("../../app/fires/testdata/firms-2023-08.golden.json")
	if err != nil || json.Unmarshal(b, &want) != nil {
		t.Fatal(err)
	}
	rows, err := p.Query(ctx, `SELECT array_agg(d.detection_id ORDER BY d.detected_at, d.detection_id), e.level
		FROM hazard.fire_detection d JOIN hazard.event e ON e.id = d.event_id
		WHERE d.detected_at BETWEEN '2023-08-17' AND '2023-09-09' GROUP BY e.id, e.level`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var ids []string
		var level int
		if err := rows.Scan(&ids, &level); err != nil {
			t.Fatal(err)
		}
		got[ids[0]+"|"+strconv.Itoa(len(ids))] = fire.Label(hazard.Level(level))
	}
	if len(got) != len(want.Events) {
		t.Fatalf("%d kejadian, acuan %d", len(got), len(want.Events))
	}
	for _, w := range want.Events {
		if got[w.Members[0]+"|"+strconv.Itoa(len(w.Members))] != w.Level {
			t.Fatalf("kejadian %s (%d anggota): %q, acuan %s", w.Members[0], len(w.Members), got[w.Members[0]+"|"+strconv.Itoa(len(w.Members))], w.Level)
		}
	}
}
