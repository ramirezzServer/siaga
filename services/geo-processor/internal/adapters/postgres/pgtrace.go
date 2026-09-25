package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ramirezzServer/siaga/libs/go/platform/otelx"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// TraceScope adalah nama instrumentasi query PostgreSQL.
const TraceScope = "github.com/ramirezzServer/siaga/services/geo-processor/postgres"

// maxQueryText membatasi teks SQL di span. SQL di adapter ini adalah
// konstanta tanpa nilai parameter, jadi aman direkam; argumen tidak pernah
// ikut dicatat.
const maxQueryText = 2048

var queryBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

// Tracer adalah pgx.QueryTracer yang membuat satu span client per query dan
// mencatat histogram db.client.operation.duration, supaya query lambat
// terlihat per ringkasan query (misal "SELECT hazard.event") di Grafana.
type Tracer struct {
	tracer   trace.Tracer
	duration metric.Float64Histogram
	// summaries menyimpan ringkasan per teks SQL; SQL adapter berupa
	// konstanta, jadi jumlah entrinya kecil dan tetap.
	summaries sync.Map
}

var _ pgx.QueryTracer = (*Tracer)(nil)

// NewTracer membuat Tracer dari provider yang diberikan.
func NewTracer(tp trace.TracerProvider, mp metric.MeterProvider) (*Tracer, error) {
	h, err := mp.Meter(TraceScope).Float64Histogram("db.client.operation.duration",
		metric.WithUnit("s"), metric.WithDescription("Lama satu query PostgreSQL."),
		metric.WithExplicitBucketBoundaries(queryBuckets...))
	if err != nil {
		return nil, err
	}
	return &Tracer{tracer: tp.Tracer(TraceScope), duration: h}, nil
}

type queryKey struct{}

type queryState struct {
	start   time.Time
	summary summary
	span    trace.Span
}

// TraceQueryStart memenuhi pgx.QueryTracer.
func (t *Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	sum := t.summary(data.SQL)
	attrs := []attribute.KeyValue{
		semconv.DBSystemNamePostgreSQL,
		semconv.DBOperationName(sum.operation),
		semconv.DBQuerySummary(sum.text),
		semconv.DBQueryText(truncate(data.SQL, maxQueryText)),
	}
	if conn != nil {
		if cfg := conn.Config(); cfg != nil {
			attrs = append(attrs, semconv.DBNamespace(cfg.Database), semconv.ServerAddress(cfg.Host))
		}
	}
	ctx, span := t.tracer.Start(ctx, sum.text, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	return context.WithValue(ctx, queryKey{}, &queryState{start: time.Now(), summary: sum, span: span})
}

// TraceQueryEnd memenuhi pgx.QueryTracer.
func (t *Tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(queryKey{}).(*queryState)
	if !ok {
		return
	}
	defer st.span.End()
	attrs := []attribute.KeyValue{
		semconv.DBSystemNamePostgreSQL,
		semconv.DBOperationName(st.summary.operation),
		semconv.DBQuerySummary(st.summary.text),
	}
	if errType := queryErrorType(data.Err); errType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errType))
		st.span.RecordError(data.Err)
		st.span.SetStatus(codes.Error, errType)
	} else {
		st.span.SetAttributes(attribute.Int64("db.response.returned_rows", data.CommandTag.RowsAffected()))
	}
	t.duration.Record(context.WithoutCancel(ctx), time.Since(st.start).Seconds(), metric.WithAttributes(attrs...))
}

// queryErrorType mengembalikan SQLSTATE untuk galat PostgreSQL, "_OTHER"
// untuk galat lain, dan "" bila tidak ada galat. pgx.ErrNoRows bukan galat
// operasi.
func queryErrorType(err error) string {
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return semconv.ErrorTypeOther.Value.AsString()
}

type summary struct {
	operation string
	text      string
}

func (t *Tracer) summary(sql string) summary {
	if v, ok := t.summaries.Load(sql); ok {
		return v.(summary)
	}
	op, text := Summarize(sql)
	s := summary{operation: op, text: text}
	t.summaries.Store(sql, s)
	return s
}

// qualified mencocokkan tabel berschema sesudah FROM, INTO, UPDATE, atau
// JOIN; semua SQL adapter ini memakai nama berschema (hazard., ts., ref.).
var qualified = regexp.MustCompile(`\b(?:from|into|update|join)\s+([a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*)\b`)

// Summarize membentuk ringkasan query berkardinalitas rendah: operasi (kata
// pertama, huruf besar) dan tabel berschema pertama, misal
// "INSERT hazard.outbox". Query tanpa tabel berschema cukup operasinya
// (misal "BEGIN").
func Summarize(sql string) (operation, text string) {
	fields := strings.Fields(sql)
	op := "QUERY"
	if len(fields) > 0 {
		op = strings.ToUpper(strings.Trim(fields[0], "(;"))
	}
	text = op
	if m := qualified.FindStringSubmatch(strings.ToLower(sql)); m != nil {
		text = op + " " + m[1]
	}
	return op, text
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// traceParent memilih konteks trace untuk baris outbox: nilai yang dibawa
// pesan bila sah, atau span di ctx. Nilai yang tidak sah dibuang, bukan
// menggagalkan transaksi (telemetri tidak boleh menahan peringatan).
func traceParent(ctx context.Context, msg ports.OutboxMessage) string {
	if msg.TraceParent != "" {
		if otelx.ValidTraceParent(msg.TraceParent) {
			return msg.TraceParent
		}
		return ""
	}
	return otelx.TraceParent(ctx)
}
