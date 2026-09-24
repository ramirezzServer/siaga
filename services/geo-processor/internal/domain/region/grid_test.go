package region

import (
	"errors"
	"slices"
	"testing"
)

func TestGridNodes(t *testing.T) {
	// Dua desa: satu di sel (-7,00; 107,50), satu melintasi batas sel ke (-6,75; 107,50).
	a := MultiPolygon{{Ring{{107.45, -7.02}, {107.55, -7.02}, {107.55, -6.98}, {107.45, -7.02}}}}
	b := MultiPolygon{{Ring{{107.50, -6.90}, {107.52, -6.86}, {107.51, -6.85}, {107.50, -6.90}}}}
	nodes, err := GridNodes([]MultiPolygon{b, a}, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	got := make([][2]float64, len(nodes))
	for i, n := range nodes {
		got[i] = [2]float64{n.Lat(), n.Lon()}
	}
	want := [][2]float64{{-7.0, 107.5}, {-6.75, 107.5}}
	if !slices.Equal(got, want) {
		t.Fatalf("%v, ingin %v", got, want)
	}
	if nodes, _ := GridNodes(nil, 0.25); len(nodes) != 0 {
		t.Fatalf("tanpa wilayah: %v", nodes)
	}
	for _, step := range []float64{0, -1, 0.001, 10} {
		if _, err := GridNodes([]MultiPolygon{a}, step); !errors.Is(err, ErrInvalidGridStep) {
			t.Errorf("step %v: %v", step, err)
		}
	}
}
