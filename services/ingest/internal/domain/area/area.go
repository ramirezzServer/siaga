// Package area berisi kotak wilayah (bounding box) yang dipakai konektor
// berbasis wilayah (FIRMS, OpenAQ).
package area

import (
	"fmt"
	"strconv"
	"strings"
)

// Box adalah kotak wilayah dalam derajat WGS84.
type Box struct {
	West, South, East, North float64
}

// JawaBarat menutup seluruh desa provinsi 32 (batas 106,37–108,85 BT,
// 7,82–5,81 LS) dengan sedikit kelonggaran.
var JawaBarat = Box{West: 106.3, South: -7.9, East: 108.9, North: -5.7}

// ParseBox membaca "barat,selatan,timur,utara".
func ParseBox(s string) (Box, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return Box{}, fmt.Errorf("kotak %q harus berisi 4 angka barat,selatan,timur,utara", s)
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return Box{}, fmt.Errorf("kotak %q: %w", s, err)
		}
		v[i] = f
	}
	b := Box{West: v[0], South: v[1], East: v[2], North: v[3]}
	return b, b.Validate()
}

// Validate memastikan kotak berada di dalam Indonesia dan tidak terbalik.
func (b Box) Validate() error {
	if !(b.West >= 94 && b.East <= 142 && b.South >= -12 && b.North <= 7 && b.West < b.East && b.South < b.North) {
		return fmt.Errorf("kotak %s harus di dalam Indonesia dengan barat < timur dan selatan < utara", b)
	}
	return nil
}

// String menulis "barat,selatan,timur,utara" tanpa nol berlebih.
func (b Box) String() string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	return f(b.West) + "," + f(b.South) + "," + f(b.East) + "," + f(b.North)
}
