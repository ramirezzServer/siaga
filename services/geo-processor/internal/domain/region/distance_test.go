package region

import (
	"math"
	"testing"
)

func square(lon0, lat0, lon1, lat1 float64) Ring {
	return Ring{{lon0, lat0}, {lon1, lat0}, {lon1, lat1}, {lon0, lat1}, {lon0, lat0}}
}

func TestHaversine(t *testing.T) {
	// Satu derajat bujur di khatulistiwa ≈ 111,19 km.
	if d := HaversineKm(0, 107, 0, 108); math.Abs(d-111.19) > 0.01 {
		t.Fatal(d)
	}
	if d := HaversineKm(-6.9, 107.6, -6.9, 107.6); d != 0 {
		t.Fatal(d)
	}
}

func TestContainsAndDistance(t *testing.T) {
	// Kotak 1°×1° dengan lubang 0,2° di tengah, plus pulau terpisah.
	m := MultiPolygon{
		{square(107, -7, 108, -6), square(107.4, -6.6, 107.6, -6.4)},
		{square(110, -7, 110.1, -6.9)},
	}
	if !m.Contains(-6.8, 107.2) || m.Contains(-6.5, 107.5) || !m.Contains(-6.95, 110.05) || m.Contains(-5, 107.5) {
		t.Fatal("uji titik di dalam salah")
	}
	if d := m.DistanceKm(-6.8, 107.2); d != 0 {
		t.Fatalf("di dalam: %v", d)
	}
	// Titik di lubang: jarak ke tepi lubang (0,1° ≈ 11 km).
	if d := m.DistanceKm(-6.5, 107.5); math.Abs(d-11.04) > 0.1 {
		t.Fatalf("lubang: %v", d)
	}
	// 1° di timur tepi barat pulau? Tidak: titik 108,5 di antara kotak dan pulau,
	// terdekat ke tepi timur kotak (0,5°).
	want := HaversineKm(-6.5, 108.5, -6.5, 108)
	if d := m.DistanceKm(-6.5, 108.5); math.Abs(d-want) > 0.01 {
		t.Fatalf("luar: %v ingin %v", d, want)
	}
	// Terdekat ke sudut.
	want = HaversineKm(-5.5, 108.5, -6, 108)
	if d := m.DistanceKm(-5.5, 108.5); math.Abs(d-want) > 0.01 {
		t.Fatalf("sudut: %v ingin %v", d, want)
	}
	if d := (MultiPolygon{}).DistanceKm(0, 0); !math.IsInf(d, 1) {
		t.Fatalf("kosong: %v", d)
	}
}

// Properti: jarak tidak negatif, nol tepat bila titik di dalam, dan tidak
// lebih besar dari jarak ke titik sudut mana pun.
func FuzzDistanceKm(f *testing.F) {
	f.Add(-6.5, 108.5, 107.0, -7.0, 1.0)
	f.Add(-6.8, 107.2, 107.0, -7.0, 0.3)
	f.Fuzz(func(t *testing.T, lat, lon, lon0, lat0, size float64) {
		if !(lat >= -30 && lat <= 30 && lon >= 80 && lon <= 150 && lon0 >= 80 && lon0 <= 150 && lat0 >= -30 && lat0 <= 30 && size > 0.001 && size <= 5) {
			t.Skip()
		}
		ring := square(lon0, lat0, lon0+size, lat0+size)
		m := MultiPolygon{{ring}}
		d := m.DistanceKm(lat, lon)
		if d < 0 || math.IsNaN(d) {
			t.Fatalf("jarak %v", d)
		}
		if (d == 0) != m.Contains(lat, lon) && d > 1e-6 {
			t.Fatalf("di dalam=%v jarak %v", m.Contains(lat, lon), d)
		}
		for _, p := range ring {
			if v := HaversineKm(lat, lon, p[1], p[0]); d > v+1e-6 {
				t.Fatalf("jarak %v > sudut %v", d, v)
			}
		}
	})
}
