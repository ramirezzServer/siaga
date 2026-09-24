package sweep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var t0 = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

// fakeSource: body "ok:<isi>" menjadi event, selain itu ditolak.
type fakeSource struct{}

func (fakeSource) Name() string       { return "bmkg-prakiraan" }
func (fakeSource) ArchiveExt() string { return "json" }
func (fakeSource) Request(code string) ports.Request {
	return ports.Request{URL: "adm4=" + code}
}

func (fakeSource) Parse(code string, body []byte, _ time.Time) (ports.Event, error) {
	content, ok := strings.CutPrefix(string(body), "ok:")
	if !ok {
		return nil, fmt.Errorf("payload %s rusak", code)
	}
	return fakeEvent{code: code, content: content}, nil
}

type fakeEvent struct{ code, content string }

func (e fakeEvent) Subject() string          { return "raw.forecast.bmkg" }
func (e fakeEvent) Key() string              { return e.code }
func (e fakeEvent) Content() ([]byte, error) { return []byte(e.code + "=" + e.content), nil }
func (e fakeEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	return []byte(e.code + "=" + e.content + "|" + m.ArchiveKey), nil
}

type reply struct {
	body string
	etag string
	err  error
}

type statusError int

func (e statusError) Error() string   { return fmt.Sprintf("HTTP %d", int(e)) }
func (e statusError) HTTPStatus() int { return int(e) }

// fakeFetcher menjawab per kode dari antrean; balasan terakhir diulang.
type fakeFetcher struct {
	mu       sync.Mutex
	replies  map[string][]reply
	requests []ports.Request
}

func (f *fakeFetcher) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	code := strings.TrimPrefix(r.URL, "adm4=")
	q := f.replies[code]
	rep := q[0]
	if len(q) > 1 {
		f.replies[code] = q[1:]
	}
	if rep.err != nil {
		return ports.Response{}, rep.err
	}
	if rep.etag != "" && r.ETag == rep.etag {
		return ports.Response{NotModified: true}, nil
	}
	return ports.Response{Body: []byte(rep.body), ETag: rep.etag}, nil
}

type fakeArchive struct{ fail bool }

func (a *fakeArchive) Put(context.Context, string, []byte) error {
	if a.fail {
		return errors.New("disk penuh")
	}
	return nil
}

type fakePub struct {
	msgs  []string
	ids   map[string]bool
	failN int
}

func (p *fakePub) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	if p.failN > 0 {
		p.failN--
		return ports.PublishResult{}, errors.New("NATS mati")
	}
	dup := p.ids[m.ID]
	p.ids[m.ID] = true
	p.msgs = append(p.msgs, string(m.Data))
	return ports.PublishResult{Duplicate: dup}, nil
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
	cancel context.CancelFunc
	after  int // batalkan ctx setelah sekian Sleep
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	stop := c.cancel != nil && len(c.sleeps) >= c.after
	c.mu.Unlock()
	if stop {
		c.cancel()
	}
	return ctx.Err()
}

// tickThrottle memajukan jam 1,2 detik per request (50/menit) dan bisa
// menjalankan hook, misal membaca Snapshot di tengah sapuan.
type tickThrottle struct {
	clock *fakeClock
	n     int
	hook  func()
}

func (t *tickThrottle) Wait(ctx context.Context) error {
	t.n++
	if t.hook != nil {
		t.hook()
	}
	t.clock.mu.Lock()
	t.clock.now = t.clock.now.Add(1200 * time.Millisecond)
	t.clock.mu.Unlock()
	return ctx.Err()
}

type rig struct {
	s     *Sweeper
	f     *fakeFetcher
	pub   *fakePub
	clock *fakeClock
	th    *tickThrottle
	logs  *bytes.Buffer
}

func newRig(t *testing.T, codes []string, replies map[string][]reply, mutate ...func(*Options)) *rig {
	t.Helper()
	opts := DefaultOptions()
	opts.PauseAfter = 3
	for _, m := range mutate {
		m(&opts)
	}
	clk := &fakeClock{now: t0}
	r := &rig{f: &fakeFetcher{replies: replies}, pub: &fakePub{ids: map[string]bool{}}, clock: clk, th: &tickThrottle{clock: clk}, logs: &bytes.Buffer{}}
	log := slog.New(slog.NewTextHandler(r.logs, nil))
	s, err := New(fakeSource{}, codes, r.f, &fakeArchive{}, r.pub, clk, r.th, log, opts)
	if err != nil {
		t.Fatal(err)
	}
	r.s = s
	return r
}

func TestSweepOutcomes(t *testing.T) {
	r := newRig(t, []string{"A", "B", "C", "D", "E"}, map[string][]reply{
		"A": {{body: "ok:1", etag: `"a1"`}},
		"B": {{err: statusError(http.StatusNotFound)}},
		"C": {{body: "rusak"}},
		"D": {{err: errors.New("timeout")}, {body: "ok:1"}},
		"E": {{body: "ok:1", etag: `"e1"`}, {body: "ok:1", etag: `"e2"`}},
	})
	p, err := r.s.Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 5 || p.Done != 5 || p.Published != 3 || p.NotFound != 1 || p.Rejected != 1 || p.Failed != 0 ||
		p.NotFoundSample[0] != "B" || p.RejectedSample[0] != "C" || r.th.n != 6 {
		t.Fatalf("%+v throttle=%d", p, r.th.n)
	}
	// D gagal di putaran pertama lalu berhasil di putaran ulang (urutan terbit: A, E, D).
	if !strings.HasPrefix(r.pub.msgs[2], "D=1|bmkg-prakiraan/2026/") {
		t.Fatalf("%v", r.pub.msgs)
	}
	st := r.s.Snapshot()
	if st.Completed != 1 || st.Current != nil || st.Last == nil || st.Last.Published != 3 || st.Codes != 5 {
		t.Fatalf("%+v", st)
	}

	// Sapuan kedua: A 304 (ETag sama), E isi sama dengan ETag baru, D isi sama tanpa ETag.
	p, _ = r.s.Sweep(t.Context())
	if p.NotModified != 1 || p.Unchanged != 2 || p.Published != 0 || p.NotFound != 1 {
		t.Fatalf("%+v", p)
	}
	if r.f.requests[len(r.f.requests)-5].ETag != `"a1"` {
		t.Fatal("ETag tidak dikirim")
	}
	// B 404: ETag-nya dibuang, C ditolak lagi.
	if st := r.s.Snapshot(); st.Completed != 2 || st.Last.Rejected != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestFailuresPauseAndRetryAfter(t *testing.T) {
	boom := errors.New("502")
	r := newRig(t, []string{"A", "B", "C", "D", "E"}, map[string][]reply{
		"A": {{err: boom}}, "B": {{err: boom}}, "C": {{err: boom}},
		"D": {{err: &ports.RetryAfterError{Status: 429, After: 90 * time.Second}}, {body: "ok:1"}},
		"E": {{body: "ok:1"}},
	}, func(o *Options) { o.Retries = 0 })
	p, err := r.s.Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Setelah 3 gagal beruntun: jeda 1 menit; lalu D gagal lagi (Retry-After 90 dtk)
	// dan jeda naik ke 2 menit, yang lebih lama dari permintaan sumber.
	if len(r.clock.sleeps) != 2 || r.clock.sleeps[0] != time.Minute || r.clock.sleeps[1] != 2*time.Minute {
		t.Fatalf("jeda %v", r.clock.sleeps)
	}
	if p.Failed != 4 || p.Published != 1 || p.Pauses != 2 || len(p.FailedSample) != 4 || p.Done != 5 {
		t.Fatalf("%+v", p)
	}
	st := r.s.Snapshot()
	if st.ConsecutiveFailures != 0 || !st.PausedUntil.IsZero() || !strings.Contains(st.LastError, "D:") {
		t.Fatalf("%+v", st)
	}

	// Retry-After tanpa kegagalan beruntun tetap dihormati.
	r = newRig(t, []string{"A", "B"}, map[string][]reply{
		"A": {{err: &ports.RetryAfterError{Status: 503, After: 5 * time.Minute}}, {body: "ok:1"}},
		"B": {{body: "ok:1"}},
	})
	if p, _ := r.s.Sweep(t.Context()); p.Published != 2 || len(r.clock.sleeps) != 1 || r.clock.sleeps[0] != 5*time.Minute {
		t.Fatalf("%+v jeda %v", p, r.clock.sleeps)
	}
}

func TestPublishFailureAndArchiveFailure(t *testing.T) {
	r := newRig(t, []string{"A"}, map[string][]reply{"A": {{body: "ok:1", etag: `"a"`}}})
	r.pub.failN = 1
	p, _ := r.s.Sweep(t.Context())
	if p.Published != 1 || len(r.f.requests) != 2 || r.f.requests[1].ETag != "" {
		t.Fatalf("%+v %+v", p, r.f.requests)
	}
	r = newRig(t, []string{"A"}, map[string][]reply{"A": {{body: "ok:1"}}})
	r.s.archive = &fakeArchive{fail: true}
	if p, _ := r.s.Sweep(t.Context()); p.Published != 1 || r.pub.msgs[0] != "A=1|" || !strings.Contains(r.logs.String(), "arsip gagal") {
		t.Fatalf("%+v %v", p, r.pub.msgs)
	}
	// Broker sudah punya ID yang sama (misal setelah restart): dihitung duplikat.
	r = newRig(t, []string{"A"}, map[string][]reply{"A": {{body: "ok:1"}}})
	_, _ = r.s.Sweep(t.Context())
	r.s.seen = map[string]string{}
	if p, _ := r.s.Sweep(t.Context()); p.Duplicates != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestRunSchedulesAndStops(t *testing.T) {
	r := newRig(t, []string{"A", "B"}, map[string][]reply{"A": {{body: "ok:1"}}, "B": {{body: "ok:1"}}},
		func(o *Options) { o.Interval = time.Hour })
	ctx, cancel := context.WithCancel(t.Context())
	r.clock.cancel, r.clock.after = cancel, 2
	r.s.Run(ctx)
	// Dua sapuan, masing-masing ±2,4 detik, lalu tidur sampai satu jam sejak awal sapuan.
	if len(r.clock.sleeps) != 2 || r.clock.sleeps[0] != time.Hour-2400*time.Millisecond {
		t.Fatalf("jeda %v", r.clock.sleeps)
	}
	if st := r.s.Snapshot(); st.Completed != 2 || !st.NextAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("%+v", st)
	}
}

func TestCancelMidSweep(t *testing.T) {
	r := newRig(t, []string{"A", "B", "C"}, map[string][]reply{"A": {{body: "ok:1"}}, "B": {{body: "ok:1"}}, "C": {{body: "ok:1"}}})
	ctx, cancel := context.WithCancel(t.Context())
	r.th.hook = func() {
		if r.th.n == 2 {
			if st := r.s.Snapshot(); st.Current == nil || st.Current.Done != 1 {
				t.Errorf("snapshot di tengah sapuan %+v", st.Current)
			}
			cancel()
		}
	}
	if _, err := r.s.Sweep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if st := r.s.Snapshot(); st.Completed != 0 || st.Current != nil || st.Last != nil {
		t.Fatalf("%+v", st)
	}
	// Run berhenti bila sapuan dibatalkan.
	r2 := newRig(t, []string{"A"}, map[string][]reply{"A": {{body: "ok:1"}}})
	ctx2, cancel2 := context.WithCancel(t.Context())
	cancel2()
	r2.s.Run(ctx2)
	// Jeda karena kegagalan beruntun juga berhenti saat ctx batal.
	r3 := newRig(t, []string{"A", "B", "C", "D"}, map[string][]reply{"A": {{err: errors.New("x")}}, "B": {{err: errors.New("x")}}, "C": {{err: errors.New("x")}}, "D": {{body: "ok:1"}}})
	ctx3, cancel3 := context.WithCancel(t.Context())
	r3.clock.cancel, r3.clock.after = cancel3, 1
	if _, err := r3.s.Sweep(ctx3); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewValidatesAndLimits(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	clk := &fakeClock{now: t0}
	if _, err := New(fakeSource{}, nil, nil, nil, nil, clk, nil, log, DefaultOptions()); err == nil {
		t.Fatal("tanpa kode harus ditolak")
	}
	bad := DefaultOptions()
	bad.PauseMax = time.Second
	if _, err := New(fakeSource{}, []string{"A"}, nil, nil, nil, clk, nil, log, bad); err == nil {
		t.Fatal("opsi salah harus ditolak")
	}
	r := newRig(t, []string{"A", "B", "C"}, map[string][]reply{"A": {{body: "ok:1"}}, "B": {{body: "ok:1"}}},
		func(o *Options) { o.Limit = 2 })
	if p, _ := r.s.Sweep(t.Context()); p.Total != 2 || p.Published != 2 || r.s.Name() != "bmkg-prakiraan" {
		t.Fatalf("%+v", p)
	}
}
