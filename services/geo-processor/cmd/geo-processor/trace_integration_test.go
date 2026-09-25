//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

// Satu gempa bisa dilacak dalam satu trace (ADR 0015): traceparent di pesan
// raw.quake.* → span pemrosesan geo-processor → query PostgreSQL → baris
// outbox → span penerbitan hazard.quake.created, yang header traceparent-nya
// berada di trace yang sama.
func TestTraceFromRawToHazard(t *testing.T) {
	dbURL := os.Getenv("SIAGA_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("SIAGA_TEST_DATABASE_URL tidak diisi")
	}
	rec := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetTextMapPropagator(prevProp) })

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	natsURL := natstest.RunServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	nc, err := natsx.Connect(natsURL, "ingest-palsu", quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		ctx := context.Background()
		pg := mustPool(ctx, t, dbURL)
		defer pg.Close()
		for _, q := range []string{
			`DELETE FROM hazard.outbox`,
			`DELETE FROM hazard.event_source WHERE occurred_at < '2002-01-01'`,
			`UPDATE hazard.event SET merged_into = NULL, status = 'expired' WHERE occurred_at < '2002-01-01' AND status = 'merged'`,
			`DELETE FROM hazard.event WHERE occurred_at < '2002-01-01'`,
		} {
			if _, err := pg.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	seedRegions(ctx, t, dbURL)

	cfg := settings{databaseURL: dbURL, natsURL: natsURL, logLevel: slog.LevelInfo, rules: quake.DefaultRules(), weather: weather.DefaultPolicy()}
	svcCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serve(svcCtx, cfg, quiet) }()

	at := time.Date(2001, 12, 3, 10, 0, 0, 0, time.UTC)
	report := &rawv1.QuakeReport{
		Meta:   &rawv1.FetchMeta{Connector: "bmkg-autogempa", FetchedAt: timestamppb.New(at.Add(3 * time.Minute))},
		Source: hazardv1.Source_SOURCE_BMKG, Feed: rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST,
		SourceEventId: at.Format("20060102150405"), OccurredAt: timestamppb.New(at),
		Epicenter: &commonv1.Point{Latitude: -6.9, Longitude: 107.1}, Magnitude: 5.1, DepthKm: 10,
		Place: "uji trace", TsunamiPotential: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE,
	}
	b, err := proto.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	const upstreamTrace = "0af7651916cd43dd8448eb211c80319c"
	msg := nats.NewMsg("raw.quake.bmkg")
	msg.Data = b
	msg.Header.Set(jetstream.MsgIDHeader, "trace-1")
	msg.Header.Set("traceparent", "00-"+upstreamTrace+"-b7ad6b7169203331-01")
	msg.Header.Set(natsx.HeaderFetchedAt, at.Add(3*time.Minute).Format(time.RFC3339Nano))
	if _, err := js.PublishMsg(ctx, msg); err != nil {
		t.Fatal(err)
	}

	var created *jetstream.RawStreamMsg
	for deadline := time.Now().Add(30 * time.Second); created == nil; {
		if hz, err := js.Stream(ctx, streams.Hazard.Name); err == nil {
			if m, err := hz.GetLastMsgForSubject(ctx, "hazard.quake.created"); err == nil {
				created = m
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("hazard.quake.created tidak terbit dalam 30 detik")
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}

	tp := created.Header.Get("traceparent")
	if !strings.HasPrefix(tp, "00-"+upstreamTrace+"-") {
		t.Fatalf("hazard.quake.created harus melanjutkan trace %s, header traceparent %q", upstreamTrace, tp)
	}
	names := map[string]int{}
	for _, s := range rec.Ended() {
		if s.SpanContext().TraceID().String() == upstreamTrace {
			names[s.Name()]++
		}
	}
	for _, want := range []string{"process raw.quake.bmkg", "INSERT hazard.outbox", "send hazard.quake.created", "BEGIN"} {
		if names[want] == 0 {
			t.Errorf("span %q tidak ada di trace (ada: %v)", want, names)
		}
	}
}
