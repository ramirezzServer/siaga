package sources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
)

// memStore adalah ports.SourceStatusStore di memori.
type memStore struct {
	rows map[string]sourcestatus.State
	err  error
}

func (m *memStore) Update(_ context.Context, connector string, fn func(*sourcestatus.State) (sourcestatus.State, bool)) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	var prev *sourcestatus.State
	if s, ok := m.rows[connector]; ok {
		prev = &s
	}
	next, save := fn(prev)
	if save {
		m.rows[connector] = next
	}
	return save, nil
}

var t0 = time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

func poll(at time.Duration, ok bool) sourcestatus.Poll {
	return sourcestatus.Poll{
		Connector: "bmkg-cap", Subject: "raw.weather.bmkg", Interval: 2 * time.Minute,
		StartedAt: t0.Add(at), FinishedAt: t0.Add(at + time.Second), OK: ok,
	}
}

func TestRecord(t *testing.T) {
	store := &memStore{rows: map[string]sourcestatus.State{}}
	svc := New(store, func() time.Time { return t0.Add(time.Hour) })
	ctx := context.Background()

	if res, err := svc.Record(ctx, poll(0, true)); err != nil || !res.Changed {
		t.Fatalf("pertama %+v %v", res, err)
	}
	if res, err := svc.Record(ctx, poll(0, true)); err != nil || res.Changed {
		t.Fatalf("ulangan harus tidak mengubah apa pun: %+v %v", res, err)
	}
	if res, err := svc.Record(ctx, poll(2*time.Minute, false)); err != nil || !res.Changed {
		t.Fatalf("gagal %+v %v", res, err)
	}
	if st := store.rows["bmkg-cap"]; st.ConsecutiveFailures != 1 || !st.LastSuccessAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("keadaan %+v", st)
	}

	bad := poll(3*time.Minute, true)
	bad.Subject = "raw.quake.emsc"
	if _, err := svc.Record(ctx, bad); !errors.Is(err, sourcestatus.ErrInvalid) {
		t.Fatalf("subjek tidak dikenal: %v", err)
	}

	// Keadaan tersimpan yang melanggar invarian tidak ditimpa.
	broken := store.rows["bmkg-cap"]
	broken.FailingSince = time.Time{}
	store.rows["bmkg-cap"] = broken
	if res, err := svc.Record(ctx, poll(4*time.Minute, false)); !errors.Is(err, sourcestatus.ErrInvalid) || res.Changed {
		t.Fatalf("keadaan rusak: %+v %v", res, err)
	}
	if store.rows["bmkg-cap"] != broken {
		t.Fatal("keadaan rusak tidak boleh ditimpa")
	}

	boom := errors.New("database mati")
	store.err = boom
	if _, err := svc.Record(ctx, poll(5*time.Minute, true)); !errors.Is(err, boom) {
		t.Fatalf("galat database harus diteruskan: %v", err)
	}
}
