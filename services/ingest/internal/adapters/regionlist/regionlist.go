// Package regionlist menyediakan daftar kode kelurahan/desa (adm4) per
// provinsi untuk sapuan prakiraan. Daftar dibuat `make adm4-list` dari data
// batas wilayah yang sama dengan ref.region milik geo-processor, lalu
// disematkan ke binary, jadi ingest tidak perlu membaca schema layanan lain.
package regionlist

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
)

//go:embed data/adm4_*.txt
var data embed.FS

// ErrUnknownProvince berarti belum ada daftar untuk provinsi itu.
var ErrUnknownProvince = errors.New("daftar kelurahan/desa provinsi tidak ada")

var countLine = regexp.MustCompile(`^#\s*([0-9]+) kode\s*$`)

// Provinces mengembalikan kode provinsi yang daftarnya tersedia.
func Provinces() []string {
	names, _ := fs.Glob(data, "data/adm4_*.txt")
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(n, "data/adm4_"), ".txt"))
	}
	return out
}

// Load mengembalikan kode adm4 satu provinsi, urut seperti di file.
func Load(province string) ([]string, error) {
	b, err := data.ReadFile("data/adm4_" + province + ".txt")
	if err != nil {
		return nil, fmt.Errorf("%w: %q (tersedia: %v)", ErrUnknownProvince, province, Provinces())
	}
	return Parse(b, province)
}

// Parse membaca daftar: satu kode per baris, baris kosong dan komentar (#)
// dilewati. Baris komentar "# N kode" wajib ada dan harus cocok dengan jumlah
// kode, supaya file yang terpotong ketahuan.
func Parse(b []byte, province string) ([]string, error) {
	var codes []string
	seen := map[string]bool{}
	want := -1
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if m := countLine.FindStringSubmatch(line); m != nil {
			want, _ = strconv.Atoi(m[1])
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case !forecast.ValidCode(line):
			return nil, fmt.Errorf("baris %d: %q bukan kode adm4", n, line)
		case !strings.HasPrefix(line, province+"."):
			return nil, fmt.Errorf("baris %d: %s bukan milik provinsi %s", n, line, province)
		case seen[line]:
			return nil, fmt.Errorf("baris %d: kode %s ganda", n, line)
		}
		seen[line] = true
		codes = append(codes, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if want != len(codes) {
		return nil, fmt.Errorf("daftar provinsi %s berisi %d kode, header menyebut %d", province, len(codes), want)
	}
	return codes, nil
}
