package region

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidName menandai nama wilayah kosong.
var ErrInvalidName = errors.New("nama wilayah tidak valid")

// Region adalah satu wilayah administrasi beserta batasnya.
type Region struct {
	Code     Code
	Name     string
	Boundary MultiPolygon
}

// New membangun Region yang sudah memenuhi semua invarian domain.
// Nama dirapikan (spasi ganda dan spasi tepi dibuang).
func New(code Code, name string, boundary MultiPolygon) (Region, error) {
	if code.IsZero() {
		return Region{}, fmt.Errorf("%w: kode kosong", ErrInvalidCode)
	}
	clean := strings.Join(strings.Fields(name), " ")
	if clean == "" {
		return Region{}, fmt.Errorf("%w: nama kosong untuk %s", ErrInvalidName, code)
	}
	if len(boundary) == 0 {
		return Region{}, fmt.Errorf("%w: batas kosong untuk %s", ErrInvalidGeometry, code)
	}
	return Region{Code: code, Name: clean, Boundary: boundary}, nil
}

// Kind adalah jenis wilayah turunan dari kode.
func (r Region) Kind() Kind { return r.Code.Kind() }
