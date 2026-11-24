//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/sources"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
)

// Konektor uji berawalan uji-geo-, waktu di tahun 2001 (konvensi data uji),
// jadi aman dijalankan di database lokal yang berisi status konektor asli.
func sourceStatusPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := pool(t)
	clean := func() {
		if _, err := p.Exec(context.Background(), `DELETE FROM ts.source_status WHERE connector LIKE 'uji-geo-%'`); err != nil {
			t.Fatal(err)
		}
	}
	clean()
	t.Cleanup(clean)
	return p
}

func TestSourceStatusStoreFoldsPolls(t *testing.T) {
	p := sourceStatusPool(t)
	ctx := context.Background()
	t0 := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := sources.New(postgres.NewSourceStatusStore(p), func() time.Time { return t0.Add(24 * time.Hour) })
	at := func(d time.Duration, ok, changed bool) sourcestatus.Poll {
		p := sourcestatus.Poll{
			Connector: "uji-geo-sapuan", Subject: "raw.forecast.bmkg", Interval: 6 * time.Hour,
			StartedAt: t0.Add(d), FinishedAt: t0.Add(d + 2*time.Hour), OK: ok,
		}
		if changed {
			p.Published = 12
		}
		return p
	}
	for i, step := range []struct {
		p       sourcestatus.Poll
		changed bool
	}{
		{at(0, true, true), true},
		{at(0, true, true), false}, // ulangan
		{at(6*time.Hour, true, false), true},
		{at(12*time.Hour, false, false), true},
		{at(18*time.Hour, false, false), true},
		{at(9*time.Hour, true, true), false}, // terlambat
	} {
		res, err := svc.Record(ctx, step.p)
		if err != nil || res.Changed != step.changed {
			t.Fatalf("langkah %d: %+v %v, ingin changed=%v", i, res, err, step.changed)
		}
	}
	var (
		subject                       string
		intervalSec                   float64
		attempt, success, change, bad time.Time
		failures                      int
	)
	err := p.QueryRow(ctx, `SELECT subject, extract(epoch FROM poll_interval), last_attempt_at, last_success_at,
		last_change_at, failing_since, consecutive_failures FROM ts.source_status WHERE connector = 'uji-geo-sapuan'`).
		Scan(&subject, &intervalSec, &attempt, &success, &change, &bad, &failures)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "raw.forecast.bmkg" || intervalSec != 6*3600 || !attempt.Equal(t0.Add(20*time.Hour)) ||
		!success.Equal(t0.Add(8*time.Hour)) || !change.Equal(t0.Add(2*time.Hour)) || !bad.Equal(t0.Add(14*time.Hour)) || failures != 2 {
		t.Fatalf("baris %s %v coba=%v ok=%v ubah=%v gagal=%v x%d", subject, intervalSec, attempt, success, change, bad, failures)
	}

	// Konektor yang gagal sejak percobaan pertama: kolom berhasil dan berubah NULL.
	first := at(0, false, false)
	first.Connector = "uji-geo-mati"
	if _, err := svc.Record(ctx, first); err != nil {
		t.Fatal(err)
	}
	var nulls bool
	if err := p.QueryRow(ctx, `SELECT last_success_at IS NULL AND last_change_at IS NULL AND failing_since = last_attempt_at
		FROM ts.source_status WHERE connector = 'uji-geo-mati'`).Scan(&nulls); err != nil || !nulls {
		t.Fatalf("konektor mati: %v %v", nulls, err)
	}
}

// Constraint database menjaga invarian yang sama dengan domain: keadaan
// yang lolos ke adapter tanpa Validate tetap ditolak sebagai ErrInvalid.
func TestSourceStatusStoreConstraints(t *testing.T) {
	p := sourceStatusPool(t)
	ctx := context.Background()
	store := postgres.NewSourceStatusStore(p)
	t0 := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	good := sourcestatus.State{
		Connector: "uji-geo-cek", Subject: "raw.quake.usgs", Interval: time.Minute,
		LastAttemptAt: t0, LastSuccessAt: t0, LastChangeAt: t0,
	}
	for name, mutate := range map[string]func(*sourcestatus.State){
		"subjek":           func(s *sourcestatus.State) { s.Subject = "raw.quake.emsc" },
		"interval":         func(s *sourcestatus.State) { s.Interval = 8 * 24 * time.Hour },
		"berhasil setelah": func(s *sourcestatus.State) { s.LastSuccessAt = t0.Add(time.Second) },
		"gagal tanpa sejak": func(s *sourcestatus.State) {
			s.ConsecutiveFailures = 1
		},
		"ok tanpa berhasil": func(s *sourcestatus.State) { s.LastSuccessAt, s.LastChangeAt = time.Time{}, time.Time{} },
		"sejak sebelum ok": func(s *sourcestatus.State) {
			s.ConsecutiveFailures, s.FailingSince, s.LastAttemptAt = 1, t0.Add(-time.Second), t0.Add(time.Minute)
		},
	} {
		s := good
		mutate(&s)
		_, err := store.Update(ctx, s.Connector, func(*sourcestatus.State) (sourcestatus.State, bool) { return s, true })
		if !errors.Is(err, sourcestatus.ErrInvalid) {
			t.Errorf("%s: err = %v, ingin ErrInvalid", name, err)
		}
	}
	if saved, err := store.Update(ctx, good.Connector, func(prev *sourcestatus.State) (sourcestatus.State, bool) {
		if prev != nil {
			t.Error("keadaan yang ditolak tidak boleh tersimpan")
		}
		return good, true
	}); err != nil || !saved {
		t.Fatalf("keadaan valid: %v %v", saved, err)
	}
	if saved, err := store.Update(ctx, good.Connector, func(prev *sourcestatus.State) (sourcestatus.State, bool) {
		if prev == nil || *prev != good {
			t.Errorf("dibaca ulang %+v, ingin %+v", prev, good)
		}
		return good, false
	}); err != nil || saved {
		t.Fatalf("tanpa simpan: %v %v", saved, err)
	}
}
