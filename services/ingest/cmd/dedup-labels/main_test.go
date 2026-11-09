package main

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/quakeml"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// iscFake menjawab minggu 15–21 Jan 2023 dengan sampel bulletin dan minggu
// lain dengan dokumen kosong; sampel berisi kejadian 8 Jan juga, jadi
// kejadian yang sama muncul di dua minggu (seperti kejadian di batas minggu).
type iscFake struct {
	mu       sync.Mutex
	requests int
	sample   []byte
}

func (f *iscFake) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	u, err := url.Parse(r.URL)
	if err != nil {
		return ports.Response{}, err
	}
	switch u.Query().Get("starttime") {
	case "2023-01-15T00:00:00", "2023-01-08T00:00:00":
		return ports.Response{Body: f.sample}, nil
	}
	return ports.Response{Body: []byte(quakeml.Empty)}, nil
}

type sleepless struct{}

func (sleepless) Now() time.Time                                   { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }
func (sleepless) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func setup(t *testing.T) (*iscFake, deps, string) {
	t.Helper()
	b, err := os.ReadFile("../../internal/adapters/quakeml/testdata/isc-sampel.xml")
	if err != nil {
		t.Fatal(err)
	}
	f := &iscFake{sample: b}
	return f, deps{fetch: f, clock: sleepless{}}, t.TempDir()
}

func TestRun(t *testing.T) {
	f, d, dir := setup(t)
	out := filepath.Join(dir, "berlabel.csv")
	args := []string{"-from", "2023-01-01", "-to", "2023-01-28", "-cache", filepath.Join(dir, "cache"), "-out", out}

	var log bytes.Buffer
	if err := runWith(t.Context(), append(args, "-plan"), &log, d); err != nil {
		t.Fatal(err)
	}
	if f.requests != 0 || !strings.Contains(log.String(), "4 minggu 2023-01-01 s.d. 2023-01-28, 0 dari cache, 4 request ke ISC") ||
		!strings.Contains(log.String(), "includeallorigins=true") {
		t.Fatalf("-plan:\n%s", log.String())
	}
	if err := runWith(t.Context(), append(args, "-offline"), &log, d); err == nil || !strings.Contains(err.Error(), "-offline") {
		t.Fatalf("-offline tanpa cache: %v", err)
	}

	log.Reset()
	if err := runWith(t.Context(), args, &log, d); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if f.requests != 4 {
		t.Fatalf("%d request", f.requests)
	}
	first, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "# 6 kejadian ISC, 4 origin USGS (4 berpasangan BMKG), 4 origin BMKG, 2 pasangan negatif") ||
		strings.Count(string(first), "\nisc,") != 8 {
		t.Fatalf("CSV:\n%s", first)
	}
	if !strings.Contains(log.String(), "4 diambil") || !strings.Contains(log.String(), "1 origin tanpa magnitudo") {
		t.Fatalf("log:\n%s", log.String())
	}

	// Jalan ulang offline: tanpa request, isi identik.
	log.Reset()
	if err := runWith(t.Context(), append(args, "-offline"), &log, d); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatal(err)
	}
	if f.requests != 4 || !bytes.Equal(first, again) || !strings.Contains(log.String(), "4 dari cache") {
		t.Fatalf("offline: %d request, sama=%v\n%s", f.requests, bytes.Equal(first, again), log.String())
	}

	// -min-mag 4,5: 620012962 (mb 4,4) dan 625597758 (mb 4,3) keluar.
	if err := runWith(t.Context(), append(args, "-offline", "-min-mag", "4.5"), &log, d); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "2 origin USGS (2 berpasangan BMKG)") {
		t.Fatalf("min-mag:\n%s", b)
	}
}

func TestRunBudget(t *testing.T) {
	_, d, dir := setup(t)
	var log bytes.Buffer
	err := runWith(t.Context(), []string{"-from", "2023-01-01", "-to", "2023-01-28", "-cache", dir, "-out", filepath.Join(dir, "x.csv"), "-max-requests", "1"}, &log, d)
	if err == nil || !strings.Contains(err.Error(), "batas request") {
		t.Fatalf("%v", err)
	}
}

func TestRunFlags(t *testing.T) {
	_, d, dir := setup(t)
	for _, args := range [][]string{
		{"-from", "2023-13-01"},
		{"-to", "kemarin"},
		{"-from", "2024-01-02", "-to", "2024-01-01"},
		{"-bbox", "0,0,1,1"},
		{"-min-mag", "-1"},
		{"lebih"},
		{"-tidak-ada"},
	} {
		var log bytes.Buffer
		if err := runWith(t.Context(), append(args, "-cache", dir, "-plan"), &log, d); err == nil {
			t.Errorf("%v harus galat", args)
		}
	}
	if err := run(t.Context(), []string{"-plan", "-cache", dir, "-from", "2024-10-31"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run bawaan -plan: %v", err)
	}
}
