// Package logx menyiapkan logger JSON terstruktur yang seragam untuk semua layanan.
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
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

// New membuat logger JSON yang setiap barisnya membawa nama layanan.
func New(w io.Writer, service string, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h).With(slog.String("service", service))
}
