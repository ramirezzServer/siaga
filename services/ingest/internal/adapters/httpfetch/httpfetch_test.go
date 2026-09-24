package httpfetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func TestFetchConditionalAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent || r.Header.Get("Accept") != "application/json" {
			t.Errorf("header salah: %v", r.Header)
		}
		if r.Header.Get("If-None-Match") == `"v1"` && r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 23 Sep 2026 12:17:00 GMT")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	f := New(5 * time.Second)
	ctx := context.Background()

	resp, err := f.Fetch(ctx, ports.Request{URL: srv.URL, Accept: "application/json"})
	if err != nil || string(resp.Body) != `{"ok":true}` || resp.ETag != `"v1"` || resp.LastModified == "" {
		t.Fatalf("%+v, %v", resp, err)
	}
	resp, err = f.Fetch(ctx, ports.Request{URL: srv.URL, Accept: "application/json", ETag: resp.ETag, LastModified: resp.LastModified})
	if err != nil || !resp.NotModified {
		t.Fatalf("harus 304: %+v, %v", resp, err)
	}
}

func TestFetchErrors(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/429":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/503":
			w.Header().Set("Retry-After", now.Add(90*time.Second).Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/500":
			http.Error(w, strings.Repeat("rusak ", 100), http.StatusInternalServerError)
		case "/besar":
			_, _ = w.Write(make([]byte, 101))
		}
	}))
	defer srv.Close()
	f := New(5 * time.Second)
	f.now = func() time.Time { return now }
	ctx := context.Background()

	_, err := f.Fetch(ctx, ports.Request{URL: srv.URL + "/429"})
	if ra, ok := errors.AsType[*ports.RetryAfterError](err); !ok || ra.After != 2*time.Minute || ra.Status != 429 {
		t.Fatalf("429: %v", err)
	}
	_, err = f.Fetch(ctx, ports.Request{URL: srv.URL + "/503"})
	if ra, ok := errors.AsType[*ports.RetryAfterError](err); !ok || ra.After != 90*time.Second || !strings.Contains(ra.Error(), "503") {
		t.Fatalf("503: %v", err)
	}
	_, err = f.Fetch(ctx, ports.Request{URL: srv.URL + "/500"})
	if se, ok := errors.AsType[*StatusError](err); !ok || se.Status != 500 || len(se.Body) > 256 || !strings.Contains(se.Error(), "HTTP 500") {
		t.Fatalf("500: %v", err)
	}
	if _, err = f.Fetch(ctx, ports.Request{URL: srv.URL + "/besar", MaxBytes: 100}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("besar: %v", err)
	}
	if resp, err := f.Fetch(ctx, ports.Request{URL: srv.URL + "/besar", MaxBytes: 101}); err != nil || len(resp.Body) != 101 {
		t.Fatalf("tepat di batas harus diterima: %v", err)
	}
	if _, err = f.Fetch(ctx, ports.Request{URL: "://rusak"}); err == nil {
		t.Fatal("URL rusak harus gagal")
	}
	srv.Close()
	if _, err = f.Fetch(ctx, ports.Request{URL: srv.URL}); err == nil {
		t.Fatal("server mati harus gagal")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":        0,
		"30":      30 * time.Second,
		" 5 ":     5 * time.Second,
		"-1":      0,
		"0":       0,
		"9999999": 0,
		"besok":   0,
		now.Add(-time.Minute).Format(http.TimeFormat): 0,
		now.Add(time.Minute).Format(http.TimeFormat):  time.Minute,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, ingin %v", in, got, want)
		}
	}
}

// Key di header ikut terkirim, dan key di path tidak pernah muncul di galat
// (koneksi gagal, status bukan 200 yang memantulkan URL, atau batas waktu).
func TestFetchSecrets(t *testing.T) {
	const secret = "KUNCIRAHASIA123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "path salah: "+r.URL.Path) //nolint:gosec // server uji, bukan HTML
	}))
	defer srv.Close()
	f := New(5 * time.Second)
	ctx := context.Background()

	_, err := f.Fetch(ctx, ports.Request{URL: srv.URL + "/api/" + secret + "/x", Header: map[string]string{"X-API-Key": secret}, Secrets: []string{secret}})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest || strings.Contains(err.Error(), secret) || !strings.Contains(se.Body, "/api/***/x") {
		t.Fatalf("%v", err)
	}
	_, err = f.Fetch(ctx, ports.Request{URL: "http://127.0.0.1:1/api/" + secret, Secrets: []string{secret}})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "/api/***") {
		t.Fatalf("%v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.Fetch(cctx, ports.Request{URL: srv.URL + "/" + secret, Secrets: []string{secret}})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), secret) {
		t.Fatalf("%v", err)
	}
	if got := ports.Redact("a/K/b/K", []string{"", "K"}); got != "a/***/b/***" {
		t.Fatal(got)
	}
}
