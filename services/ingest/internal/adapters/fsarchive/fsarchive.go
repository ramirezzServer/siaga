// Package fsarchive menyimpan payload mentah di filesystem lokal. Kuncinya
// sama dengan yang dipakai di object storage (Garage), jadi folder arsip bisa
// disalin ke sana apa adanya (perintah archive cp) dan dipakai untuk uji replay.
package fsarchive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// tmpPrefix adalah awalan file sementara Put; tidak pernah muncul di List.
const tmpPrefix = ".tmp-"

// ErrInvalidKey menandai kunci yang bisa keluar dari folder arsip.
var ErrInvalidKey = errors.New("kunci arsip tidak valid")

// Archive adalah ports.ArchiveStore di atas satu folder.
type Archive struct {
	root string
}

var _ ports.ArchiveStore = (*Archive)(nil)

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
	dst, err := a.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("membuat folder %s: %w", filepath.Dir(dst), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), tmpPrefix+"*")
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
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("memindahkan %s: %w", key, err)
	}
	return nil
}

// path memetakan kunci ke file di bawah root, menolak kunci yang bisa keluar
// dari root atau menunjuk file sementara.
func (a *Archive) path(key string) (string, error) {
	if key == "" || strings.Contains(key, `\`) || !filepath.IsLocal(key) || strings.HasPrefix(path.Base(key), tmpPrefix) {
		return "", fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	return filepath.Join(a.root, filepath.FromSlash(key)), nil
}

// Get membaca isi file untuk kunci; ports.ErrArchiveNotFound bila tidak ada.
func (a *Archive) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := a.path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p) //nolint:gosec // p sudah dipastikan di bawah root oleh a.path.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%w: %s", ports.ErrArchiveNotFound, key)
	case err != nil:
		return nil, fmt.Errorf("membaca %s: %w", key, err)
	}
	return b, nil
}

// List mengembalikan file berawalan prefix dengan kunci > startAfter, urut
// byte kunci (sama dengan S3). Urutan WalkDir per komponen path tidak sama
// dengan urutan byte kunci utuh ("a-b/x" < "a/x" karena '-' < '/'), jadi
// kunci dikumpulkan dulu lalu diurutkan.
func (a *Archive) List(ctx context.Context, prefix, startAfter string) iter.Seq2[ports.ArchiveObject, error] {
	return func(yield func(ports.ArchiveObject, error) bool) {
		objs, err := a.collect(ctx, prefix, startAfter)
		if err != nil {
			yield(ports.ArchiveObject{}, err)
			return
		}
		for _, o := range objs {
			if err := ctx.Err(); err != nil {
				yield(ports.ArchiveObject{}, err)
				return
			}
			if !yield(o, nil) {
				return
			}
		}
	}
}

func (a *Archive) collect(ctx context.Context, prefix, startAfter string) ([]ports.ArchiveObject, error) {
	dir := prefix[:strings.LastIndex(prefix, "/")+1]
	if strings.Contains(prefix, `\`) || (dir != "" && !filepath.IsLocal(dir)) {
		return nil, fmt.Errorf("%w: awalan %q", ErrInvalidKey, prefix)
	}
	var out []ports.ArchiveObject
	err := filepath.WalkDir(filepath.Join(a.root, filepath.FromSlash(dir)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == filepath.Join(a.root, filepath.FromSlash(dir)) {
				return fs.SkipAll // awalan yang belum pernah ditulis: daftar kosong
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasPrefix(d.Name(), tmpPrefix) {
			return nil
		}
		rel, err := filepath.Rel(a.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) || key <= startAfter {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, ports.ArchiveObject{Key: key, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mendaftar arsip %q: %w", prefix, err)
	}
	slices.SortFunc(out, func(x, y ports.ArchiveObject) int { return cmp.Compare(x.Key, y.Key) })
	return out, nil
}

// Location mengembalikan lokasi arsip untuk log.
func (a *Archive) Location() string { return "file://" + filepath.ToSlash(a.root) }

// Check memastikan folder arsip bisa ditulis.
func (a *Archive) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(a.root, tmpPrefix+"cek-*")
	if err != nil {
		return fmt.Errorf("folder arsip %s tidak bisa ditulis: %w", a.root, err)
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
