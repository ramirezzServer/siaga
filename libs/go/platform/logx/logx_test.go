package logx_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

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
