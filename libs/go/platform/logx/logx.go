// Package logx menyiapkan logger JSON terstruktur yang seragam untuk semua layanan.
package logx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// ParseLevel mengubah teks (debug, info, warn, error) menjadi slog.Level.
// String kosong dianggap info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logx: level log tidak dikenal %q", s)
	}
}

// New membuat logger JSON yang setiap barisnya membawa nama layanan, serta
// trace_id dan span_id bila dicatat dengan context yang membawa span
// (InfoContext dan sejenisnya). extra adalah handler tambahan yang menerima
// catatan yang sama, misal pengirim log OTLP (otelx.Telemetry.LogHandler);
// nil diabaikan. Level berlaku untuk semua handler.
func New(w io.Writer, service string, level slog.Level, extra ...slog.Handler) *slog.Logger {
	handlers := []slog.Handler{traceHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})}}
	for _, h := range extra {
		if h != nil {
			handlers = append(handlers, h)
		}
	}
	if len(handlers) == 1 {
		return slog.New(handlers[0]).With(slog.String("service", service))
	}
	return slog.New(fanout{level: level, handlers: handlers}).With(slog.String("service", service))
}

// traceHandler menambahkan trace_id dan span_id dari span di context.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// fanout meneruskan setiap catatan ke semua handler yang menerimanya.
type fanout struct {
	level    slog.Leveler
	handlers []slog.Handler
}

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	if l < f.level.Level() {
		return false
	}
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := fanout{level: f.level, handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := fanout{level: f.level, handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithGroup(name)
	}
	return out
}
