package otelx

import (
	"context"
	"regexp"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// traceParentPattern adalah bentuk header traceparent W3C versi 00 (flag
// hanya bit sampled dan random). Sama dengan constraint kolom
// hazard.outbox.traceparent.
var traceParentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-0[0-3]$`)

// ValidTraceParent melaporkan apakah s adalah traceparent W3C versi 00 yang
// menunjuk span sah (trace ID dan span ID bukan nol semua).
func ValidTraceParent(s string) bool {
	if !traceParentPattern.MatchString(s) {
		return false
	}
	return trace.SpanContextFromContext(ContextWithTraceParent(context.Background(), s)).IsValid()
}

// TraceParent mengembalikan traceparent W3C dari span di ctx, atau "" bila
// ctx tidak membawa span yang sah. Dipakai untuk menyimpan konteks trace di
// luar proses, misal di baris outbox.
func TraceParent(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier["traceparent"]
}

// ContextWithTraceParent mengembalikan ctx dengan span jarak jauh dari
// traceparent. Nilai kosong atau tidak sah mengembalikan ctx apa adanya.
func ContextWithTraceParent(ctx context.Context, traceParent string) context.Context {
	if traceParent == "" {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": traceParent})
}
