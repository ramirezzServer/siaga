package natsjs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/libs/go/platform/otelx"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func setup(t *testing.T) (jetstream.JetStream, context.Context) {
	t.Helper()
	nc, err := natsx.Connect(natstest.RunServer(t), "test", quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	for _, s := range []struct {
		spec  streams.Spec
		owner string
	}{{streams.Raw, "ingest"}, {streams.Hazard, "geo-processor"}, {streams.DLQ, "geo-processor"}} {
		if _, err := natsx.EnsureStream(ctx, js, s.spec, s.owner); err != nil {
			t.Fatal(err)
		}
	}
	return js, ctx
}

func TestPublisherDeduplicatesAndChecksStream(t *testing.T) {
	js, ctx := setup(t)
	pub := NewPublisher(js, streams.Hazard.Name)
	for range 2 {
		if err := pub.Publish(ctx, "hazard.quake.created", "id:r1", []byte("x"), map[string]string{"Content-Type": "application/protobuf"}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := js.Stream(ctx, streams.Hazard.Name)
	if err != nil {
		t.Fatal(err)
	}
	if n := info.CachedInfo().State.Msgs; n != 1 {
		t.Fatalf("pesan dengan ID sama harus satu, ada %d", n)
	}
	wrong := NewPublisher(js, streams.DLQ.Name)
	if err := wrong.Publish(ctx, "hazard.quake.created", "id:r2", nil, nil); err == nil {
		t.Fatal("subjek di stream lain harus ditolak")
	}
}

// Consumer menjalankan ketiga keputusan Handler terhadap server sungguhan:
// pesan berhasil di-ack, pesan rusak masuk DLQ dengan header asal, galat
// sementara dikirim ulang lalu masuk DLQ setelah batas percobaan.
func TestConsumerActions(t *testing.T) {
	js, ctx := setup(t)
	var calls atomic.Int32
	process := func(_ context.Context, r quake.Report) (consume.Outcome, error) {
		calls.Add(1)
		if r.EventID == "sementara" {
			return consume.Outcome{}, errors.New("database\nputus")
		}
		return consume.Outcome{Changed: true}, nil
	}
	decode := func(b []byte) (quake.Report, error) {
		if string(b) == "rusak" {
			return quake.Report{}, quake.ErrInvalid
		}
		return quake.Report{EventID: string(b)}, nil
	}
	opts := consume.Options{MaxDeliveries: 3, Backoff: []time.Duration{10 * time.Millisecond}}
	h, err := consume.New(decode, process, []error{quake.ErrInvalid}, opts, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var acked atomic.Int32
	spec := QuakeConsumer
	spec.Durable = "test-quake"
	c, err := NewConsumer(ctx, js, spec, "geo-processor", h, quiet, func() { acked.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx) }()

	for _, body := range []string{"baik", "rusak", "sementara"} {
		m := nats.NewMsg("raw.quake.bmkg")
		m.Data = []byte(body)
		m.Header.Set(jetstream.MsgIDHeader, body)
		m.Header.Set("Content-Type", "application/protobuf")
		if _, err := js.PublishMsg(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	dlq, err := js.Stream(ctx, streams.DLQ.Name)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err := dlq.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.State.Msgs == 2 && acked.Load() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DLQ=%d ack=%d setelah 10 detik", info.State.Msgs, acked.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	// baik 1× + sementara 3× (rusak tidak sampai ke process).
	if n := calls.Load(); n != 4 {
		t.Errorf("process dipanggil %d kali, ingin 4", n)
	}

	var reasons []string
	for seq := uint64(1); seq <= 2; seq++ {
		msg, err := dlq.GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		if msg.Subject != "dlq.geo-processor" || msg.Header.Get(HeaderDLQSubject) != "raw.quake.bmkg" ||
			msg.Header.Get(HeaderDLQStream) != "RAW" || msg.Header.Get(HeaderDLQConsumer) != "test-quake" ||
			msg.Header.Get("Content-Type") != "application/protobuf" {
			t.Errorf("header DLQ %v", msg.Header)
		}
		reasons = append(reasons, string(msg.Data)+"@"+msg.Header.Get(HeaderDLQDelivered)+"@"+msg.Header.Get(HeaderDLQError))
	}
	joined := strings.Join(reasons, ";")
	if !strings.Contains(joined, "rusak@1@") || !strings.Contains(joined, "sementara@3@database | putus") {
		t.Errorf("isi DLQ %q", joined)
	}
	if s := h.Snapshot(); s.DeadLettered != 2 || s.Retried != 2 || s.Applied != 1 {
		t.Errorf("stats %+v", s)
	}
}

func TestNewConsumerNeedsRawStream(t *testing.T) {
	nc, err := natsx.Connect(natstest.RunServer(t), "test", quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	h, _ := consume.New[quake.Report](nil, nil, nil, consume.DefaultOptions(), time.Now)
	_, err = NewConsumer(context.Background(), js, WeatherConsumer, "geo-processor", h, quiet, nil)
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("err = %v, ingin ErrStreamNotFound", err)
	}
}

func TestHeaderValue(t *testing.T) {
	if got := headerValue("a\r\nb", 100); got != "a  | b" {
		t.Errorf("%q", got)
	}
	if got := headerValue("ééé", 3); got != "é" {
		t.Errorf("potongan harus di batas rune: %q", got)
	}
	cfg := ConsumerConfig(QuakeConsumer)
	if cfg.Durable != "geo-processor-quake" || cfg.FilterSubject != "raw.quake.>" || cfg.AckPolicy != jetstream.AckExplicitPolicy || cfg.MaxDeliver != -1 {
		t.Errorf("config %+v", cfg)
	}
	if w := ConsumerConfig(WeatherConsumer); w.Durable != "geo-processor-weather" || w.FilterSubject != "raw.weather.>" {
		t.Errorf("config cuaca %+v", w)
	}
	// Consumer deret waktu: satu per subjek, nama durable unik.
	seen := map[string]bool{}
	for spec, subject := range map[ConsumerSpec]string{
		ForecastBMKGConsumer:        "raw.forecast.bmkg",
		ForecastOpenMeteoConsumer:   "raw.forecast.openmeteo",
		AirQualityOpenMeteoConsumer: "raw.aq.openmeteo",
		FloodOpenMeteoConsumer:      "raw.flood.openmeteo",
		AirQualityOpenAQConsumer:    "raw.aq.openaq",
		FireFIRMSConsumer:           "raw.fire.firms",
	} {
		if spec.Filter != subject || seen[spec.Durable] || spec.Description == "" {
			t.Errorf("%+v", spec)
		}
		seen[spec.Durable] = true
	}
}

// Trace berlanjut dari penerbit raw.* ke pemrosesan, lalu ke penerbitan
// hazard.* lewat traceparent yang tersimpan (seperti dari outbox); histogram
// antrean, pemrosesan, dan latensi pipa tercatat.
func TestConsumerContinuesTraceAndRecordsLatency(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	prevTP, prevMP, prevProp := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
		otel.SetTextMapPropagator(prevProp)
	})

	js, ctx := setup(t)
	processed := make(chan string, 1)
	h, err := consume.New(func(b []byte) (quake.Report, error) { return quake.Report{EventID: string(b)}, nil },
		func(ctx context.Context, _ quake.Report) (consume.Outcome, error) {
			processed <- otelx.TraceParent(ctx)
			return consume.Outcome{Changed: true}, nil
		}, nil, consume.DefaultOptions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	spec := QuakeConsumer
	spec.Durable = "test-trace"
	c, err := NewConsumer(ctx, js, spec, "geo-processor", h, quiet, nil)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx) }()

	const upstream = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	m := nats.NewMsg("raw.quake.bmkg")
	m.Data = []byte("g1")
	m.Header.Set(jetstream.MsgIDHeader, "g1")
	m.Header.Set("traceparent", upstream)
	m.Header.Set(natsx.HeaderFetchedAt, time.Now().Add(-2*time.Second).UTC().Format(time.RFC3339Nano))
	if _, err := js.PublishMsg(ctx, m); err != nil {
		t.Fatal(err)
	}
	var inHandler string
	select {
	case inHandler = <-processed:
	case <-time.After(10 * time.Second):
		t.Fatal("pesan tidak diproses")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inHandler, "00-4bf92f3577b34da6a3ce929d0e0e4736-") || inHandler == upstream {
		t.Fatalf("handler harus berada di span anak trace penerbit, dapat %q", inHandler)
	}

	// Relay meneruskan traceparent dari outbox lewat header.
	pub := NewPublisher(js, streams.Hazard.Name)
	if err := pub.Publish(context.Background(), "hazard.quake.created", "g1:r1", []byte("x"), map[string]string{"traceparent": inHandler}); err != nil {
		t.Fatal(err)
	}
	hz, err := js.Stream(ctx, streams.Hazard.Name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hz.GetLastMsgForSubject(ctx, "hazard.quake.created")
	if err != nil {
		t.Fatal(err)
	}
	tp := out.Header.Get("traceparent")
	if !strings.HasPrefix(tp, "00-4bf92f3577b34da6a3ce929d0e0e4736-") || tp == inHandler {
		t.Fatalf("hazard.* harus membawa span penerbitan baru di trace yang sama, dapat %q", tp)
	}

	var process, send sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		switch s.Name() {
		case "process raw.quake.bmkg":
			process = s
		case "send hazard.quake.created":
			send = s
		}
	}
	if process == nil || send == nil {
		t.Fatalf("span %v", rec.Ended())
	}
	if process.Parent().SpanID().String() != "00f067aa0ba902b7" || !process.Parent().IsRemote() {
		t.Errorf("parent span pemrosesan %v", process.Parent())
	}
	if send.Parent().SpanID().String() != inHandler[36:52] {
		t.Errorf("parent span penerbitan %v, ingin %s", send.Parent().SpanID(), inHandler[36:52])
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	counts := map[string]uint64{}
	var pipelineSum float64
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			if hist, ok := mt.Data.(metricdata.Histogram[float64]); ok {
				for _, dp := range hist.DataPoints {
					counts[mt.Name] += dp.Count
					if mt.Name == "siaga.pipeline.latency" {
						pipelineSum += dp.Sum
					}
				}
			}
		}
	}
	if counts["messaging.process.duration"] != 1 || counts["siaga.geo.queue.duration"] != 1 || counts["siaga.pipeline.latency"] != 1 {
		t.Fatalf("histogram %v", counts)
	}
	if pipelineSum < 2 || pipelineSum > 30 {
		t.Errorf("latensi pipa %.1f detik, ingin sekitar 2", pipelineSum)
	}
}

func TestFetchedAtHeader(t *testing.T) {
	h := nats.Header{}
	if _, ok := fetchedAt(h); ok {
		t.Error("tanpa header")
	}
	h.Set(natsx.HeaderFetchedAt, "kemarin")
	if _, ok := fetchedAt(h); ok {
		t.Error("header rusak harus diabaikan")
	}
	h.Set(natsx.HeaderFetchedAt, "2026-09-25T01:02:03.5Z")
	if at, ok := fetchedAt(h); !ok || !at.Equal(time.Date(2026, 9, 25, 1, 2, 3, 500_000_000, time.UTC)) {
		t.Errorf("%v %v", at, ok)
	}
}
