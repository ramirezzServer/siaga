package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()
	cases := map[string]slog.Level{
		"": slog.LevelInfo, "info": slog.LevelInfo, " DEBUG ": slog.LevelDebug,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError,
	}
	for in, want := range cases {
		got, err := logx.ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := logx.ParseLevel("verbose"); err == nil {
		t.Error("ParseLevel(verbose) harus gagal")
	}
}

func TestNewWritesServiceAttribute(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logx.New(&buf, "geo-processor", slog.LevelInfo).Info("halo")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("output bukan JSON: %v", err)
	}
	if line["service"] != "geo-processor" || line["msg"] != "halo" {
		t.Errorf("baris log tidak sesuai: %v", line)
	}
}

// Span buatan dengan ID tetap, tanpa SDK.
func spanContext(t *testing.T) context.Context {
	t.Helper()
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestNewAddsTraceIDs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := logx.New(&buf, "ingest", slog.LevelInfo)
	log.InfoContext(spanContext(t), "dengan span")
	log.Info("tanpa span")
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("ingin 2 baris, dapat %d", len(lines))
	}
	var with, without map[string]any
	if err := errors.Join(json.Unmarshal(lines[0], &with), json.Unmarshal(lines[1], &without)); err != nil {
		t.Fatal(err)
	}
	if with["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || with["span_id"] != "00f067aa0ba902b7" {
		t.Errorf("trace_id/span_id tidak tercatat: %v", with)
	}
	if _, ok := without["trace_id"]; ok {
		t.Errorf("baris tanpa span tidak boleh punya trace_id: %v", without)
	}
}

// recorder mengumpulkan catatan dan atributnya.
type recorder struct {
	records *[]slog.Record
	attrs   []slog.Attr
}

func (r recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r recorder) Handle(_ context.Context, rec slog.Record) error {
	rec.AddAttrs(r.attrs...)
	*r.records = append(*r.records, rec)
	return nil
}

func (r recorder) WithAttrs(a []slog.Attr) slog.Handler {
	return recorder{records: r.records, attrs: append(append([]slog.Attr{}, r.attrs...), a...)}
}
func (r recorder) WithGroup(string) slog.Handler { return r }

func TestNewFansOutToExtraHandlers(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	var got []slog.Record
	log := logx.New(&buf, "geo-processor", slog.LevelInfo, nil, recorder{records: &got})
	log.Debug("di bawah level")
	log.With(slog.String("consumer", "gempa")).Warn("peringatan")
	if len(got) != 1 || got[0].Message != "peringatan" {
		t.Fatalf("handler tambahan menerima %d catatan: %+v", len(got), got)
	}
	attrs := map[string]string{}
	got[0].Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
	if attrs["service"] != "geo-processor" || attrs["consumer"] != "gempa" {
		t.Errorf("atribut tidak diteruskan ke handler tambahan: %v", attrs)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"msg":"peringatan"`)) || bytes.Contains(buf.Bytes(), []byte("di bawah level")) {
		t.Errorf("output JSON tidak sesuai: %s", buf.String())
	}
	if log.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("level debug harus mati")
	}
	grouped := log.WithGroup("g")
	grouped.Info("dalam grup", slog.Int("n", 1))
	if !bytes.Contains(buf.Bytes(), []byte(`"g":{"n":1}`)) {
		t.Errorf("grup tidak diteruskan ke JSON: %s", buf.String())
	}
}
