package s3archive

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archivetest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/s3archive/s3test"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func config(srv *s3test.Server, prefix string) Config {
	return Config{
		Endpoint: srv.URL, Region: "garage", Bucket: srv.Bucket, Prefix: prefix,
		AccessKeyID: "GKujiujiujiujiuji", SecretAccessKey: "rahasiaujirahasiauji",
		HTTPClient: srv.Client(),
	}
}

func TestConformance(t *testing.T) {
	archivetest.Run(t, func(t *testing.T) ports.ArchiveStore {
		a, err := New(config(s3test.New(t, "siaga-arsip"), "raw"))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}, archivetest.Options{Many: 250}) // 250 objek = 3 halaman tiruan
}

func TestPrefixAndCheck(t *testing.T) {
	srv := s3test.New(t, "siaga-arsip")
	a, err := New(config(srv, "raw/"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := a.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Location() != "s3://siaga-arsip/raw/" {
		t.Fatal(a.Location())
	}
	if err := a.Put(ctx, "c/2026/09/24/000000Z-aa.json.gz", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Objek di luar awalan (arsip jenis lain di bucket yang sama) tidak terlihat.
	srv.Set("backfill/c/x.gz", []byte("y"))
	if got := slices.Collect(keys(t, a)); !slices.Equal(got, []string{"c/2026/09/24/000000Z-aa.json.gz"}) {
		t.Fatal(got)
	}
	if _, ok := srv.Objects()["raw/c/2026/09/24/000000Z-aa.json.gz"]; !ok {
		t.Fatalf("objek tidak di bawah awalan: %v", srv.Objects())
	}
	// Tanpa awalan, semua objek terlihat.
	b, err := New(config(srv, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(keys(t, b)); len(got) != 2 {
		t.Fatal(got)
	}
	other := config(srv, "")
	other.Bucket = "bucket-lain"
	c, err := New(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(ctx); err == nil || !strings.Contains(err.Error(), "bucket-lain") {
		t.Fatalf("Check bucket tidak ada: %v", err)
	}
}

func keys(t *testing.T, a *Archive) func(func(string) bool) {
	return func(yield func(string) bool) {
		for o, err := range a.List(t.Context(), "", "") {
			if err != nil {
				t.Fatal(err)
			}
			if !yield(o.Key) {
				return
			}
		}
	}
}

func TestRetryAndErrors(t *testing.T) {
	srv := s3test.New(t, "siaga-arsip")
	a, err := New(config(srv, ""))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	// Satu galat 500 dicoba ulang SDK.
	srv.FailNext("PUT", 1)
	if err := a.Put(ctx, "k/x.gz", []byte("isi")); err != nil {
		t.Fatalf("Put dengan satu galat sementara: %v", err)
	}
	// Galat terus-menerus dilaporkan dengan nama kunci.
	srv.FailNext("PUT", 10)
	if err := a.Put(ctx, "k/y.gz", []byte("isi")); err == nil || !strings.Contains(err.Error(), "k/y.gz") {
		t.Fatalf("Put gagal terus: %v", err)
	}
	srv.FailNext("GET", 10)
	if _, err := a.Get(ctx, "k/x.gz"); err == nil || errors.Is(err, ports.ErrArchiveNotFound) {
		t.Fatalf("Get gagal terus: %v", err)
	}
	var listErr error
	for _, err := range a.List(ctx, "", "") {
		listErr = err
	}
	if listErr == nil {
		t.Fatal("List harus meneruskan galat server")
	}
	srv.FailNext("GET", 0)
	for _, key := range []string{"", "/abs", "a//b", "a/../b", `a\b`, "./a", "a/."} {
		if err := a.Put(ctx, key, nil); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q) = %v", key, err)
		}
		if _, err := a.Get(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Get(%q) = %v", key, err)
		}
	}
}

func TestGetSizeLimit(t *testing.T) {
	srv := s3test.New(t, "siaga-arsip")
	cfg := config(srv, "")
	cfg.MaxObjectBytes = 4
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv.Set("pas.gz", []byte("1234"))
	srv.Set("besar.gz", []byte("12345"))
	if b, err := a.Get(t.Context(), "pas.gz"); err != nil || !bytes.Equal(b, []byte("1234")) {
		t.Fatal(b, err)
	}
	if _, err := a.Get(t.Context(), "besar.gz"); err == nil || !strings.Contains(err.Error(), "lebih dari 4 byte") {
		t.Fatal(err)
	}
}

func TestPutSendsIntegrityHeaders(t *testing.T) {
	srv := s3test.New(t, "siaga-arsip")
	a, err := New(config(srv, ""))
	if err != nil {
		t.Fatal(err)
	}
	// Server tiruan menolak Content-MD5 yang salah dan badan aws-chunked, jadi
	// Put yang lolos berarti header integritas benar dan badan polos.
	for _, key := range []string{"a.gz", "b.bin"} {
		if err := a.Put(t.Context(), key, bytes.Repeat([]byte("siaga"), 1000)); err != nil {
			t.Fatal(err)
		}
	}
	if len(srv.Objects()) != 2 {
		t.Fatal(srv.Objects())
	}
}

func TestValidate(t *testing.T) {
	good := Config{Endpoint: "http://127.0.0.1:3900", Region: "garage", Bucket: "siaga-arsip", AccessKeyID: "GK1", SecretAccessKey: "s"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"endpoint kosong":    func(c *Config) { c.Endpoint = "" },
		"endpoint skema":     func(c *Config) { c.Endpoint = "ftp://x" },
		"endpoint userinfo":  func(c *Config) { c.Endpoint = "http://a:b@127.0.0.1:3900" },
		"region":             func(c *Config) { c.Region = "" },
		"bucket pendek":      func(c *Config) { c.Bucket = "ab" },
		"bucket kapital":     func(c *Config) { c.Bucket = "Siaga" },
		"bucket titik ganda": func(c *Config) { c.Bucket = "a..b" },
		"bucket tanda awal":  func(c *Config) { c.Bucket = "-abc" },
		"awalan":             func(c *Config) { c.Prefix = "../x" },
		"kredensial":         func(c *Config) { c.SecretAccessKey = "" },
		"batas negatif":      func(c *Config) { c.MaxObjectBytes = -1 },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: harus ditolak", name)
		}
		if _, err := New(c); err == nil {
			t.Errorf("%s: New harus gagal", name)
		}
	}
	if !validBucket("a.b-c") || validBucket(strings.Repeat("a", 64)) {
		t.Fatal("validBucket")
	}
}
