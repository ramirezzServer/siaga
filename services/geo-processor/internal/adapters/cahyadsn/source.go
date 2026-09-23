package cahyadsn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

const boundariesTable = "wilayah_boundaries"

var provinceCode = regexp.MustCompile(`^[0-9]{2}$`)

// DirSource membaca folder db/ dari salinan lokal repo wilayah_boundaries.
// Struktur yang diharapkan: prov/*.sql, kab/…_kab_PP.sql, kec/…_kec_PP.sql, kel/PP/*.sql.
type DirSource struct {
	fsys     fs.FS
	province string
	version  string
}

// NewDirSource membuat sumber untuk satu provinsi (misal "32" untuk Jawa Barat).
// version adalah identitas data (misal hash commit) yang dicatat di setiap baris.
func NewDirSource(root, province, version string) (*DirSource, error) {
	if !provinceCode.MatchString(province) {
		return nil, fmt.Errorf("kode provinsi %q harus 2 digit", province)
	}
	if strings.TrimSpace(version) == "" {
		return nil, errors.New("versi sumber data wajib diisi")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("folder sumber: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("folder sumber %s bukan direktori", root)
	}
	return &DirSource{fsys: os.DirFS(root), province: province, version: version}, nil
}

// Name mengembalikan nama sumber yang dicatat di database.
func (s *DirSource) Name() string { return "cahyadsn/wilayah_boundaries" }

// Version mengembalikan versi data.
func (s *DirSource) Version() string { return s.version }

func (s *DirSource) files() ([]string, error) {
	prov, err := fs.Glob(s.fsys, "prov/*.sql")
	if err != nil {
		return nil, err
	}
	kel, err := fs.Glob(s.fsys, "kel/"+s.province+"/*.sql")
	if err != nil {
		return nil, err
	}
	required := []string{
		"kab/wilayah_boundaries_kab_" + s.province + ".sql",
		"kec/wilayah_boundaries_kec_" + s.province + ".sql",
	}
	for _, f := range required {
		if _, err := fs.Stat(s.fsys, f); err != nil {
			return nil, fmt.Errorf("file wajib %s tidak ada: %w", f, err)
		}
	}
	if len(prov) == 0 || len(kel) == 0 {
		return nil, fmt.Errorf("folder prov/ atau kel/%s/ kosong; apakah root menunjuk ke folder db/?", s.province)
	}
	sort.Strings(kel)
	return append(append(prov, required...), kel...), nil
}

// Each memanggil fn untuk setiap baris milik provinsi, berurutan dari file provinsi
// sampai desa/kelurahan. Baris provinsi lain dilewati tanpa diurai lebih jauh.
func (s *DirSource) Each(ctx context.Context, fn func(ports.RawRegion) error) error {
	files, err := s.files()
	if err != nil {
		return err
	}
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src, err := fs.ReadFile(s.fsys, name)
		if err != nil {
			return err
		}
		err = ParseDump(src, boundariesTable, func(r Row) error {
			code := r["kode"].Text
			if code != s.province && !strings.HasPrefix(code, s.province+".") {
				return nil
			}
			return fn(ports.RawRegion{
				Code:   code,
				Name:   r["nama"].Text,
				Path:   []byte(r["path"].Text),
				Origin: filepath.ToSlash(name),
			})
		})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
