package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/eventsql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/quakesql"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func TestSummarize(t *testing.T) {
	cases := map[string]string{
		eventsql.Enqueue:       "INSERT hazard.outbox",
		eventsql.OutboxBatch:   "SELECT hazard.outbox",
		eventsql.OutboxDelete:  "DELETE hazard.outbox",
		eventsql.EndEvent:      "UPDATE hazard.event",
		eventsql.OutboxBacklog: "SELECT hazard.outbox",
		"begin":                "BEGIN",
		"  (select 1)":         "SELECT",
		"SELECT e.id, q.mag FROM hazard.event e JOIN hazard.quake q ON q.event_id = e.id": "SELECT hazard.event",
		"WITH x AS (SELECT * FROM ts.site) INSERT INTO ts.series SELECT * FROM x":         "WITH ts.site",
		"": "QUERY",
	}
	for sql, want := range cases {
		if _, got := Summarize(sql); got != want {
			t.Errorf("Summarize(%q) = %q, ingin %q", sql, got, want)
		}
	}
	if op, _ := Summarize(quakesql.OutboxBatch); op != "SELECT" {
		t.Errorf("operasi %q", op)
	}
	if got := truncate("ééé", 3); got != "é" {
		t.Errorf("truncate harus di batas rune: %q", got)
	}
}

func TestQueryErrorType(t *testing.T) {
	for err, want := range map[error]string{
		nil:           "",
		pgx.ErrNoRows: "",
		fmt.Errorf("x: %w", &pgconn.PgError{Code: "23514"}): "23514",
		context.Canceled:            "canceled",
		context.DeadlineExceeded:    "timeout",
		errors.New("koneksi putus"): "_OTHER",
	} {
		if got := queryErrorType(err); got != want {
			t.Errorf("queryErrorType(%v) = %q, ingin %q", err, got, want)
		}
	}
}

func TestTracerRecordsSpansAndDuration(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	tr, err := NewTracer(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)), sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: eventsql.Enqueue})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("INSERT 0 1")})
	ctx = tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: eventsql.Enqueue})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23514"}})
	tr.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{}) // tanpa Start: diabaikan

	spans := rec.Ended()
	if len(spans) != 2 || spans[0].Name() != "INSERT hazard.outbox" || spans[0].SpanKind() != trace.SpanKindClient {
		t.Fatalf("span %v", spans)
	}
	if spans[1].Status().Code != codes.Error {
		t.Errorf("status span galat %v", spans[1].Status())
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	h := rm.ScopeMetrics[0].Metrics[0]
	if h.Name != "db.client.operation.duration" || len(h.Data.(metricdata.Histogram[float64]).DataPoints) != 2 {
		t.Fatalf("metrik %+v", h)
	}
}

func TestTraceParentForOutbox(t *testing.T) {
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := traceParent(context.Background(), ports.OutboxMessage{TraceParent: tp}); got != tp {
		t.Errorf("nilai sah dari pesan: %q", got)
	}
	if got := traceParent(context.Background(), ports.OutboxMessage{TraceParent: "rusak"}); got != "" {
		t.Errorf("nilai rusak harus dibuang: %q", got)
	}
	if got := traceParent(context.Background(), ports.OutboxMessage{}); got != "" {
		t.Errorf("tanpa span: %q", got)
	}
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	if got := traceParent(ctx, ports.OutboxMessage{}); got != tp {
		t.Errorf("dari span di ctx: %q", got)
	}
}
