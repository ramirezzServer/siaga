package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/s3archive/s3test"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func fill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	a, err := fsarchive.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	for i, conn := range []string{"bmkg-autogempa", "bmkg-autogempa", "usgs-2.5-day"} {
		body := bytes.Repeat([]byte("x"), 100*(i+1))
		if _, err := emit.Archive(t.Context(), a, conn, "json", emit.Sum(body), body, at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLsVerifyCopyToS3(t *testing.T) {
	dir := fill(t)
	srv := s3test.New(t, "siaga-arsip")
	s3url := "s3://siaga-arsip/raw?endpoint=" + url.QueryEscape(srv.URL)
	env := lookup(map[string]string{
		"INGEST_ARCHIVE_URL":       s3url,
		"ARCHIVE_S3_ACCESS_KEY_ID": "GKujiujiujiujiuji", "ARCHIVE_S3_SECRET_ACCESS_KEY": "rahasiaujirahasiauji",
	})
	var out bytes.Buffer
	if err := run(t.Context(), []string{"cp", "-from", dir}, env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "3 disalin") || len(srv.Objects()) != 3 {
		t.Fatalf("%s %v", out.String(), srv.Objects())
	}
	out.Reset()
	if err := run(t.Context(), []string{"cp", "-from", dir, "-prefix", "bmkg-"}, env, &out, io.Discard); err != nil || !strings.Contains(out.String(), "2 sudah ada") {
		t.Fatalf("%s %v", out.String(), err)
	}

	out.Reset()
	if err := run(t.Context(), []string{"ls"}, env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"s3://siaga-arsip/raw/", "bmkg-autogempa  2", "usgs-2.5-day", "2026-09-24 10:02", "total"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ls tanpa %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run(t.Context(), []string{"ls", "-keys", "-prefix", "usgs"}, env, &out, io.Discard); err != nil ||
		strings.Count(out.String(), "\n") != 1 || !strings.HasPrefix(out.String(), "usgs-2.5-day/2026/09/24/030200Z-") {
		t.Fatalf("%q %v", out.String(), err)
	}

	out.Reset()
	if err := run(t.Context(), []string{"verify"}, env, &out, io.Discard); err != nil || !strings.Contains(out.String(), "3 utuh, 0 rusak") {
		t.Fatalf("%s %v", out.String(), err)
	}
	srv.Set("raw/usgs-2.5-day/2026/09/24/040000Z-000000000000.json.gz", []byte("rusak"))
	srv.Set("raw/catatan.txt", []byte("asing"))
	out.Reset()
	if err := run(t.Context(), []string{"verify"}, env, &out, io.Discard); !errors.Is(err, errCorrupt) || !strings.Contains(out.String(), "2 rusak") {
		t.Fatalf("%s %v", out.String(), err)
	}
	out.Reset()
	if err := run(t.Context(), []string{"ls"}, env, &out, io.Discard); err != nil || !strings.Contains(out.String(), "1 objek dengan kunci tidak baku") {
		t.Fatalf("%s %v", out.String(), err)
	}
}

func TestCopyFromS3ToDir(t *testing.T) {
	srv := s3test.New(t, "siaga-arsip")
	env := lookup(map[string]string{"ARCHIVE_S3_ACCESS_KEY_ID": "GKujiujiujiujiuji", "ARCHIVE_S3_SECRET_ACCESS_KEY": "rahasiaujirahasiauji"})
	src := fill(t)
	s3url := "s3://siaga-arsip?endpoint=" + url.QueryEscape(srv.URL)
	if err := run(t.Context(), []string{"cp", "-from", src, "-to", s3url}, env, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "salinan")
	if err := run(t.Context(), []string{"cp", "-from", s3url, "-to", "file://" + dst}, env, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = filepath.WalkDir(dst, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	if n != 3 {
		t.Fatalf("%d file", n)
	}
}

func TestErrors(t *testing.T) {
	dir := fill(t)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"kosong":       {nil, "subperintah kosong"},
		"opsi dulu":    {[]string{"-prefix", "x"}, "subperintah kosong"},
		"tak dikenal":  {[]string{"hapus"}, "tidak dikenal"},
		"argumen":      {[]string{"ls", "-archive", dir, "lebih"}, "argumen"},
		"arsip kosong": {[]string{"ls"}, "arsip kosong"},
		"verify url":   {[]string{"verify", "-archive", "s3://x"}, "endpoint"},
		"cp from":      {[]string{"cp", "-to", dir}, "-from"},
		"cp sama":      {[]string{"cp", "-from", dir, "-to", "file://" + dir}, "sama"},
		"cp sumber":    {[]string{"cp", "-from", "ftp://x", "-to", dir}, "sumber"},
		"cp tujuan":    {[]string{"cp", "-from", dir, "-to", "ftp://x"}, "tujuan"},
		"flag":         {[]string{"ls", "-tidak-ada"}, "flag"},
		"verify flag":  {[]string{"verify", "-x"}, "flag"},
		"cp flag":      {[]string{"cp", "-x"}, "flag"},
	} {
		err := run(t.Context(), tc.args, lookup(nil), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, ingin memuat %q", name, err, tc.want)
		}
	}
	if err := run(t.Context(), []string{"ls", "-h"}, lookup(nil), io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for b, want := range map[int64]string{10: "10 B", 2048: "2.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := human(b); got != want {
			t.Errorf("human(%d) = %s", b, got)
		}
	}
}
