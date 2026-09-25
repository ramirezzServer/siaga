package jspub

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func setup(t *testing.T) (jetstream.JetStream, context.Context) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}
	return js, ctx
}

func TestPublishDeduplicatesAndSetsHeaders(t *testing.T) {
	js, ctx := setup(t)
	p := New(js, streams.Raw.Name)
	fetched := time.Date(2026, 9, 25, 3, 4, 5, 600_000_000, time.FixedZone("WIB", 7*3600))
	msg := ports.Message{Subject: "raw.quake.bmkg", ID: "abc123", Data: []byte{1, 2, 3}, FetchedAt: fetched}

	res, err := p.Publish(ctx, msg)
	if err != nil || res.Duplicate {
		t.Fatalf("pertama: %+v, %v", res, err)
	}
	res, err = p.Publish(ctx, msg)
	if err != nil || !res.Duplicate {
		t.Fatalf("ID sama harus terdeteksi ganda: %+v, %v", res, err)
	}

	st, err := js.Stream(ctx, streams.Raw.Name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetLastMsgForSubject(ctx, "raw.quake.bmkg")
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Get(jetstream.MsgIDHeader) != "abc123" || got.Header.Get("Content-Type") != ContentType || len(got.Data) != 3 {
		t.Fatalf("pesan tersimpan salah: %+v", got)
	}
	if v := got.Header.Get(natsx.HeaderFetchedAt); v != "2026-09-24T20:04:05.6Z" {
		t.Fatalf("header %s = %q", natsx.HeaderFetchedAt, v)
	}
	info, _ := st.Info(ctx)
	if info.State.Msgs != 1 {
		t.Fatalf("stream harus berisi 1 pesan, berisi %d", info.State.Msgs)
	}
}

func TestPublishOutsideStreamFails(t *testing.T) {
	js, ctx := setup(t)
	p := New(js, streams.Raw.Name)
	if _, err := p.Publish(ctx, ports.Message{Subject: "bukan.raw", ID: "x"}); err == nil {
		t.Fatal("subjek di luar stream harus gagal (tidak ada yang menyimpan)")
	}
}

// Konteks trace penerbit ikut di header traceparent, dan span producer
// menjadi anak span di ctx.
func TestPublishPropagatesTrace(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetTextMapPropagator(prevProp) })

	js, ctx := setup(t)
	p := New(js, streams.Raw.Name)
	pctx, poll := otel.Tracer("uji").Start(ctx, "poll usgs-2.5-day")
	if _, err := p.Publish(pctx, ports.Message{Subject: "raw.quake.usgs", ID: "u1", Data: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	poll.End()
	if _, err := p.Publish(ctx, ports.Message{Subject: "bukan.raw", ID: "x"}); err == nil {
		t.Fatal("subjek di luar stream harus gagal")
	}
	st, _ := js.Stream(ctx, streams.Raw.Name)
	got, err := st.GetLastMsgForSubject(ctx, "raw.quake.usgs")
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Get(natsx.HeaderFetchedAt) != "" {
		t.Error("FetchedAt kosong tidak boleh menulis header")
	}
	var send, failed sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		switch s.Name() {
		case "send raw.quake.usgs":
			send = s
		case "send bukan.raw":
			failed = s
		}
	}
	if send == nil || failed == nil {
		t.Fatalf("span tidak lengkap: %v", rec.Ended())
	}
	tp := got.Header.Get("traceparent")
	want := "00-" + send.SpanContext().TraceID().String() + "-" + send.SpanContext().SpanID().String() + "-01"
	if tp != want || send.Parent().SpanID() != poll.SpanContext().SpanID() {
		t.Fatalf("traceparent %q, ingin %q (parent %v)", tp, want, send.Parent().SpanID())
	}
	if failed.Status().Code != codes.Error {
		t.Errorf("span publish gagal berstatus %v", failed.Status())
	}
}
