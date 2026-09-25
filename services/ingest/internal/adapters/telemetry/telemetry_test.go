package telemetry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/sweep"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

type rig struct {
	t      *Telemetry
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
}

func newRig(t *testing.T) rig {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	tel, err := New(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)), sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatal(err)
	}
	return rig{t: tel, spans: spans, reader: reader}
}

// point adalah satu titik data metrik yang sudah diratakan.
type point struct {
	attrs string
	value float64
	count uint64 // untuk histogram
}

func (r rig) collect(t *testing.T) map[string][]point {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string][]point{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					out[m.Name] = append(out[m.Name], point{attrs: attrString(dp.Attributes), value: float64(dp.Value)})
				}
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					out[m.Name] = append(out[m.Name], point{attrs: attrString(dp.Attributes), value: float64(dp.Value)})
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					out[m.Name] = append(out[m.Name], point{attrs: attrString(dp.Attributes), value: dp.Value})
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					out[m.Name] = append(out[m.Name], point{attrs: attrString(dp.Attributes), value: dp.Sum, count: dp.Count})
				}
			default:
				t.Fatalf("%s: jenis data %T tidak diharapkan", m.Name, m.Data)
			}
		}
	}
	return out
}

func attrString(s attribute.Set) string {
	out := ""
	for _, kv := range s.ToSlice() {
		out += fmt.Sprintf("%s=%s ", kv.Key, kv.Value.String())
	}
	return out
}

func find(points []point, attrs string) (point, bool) {
	for _, p := range points {
		if p.attrs == attrs {
			return p, true
		}
	}
	return point{}, false
}

func spanNamed(r rig, name string) sdktrace.ReadOnlySpan {
	for _, s := range r.spans.Ended() {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func TestPollObserver(t *testing.T) {
	r := newRig(t)
	ctx, done := r.t.Poll(context.Background(), "bmkg-autogempa")
	if !trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("context polling tidak membawa span")
	}
	done(poll.Result{Events: 3, Published: 1, AlreadySeen: 2, ArchiveKey: "k", ArchiveErr: errors.New("arsip penuh")}, nil)
	_, done = r.t.Poll(context.Background(), "bmkg-autogempa")
	done(poll.Result{}, errors.New("HTTP 500"))
	_, done = r.t.Poll(context.Background(), "usgs-2.5-day")
	done(poll.Result{NotModified: true}, nil)

	spans := r.spans.Ended()
	if len(spans) != 3 || spans[0].Name() != "poll bmkg-autogempa" {
		t.Fatalf("span %v", spans)
	}
	if spans[1].Status().Code != codes.Error || len(spans[0].Events()) != 1 {
		t.Errorf("status %v, event %v", spans[1].Status(), spans[0].Events())
	}
	m := r.collect(t)
	for attrs, want := range map[string]float64{
		"connector=bmkg-autogempa outcome=changed ":    1,
		"connector=bmkg-autogempa outcome=error ":      1,
		"connector=usgs-2.5-day outcome=not_modified ": 1,
	} {
		if p, ok := find(m["siaga.ingest.polls"], attrs); !ok || p.value != want {
			t.Errorf("siaga.ingest.polls{%s} = %+v", attrs, p)
		}
		if p, ok := find(m["siaga.ingest.poll.duration"], attrs); !ok || p.count != 1 {
			t.Errorf("siaga.ingest.poll.duration{%s} = %+v", attrs, p)
		}
	}
	if PollOutcome(poll.Result{Unchanged: true}, nil) != OutcomeUnchanged {
		t.Error("PollOutcome unchanged")
	}
}

func TestSweepItemObserver(t *testing.T) {
	r := newRig(t)
	obs := r.t.SweepItem("bmkg-prakiraan")
	for _, c := range []struct {
		code, outcome string
		err           error
	}{
		{"32.73.01.1001", sweep.OutcomePublished, nil},
		{"32.73.01.1002", sweep.OutcomeNotFound, errors.New("HTTP 404")},
		{"32.73.01.1003", sweep.OutcomeFailed, errors.New("timeout")},
	} {
		_, done := obs(context.Background(), c.code)
		done(c.outcome, c.err)
	}
	spans := r.spans.Ended()
	if len(spans) != 3 || spans[1].Status().Code == codes.Error || spans[2].Status().Code != codes.Error {
		t.Fatalf("404 bukan galat layanan, gagal adalah galat: %v %v", spans[1].Status(), spans[2].Status())
	}
	m := r.collect(t)
	if p, ok := find(m["siaga.ingest.sweep.items"], "connector=bmkg-prakiraan outcome=not_found "); !ok || p.value != 1 {
		t.Errorf("sweep.items %+v", m["siaga.ingest.sweep.items"])
	}
	if len(m["siaga.ingest.sweep.item.duration"]) != 3 {
		t.Errorf("sweep.item.duration %+v", m["siaga.ingest.sweep.item.duration"])
	}
}

type fakeFetcher struct {
	resp ports.Response
	err  error
}

func (f fakeFetcher) Fetch(context.Context, ports.Request) (ports.Response, error) {
	return f.resp, f.err
}

type statusError int

func (e statusError) Error() string   { return fmt.Sprintf("HTTP %d", int(e)) }
func (e statusError) HTTPStatus() int { return int(e) }

func TestFetcher(t *testing.T) {
	r := newRig(t)
	req := ports.Request{URL: "https://firms.modaps.eosdis.nasa.gov/api/area/csv/RAHASIA/VIIRS", Secrets: []string{"RAHASIA"}}
	cases := []struct {
		f     fakeFetcher
		attrs string
	}{
		{fakeFetcher{resp: ports.Response{Body: []byte("isi")}}, "http.request.method=GET http.response.status_code=200 server.address=firms.modaps.eosdis.nasa.gov "},
		{fakeFetcher{resp: ports.Response{NotModified: true}}, "http.request.method=GET http.response.status_code=304 server.address=firms.modaps.eosdis.nasa.gov "},
		{fakeFetcher{err: &ports.RetryAfterError{Status: 429}}, "error.type=429 http.request.method=GET http.response.status_code=429 server.address=firms.modaps.eosdis.nasa.gov "},
		{fakeFetcher{err: fmt.Errorf("bungkus: %w", statusError(404))}, "error.type=404 http.request.method=GET http.response.status_code=404 server.address=firms.modaps.eosdis.nasa.gov "},
		{fakeFetcher{err: context.DeadlineExceeded}, "error.type=timeout http.request.method=GET server.address=firms.modaps.eosdis.nasa.gov "},
		{fakeFetcher{err: context.Canceled}, "error.type=canceled http.request.method=GET server.address=firms.modaps.eosdis.nasa.gov "},
		{fakeFetcher{err: errors.New("dial tcp")}, "error.type=_OTHER http.request.method=GET server.address=firms.modaps.eosdis.nasa.gov "},
	}
	for _, c := range cases {
		if _, err := r.t.Fetcher(c.f).Fetch(context.Background(), req); !errors.Is(err, c.f.err) {
			t.Fatalf("galat tidak diteruskan: %v", err)
		}
	}
	m := r.collect(t)
	for _, c := range cases {
		if p, ok := find(m["http.client.request.duration"], c.attrs); !ok || p.count != 1 {
			t.Errorf("http.client.request.duration{%s} tidak ada: %+v", c.attrs, m["http.client.request.duration"])
		}
	}
	for _, s := range r.spans.Ended() {
		for _, kv := range s.Attributes() {
			if kv.Key == "url.full" && kv.Value.AsString() != "https://firms.modaps.eosdis.nasa.gov/api/area/csv/***/VIIRS" {
				t.Fatalf("URL di span tidak disamarkan: %s", kv.Value.AsString())
			}
		}
		if s.SpanKind() != trace.SpanKindClient {
			t.Errorf("span %s bukan client", s.Name())
		}
	}
}

type fakeArchive struct{ err error }

func (a fakeArchive) Put(context.Context, string, []byte) error { return a.err }

func TestArchive(t *testing.T) {
	r := newRig(t)
	if r.t.Archive(nil) != nil {
		t.Fatal("arsip nil harus tetap nil")
	}
	ok, bad := r.t.Archive(fakeArchive{}), r.t.Archive(fakeArchive{err: errors.New("Garage mati")})
	if err := ok.Put(context.Background(), "usgs/2026/09/25/000000Z-abc.json.gz", make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if err := bad.Put(context.Background(), "k", []byte{1}); err == nil {
		t.Fatal("galat arsip harus diteruskan")
	}
	m := r.collect(t)
	if p, _ := find(m["siaga.ingest.archive.written"], ""); p.value != 100 {
		t.Errorf("archive.written %+v", m["siaga.ingest.archive.written"])
	}
	if p, ok := find(m["siaga.ingest.archive.duration"], "result=error "); !ok || p.count != 1 {
		t.Errorf("archive.duration %+v", m["siaga.ingest.archive.duration"])
	}
	if s := spanNamed(r, "archive put"); s == nil {
		t.Error("span arsip tidak ada")
	}
}

func TestObserveGauges(t *testing.T) {
	r := newRig(t)
	now := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	l, err := runner.NewLimiter("bmkg", 55, 5)
	if err != nil {
		t.Fatal(err)
	}
	status := []runner.Status{{
		Connector: "bmkg-autogempa", Interval: 30 * time.Second, LastSuccess: now.Add(-time.Minute), LastChange: now.Add(-time.Hour),
		ConsecutiveFailures: 2, Published: 7, Rejected: 1,
	}, {Connector: "usgs-2.5-day", Interval: time.Minute}}
	sweeps := []sweep.Status{{
		Connector: "bmkg-prakiraan", Interval: 6 * time.Hour, Completed: 3,
		Last: &sweep.Progress{FinishedAt: now.Add(-2 * time.Hour)}, Current: &sweep.Progress{Total: 200, Done: 50},
	}}
	if _, err := r.t.Observe(Sources{
		Connectors: func() []runner.Status { return status },
		Sweeps:     func() []sweep.Status { return sweeps },
		Limiters:   []*runner.Limiter{l},
		Now:        func() time.Time { return now },
	}); err != nil {
		t.Fatal(err)
	}
	m := r.collect(t)
	check := func(name, attrs string, want float64) {
		t.Helper()
		if p, ok := find(m[name], attrs); !ok || p.value != want {
			t.Errorf("%s{%s} = %+v (ada %v), ingin %v", name, attrs, p, ok, want)
		}
	}
	check("siaga.ingest.source.last_success", "connector=bmkg-autogempa ", float64(now.Add(-time.Minute).Unix()))
	check("siaga.ingest.source.last_change", "connector=bmkg-autogempa ", float64(now.Add(-time.Hour).Unix()))
	check("siaga.ingest.source.interval", "connector=bmkg-autogempa ", 30)
	check("siaga.ingest.source.consecutive_failures", "connector=bmkg-autogempa ", 2)
	check("siaga.ingest.records", "connector=bmkg-autogempa result=published ", 7)
	check("siaga.ingest.records", "connector=bmkg-autogempa result=rejected ", 1)
	check("siaga.ingest.source.last_success", "connector=bmkg-prakiraan ", float64(now.Add(-2*time.Hour).Unix()))
	check("siaga.ingest.source.interval", "connector=bmkg-prakiraan ", 6*3600)
	check("siaga.ingest.sweep.progress", "connector=bmkg-prakiraan ", 0.25)
	check("siaga.ingest.sweep.completed", "connector=bmkg-prakiraan ", 3)
	check("siaga.ingest.budget.available", "budget=bmkg ", 5)
	check("siaga.ingest.budget.burst", "budget=bmkg ", 5)
	check("siaga.ingest.budget.per_minute", "budget=bmkg ", 55)
	check("siaga.ingest.budget.reservations", "budget=bmkg ", 0)
	if _, ok := find(m["siaga.ingest.source.last_success"], "connector=usgs-2.5-day "); ok {
		t.Error("konektor yang belum pernah sukses tidak boleh punya last_success")
	}
	if _, err := r.t.Observe(Sources{}); err != nil {
		t.Fatalf("sumber kosong: %v", err)
	}
	_ = r.collect(t)
}
