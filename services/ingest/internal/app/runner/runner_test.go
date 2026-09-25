package runner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// fakeClock maju sendiri saat Sleep dipanggil, jadi loop berjalan tanpa menunggu.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

type step struct {
	res poll.Result
	err error
}

// scriptPoller menjalankan skenario lalu membatalkan ctx setelah langkah terakhir.
type scriptPoller struct {
	name   string
	steps  []step
	calls  int
	cancel context.CancelFunc
	at     []time.Time
	clock  *fakeClock
}

func (p *scriptPoller) Name() string { return p.name }

func (p *scriptPoller) Poll(context.Context) (poll.Result, error) {
	p.at = append(p.at, p.clock.Now())
	s := p.steps[p.calls]
	p.calls++
	if p.calls == len(p.steps) && p.cancel != nil {
		p.cancel()
	}
	return s.res, s.err
}

func newRunner(clk *fakeClock) (*Runner, *bytes.Buffer) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(clk, log, Options{MaxBackoff: 4 * time.Minute, JitterFrac: 0.1, Rand: func() float64 { return 0.5 }}), &buf
}

func TestLoopBackoffAndRecovery(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	r, logs := newRunner(clk)
	ctx, cancel := context.WithCancel(context.Background())
	boom := errors.New("sumber mati")
	p := &scriptPoller{name: "uji", clock: clk, cancel: cancel, steps: []step{
		{res: poll.Result{Published: 2, Events: 2}},
		{err: boom},
		{err: boom},
		{err: boom},
		{err: &ports.RetryAfterError{Status: 429, After: 3 * time.Minute}},
		{res: poll.Result{Unchanged: true}},
		{res: poll.Result{Published: 1, Rejected: 1, Rejections: []ports.Rejection{{Key: "x", Reason: boom}}, ArchiveErr: boom}},
	}}
	r.Run(ctx, []Job{{Poller: p, Interval: 30 * time.Second}})

	want := []time.Duration{
		30 * time.Second,                              // sukses
		time.Minute, 2 * time.Minute, 4 * time.Minute, // backoff 2^n
		4 * time.Minute,  // gagal kelima dibatasi MaxBackoff walau Retry-After 3 menit
		30 * time.Second, // pulih
	}
	if !equal(clk.sleeps, want) {
		t.Fatalf("jeda = %v, ingin %v", clk.sleeps, want)
	}
	st := r.Snapshot()
	if len(st) != 1 || st[0].ConsecutiveFailures != 0 || st[0].Published != 3 || st[0].Rejected != 1 || st[0].LastError != "" {
		t.Fatalf("status akhir %+v", st)
	}
	if !st[0].LastChange.Equal(p.at[6]) || !st[0].LastSuccess.Equal(p.at[6]) {
		t.Fatalf("LastChange/LastSuccess %+v", st[0])
	}
	out := logs.String()
	for _, want := range []string{"level=WARN msg=\"polling gagal\"", "level=ERROR msg=\"polling gagal\"", "record ditolak", "arsip gagal"} {
		if !strings.Contains(out, want) {
			t.Errorf("log tidak memuat %q", want)
		}
	}
}

func TestRetryAfterLongerThanBackoff(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	r, _ := newRunner(clk)
	r.opt.MaxBackoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	p := &scriptPoller{name: "uji", clock: clk, cancel: cancel, steps: []step{
		{err: &ports.RetryAfterError{Status: 503, After: 20 * time.Minute}},
		{},
	}}
	r.Run(ctx, []Job{{Poller: p, Interval: 30 * time.Second}})
	if clk.sleeps[0] != 20*time.Minute {
		t.Fatalf("Retry-After harus dihormati, jeda %v", clk.sleeps[0])
	}
}

func TestSharedLimiterSpacesRequests(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	r, _ := newRunner(clk)
	lim, err := NewLimiter("bmkg", 60, 1)
	if err != nil || lim.Name() != "bmkg" {
		t.Fatal(err)
	}
	jobs := []Job{
		{Poller: &scriptPoller{name: "a", clock: clk, steps: []step{{}}}, Limiters: []*Limiter{lim}},
		{Poller: &scriptPoller{name: "b", clock: clk, steps: []step{{}}}, Limiters: []*Limiter{lim}},
		{Poller: &scriptPoller{name: "c", clock: clk, steps: []step{{err: errors.New("gagal")}}}, Limiters: []*Limiter{lim}},
	}
	err = r.RunOnce(context.Background(), jobs)
	if err == nil || !strings.Contains(err.Error(), "gagal") {
		t.Fatalf("RunOnce harus meneruskan galat: %v", err)
	}
	if !equal(clk.sleeps, []time.Duration{time.Second, time.Second}) {
		t.Fatalf("anggaran 60/menit burst 1 harus memberi jeda 1 detik, jeda %v", clk.sleeps)
	}
	if got := r.Snapshot(); len(got) != 3 || got[0].Connector != "a" || got[2].ConsecutiveFailures != 1 {
		t.Fatalf("snapshot %+v", got)
	}
}

func TestCancelledContextStopsQuietly(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	r, logs := newRunner(clk)
	lim, _ := NewLimiter("x", 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	p := &scriptPoller{name: "uji", clock: clk, cancel: cancel, steps: []step{{err: context.Canceled}}}
	r.Run(ctx, []Job{{Poller: p, Interval: time.Second, Limiters: []*Limiter{lim}}})
	if strings.Contains(logs.String(), "polling gagal") {
		t.Fatal("pembatalan saat shutdown tidak boleh dicatat sebagai kegagalan sumber")
	}
	// Anggaran habis dan ctx sudah batal: acquire berhenti tanpa polling.
	if err := r.RunOnce(ctx, []Job{{Poller: p, Limiters: []*Limiter{lim}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce dengan ctx batal: %v", err)
	}
}

func TestNewDefaultsAndLimiterValidation(t *testing.T) {
	r := New(&fakeClock{}, slog.New(slog.DiscardHandler), Options{})
	if r.opt.MaxBackoff != 10*time.Minute || r.opt.Rand == nil {
		t.Fatalf("default tidak terisi: %+v", r.opt)
	}
	if _, err := NewLimiter("x", 0, 1); err == nil {
		t.Fatal("anggaran 0/menit harus ditolak")
	}
}

func equal(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGateHighAndLowLanes(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	shared, _ := NewLimiter("bmkg", 60, 3)
	own, _ := NewLimiter("bmkg-prakiraan", 60, 1)
	g, err := NewGate(clk, own).Low(shared, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewGate(clk).Low(shared, 3); err == nil {
		t.Fatal("cadangan melebihi burst-1 harus ditolak")
	}
	// Request rendah pertama langsung jalan; berikutnya menunggu anggaran sendiri (1 dtk)
	// dan cadangan 2 izin di anggaran bersama.
	if err := g.Wait(t.Context()); err != nil || len(clk.sleeps) != 0 {
		t.Fatalf("err=%v jeda=%v", err, clk.sleeps)
	}
	// Request biasa memakai cadangan tanpa menunggu.
	for range 2 {
		if w := shared.reserve(clk.Now()); w != 0 {
			t.Fatalf("request biasa menunggu %v", w)
		}
	}
	if err := g.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Anggaran sendiri 1 dtk, lalu bersama harus pulih sampai 3 izin (sisa 3 dtk lagi).
	var total time.Duration
	for _, d := range clk.sleeps {
		total += d
	}
	if total != 3*time.Second {
		t.Fatalf("jeda total %v (%v), ingin 3 dtk", total, clk.sleeps)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	// Cadangan penuh: request rendah menunggu di anggaran bersama lalu dibatalkan.
	g2, _ := NewGate(clk).Low(shared, 2)
	for range 3 {
		_ = shared.reserve(clk.Now())
	}
	ctx2, cancel2 := context.WithCancel(t.Context())
	clk2 := &cancelClock{fakeClock: clk, cancel: cancel2}
	g2.clock = clk2
	if err := g2.Wait(ctx2); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// cancelClock membatalkan ctx pada Sleep pertama.
type cancelClock struct {
	*fakeClock
	cancel context.CancelFunc
}

func (c *cancelClock) Sleep(ctx context.Context, d time.Duration) error {
	c.cancel()
	return c.fakeClock.Sleep(ctx, d)
}

type ctxKey struct{}

func TestObserverWrapsEveryPoll(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)}
	type seen struct {
		connector string
		res       poll.Result
		err       error
	}
	var got []seen
	var polledWith []any
	boom := errors.New("sumber mati")
	p := &ctxPoller{name: "usgs-2.5-day", results: []step{{res: poll.Result{Published: 2}}, {err: boom}}, got: &polledWith}
	r := New(clk, slog.New(slog.DiscardHandler), Options{
		Rand: func() float64 { return 0.5 },
		Observe: func(ctx context.Context, connector string) (context.Context, func(poll.Result, error)) {
			return context.WithValue(ctx, ctxKey{}, "span-"+connector), func(res poll.Result, err error) {
				got = append(got, seen{connector, res, err})
			}
		},
	})
	err := r.RunOnce(context.Background(), []Job{{Poller: p, Interval: time.Minute}, {Poller: p, Interval: time.Minute}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 || got[0].res.Published != 2 || !errors.Is(got[1].err, boom) || got[0].connector != "usgs-2.5-day" {
		t.Fatalf("pengamat menerima %+v", got)
	}
	if len(polledWith) != 2 || polledWith[0] != "span-usgs-2.5-day" {
		t.Fatalf("polling tidak memakai context dari pengamat: %v", polledWith)
	}
	st := r.Snapshot()[0]
	if st.Attempts != 2 || st.Failures != 1 || st.ConsecutiveFailures != 1 {
		t.Fatalf("status %+v", st)
	}
}

// ctxPoller mencatat nilai ctxKey dari context yang dipakai polling.
type ctxPoller struct {
	name    string
	results []step
	n       int
	got     *[]any
}

func (p *ctxPoller) Name() string { return p.name }

func (p *ctxPoller) Poll(ctx context.Context) (poll.Result, error) {
	*p.got = append(*p.got, ctx.Value(ctxKey{}))
	s := p.results[p.n]
	p.n++
	return s.res, s.err
}

func TestLimiterStats(t *testing.T) {
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	l, err := NewLimiter("bmkg", 60, 3)
	if err != nil {
		t.Fatal(err)
	}
	if st := l.Stats(now); st.Available != 3 || st.Reserved != 0 || st.Burst != 3 || st.PerMinute != 60 || st.Name != "bmkg" {
		t.Fatalf("awal %+v", st)
	}
	_ = l.reserve(now)
	if ok, _ := l.reserveLow(now, 1); !ok {
		t.Fatal("jalur rendah harus dapat izin")
	}
	if ok, _ := l.reserveLow(now, 1); ok {
		t.Fatal("jalur rendah tidak boleh memakai cadangan terakhir")
	}
	if st := l.Stats(now); st.Available != 1 || st.Reserved != 2 {
		t.Fatalf("setelah dua pesanan %+v (pesanan rendah yang ditolak tidak dihitung)", st)
	}
}
