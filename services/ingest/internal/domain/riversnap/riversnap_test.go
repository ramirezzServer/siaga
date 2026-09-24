package riversnap

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

func st(id, river string, lat, lon, radius float64) Station {
	return Station{ID: id, River: river, Name: id, Approx: series.LatLon{Lat: lat, Lon: lon}, Radius: radius}
}

func cand(lat, lon, mean float64) Candidate {
	return Candidate{Cell: series.LatLon{Lat: lat, Lon: lon}, Mean: mean}
}

func TestOffsets(t *testing.T) {
	s := st("a", "A", -6.987, 107.625, 0.1)
	pts := s.Offsets()
	if len(pts) != 25 || pts[0] != (series.LatLon{Lat: -7.087, Lon: 107.525}) || pts[12] != s.Approx {
		t.Fatalf("%d titik, pertama %v, tengah %v", len(pts), pts[0], pts[12])
	}
	if pts := st("b", "B", -7, 107, 0).Offsets(); len(pts) != 1 {
		t.Fatalf("radius 0: %d titik", len(pts))
	}
}

func TestChooseNearestMainStem(t *testing.T) {
	s := st("dayeuhkolot", "Citarum", -6.987, 107.625, 0.1)
	cands := []Candidate{
		cand(-6.975, 107.625, 3.37), // sel sendiri, alur utama
		cand(-6.975, 107.525, 7.81), // hilir, debit lebih besar tetapi lebih jauh
		cand(-6.925, 107.625, 0.60), // anak sungai kecil di dekatnya
		cand(-7.025, 107.575, 0.05),
		cand(-5.0, 107.625, 999), // di luar radius
		cand(-6.975, 107.675, math.NaN()),
	}
	ch, err := Choose(s, cands)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Cell != (series.LatLon{Lat: -6.975, Lon: 107.625}) || ch.Mean != 3.37 || len(ch.Flags) != 0 || ch.Suspect() {
		t.Fatalf("%+v", ch)
	}
	if ch.DistanceKm < 1 || ch.DistanceKm > 1.6 {
		t.Fatalf("jarak %v km", ch.DistanceKm)
	}
}

func TestChooseFlags(t *testing.T) {
	s := st("x", "X", -7, 107, 0.1)
	ch, err := Choose(s, []Candidate{cand(-7.1, 107.1, 0.3), cand(-7.0, 107.0, 0.01)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ch.Flags, []Flag{FlagEdge, FlagSmall}) || !ch.Suspect() {
		t.Fatalf("%v", ch.Flags)
	}
	pinned := st("y", "Y", -7, 107, 0)
	ch, err = Choose(pinned, []Candidate{cand(-7.0, 107.0, 5)})
	if err != nil || !slices.Equal(ch.Flags, []Flag{FlagPinned}) || ch.Suspect() {
		t.Fatalf("%v %v", ch.Flags, err)
	}
	// Seri jarak dan debit dipecah dengan koordinat: hasil tidak bergantung urutan.
	a, _ := Choose(s, []Candidate{cand(-7.05, 107.0, 2), cand(-6.95, 107.0, 2)})
	b, _ := Choose(s, []Candidate{cand(-6.95, 107.0, 2), cand(-7.05, 107.0, 2)})
	if a.Cell != b.Cell {
		t.Fatalf("%v != %v", a.Cell, b.Cell)
	}
}

func TestChooseErrors(t *testing.T) {
	if _, err := Choose(st("x", "X", -7, 107, 0.1), []Candidate{cand(-8, 107, 5)}); !errors.Is(err, ErrNoCandidate) {
		t.Fatal(err)
	}
	for _, s := range []Station{st("x", "X", 50, 107, 0.1), st("x", "X", -7, 107, 0.5), st("x", "X", -7, 107, -1)} {
		if _, err := Choose(s, nil); err == nil || errors.Is(err, ErrNoCandidate) {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestReview(t *testing.T) {
	in := []Choice{
		{Station: st("a", "Citarum", 0, 0, 0.1), Cell: series.LatLon{Lat: -7, Lon: 107}, Mean: 5},
		{Station: st("b", "Citarum", 0, 0, 0.1), Cell: series.LatLon{Lat: -6.9, Lon: 107}, Mean: 3},
		{Station: st("c", "Cimanuk", 0, 0, 0.1), Cell: series.LatLon{Lat: -7, Lon: 107}, Mean: 1},
	}
	out := Review(in)
	if !slices.Equal(out[0].Flags, []Flag{FlagShared}) || !slices.Equal(out[1].Flags, []Flag{FlagDownstream}) ||
		!slices.Equal(out[2].Flags, []Flag{FlagShared}) || len(in[0].Flags) != 0 {
		t.Fatalf("%v %v %v", out[0].Flags, out[1].Flags, out[2].Flags)
	}
}
