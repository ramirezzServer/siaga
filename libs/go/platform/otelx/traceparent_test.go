package otelx

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const sample = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestTraceParentRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := ContextWithTraceParent(context.Background(), sample)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() || !sc.IsRemote() || sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("span context %+v", sc)
	}
	if got := TraceParent(ctx); got != sample {
		t.Fatalf("TraceParent = %q, ingin %q", got, sample)
	}
	if got := TraceParent(context.Background()); got != "" {
		t.Errorf("ctx tanpa span: %q", got)
	}
	for _, bad := range []string{"", "bukan", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ff"} {
		if ctx := ContextWithTraceParent(context.Background(), bad); trace.SpanContextFromContext(ctx).IsValid() {
			t.Errorf("%q menghasilkan span sah", bad)
		}
	}
}

func TestValidTraceParent(t *testing.T) {
	t.Parallel()
	valid := []string{sample, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"}
	invalid := []string{
		"", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01", // huruf besar
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01", // trace ID nol
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", // span ID nol
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", // versi terlarang
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", // versi lain
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-04", // bit flag cadangan
		sample + "-tambahan", " " + sample,
	}
	for _, s := range valid {
		if !ValidTraceParent(s) {
			t.Errorf("%q harus sah", s)
		}
	}
	for _, s := range invalid {
		if ValidTraceParent(s) {
			t.Errorf("%q harus tidak sah", s)
		}
	}
}

// dbConstraint meniru CHECK outbox_traceparent_format di migrasi 00006.
var dbPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-0[0-3]$`)

func dbConstraint(s string) bool {
	return dbPattern.MatchString(s) && s[3:35] != strings.Repeat("0", 32) && s[36:52] != strings.Repeat("0", 16)
}

// Properti: ValidTraceParent sama persis dengan constraint database, dan
// setiap nilai sah bisa dibaca lalu ditulis ulang tanpa mengubah ID-nya.
func FuzzValidTraceParent(f *testing.F) {
	f.Add(sample)
	f.Add("00-00000000000000000000000000000000-00f067aa0ba902b7-01")
	f.Add("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ff")
	f.Fuzz(func(t *testing.T, s string) {
		valid := ValidTraceParent(s)
		if valid != dbConstraint(s) {
			t.Fatalf("ValidTraceParent(%q) = %v, constraint database = %v", s, valid, dbConstraint(s))
		}
		if !valid {
			return
		}
		again := TraceParent(ContextWithTraceParent(context.Background(), s))
		if !ValidTraceParent(again) || again[:53] != s[:53] {
			t.Fatalf("tulis ulang %q menjadi %q", s, again)
		}
	})
}
