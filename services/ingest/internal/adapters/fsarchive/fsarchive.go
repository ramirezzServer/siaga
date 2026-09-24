// Package fsarchive menyimpan payload mentah di filesystem lokal. Kuncinya
// sama dengan yang nanti dipakai di object storage (Garage), jadi folder arsip
// bisa diunggah apa adanya dan dipakai untuk uji replay.
package fsarchive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidKey menandai kunci yang bisa keluar dari folder arsip.
var ErrInvalidKey = errors.New("kunci arsip tidak valid")

// Archive adalah ports.Archive di atas satu folder.
type Archive struct {
	root string
}

// New membuat arsip di folder root (dibuat bila belum ada).
func New(root string) (*Archive, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("folder arsip %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("membuat folder arsip: %w", err)
	}
	return &Archive{root: abs}, nil
}

// Root mengembalikan folder arsip absolut.
func (a *Archive) Root() string { return a.root }

// Put menulis data secara atomik (file sementara lalu rename). Kunci yang
// sudah ada ditimpa dengan isi yang sama, jadi Put idempotent.
func (a *Archive) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" || strings.Contains(key, `\`) || !filepath.IsLocal(key) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	path := filepath.Join(a.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("membuat folder %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("file sementara: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("menulis %s: %w", key, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("menutup %s: %w", key, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("memindahkan %s: %w", key, err)
	}
	return nil
}
