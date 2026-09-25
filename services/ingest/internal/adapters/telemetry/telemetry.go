// Package telemetry adalah instrumentasi OpenTelemetry ingest (ADR 0015):
// span dan metrik per polling, per kode sapuan, per request HTTP sumber, dan
// per penulisan arsip, serta gauge kesehatan sumber dan sisa anggaran request.
//
// Use case tidak mengimpor OpenTelemetry; mereka menerima pengamat
// (runner.PollObserver, sweep.ItemObserver) dan port yang dibungkus di sini.
// Tanpa SDK terpasang (otelx tanpa endpoint), semua instrumen adalah noop.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/sweep"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Scope adalah nama instrumentasi ingest.
const Scope = "github.com/ramirezzServer/siaga/services/ingest"

// Batas bucket histogram dalam detik.
var (
	requestBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60}
	storageBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
)

// Hasil satu polling untuk atribut outcome.
const (
	OutcomeChanged     = "changed"
	OutcomeUnchanged   = "unchanged"
	OutcomeNotModified = "not_modified"
	OutcomeError       = "error"
)

// Telemetry memegang instrumen ingest.
type Telemetry struct {
	tracer trace.Tracer
	meter  metric.Meter

	polls        metric.Int64Counter
	pollDuration metric.Float64Histogram
	items        metric.Int64Counter
	itemDuration metric.Float64Histogram
	httpDuration metric.Float64Histogram
	archiveDur   metric.Float64Histogram
	archiveBytes metric.Int64Counter
}

// New membuat instrumen dari provider yang diberikan (biasanya global dari
// otel.GetTracerProvider dan otel.GetMeterProvider).
func New(tp trace.TracerProvider, mp metric.MeterProvider) (*Telemetry, error) {
	m := mp.Meter(Scope)
	t := &Telemetry{tracer: tp.Tracer(Scope), meter: m}
	var errs [7]error
	t.polls, errs[0] = m.Int64Counter("siaga.ingest.polls",
		metric.WithUnit("{poll}"), metric.WithDescription("Polling per konektor menurut hasilnya."))
	t.pollDuration, errs[1] = m.Float64Histogram("siaga.ingest.poll.duration",
		metric.WithUnit("s"), metric.WithDescription("Lama satu polling: ambil, arsip, parse, terbit."),
		metric.WithExplicitBucketBoundaries(requestBuckets...))
	t.items, errs[2] = m.Int64Counter("siaga.ingest.sweep.items",
		metric.WithUnit("{item}"), metric.WithDescription("Kode yang diambil sapuan menurut hasilnya."))
	t.itemDuration, errs[3] = m.Float64Histogram("siaga.ingest.sweep.item.duration",
		metric.WithUnit("s"), metric.WithDescription("Lama mengambil dan menerbitkan satu kode sapuan."),
		metric.WithExplicitBucketBoundaries(requestBuckets...))
	t.httpDuration, errs[4] = m.Float64Histogram("http.client.request.duration",
		metric.WithUnit("s"), metric.WithDescription("Lama request HTTP ke sumber data."),
		metric.WithExplicitBucketBoundaries(requestBuckets...))
	t.archiveDur, errs[5] = m.Float64Histogram("siaga.ingest.archive.duration",
		metric.WithUnit("s"), metric.WithDescription("Lama menulis satu payload ke arsip."),
		metric.WithExplicitBucketBoundaries(storageBuckets...))
	t.archiveBytes, errs[6] = m.Int64Counter("siaga.ingest.archive.written",
		metric.WithUnit("By"), metric.WithDescription("Byte terkompresi yang berhasil ditulis ke arsip."))
	if err := errors.Join(errs[:]...); err != nil {
		return nil, fmt.Errorf("telemetry: instrumen: %w", err)
	}
	return t, nil
}

func connectorAttr(name string) attribute.KeyValue { return attribute.String("connector", name) }

// Poll memenuhi runner.PollObserver: satu span "poll <konektor>" per
// polling, beserta hitungan dan lama polling menurut hasilnya.
func (t *Telemetry) Poll(ctx context.Context, connector string) (context.Context, func(poll.Result, error)) {
	start := time.Now()
	ctx, span := t.tracer.Start(ctx, "poll "+connector, trace.WithAttributes(connectorAttr(connector)))
	return ctx, func(res poll.Result, err error) {
		defer span.End()
		outcome := PollOutcome(res, err)
		span.SetAttributes(
			attribute.String("siaga.outcome", outcome),
			attribute.Int("siaga.events", res.Events), attribute.Int("siaga.published", res.Published),
			attribute.Int("siaga.duplicates", res.Duplicates), attribute.Int("siaga.already_seen", res.AlreadySeen),
			attribute.Int("siaga.rejected", res.Rejected), attribute.Int("siaga.failed", res.Failed),
		)
		if res.ArchiveKey != "" {
			span.SetAttributes(attribute.String("siaga.archive_key", res.ArchiveKey))
		}
		if res.ArchiveErr != nil {
			span.AddEvent("arsip gagal", trace.WithAttributes(attribute.String("error", res.ArchiveErr.Error())))
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "polling gagal")
		}
		attrs := metric.WithAttributes(connectorAttr(connector), attribute.String("outcome", outcome))
		ctx := context.WithoutCancel(ctx)
		t.polls.Add(ctx, 1, attrs)
		t.pollDuration.Record(ctx, time.Since(start).Seconds(), attrs)
	}
}

// PollOutcome meringkas hasil polling untuk atribut metrik.
func PollOutcome(res poll.Result, err error) string {
	switch {
	case err != nil:
		return OutcomeError
	case res.NotModified:
		return OutcomeNotModified
	case res.Unchanged:
		return OutcomeUnchanged
	default:
		return OutcomeChanged
	}
}

// SweepItem mengembalikan sweep.ItemObserver untuk satu konektor sapuan: satu
// span "sweep <konektor>" per kode, beserta hitungan dan lamanya.
func (t *Telemetry) SweepItem(connector string) sweep.ItemObserver {
	return func(ctx context.Context, code string) (context.Context, func(string, error)) {
		start := time.Now()
		ctx, span := t.tracer.Start(ctx, "sweep "+connector,
			trace.WithAttributes(connectorAttr(connector), attribute.String("siaga.code", code)))
		return ctx, func(outcome string, err error) {
			defer span.End()
			span.SetAttributes(attribute.String("siaga.outcome", outcome))
			// 404 berarti kode tidak dikenal sumber; bukan kegagalan layanan.
			if err != nil && outcome != sweep.OutcomeNotFound {
				span.RecordError(err)
				span.SetStatus(codes.Error, outcome)
			}
			attrs := metric.WithAttributes(connectorAttr(connector), attribute.String("outcome", outcome))
			ctx := context.WithoutCancel(ctx)
			t.items.Add(ctx, 1, attrs)
			t.itemDuration.Record(ctx, time.Since(start).Seconds(), attrs)
		}
	}
}

// Fetcher membungkus fetcher sumber: satu span client per request dan
// histogram http.client.request.duration. URL di span sudah disamarkan
// (Request.Redacted). Konteks trace sengaja tidak dikirim ke sumber pihak
// ketiga.
func (t *Telemetry) Fetcher(f ports.Fetcher) ports.Fetcher { return fetcher{t: t, next: f} }

type fetcher struct {
	t    *Telemetry
	next ports.Fetcher
}

func (f fetcher) Fetch(ctx context.Context, req ports.Request) (ports.Response, error) {
	start := time.Now()
	attrs := []attribute.KeyValue{semconv.HTTPRequestMethodGet}
	if u, err := url.Parse(req.URL); err == nil && u.Hostname() != "" {
		attrs = append(attrs, semconv.ServerAddress(u.Hostname()))
	}
	ctx, span := f.t.tracer.Start(ctx, "GET", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(attrs, semconv.URLFull(req.Redacted()))...))
	defer span.End()

	resp, err := f.next.Fetch(ctx, req)
	status, errType := httpOutcome(resp, err)
	if status != 0 {
		attrs = append(attrs, semconv.HTTPResponseStatusCode(status))
	}
	if errType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errType))
		span.RecordError(err)
		span.SetStatus(codes.Error, errType)
	}
	span.SetAttributes(attrs...)
	if err == nil && !resp.NotModified {
		span.SetAttributes(attribute.Int("http.response.body.size", len(resp.Body)))
	}
	f.t.httpDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(), metric.WithAttributes(attrs...))
	return resp, err
}

// httpOutcome menurunkan kode status HTTP dan jenis galat dari hasil fetch.
// Status 0 berarti tidak ada respons (misal timeout).
func httpOutcome(resp ports.Response, err error) (status int, errType string) {
	if err == nil {
		if resp.NotModified {
			return 304, ""
		}
		return 200, ""
	}
	if ra, ok := errors.AsType[*ports.RetryAfterError](err); ok {
		return ra.Status, strconv.Itoa(ra.Status)
	}
	if sc, ok := errors.AsType[ports.StatusCoder](err); ok {
		return sc.HTTPStatus(), strconv.Itoa(sc.HTTPStatus())
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return 0, "timeout"
	case errors.Is(err, context.Canceled):
		return 0, "canceled"
	default:
		return 0, semconv.ErrorTypeOther.Value.AsString()
	}
}

// Archive membungkus arsip payload: satu span per Put, lama tulis, dan byte
// yang tersimpan. nil tetap nil (arsip dimatikan).
func (t *Telemetry) Archive(a ports.Archive) ports.Archive {
	if a == nil {
		return nil
	}
	return archive{t: t, next: a}
}

type archive struct {
	t    *Telemetry
	next ports.Archive
}

func (a archive) Put(ctx context.Context, key string, data []byte) error {
	start := time.Now()
	ctx, span := a.t.tracer.Start(ctx, "archive put", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("siaga.archive_key", key), attribute.Int("siaga.size", len(data))))
	defer span.End()
	err := a.next.Put(ctx, key, data)
	result := "ok"
	if err != nil {
		result = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "arsip gagal")
	}
	ctx = context.WithoutCancel(ctx)
	a.t.archiveDur.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("result", result)))
	if err == nil {
		a.t.archiveBytes.Add(ctx, int64(len(data)))
	}
	return err
}

// Sources adalah sumber keadaan untuk gauge; semua boleh nil.
type Sources struct {
	Connectors func() []runner.Status
	Sweeps     func() []sweep.Status
	Limiters   []*runner.Limiter
	Now        func() time.Time
}

// Observe mendaftarkan gauge kesehatan tiap konektor dan sapuan (waktu sukses
// dan perubahan terakhir, interval, kegagalan beruntun, jumlah record) dan
// sisa anggaran request per sumber. Nilainya dibaca dari snapshot saat metrik
// dikumpulkan, jadi tidak ada pencatatan tambahan di jalur polling.
func (t *Telemetry) Observe(src Sources) (metric.Registration, error) {
	if src.Now == nil {
		src.Now = time.Now
	}
	m := t.meter
	var errs [11]error
	var (
		lastSuccess, lastChange, interval, progress metric.Float64ObservableGauge
		failures, available, burst, perMinute       metric.Int64ObservableGauge
		records, reserved, sweeps                   metric.Int64ObservableCounter
	)
	lastSuccess, errs[0] = m.Float64ObservableGauge("siaga.ingest.source.last_success",
		metric.WithUnit("s"), metric.WithDescription("Waktu Unix polling atau sapuan sukses terakhir."))
	lastChange, errs[1] = m.Float64ObservableGauge("siaga.ingest.source.last_change",
		metric.WithUnit("s"), metric.WithDescription("Waktu Unix payload baru terakhir dari sumber."))
	interval, errs[2] = m.Float64ObservableGauge("siaga.ingest.source.interval",
		metric.WithUnit("s"), metric.WithDescription("Jarak polling atau sapuan yang dijadwalkan."))
	failures, errs[3] = m.Int64ObservableGauge("siaga.ingest.source.consecutive_failures",
		metric.WithUnit("{failure}"), metric.WithDescription("Kegagalan beruntun terakhir."))
	records, errs[4] = m.Int64ObservableCounter("siaga.ingest.records",
		metric.WithUnit("{record}"), metric.WithDescription("Record per konektor: terbit, ditolak validasi, gagal diambil."))
	available, errs[5] = m.Int64ObservableGauge("siaga.ingest.budget.available",
		metric.WithUnit("{request}"), metric.WithDescription("Izin request yang bisa dipakai sekarang tanpa menunggu."))
	burst, errs[6] = m.Int64ObservableGauge("siaga.ingest.budget.burst",
		metric.WithUnit("{request}"), metric.WithDescription("Lonjakan maksimum anggaran request."))
	perMinute, errs[7] = m.Int64ObservableGauge("siaga.ingest.budget.per_minute",
		metric.WithUnit("{request}"), metric.WithDescription("Laju anggaran request per menit."))
	reserved, errs[8] = m.Int64ObservableCounter("siaga.ingest.budget.reservations",
		metric.WithUnit("{request}"), metric.WithDescription("Izin request yang dipesan sejak start."))
	progress, errs[9] = m.Float64ObservableGauge("siaga.ingest.sweep.progress",
		metric.WithUnit("1"), metric.WithDescription("Bagian kode yang sudah diambil sapuan yang sedang berjalan."))
	sweeps, errs[10] = m.Int64ObservableCounter("siaga.ingest.sweep.completed",
		metric.WithUnit("{sweep}"), metric.WithDescription("Sapuan yang selesai sejak start."))
	if err := errors.Join(errs[:]...); err != nil {
		return nil, fmt.Errorf("telemetry: gauge: %w", err)
	}
	unix := func(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }
	return m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		if src.Connectors != nil {
			for _, s := range src.Connectors() {
				c := metric.WithAttributes(connectorAttr(s.Connector))
				o.ObserveFloat64(interval, s.Interval.Seconds(), c)
				o.ObserveInt64(failures, int64(s.ConsecutiveFailures), c)
				if !s.LastSuccess.IsZero() {
					o.ObserveFloat64(lastSuccess, unix(s.LastSuccess), c)
				}
				if !s.LastChange.IsZero() {
					o.ObserveFloat64(lastChange, unix(s.LastChange), c)
				}
				for result, n := range map[string]int{"published": s.Published, "rejected": s.Rejected, "failed": s.Failed} {
					o.ObserveInt64(records, int64(n), metric.WithAttributes(connectorAttr(s.Connector), attribute.String("result", result)))
				}
			}
		}
		if src.Sweeps != nil {
			for _, s := range src.Sweeps() {
				c := metric.WithAttributes(connectorAttr(s.Connector))
				o.ObserveFloat64(interval, s.Interval.Seconds(), c)
				o.ObserveInt64(failures, int64(s.ConsecutiveFailures), c)
				o.ObserveInt64(sweeps, int64(s.Completed), c)
				if s.Last != nil && !s.Last.FinishedAt.IsZero() {
					o.ObserveFloat64(lastSuccess, unix(s.Last.FinishedAt), c)
				}
				if s.Current != nil && s.Current.Total > 0 {
					o.ObserveFloat64(progress, float64(s.Current.Done)/float64(s.Current.Total), c)
				}
			}
		}
		now := src.Now()
		for _, l := range src.Limiters {
			st := l.Stats(now)
			b := metric.WithAttributes(attribute.String("budget", st.Name))
			o.ObserveInt64(available, int64(st.Available), b)
			o.ObserveInt64(burst, int64(st.Burst), b)
			o.ObserveInt64(perMinute, int64(st.PerMinute), b)
			o.ObserveInt64(reserved, st.Reserved, b)
		}
		return nil
	}, lastSuccess, lastChange, interval, failures, records, available, burst, perMinute, reserved, progress, sweeps)
}
