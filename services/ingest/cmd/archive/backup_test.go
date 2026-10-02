package main

import (
	"bytes"
	"errors"
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

func withNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

// hotArchive mengisi folder arsip: prakiraan dan gempa pada 10 Sep (lama)
// dan 1 Okt 2026.
func hotArchive(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	a, err := fsarchive.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []time.Time{time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)} {
		for i, conn := range []string{"bmkg-prakiraan", "bmkg-prakiraan", "bmkg-autogempa"} {
			body := []byte(conn + day.String() + strings.Repeat("x", i))
			if _, err := emit.Archive(t.Context(), a, conn, "json", emit.Sum(body), body, day.Add(time.Duration(i)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func count(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBackupPruneRestore(t *testing.T) {
	hot := hotArchive(t)
	srv := s3test.New(t, "siaga-cadangan")
	// Seperti key B2 yang dibatasi ke satu bucket.
	srv.DenyHeadBucket = true
	env := lookup(map[string]string{ //nolint:gosec // kredensial tiruan untuk server S3 test.
		"INGEST_ARCHIVE_URL":                  hot,
		"ARCHIVE_BACKUP_URL":                  "s3://siaga-cadangan/arsip/raw?endpoint=" + url.QueryEscape(srv.URL) + "&region=us-west-004",
		"ARCHIVE_BACKUP_S3_ACCESS_KEY_ID":     "KUNCIUJIKUNCIUJI",
		"ARCHIVE_BACKUP_S3_SECRET_ACCESS_KEY": "RAHASIAUJIRAHASIAUJI",
	})
	withNow(t, time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	if err := run(t.Context(), []string{"backup"}, env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hari sebelum 2026-10-02", "bmkg-prakiraan  2026-09-10  1       2", "4 bagian baru"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("backup tanpa %q:\n%s", want, out.String())
		}
	}
	if _, ok := srv.Objects()["arsip/raw/bmkg-prakiraan/2026/10/01.p1.tar.zst"]; !ok {
		t.Fatalf("bundel tidak di bawah awalan: %v", keysOf(srv.Objects()))
	}
	out.Reset()
	if err := run(t.Context(), []string{"backup", "-json"}, env, &out, io.Discard); err != nil || !strings.Contains(out.String(), `"covered": 4`) {
		t.Fatalf("%s %v", out.String(), err)
	}

	out.Reset()
	if err := run(t.Context(), []string{"backup-verify"}, env, &out, io.Discard); err != nil ||
		!strings.Contains(out.String(), "2 bagian, 3 objek utuh, 0 bagian rusak (hari 2026-09-30 s.d. 2026-10-01)") {
		t.Fatalf("%s %v", out.String(), err)
	}
	out.Reset()
	if err := run(t.Context(), []string{"backup-verify", "-all"}, env, &out, io.Discard); err != nil || !strings.Contains(out.String(), "4 bagian, 6 objek utuh") {
		t.Fatalf("%s %v", out.String(), err)
	}

	out.Reset()
	if err := run(t.Context(), []string{"prune"}, env, &out, io.Discard); err != nil ||
		!strings.Contains(out.String(), "bmkg-prakiraan  14 hari  2026-09-18     1     2") || !strings.Contains(out.String(), "tambahkan -apply") {
		t.Fatalf("%s %v", out.String(), err)
	}
	if count(t, hot) != 6 {
		t.Fatal("prune tanpa -apply menghapus")
	}
	out.Reset()
	if err := run(t.Context(), []string{"prune", "-apply"}, env, &out, io.Discard); err != nil || count(t, hot) != 4 {
		t.Fatalf("%s %v %d", out.String(), err, count(t, hot))
	}

	into := t.TempDir()
	out.Reset()
	if err := run(t.Context(), []string{"restore", "-from", "2026-09-01", "-to", "2026-09-30", "-into", into}, env, &out, io.Discard); err != nil ||
		!strings.Contains(out.String(), "2 bagian, 3 dipulihkan, 0 sudah ada, 0 bagian rusak") || count(t, into) != 3 {
		t.Fatalf("%s %v", out.String(), err)
	}
	// Pulihkan ke arsip panas: hanya yang dipangkas yang ditulis kembali.
	out.Reset()
	if err := run(t.Context(), []string{"restore", "-from", "2026-09-01", "-connectors", "bmkg-prakiraan"}, env, &out, io.Discard); err != nil ||
		!strings.Contains(out.String(), "2 bagian, 2 dipulihkan, 2 sudah ada") || count(t, hot) != 6 {
		t.Fatalf("%s %v", out.String(), err)
	}

	srv.Set("arsip/raw/bmkg-autogempa/2026/10/01.p1.tar.zst", []byte("rusak"))
	out.Reset()
	if err := run(t.Context(), []string{"backup-verify"}, env, &out, io.Discard); !errors.Is(err, errVerify) {
		t.Fatalf("%s %v", out.String(), err)
	}
	if err := run(t.Context(), []string{"restore", "-from", "2026-10-01", "-into", t.TempDir()}, env, io.Discard, io.Discard); !errors.Is(err, errVerify) {
		t.Fatal(err)
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestBackupArguments(t *testing.T) {
	hot := hotArchive(t)
	base := map[string]string{
		"INGEST_ARCHIVE_URL":                  hot,
		"ARCHIVE_BACKUP_URL":                  "s3://siaga-cadangan/arsip?endpoint=http://127.0.0.1:1",
		"ARCHIVE_BACKUP_S3_ACCESS_KEY_ID":     "ganti-saya",
		"ARCHIVE_BACKUP_S3_SECRET_ACCESS_KEY": "ganti-saya",
	}
	with := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return lookup(m)
	}
	cases := []struct {
		name string
		args []string
		env  func(string) (string, bool)
		want string
	}{
		{"kredensial contoh", []string{"backup"}, with(), "kredensial cadangan belum diisi"},
		{"tanpa URL", []string{"backup-verify"}, with("ARCHIVE_BACKUP_URL", ""), "URL cadangan kosong"},
		{"sama dengan arsip", []string{"backup", "-backup", hot}, with(), "cadangan dan arsip sama"},
		{"restore tanpa -from", []string{"restore", "-backup", t.TempDir()}, with(), "-from wajib"},
		{"tanggal salah", []string{"prune", "-from", "1/10/2026"}, with(), "-from harus YYYY-MM-DD"},
		{"tanggal terbalik", []string{"backup", "-from", "2026-10-02", "-to", "2026-10-01"}, with(), "-to sebelum -from"},
		{"aturan salah", []string{"prune", "-retention", "bmkg-prakiraan=3", "-backup", t.TempDir()}, with(), "minimal 8"},
		{"aturan dari env", []string{"prune", "-backup", t.TempDir()}, with("ARCHIVE_RETENTION", "x"), "konektor=hari"},
		{"argumen sisa", []string{"backup", "lebih"}, with(), "argumen tidak dikenal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := run(t.Context(), c.args, c.env, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("galat %v, ingin memuat %q", err, c.want)
			}
		})
	}
	// Cadangan ke folder tidak butuh kredensial.
	withNow(t, time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	if err := run(t.Context(), []string{"backup", "-backup", "file://" + t.TempDir(), "-connectors", "bmkg-autogempa", "-from", "2026-10-01"}, with(), &out, io.Discard); err != nil ||
		!strings.Contains(out.String(), "1 bagian baru") {
		t.Fatalf("%s %v", out.String(), err)
	}
}
