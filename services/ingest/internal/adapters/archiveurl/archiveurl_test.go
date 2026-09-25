package archiveurl

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/s3archive/s3test"
)

var creds = Credentials{AccessKeyID: "GKujiujiujiujiuji", SecretAccessKey: "rahasiaujirahasiauji"}

func TestOpenDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "arsip")
	for _, raw := range []string{dir, "file://" + dir, "file://localhost" + dir, "  " + dir + "  "} {
		s, err := Open(raw, Credentials{}, nil)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if s.Location() != "file://"+filepath.ToSlash(dir) {
			t.Fatal(s.Location())
		}
		if err := s.Check(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenS3(t *testing.T) {
	srv := s3test.New(t, "siaga-arsip")
	raw := "s3://siaga-arsip/raw?endpoint=" + url.QueryEscape(srv.URL)
	s, err := Open(raw, creds, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.Location() != "s3://siaga-arsip/raw/" {
		t.Fatal(s.Location())
	}
	if err := s.Put(t.Context(), "c/x.gz", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Objects()["raw/c/x.gz"]; !ok {
		t.Fatal(srv.Objects())
	}
	// Region eksplisit dan tanpa awalan.
	if _, err := Open("s3://siaga-arsip?endpoint="+url.QueryEscape(srv.URL)+"&region=lain", creds, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejects(t *testing.T) {
	for raw, want := range map[string]string{
		"":              "kosong",
		"file://":       "kosong",
		"file://host/x": "file:///path",
		"s3://u:p@siaga-arsip?endpoint=http://x:1": "kredensial",
		"ftp://x/y":            "skema",
		"s3://siaga-arsip/raw": "endpoint",
		"s3://siaga-arsip?endpoint=http://x:1&kunci=a": "tidak dikenal",
		"s3://Salah?endpoint=http://x:1":               "bucket",
		"s3://siaga-arsip?endpoint=%zz":                "URL arsip",
	} {
		_, err := Open(raw, creds, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Open(%q) = %v, ingin memuat %q", raw, err, want)
		}
	}
	if _, err := Open("s3://siaga-arsip?endpoint=http://x:1", Credentials{}, nil); err == nil {
		t.Error("tanpa kredensial harus ditolak")
	}
}
