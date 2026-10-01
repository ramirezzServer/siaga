// Package reanalysiscache menyimpan cache deret historis alat kalibrasi
// (app/reanalysis) sebagai file JSON di .cache/, satu file per sumber.
package reanalysiscache

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
)

// Path adalah lokasi file cache sumber src di folder dir.
func Path(dir string, src reanalysis.Source) string {
	return filepath.Join(dir, src.Key()+".json")
}

// Load membaca cache untuk src; file yang belum ada berarti cache kosong.
func Load(path string, src reanalysis.Source) (*reanalysis.Cache, error) {
	f, err := os.Open(path) //nolint:gosec // path dari flag operator
	if errors.Is(err, fs.ErrNotExist) {
		return reanalysis.NewCache(src), nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return reanalysis.ReadCache(f, src)
}

// Save menulis cache lewat file sementara lalu rename, jadi file tetap utuh
// bila proses berhenti di tengah.
func Save(path string, c *reanalysis.Cache) error {
	var b bytes.Buffer
	if err := c.Write(&b); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
