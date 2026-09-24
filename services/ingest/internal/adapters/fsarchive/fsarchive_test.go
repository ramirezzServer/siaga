package fsarchive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPut(t *testing.T) {
	a, err := New(filepath.Join(t.TempDir(), "arsip"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "bmkg-autogempa/2026/09/23/121730Z-0123456789ab.json.gz"
	for range 2 { // idempotent
		if err := a.Put(ctx, key, []byte("isi")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(a.Root(), filepath.FromSlash(key)))
	if err != nil || string(got) != "isi" {
		t.Fatalf("%q, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(filepath.Join(a.Root(), filepath.FromSlash(key))))
	if len(entries) != 1 {
		t.Fatalf("file sementara tertinggal: %v", entries)
	}
}

func TestPutRejectsEscapingKeys(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "../luar", "/abs/x", "a/../../b", `a\..\b`} {
		if err := a.Put(context.Background(), key, nil); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q) = %v, ingin ErrInvalidKey", key, err)
		}
	}
}

func TestPutHonoursContextAndFsErrors(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Put(ctx, "x", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx batal: %v", err)
	}
	// "file" sudah berupa file, jadi tidak bisa jadi folder.
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(context.Background(), "file/x", nil); err == nil {
		t.Fatal("folder yang ternyata file harus gagal")
	}
	if _, err := New(filepath.Join(root, "file", "sub")); err == nil {
		t.Fatal("New di bawah file harus gagal")
	}
}
