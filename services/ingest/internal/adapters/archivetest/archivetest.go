// Package archivetest adalah uji kesesuaian bersama untuk implementasi
// ports.ArchiveStore (filesystem, S3 tiruan, dan Garage asli di uji
// integrasi), supaya replay dan penyalinan arsip berperilaku sama di semua
// penyimpanan.
package archivetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Options mengatur Run.
type Options struct {
	// Many adalah jumlah objek untuk uji daftar panjang; lebih dari 1.000
	// menguji paginasi ListObjectsV2. Nol = 30.
	Many int
}

// Run menguji store baru dari fresh (satu store kosong per subtest).
func Run(t *testing.T, fresh func(t *testing.T) ports.ArchiveStore, opts Options) {
	t.Helper()
	if opts.Many == 0 {
		opts.Many = 30
	}
	t.Run("PutGet", func(t *testing.T) {
		s := fresh(t)
		ctx := t.Context()
		key := "bmkg-autogempa/2026/09/24/002005Z-0123456789ab.json.gz"
		for range 2 { // idempotent
			if err := s.Put(ctx, key, []byte("isi")); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Get(ctx, key)
		if err != nil || string(got) != "isi" {
			t.Fatalf("Get = %q, %v", got, err)
		}
		// Isi biner dan kosong tetap utuh.
		bin := []byte{0x1f, 0x8b, 0, 0xff, '\n', '\r'}
		if err := s.Put(ctx, "biner/x.gz", bin); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, "biner/x.gz"); err != nil || !bytes.Equal(got, bin) {
			t.Fatalf("biner: %v, %v", got, err)
		}
		if err := s.Put(ctx, "kosong/x.gz", nil); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, "kosong/x.gz"); err != nil || len(got) != 0 {
			t.Fatalf("kosong: %v, %v", got, err)
		}
	})
	t.Run("GetNotFound", func(t *testing.T) {
		s := fresh(t)
		if _, err := s.Get(t.Context(), "tidak/ada.gz"); !errors.Is(err, ports.ErrArchiveNotFound) {
			t.Fatalf("Get = %v, ingin ErrArchiveNotFound", err)
		}
	})
	t.Run("ListOrderPrefixStartAfter", func(t *testing.T) {
		s := fresh(t)
		ctx := t.Context()
		keys := []string{
			"c/2026/09/24/000001Z-aa.json.gz",
			"c/2026/09/24/000000Z-bb.json.gz",
			"c-latest/2026/09/24/000000Z-cc.json.gz", // '-' < '/': sebelum "c/..." dalam urutan byte
			"c/2026/09/25/000000Z-dd.json.gz",
			"d/2026/09/24/000000Z-ee.json.gz",
			"c/2026/09/24/000001Z-ab.json.gz",
		}
		for i, k := range keys {
			if err := s.Put(ctx, k, bytes.Repeat([]byte("x"), i+1)); err != nil {
				t.Fatal(err)
			}
		}
		all := collect(t, s, "", "")
		want := slices.Clone(keys)
		slices.Sort(want)
		if !slices.Equal(names(all), want) {
			t.Fatalf("urutan:\n%v\ningin\n%v", names(all), want)
		}
		for _, o := range all {
			if o.Size != int64(slices.Index(keys, o.Key)+1) {
				t.Fatalf("ukuran %s = %d", o.Key, o.Size)
			}
		}
		if got := names(collect(t, s, "c/", "")); !slices.Equal(got, []string{
			"c/2026/09/24/000000Z-bb.json.gz", "c/2026/09/24/000001Z-aa.json.gz",
			"c/2026/09/24/000001Z-ab.json.gz", "c/2026/09/25/000000Z-dd.json.gz",
		}) {
			t.Fatalf("awalan c/: %v", got)
		}
		if got := names(collect(t, s, "c", "c/2026/09/24/000001Z")); !slices.Equal(got, []string{
			"c/2026/09/24/000001Z-aa.json.gz", "c/2026/09/24/000001Z-ab.json.gz", "c/2026/09/25/000000Z-dd.json.gz",
		}) {
			t.Fatalf("startAfter: %v", got)
		}
		if got := names(collect(t, s, "c/2026/09/2", "c/2026/09/24/000001Z-ab.json.gz")); !slices.Equal(got, []string{"c/2026/09/25/000000Z-dd.json.gz"}) {
			t.Fatalf("startAfter sama dengan kunci: %v", got)
		}
		if got := collect(t, s, "tidak-ada/", ""); len(got) != 0 {
			t.Fatalf("awalan kosong: %v", got)
		}
		// Berhenti di tengah iterasi tidak boleh panik atau menggantung.
		n := 0
		for _, err := range s.List(ctx, "", "") {
			if err != nil {
				t.Fatal(err)
			}
			if n++; n == 2 {
				break
			}
		}
	})
	t.Run("ListMany", func(t *testing.T) {
		s := fresh(t)
		ctx := t.Context()
		want := make([]string, opts.Many)
		for i := range want {
			want[i] = fmt.Sprintf("m/2026/09/24/%06dZ-%x.json.gz", i%240000, i)
			if err := s.Put(ctx, want[i], []byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		slices.Sort(want)
		if got := names(collect(t, s, "m/", "")); !slices.Equal(got, want) {
			t.Fatalf("%d objek, ingin %d", len(got), len(want))
		}
	})
	t.Run("ContextCanceled", func(t *testing.T) {
		s := fresh(t)
		if err := s.Put(t.Context(), "x/y.gz", []byte("1")); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := s.Put(ctx, "x/z.gz", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("Put: %v", err)
		}
		if _, err := s.Get(ctx, "x/y.gz"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Get: %v", err)
		}
		var listErr error
		for _, err := range s.List(ctx, "", "") {
			listErr = err
		}
		if !errors.Is(listErr, context.Canceled) {
			t.Fatalf("List: %v", listErr)
		}
	})
}

func collect(t *testing.T, s ports.ArchiveReader, prefix, startAfter string) []ports.ArchiveObject {
	t.Helper()
	var out []ports.ArchiveObject
	for o, err := range s.List(t.Context(), prefix, startAfter) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

func names(objs []ports.ArchiveObject) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.Key
	}
	return out
}
