// Package region berisi aturan murni tentang wilayah administrasi Kemendagri:
// format kode, level, jenis wilayah, dan geometri batas. Tanpa I/O.
package region

import (
	"errors"
	"fmt"
	"strings"
)

// Level adalah tingkat administrasi: 1 provinsi, 2 kab/kota, 3 kecamatan, 4 desa/kelurahan.
type Level int

// Level yang dikenal.
const (
	LevelProvinsi Level = iota + 1
	LevelKabKota
	LevelKecamatan
	LevelDesaKelurahan
)

// Kind adalah jenis wilayah. Nilainya sama persis dengan CHECK constraint di ref.region.
type Kind string

// Jenis wilayah yang dikenal.
const (
	KindProvinsi  Kind = "provinsi"
	KindKabupaten Kind = "kabupaten"
	KindKota      Kind = "kota"
	KindKecamatan Kind = "kecamatan"
	KindKelurahan Kind = "kelurahan"
	KindDesa      Kind = "desa"
)

// ErrInvalidCode menandai kode wilayah yang tidak sesuai format Kemendagri.
var ErrInvalidCode = errors.New("kode wilayah tidak valid")

// segmentWidths adalah jumlah digit tiap segmen kode: 32.73.02.1003.
var segmentWidths = [...]int{2, 2, 2, 4}

// Code adalah kode wilayah Kemendagri yang sudah tervalidasi, misalnya "32.73.02.1003".
// Nilai nol tidak valid; buat Code hanya lewat ParseCode. Code bisa dibandingkan
// dengan == dan dipakai sebagai kunci map.
type Code struct {
	raw string
}

// ParseCode memvalidasi dan mengembalikan Code.
func ParseCode(s string) (Code, error) {
	segs := strings.Split(s, ".")
	if len(segs) > len(segmentWidths) {
		return Code{}, fmt.Errorf("%w: %q punya lebih dari 4 segmen", ErrInvalidCode, s)
	}
	for i, seg := range segs {
		if len(seg) != segmentWidths[i] || !allDigits(seg) {
			return Code{}, fmt.Errorf("%w: segmen %d dari %q harus %d digit", ErrInvalidCode, i+1, s, segmentWidths[i])
		}
	}
	// Level 4: digit pertama menentukan kelurahan (1) atau desa (2).
	if len(segs) == 4 && segs[3][0] != '1' && segs[3][0] != '2' {
		return Code{}, fmt.Errorf("%w: segmen desa/kelurahan %q harus diawali 1 atau 2", ErrInvalidCode, segs[3])
	}
	return Code{raw: s}, nil
}

// MustParseCode seperti ParseCode tapi panik bila gagal. Hanya untuk konstanta dan test.
func MustParseCode(s string) Code {
	c, err := ParseCode(s)
	if err != nil {
		panic(err)
	}
	return c
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String mengembalikan kode dalam format bertitik.
func (c Code) String() string { return c.raw }

// IsZero melaporkan apakah c adalah nilai nol (belum di-parse).
func (c Code) IsZero() bool { return c.raw == "" }

func (c Code) segments() []string {
	if c.IsZero() {
		return nil
	}
	return strings.Split(c.raw, ".")
}

// Level mengembalikan tingkat administrasi kode; 0 untuk nilai nol.
func (c Code) Level() Level { return Level(len(c.segments())) }

// Parent mengembalikan kode induk; ok bernilai false untuk provinsi dan nilai nol.
func (c Code) Parent() (parent Code, ok bool) {
	i := strings.LastIndexByte(c.raw, '.')
	if i < 0 {
		return Code{}, false
	}
	return Code{raw: c.raw[:i]}, true
}

// Province mengembalikan dua digit kode provinsi.
func (c Code) Province() string {
	if c.IsZero() {
		return ""
	}
	return c.raw[:2]
}

// Kind menurunkan jenis wilayah dari kode, sesuai konvensi Kemendagri:
// kab/kota dengan nomor >= 71 adalah kota; desa/kelurahan berawalan 1 adalah kelurahan.
func (c Code) Kind() Kind {
	segs := c.segments()
	switch Level(len(segs)) {
	case LevelProvinsi:
		return KindProvinsi
	case LevelKabKota:
		if segs[1] >= "71" {
			return KindKota
		}
		return KindKabupaten
	case LevelKecamatan:
		return KindKecamatan
	case LevelDesaKelurahan:
		if segs[3][0] == '1' {
			return KindKelurahan
		}
		return KindDesa
	default:
		return ""
	}
}
