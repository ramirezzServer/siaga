//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/hazardpb"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/quakes"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Semua data uji memakai tahun 2001 dan provinsi 99 supaya tidak menyentuh data nyata.
var base = time.Date(2001, 1, 2, 3, 4, 5, 0, time.UTC)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("SIAGA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SIAGA_TEST_DATABASE_URL tidak diisi")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	for _, q := range []string{
		`DELETE FROM hazard.outbox`,
		`DELETE FROM hazard.cap_message WHERE sent < '2002-01-01'`,
		`DELETE FROM hazard.event_source WHERE occurred_at < '2002-01-01'`,
		`UPDATE hazard.event SET merged_into = NULL, status = 'expired' WHERE occurred_at < '2002-01-01' AND status = 'merged'`,
		`DELETE FROM hazard.event WHERE occurred_at < '2002-01-01'`,
	} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Wilayah uji: kotak 0,02° di sekitar (-7, 107) dan satu desa ±40 km di selatan.
	conn, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `DELETE FROM hazard.impact_region WHERE region_code LIKE '99.%'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM ref.region WHERE code = '99' OR code LIKE '99.%'`); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRegionStore(conn.Conn())
	regions := []region.Region{
		box(t, "99", "Provinsi Uji", [4]float64{106, -8, 108, -6}),
		box(t, "99.01", "Kabupaten Uji", [4]float64{106, -8, 108, -6}),
		box(t, "99.01.01", "Kecamatan Uji", [4]float64{106, -8, 108, -6}),
		box(t, "99.01.01.2001", "Desa Pusat", [4]float64{106.99, -7.01, 107.01, -6.99}),
		box(t, "99.01.01.2002", "Desa Selatan", [4]float64{106.99, -7.37, 107.01, -7.35}),
	}
	if _, err := store.UpsertAll(ctx, regions, "test", "v1"); err != nil {
		t.Fatal(err)
	}
	return p
}

func box(t *testing.T, code, name string, b [4]float64) region.Region {
	t.Helper()
	ring := region.Ring{{b[0], b[1]}, {b[2], b[1]}, {b[2], b[3]}, {b[0], b[3]}, {b[0], b[1]}}
	r, err := region.New(region.MustParseCode(code), name, region.MultiPolygon{{ring}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func report(src quake.Source, id string, dt time.Duration, lat, mag float64, fetched time.Duration) quake.Report {
	r := quake.Report{
		Source: src, EventID: id, OccurredAt: base.Add(dt), Latitude: lat, Longitude: 107, Magnitude: mag,
		DepthKm: 10, Place: "uji", FetchedAt: base.Add(fetched), Tsunami: quake.TsunamiNone,
	}
	switch src {
	case quake.SourceBMKG:
		r.Feed = quake.FeedBMKGLatest
		r.ShakemapURL = "https://data.bmkg.go.id/x.mmi.jpg"
	case quake.SourceUSGS:
		r.Feed = quake.FeedUSGSSummary
		r.SourceUpdatedAt = base.Add(fetched - time.Second)
		r.ReviewStatus = "automatic"
		r.AlternateIDs = []string{"at" + id}
		r.MagnitudeType = "mb"
	}
	return r
}

func service(t *testing.T, store *postgres.QuakeStore) *quakes.Service {
	t.Helper()
	s, err := quakes.New(store, hazardpb.Encoder{}, quake.DefaultRules(), quake.DefaultPolicy(), func() time.Time { return base.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func drain(t *testing.T, store *postgres.QuakeStore) []string {
	t.Helper()
	var got []string
	for {
		n, err := store.Drain(context.Background(), 2, func(_ context.Context, m ports.OutboxMessage) error {
			got = append(got, m.Subject)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return got
		}
	}
}

func TestQuakePipelineInDatabase(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	store := postgres.NewQuakeStore(p)
	svc := service(t, store)

	b := report(quake.SourceBMKG, "20010102030405", 0, -7.0, 5.0, time.Minute)
	if res, err := svc.Process(ctx, b); err != nil || res.Created != 1 {
		t.Fatalf("BMKG: %+v %v", res, err)
	}
	// Ulangan persis: tidak ada perubahan.
	if res, err := svc.Process(ctx, b); err != nil || res.Changed {
		t.Fatalf("ulangan: %+v %v", res, err)
	}
	u := report(quake.SourceUSGS, "us0001", -2*time.Second, -7.05, 5.2, 3*time.Minute)
	if res, err := svc.Process(ctx, u); err != nil || res.Updated != 1 {
		t.Fatalf("USGS: %+v %v", res, err)
	}

	var (
		events, members, impacts int
		level, rev               int
		area                     bool
		codes                    []string
	)
	row := p.QueryRow(ctx, `SELECT count(*), max(level), max(revision), bool_and(area IS NOT NULL) FROM hazard.event WHERE occurred_at < '2002-01-01'`)
	if err := row.Scan(&events, &level, &rev, &area); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT count(DISTINCT event_id) FILTER (WHERE event_id IS NOT NULL), count(*) FROM hazard.event_source WHERE occurred_at < '2002-01-01'`).Scan(&members, &impacts); err != nil {
		t.Fatal(err)
	}
	if events != 1 || members != 1 || rev != 2 || !area || level != int(quake.LevelSiaga) {
		t.Fatalf("event=%d tertaut=%d revisi=%d area=%v level=%d", events, members, rev, area, level)
	}
	rows, err := p.Query(ctx, `SELECT region_code FROM hazard.impact_region WHERE region_code LIKE '99.%' ORDER BY distance_km`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		codes = append(codes, c)
	}
	if !slices.Equal(codes, []string{"99.01.01.2001", "99.01.01.2002"}) {
		t.Fatalf("wilayah terdampak %v", codes)
	}
	if got := drain(t, store); !slices.Equal(got, []string{"hazard.quake.created", "hazard.quake.updated"}) {
		t.Fatalf("outbox %v", got)
	}

	// Kedaluwarsa setelah 6 jam; pesan expired masuk outbox.
	if n, err := svc.Expire(ctx, base.Add(7*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("Expire: %d %v", n, err)
	}
	if got := drain(t, store); !slices.Equal(got, []string{"hazard.quake.expired"}) {
		t.Fatalf("outbox setelah expire %v", got)
	}
}

func TestMergeAndRetractInDatabase(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	store := postgres.NewQuakeStore(p)
	svc := service(t, store)

	mustProcess := func(r quake.Report) quakes.Result {
		t.Helper()
		res, err := svc.Process(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	mustProcess(report(quake.SourceBMKG, "20010102030405", 0, -7.0, 5.0, time.Minute))
	far := report(quake.SourceUSGS, "us0002", 0, -8.5, 5.0, 2*time.Minute)
	if res := mustProcess(far); res.Created != 1 {
		t.Fatalf("USGS jauh: %+v", res)
	}
	near := report(quake.SourceUSGS, "us0002", 0, -7.1, 5.0, 10*time.Minute)
	if res := mustProcess(near); res.Ended != 1 || res.Updated != 1 {
		t.Fatalf("revisi masuk jangkauan: %+v", res)
	}
	var merged int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM hazard.event WHERE status = 'merged' AND merged_into IS NOT NULL AND occurred_at < '2002-01-01'`).Scan(&merged); err != nil || merged != 1 {
		t.Fatalf("merged=%d %v", merged, err)
	}

	lone := report(quake.SourceUSGS, "us0003", time.Hour, -6.5, 4.0, time.Hour+time.Minute)
	mustProcess(lone)
	lone.ReviewStatus = quake.ReviewDeleted
	lone.SourceUpdatedAt = lone.SourceUpdatedAt.Add(time.Minute)
	lone.FetchedAt = lone.FetchedAt.Add(time.Minute)
	if res := mustProcess(lone); res.Ended != 1 {
		t.Fatalf("ditarik: %+v", res)
	}
	var retracted, detached int
	_ = p.QueryRow(ctx, `SELECT count(*) FROM hazard.event WHERE status = 'retracted' AND occurred_at < '2002-01-01'`).Scan(&retracted)
	_ = p.QueryRow(ctx, `SELECT count(*) FROM hazard.event_source WHERE source_event_id = 'us0003' AND event_id IS NULL`).Scan(&detached)
	if retracted != 1 || detached != 1 {
		t.Fatalf("retracted=%d detached=%d", retracted, detached)
	}
}

// Invarian dijaga dua kali: bila kode domain salah, constraint database tetap menolak.
func TestDatabaseRejectsBrokenInvariants(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	store := postgres.NewQuakeStore(p)
	id := quake.NewEventID(quake.IdentityKey{Source: quake.SourceBMKG, EventID: "20010102030405"}, 0)
	good := ports.EventRecord{Revision: 1}
	good.ID = id
	good.Level = quake.LevelInfo
	good.Title = "Gempa uji"
	good.Primary = quake.Solution{Key: quake.IdentityKey{Source: quake.SourceBMKG, EventID: "20010102030405"}, OccurredAt: base, Latitude: -7, Longitude: 107, Magnitude: 4}
	good.DetectedAt = base
	good.ExpiresAt = base.Add(6 * time.Hour)

	for name, mutate := range map[string]func(*ports.EventRecord){
		"tingkat di luar rentang": func(r *ports.EventRecord) { r.Level = 9 },
		"kedaluwarsa sebelum":     func(r *ports.EventRecord) { r.ExpiresAt = base.Add(-time.Second) },
		"revisi nol":              func(r *ports.EventRecord) { r.Revision = 0 },
		"magnitudo mustahil":      func(r *ports.EventRecord) { r.Primary.Magnitude = 12 },
		"wilayah bukan desa": func(r *ports.EventRecord) {
			r.Impacts = []quake.Impact{{RegionDistance: quake.RegionDistance{Code: "99.01"}, Level: quake.LevelSiaga}}
		},
		"tingkat wilayah Info": func(r *ports.EventRecord) {
			r.Impacts = []quake.Impact{{RegionDistance: quake.RegionDistance{Code: "99.01.01.2001"}, Level: quake.LevelInfo}}
		},
		"wilayah tidak terdaftar": func(r *ports.EventRecord) {
			r.Impacts = []quake.Impact{{RegionDistance: quake.RegionDistance{Code: "99.01.01.2999"}, Level: quake.LevelSiaga}}
		},
		"shakemap bukan https": func(r *ports.EventRecord) { r.Primary.ShakemapURL = "http://x" },
	} {
		rec := good
		mutate(&rec)
		err := store.InTx(ctx, func(tx ports.QuakeTx) error { return tx.SaveEvent(ctx, rec) })
		if err == nil {
			t.Errorf("%s: seharusnya ditolak database", name)
		}
	}
	err := store.InTx(ctx, func(tx ports.QuakeTx) error {
		if err := tx.SaveEvent(ctx, good); err != nil {
			return err
		}
		// Status merged wajib punya tujuan.
		return tx.EndEvent(ctx, id, ports.StatusMerged, quake.EventID{}, base.Add(time.Hour), 2)
	})
	if err == nil {
		t.Error("merged tanpa tujuan seharusnya ditolak")
	}
	err = store.InTx(ctx, func(tx ports.QuakeTx) error {
		return tx.EndEvent(ctx, id, ports.StatusExpired, quake.EventID{}, base, 2)
	})
	if err == nil {
		t.Error("mengakhiri kejadian yang tidak ada harus gagal")
	}
	bad := quake.Stored{Report: report(quake.SourceUSGS, "us0009", 0, -7, 4, time.Minute)}
	bad.FirstSeenAt = bad.FetchedAt.Add(time.Hour)
	err = store.InTx(ctx, func(tx ports.QuakeTx) error { return tx.SaveReport(ctx, bad) })
	if err == nil {
		t.Error("first_seen_at setelah fetched_at seharusnya ditolak")
	}
}

func TestRegionsWithinAndOutboxLocking(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	store := postgres.NewQuakeStore(p)
	var got []quake.RegionDistance
	err := store.InTx(ctx, func(tx ports.QuakeTx) error {
		var err error
		got, err = tx.RegionsWithin(ctx, -7, 107, 45)
		if err != nil {
			return err
		}
		none, err := tx.RegionsWithin(ctx, -7, 107, 0)
		if len(none) != 0 {
			t.Error("radius nol harus kosong")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var test []quake.RegionDistance
	for _, r := range got {
		if len(r.Code) > 3 && r.Code[:3] == "99." {
			test = append(test, r)
		}
	}
	if len(test) != 2 || test[0].DistanceKm != 0 || test[1].DistanceKm < 37 || test[1].DistanceKm > 39.5 {
		t.Fatalf("wilayah dalam 45 km: %+v", test)
	}

	// Pesan outbox yang gagal terbit tetap ada untuk dicoba lagi.
	err = store.InTx(ctx, func(tx ports.QuakeTx) error {
		for _, id := range []string{"a", "b"} {
			if err := tx.Enqueue(ctx, ports.OutboxMessage{Subject: "hazard.quake.created", MsgID: "uji-" + id, Payload: []byte(id)}); err != nil {
				return err
			}
		}
		return tx.Enqueue(ctx, ports.OutboxMessage{Subject: "hazard.quake.created", MsgID: "uji-a", Payload: []byte("dup")})
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := store.Drain(ctx, 10, func(_ context.Context, m ports.OutboxMessage) error {
		if m.MsgID == "uji-b" {
			return errors.New("broker mati")
		}
		return nil
	})
	if n != 1 || err == nil {
		t.Fatalf("Drain sebagian: %d %v", n, err)
	}
	if left := drain(t, store); !slices.Equal(left, []string{"hazard.quake.created"}) {
		t.Fatalf("sisa outbox %v", left)
	}
}
