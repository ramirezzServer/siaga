package region

import (
	"cmp"
	"errors"
	"math"
	"slices"
)

// ErrInvalidGridStep menandai jarak grid yang tidak bisa dipakai.
var ErrInvalidGridStep = errors.New("jarak grid tidak valid")

// GridNode adalah simpul grid lintang/bujur berjarak tetap, disimpan sebagai
// indeks bilangan bulat supaya bebas galat pembulatan: Lat = LatIndex*Step.
type GridNode struct {
	LatIndex, LonIndex int
	Step               float64
}

// Lat mengembalikan lintang simpul.
func (n GridNode) Lat() float64 { return float64(n.LatIndex) * n.Step }

// Lon mengembalikan bujur simpul.
func (n GridNode) Lon() float64 { return float64(n.LonIndex) * n.Step }

// GridNodes mengembalikan simpul grid berjarak step derajat yang selnya
// (simpul ± step/2) memuat paling sedikit satu titik batas wilayah. Untuk
// batas kelurahan/desa (±1–10 km) di grid 0,25° (±28 km) ini sama dengan
// "sel yang menyentuh daratan wilayah": tidak ada sel darat yang lolos tanpa
// satu pun titik batas di dalamnya. Hasil urut lintang lalu bujur, tanpa ganda.
func GridNodes(boundaries []MultiPolygon, step float64) ([]GridNode, error) {
	if !(step >= 0.01 && step <= 5) {
		return nil, ErrInvalidGridStep
	}
	seen := map[[2]int]bool{}
	for _, mp := range boundaries {
		for _, poly := range mp {
			for _, ring := range poly {
				for _, p := range ring {
					seen[[2]int{int(math.Round(p[1] / step)), int(math.Round(p[0] / step))}] = true
				}
			}
		}
	}
	out := make([]GridNode, 0, len(seen))
	for k := range seen {
		out = append(out, GridNode{LatIndex: k[0], LonIndex: k[1], Step: step})
	}
	slices.SortFunc(out, func(a, b GridNode) int {
		return cmp.Or(cmp.Compare(a.LatIndex, b.LatIndex), cmp.Compare(a.LonIndex, b.LonIndex))
	})
	return out, nil
}
