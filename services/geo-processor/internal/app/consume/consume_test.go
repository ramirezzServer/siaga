package consume

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

var now = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

func handler(t *testing.T, decodeErr, processErr error, res Outcome) *Handler[quake.Report] {
	t.Helper()
	h, err := New(
		func([]byte) (quake.Report, error) { return quake.Report{}, decodeErr },
		func(context.Context, quake.Report) (Outcome, error) { return res, processErr },
		[]error{quake.ErrInvalid, weather.ErrInvalid},
		DefaultOptions(), func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHandleDecisions(t *testing.T) {
	ctx := context.Background()
	transient := errors.New("koneksi database putus")
	cases := []struct {
		name       string
		decodeErr  error
		processErr error
		delivered  int
		want       Action
		delay      time.Duration
	}{
		{"berhasil", nil, nil, 1, Ack, 0},
		{"payload rusak", quake.ErrInvalid, nil, 1, DeadLetter, 0},
		{"invarian dilanggar", nil, quake.ErrInvalid, 1, DeadLetter, 0},
		{"invarian cuaca dilanggar", nil, weather.ErrInvalid, 1, DeadLetter, 0},
		{"sementara pertama", nil, transient, 1, Retry, time.Second},
		{"sementara kedua", nil, transient, 2, Retry, 5 * time.Second},
		{"sementara keempat", nil, transient, 4, Retry, 30 * time.Second},
		{"sementara kelima", nil, transient, 5, DeadLetter, 0},
	}
	for _, c := range cases {
		d := handler(t, c.decodeErr, c.processErr, Outcome{}).Handle(ctx, nil, c.delivered)
		if d.Action != c.want || d.Delay != c.delay {
			t.Errorf("%s: %v/%v, ingin %v/%v", c.name, d.Action, d.Delay, c.want, c.delay)
		}
		if (d.Action == Ack) != (d.Err == nil) {
			t.Errorf("%s: Err %v tidak sesuai aksi", c.name, d.Err)
		}
	}
}

func TestHandleRetriesQuicklyOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := handler(t, nil, context.Canceled, Outcome{}).Handle(ctx, nil, 5)
	if d.Action != Retry || d.Delay != 0 {
		t.Fatalf("berhenti: %+v", d)
	}
}

func TestStatsAndActionString(t *testing.T) {
	h := handler(t, nil, nil, Outcome{Changed: true, Created: 1, Updated: 2, Ended: 1})
	h.Handle(context.Background(), nil, 1)
	u := handler(t, nil, nil, Outcome{})
	u.Handle(context.Background(), nil, 1)
	f := handler(t, quake.ErrInvalid, nil, Outcome{})
	f.Handle(context.Background(), nil, 1)
	r := handler(t, nil, errors.New("x"), Outcome{})
	r.Handle(context.Background(), nil, 1)

	if s := h.Snapshot(); s.Received != 1 || s.Applied != 1 || s.Created != 1 || s.Updated != 2 || s.Ended != 1 || !s.LastSuccess.Equal(now) {
		t.Errorf("stats berhasil %+v", s)
	}
	if s := u.Snapshot(); s.Unchanged != 1 || s.Applied != 0 {
		t.Errorf("stats ulangan %+v", s)
	}
	if s := f.Snapshot(); s.DeadLettered != 1 || s.LastError == "" {
		t.Errorf("stats DLQ %+v", s)
	}
	if s := r.Snapshot(); s.Retried != 1 {
		t.Errorf("stats retry %+v", s)
	}
	for a, want := range map[Action]string{Ack: "ack", Retry: "retry", DeadLetter: "dead-letter", 0: "unknown"} {
		if a.String() != want {
			t.Errorf("%d: %q", a, a.String())
		}
	}
	if _, err := New[quake.Report](nil, nil, nil, Options{}, time.Now); err == nil {
		t.Error("opsi kosong harus ditolak")
	}
}
