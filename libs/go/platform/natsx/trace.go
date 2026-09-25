package natsx

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerName adalah nama instrumentasi span NATS.
const TracerName = "github.com/ramirezzServer/siaga/libs/go/platform/natsx"

// HeaderFetchedAt membawa waktu ambil payload sumber (RFC 3339, UTC) di
// setiap event raw.*, supaya consumer bisa mengukur latensi pipa tanpa
// membuka isi pesan (docs/events.md).
const HeaderFetchedAt = "Siaga-Fetched-At"

// HeaderCarrier menyesuaikan nats.Header dengan propagation.TextMapCarrier.
// Header traceparent dan tracestate W3C ditulis apa adanya (huruf kecil).
type HeaderCarrier nats.Header

var _ propagation.TextMapCarrier = HeaderCarrier{}

// Get mengembalikan nilai pertama header key.
func (c HeaderCarrier) Get(key string) string { return nats.Header(c).Get(key) }

// Set mengganti nilai header key.
func (c HeaderCarrier) Set(key, value string) { nats.Header(c).Set(key, value) }

// Keys mengembalikan semua nama header.
func (c HeaderCarrier) Keys() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}

// Inject menulis konteks trace dari ctx ke header pesan. Tidak menulis
// apa pun bila ctx tidak membawa span.
func Inject(ctx context.Context, h nats.Header) {
	otel.GetTextMapPropagator().Inject(ctx, HeaderCarrier(h))
}

// Extract membaca konteks trace dari header pesan ke ctx.
func Extract(ctx context.Context, h nats.Header) context.Context {
	if h == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, HeaderCarrier(h))
}

func tracer() trace.Tracer { return otel.Tracer(TracerName) }

// StartPublish memulai span producer "send <subject>" untuk satu pesan.
// Pemanggil menulis konteks span ke header lewat Inject(ctx, msg.Header),
// lalu menutup span setelah PubAck diterima.
func StartPublish(ctx context.Context, subject, msgID string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	base := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("nats"),
		semconv.MessagingOperationTypeSend,
		semconv.MessagingOperationName("publish"),
		semconv.MessagingDestinationName(subject),
	}
	if msgID != "" {
		base = append(base, semconv.MessagingMessageID(msgID))
	}
	return tracer().Start(ctx, "send "+subject,
		trace.WithSpanKind(trace.SpanKindProducer), trace.WithAttributes(append(base, attrs...)...))
}

// StartProcess memulai span consumer "process <subject>" untuk satu pesan,
// sebagai anak dari span penerbit bila header membawa traceparent.
func StartProcess(ctx context.Context, subject, consumer string, h nats.Header, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx = Extract(ctx, h)
	base := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("nats"),
		semconv.MessagingOperationTypeProcess,
		semconv.MessagingOperationName("process"),
		semconv.MessagingDestinationName(subject),
		semconv.MessagingConsumerGroupName(consumer),
	}
	if id := h.Get("Nats-Msg-Id"); id != "" {
		base = append(base, semconv.MessagingMessageID(id))
	}
	return tracer().Start(ctx, "process "+subject,
		trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(append(base, attrs...)...))
}
