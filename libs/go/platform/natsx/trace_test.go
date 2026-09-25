package natsx_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
)

// setupTracing memasang SDK trace dengan perekam span dalam memori sebagai
// provider global untuk satu test.
func setupTracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return rec
}

func TestHeaderCarrier(t *testing.T) {
	h := nats.Header{}
	c := natsx.HeaderCarrier(h)
	c.Set("traceparent", "a")
	c.Set("tracestate", "b")
	if c.Get("traceparent") != "a" || h.Get("tracestate") != "b" {
		t.Fatalf("header %v", h)
	}
	keys := c.Keys()
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"traceparent", "tracestate"}) {
		t.Fatalf("keys %v", keys)
	}
	if ctx := natsx.Extract(context.Background(), nil); trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("header nil tidak boleh menghasilkan span")
	}
}

// Satu trace melintasi NATS: span producer di penerbit, span consumer di
// penerima sebagai anaknya.
func TestTraceAcrossNATS(t *testing.T) {
	rec := setupTracing(t)
	js := jetStream(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}

	root, rootSpan := otel.Tracer("uji").Start(ctx, "poll bmkg-autogempa")
	pctx, pub := natsx.StartPublish(root, "raw.quake.bmkg", "id-1", attribute.String("siaga.uji", "ya"))
	m := nats.NewMsg("raw.quake.bmkg")
	m.Data = []byte("isi")
	m.Header.Set(jetstream.MsgIDHeader, "id-1")
	natsx.Inject(pctx, m.Header)
	if _, err := js.PublishMsg(ctx, m); err != nil {
		t.Fatal(err)
	}
	pub.End()
	rootSpan.End()

	cons, err := js.CreateOrUpdateConsumer(ctx, streams.Raw.Name, jetstream.ConsumerConfig{Durable: "uji", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, proc := natsx.StartProcess(context.Background(), msg.Subject(), "uji", msg.Headers())
	proc.End()

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.Name()] = s
	}
	send, process := spans["send raw.quake.bmkg"], spans["process raw.quake.bmkg"]
	if send == nil || process == nil {
		t.Fatalf("span yang direkam: %v", spans)
	}
	if send.SpanKind() != trace.SpanKindProducer || process.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("jenis span %v / %v", send.SpanKind(), process.SpanKind())
	}
	if process.Parent().SpanID() != send.SpanContext().SpanID() || process.SpanContext().TraceID() != spans["poll bmkg-autogempa"].SpanContext().TraceID() {
		t.Errorf("span consumer bukan anak span producer: parent %v, producer %v", process.Parent().SpanID(), send.SpanContext().SpanID())
	}
	attrs := map[attribute.Key]string{}
	for _, kv := range process.Attributes() {
		attrs[kv.Key] = kv.Value.String()
	}
	if attrs["messaging.system"] != "nats" || attrs["messaging.consumer.group.name"] != "uji" || attrs["messaging.message.id"] != "id-1" {
		t.Errorf("atribut consumer %v", attrs)
	}
}

func jetStream(t *testing.T) jetstream.JetStream {
	t.Helper()
	nc, err := natsx.Connect(natstest.RunServer(t), "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func TestObserveStreamsAndConsumers(t *testing.T) {
	js := jetStream(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := js.Publish(ctx, "raw.quake.bmkg", []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := js.CreateOrUpdateConsumer(ctx, streams.Raw.Name, jetstream.ConsumerConfig{Durable: "uji", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	reader := sdkmetric.NewManualReader()
	m := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("uji")
	if _, err := natsx.ObserveStreams(m, js, streams.Raw.Name, "TIDAK_ADA"); err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.ObserveConsumers(m, js, streams.Raw.Name, "uji", "tidak-ada"); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			g, ok := mt.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("%s bukan gauge int64", mt.Name)
			}
			for _, dp := range g.DataPoints {
				got[mt.Name] += dp.Value
				if v, _ := dp.Attributes.Value("stream"); v.AsString() != streams.Raw.Name {
					t.Errorf("%s: atribut stream %v", mt.Name, dp.Attributes)
				}
			}
		}
	}
	if got["siaga.nats.stream.messages"] != 3 || got["siaga.nats.stream.size"] <= 0 || got["siaga.nats.consumer.pending"] != 3 {
		t.Errorf("metrik NATS %v", got)
	}
}
