package region_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

func TestParseLatLngPathPolygonSwapsAxes(t *testing.T) {
	t.Parallel()
	// Kedalaman 3: satu poligon, titik [lat, lng], cincin sudah tertutup.
	raw := []byte(`[[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7],[-6.9,107.6]]]`)
	mp, rep, err := region.ParseLatLngPath(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Any() {
		t.Errorf("tidak boleh ada perbaikan: %+v", rep)
	}
	if len(mp) != 1 || len(mp[0]) != 1 || len(mp[0][0]) != 4 {
		t.Fatalf("bentuk salah: %v", mp)
	}
	if got := mp[0][0][0]; got != (region.Position{107.6, -6.9}) {
		t.Errorf("titik pertama = %v; want [107.6 -6.9] (lng, lat)", got)
	}
}

func TestParseLatLngPathMultiPolygonClosesRings(t *testing.T) {
	t.Parallel()
	// Kedalaman 4: dua poligon; poligon kedua belum tertutup.
	raw := []byte(`[
	  [[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7],[-6.9,107.6]]],
	  [[[-7.0,107.0],[-7.0,107.1],[-6.95,107.1]]]
	]`)
	mp, rep, err := region.ParseLatLngPath(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(mp) != 2 || rep.ClosedRings != 1 {
		t.Fatalf("len=%d rep=%+v", len(mp), rep)
	}
	ring := mp[1][0]
	if ring[0] != ring[len(ring)-1] {
		t.Error("cincin kedua harus ditutup")
	}
}

func TestParseLatLngPathDropsDegenerateParts(t *testing.T) {
	t.Parallel()
	raw := []byte(`[
	  [[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7],[-6.9,107.6]], [[-6.85,107.65],[-6.86,107.66]]],
	  [[[-7.0,107.0],[-7.0,107.1]]]
	]`)
	mp, rep, err := region.ParseLatLngPath(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(mp) != 1 || len(mp[0]) != 1 || rep.DroppedRings != 1 || rep.DroppedPolygon != 1 {
		t.Errorf("mp=%v rep=%+v", mp, rep)
	}
}

func TestParseLatLngPathRejects(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"bukan json":      `[[[`,
		"objek":           `{"a":1}`,
		"kedalaman 2":     `[[-6.9,107.6]]`,
		"kedalaman 5":     `[[[[[-6.9,107.6]]]]]`,
		"lintang di luar": `[[[-96,107.6],[-6.9,107.7],[-6.8,107.7],[-96,107.6]]]`,
		"semua rusak":     `[[[-6.9,107.6],[-6.9,107.7]]]`,
		"kosong":          `[]`,
	} {
		if _, _, err := region.ParseLatLngPath([]byte(raw)); !errors.Is(err, region.ErrInvalidGeometry) {
			t.Errorf("%s: error = %v; want ErrInvalidGeometry", name, err)
		}
	}
}

func TestMarshalGeoJSONAndBBox(t *testing.T) {
	t.Parallel()
	mp, _, err := region.ParseLatLngPath([]byte(`[[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7],[-6.9,107.6]]]`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := mp.MarshalGeoJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj struct {
		Type        string          `json:"type"`
		Coordinates [][][][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(b, &obj); err != nil || obj.Type != "MultiPolygon" || len(obj.Coordinates) != 1 {
		t.Fatalf("GeoJSON salah: %s (%v)", b, err)
	}
	if got, want := mp.BBox(), [4]float64{107.6, -6.9, 107.7, -6.8}; got != want {
		t.Errorf("BBox = %v; want %v", got, want)
	}
}

// FuzzParseLatLngPath: untuk input apa pun, parser tidak boleh panik; bila berhasil,
// setiap cincin tertutup, punya minimal 4 titik, dan semua koordinat dalam rentang.
func FuzzParseLatLngPath(f *testing.F) {
	f.Add([]byte(`[[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7]]]`))
	f.Add([]byte(`[[[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7],[-6.9,107.6]]]]`))
	f.Add([]byte(`[[[1e400,0]]]`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		mp, _, err := region.ParseLatLngPath(raw)
		if err != nil {
			return
		}
		for _, poly := range mp {
			if len(poly) == 0 {
				t.Fatal("poligon tanpa cincin")
			}
			for _, ring := range poly {
				if len(ring) < 4 || ring[0] != ring[len(ring)-1] {
					t.Fatalf("cincin tidak valid: %v", ring)
				}
				for _, p := range ring {
					if p[0] < -180 || p[0] > 180 || p[1] < -90 || p[1] > 90 {
						t.Fatalf("koordinat di luar rentang: %v", p)
					}
				}
			}
		}
		if _, err := mp.MarshalGeoJSON(); err != nil {
			t.Fatal(fmt.Errorf("marshal: %w", err))
		}
	})
}
