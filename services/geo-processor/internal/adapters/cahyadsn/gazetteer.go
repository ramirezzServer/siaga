package cahyadsn

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/gazetteer"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

// namesTable adalah tabel nama di db/wilayah.sql repo cahyadsn/wilayah.
const namesTable = "wilayah"

// ReadNames membaca semua kode dan nama wilayah (provinsi sampai
// desa/kelurahan, seluruh Indonesia) dari db/wilayah.sql repo
// github.com/cahyadsn/wilayah (MIT).
func ReadNames(path string) ([]gazetteer.Entry, error) {
	src, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var out []gazetteer.Entry
	err = ParseDump(src, namesTable, func(r Row) error {
		out = append(out, gazetteer.Entry{Code: r["kode"].Text, Name: r["nama"].Text})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: tidak ada baris tabel %s", path, namesTable)
	}
	return out, nil
}

// BoundaryStats merangkum ReadBoundaries.
type BoundaryStats struct {
	Files, Rows, Invalid int
	Repaired             int
}

// ReadBoundaries membaca batas provinsi, kab/kota, dan kecamatan seluruh
// Indonesia dari folder db/ repo wilayah_boundaries (prov/*.sql, kab/*.sql,
// kec/*.sql). Desa/kelurahan tidak dibaca (±400 MB untuk seluruh
// Indonesia). Baris dengan batas rusak dilewati dan dihitung di Invalid.
func ReadBoundaries(root string) (map[string]region.MultiPolygon, BoundaryStats, error) {
	var st BoundaryStats
	fsys := os.DirFS(root)
	var files []string
	for _, dir := range []string{"prov", "kab", "kec"} {
		m, err := fs.Glob(fsys, dir+"/*.sql")
		if err != nil {
			return nil, st, err
		}
		if len(m) == 0 {
			return nil, st, fmt.Errorf("folder %s/%s kosong; apakah root menunjuk ke folder db/?", root, dir)
		}
		sort.Strings(m)
		files = append(files, m...)
	}
	out := map[string]region.MultiPolygon{}
	for _, name := range files {
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, st, err
		}
		st.Files++
		err = ParseDump(src, boundariesTable, func(r Row) error {
			st.Rows++
			mp, rep, err := region.ParseLatLngPath([]byte(r["path"].Text))
			if err != nil {
				st.Invalid++
				return nil //nolint:nilerr // baris rusak dihitung, bukan menghentikan pembacaan
			}
			if rep.Any() {
				st.Repaired++
			}
			out[r["kode"].Text] = mp
			return nil
		})
		if err != nil {
			return nil, st, fmt.Errorf("%s: %w", name, err)
		}
	}
	return out, st, nil
}
