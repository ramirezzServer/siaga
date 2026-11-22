package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archiveurl"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

type usgsFake struct{ requests int }

func (f *usgsFake) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.requests++
	switch {
	case strings.Contains(r.URL, "producttype=dyfi"):
		return ports.Response{Body: []byte(`{"type":"FeatureCollection","features":[{"type":"Feature","id":"us7000ir9t",
"properties":{"mag":5.6,"time":1669011667066,"felt":169,"magType":"mww"},"geometry":{"coordinates":[106.9968,-6.836,10]}}]}`)}, nil
	case strings.Contains(r.URL, "eventid="):
		return ports.Response{Body: []byte(`{"type":"Feature","properties":{"products":{"dyfi":[{"type":"dyfi","code":"us7000ir9t",
"properties":{"num-responses":"169"},"contents":{"cdi_geo.txt":{"url":"https://x/cdi_geo.txt"}}}]}}}`)}, nil
	default:
		return ports.Response{Body: []byte("# Columns: Geocoded box,CDI,No. of responses,Hypocentral distance,Latitude,Longitude,Suspect?,City,State\n" +
			`"UTM:(48M 072 923 10000)",6.5,1,14,-6.9165,107.0363,0,"Sukabumi",""` + "\n")}, nil
	}
}

type sleepless struct{}

func (sleepless) Now() time.Time                                   { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }
func (sleepless) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func setup(t *testing.T) (*usgsFake, deps, string) {
	t.Helper()
	dir := t.TempDir()
	ar, err := fsarchive.New(filepath.Join(dir, "arsip"))
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"Infogempa":{"gempa":{"DateTime":"2026-09-25T08:04:42+00:00","Coordinates":"-6.90,107.11","Magnitude":"2.4",
"Kedalaman":"7 km","Wilayah":"Darat","Dirasakan":"II - III Kota Cianjur"}}}`
	for _, at := range []time.Time{time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)} {
		if err := ar.Put(t.Context(), archivekey.Build("bmkg-gempadirasakan", "json", "abcdef0123456789", at), gz(t, payload)); err != nil {
			t.Fatal(err)
		}
	}
	f := &usgsFake{}
	lookup := envx.Lookup(func(string) (string, bool) { return "", false })
	return f, deps{fetch: f, clock: sleepless{}, lookup: lookup, archive: func(raw string, _ archiveurl.Credentials) (ports.ArchiveReader, error) {
		return archiveurl.Open(raw, archiveurl.Credentials{}, nil)
	}}, dir
}

func TestRun(t *testing.T) {
	f, d, dir := setup(t)
	dyfiOut, bmkgOut := filepath.Join(dir, "dyfi.csv"), filepath.Join(dir, "bmkg.csv")
	args := []string{"-cache", filepath.Join(dir, "cache"), "-archive", filepath.Join(dir, "arsip"), "-out-dyfi", dyfiOut, "-out-bmkg", bmkgOut, "-bmkg-until", "2026-11-21"}
	var log bytes.Buffer
	if err := runWith(t.Context(), args, &log, d); err != nil {
		t.Fatal(err, log.String())
	}
	if f.requests != 3 {
		t.Fatalf("%d request", f.requests)
	}
	first, err := os.ReadFile(filepath.Clean(dyfiOut))
	if err != nil {
		t.Fatal(err)
	}
	bm, err := os.ReadFile(filepath.Clean(bmkgOut))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "us7000ir9t,2022-11-21T06:21:07.066Z") || !strings.Contains(string(bm), ",II - III Kota Cianjur,") ||
		!strings.Contains(string(bm), "1 payload") {
		t.Fatalf("%s\n%s\n%s", first, bm, log.String())
	}
	for _, want := range []string{"DYFI: 1 kejadian (0 tanpa produk, 0 tanpa cdi_geo), 1 sel", "BMKG: 1 payload"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log tanpa %q:\n%s", want, log.String())
		}
	}

	// -offline: tanpa request, CSV identik.
	log.Reset()
	if err := runWith(t.Context(), append(args, "-offline"), &log, d); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Clean(dyfiOut))
	if f.requests != 3 || !bytes.Equal(first, again) {
		t.Fatalf("offline: %d request, sama=%v", f.requests, bytes.Equal(first, again))
	}
}

func TestPlanAndErrors(t *testing.T) {
	f, d, dir := setup(t)
	var log bytes.Buffer
	if err := runWith(t.Context(), []string{"-plan", "-cache", filepath.Join(dir, "c")}, &log, d); err != nil || f.requests != 0 {
		t.Fatal(err, f.requests)
	}
	if !strings.Contains(log.String(), "contoh request: https://earthquake.usgs.gov/fdsnws/event/1/query?") || !strings.Contains(log.String(), "dilewati") {
		t.Fatal(log.String())
	}
	for name, args := range map[string][]string{
		"argumen":    {"x"},
		"tanggal":    {"-from", "2026-13-01"},
		"sampai":     {"-to", "kemarin"},
		"terbalik":   {"-from", "2026-01-02", "-to", "2026-01-01"},
		"kotak":      {"-bbox", "1,2"},
		"bmkg-until": {"-bmkg-until", "x"},
		"flag":       {"-tidak-ada"},
	} {
		if err := runWith(t.Context(), append(args, "-cache", filepath.Join(dir, "c")), &log, d); err == nil {
			t.Errorf("%s: harus galat", name)
		}
	}
	d.archive = func(string, archiveurl.Credentials) (ports.ArchiveReader, error) {
		return nil, errors.New("tidak terbuka")
	}
	if err := runWith(t.Context(), []string{"-cache", filepath.Join(dir, "c"), "-archive", "s3://x", "-out-dyfi", filepath.Join(dir, "d.csv")}, &log, d); err == nil {
		t.Fatal("arsip gagal dibuka harus galat")
	}
}
