package region

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Position adalah satu titik dalam urutan GeoJSON: [bujur, lintang].
type Position [2]float64

// Ring adalah cincin tertutup (titik pertama sama dengan titik terakhir).
type Ring []Position

// Polygon adalah cincin luar diikuti nol atau lebih lubang.
type Polygon []Ring

// MultiPolygon adalah geometri batas wilayah. Semua batas disimpan sebagai
// MultiPolygon supaya pulau-pulau kecil milik satu wilayah tidak hilang.
type MultiPolygon []Polygon

// ErrInvalidGeometry menandai batas yang tidak bisa dipakai sama sekali.
var ErrInvalidGeometry = errors.New("geometri batas tidak valid")

// minRingPositions adalah jumlah titik minimum cincin tertutup (segitiga + titik penutup).
const minRingPositions = 4

// GeometryRepair mencatat perbaikan ringan yang dilakukan saat konversi,
// supaya importer bisa melaporkannya alih-alih memperbaiki diam-diam.
type GeometryRepair struct {
	ClosedRings    int // cincin yang titik akhirnya ditambahkan agar tertutup
	DroppedRings   int // lubang yang dibuang karena kurang dari 4 titik
	DroppedPolygon int // poligon yang dibuang karena cincin luarnya rusak
}

// Any melaporkan apakah ada perbaikan.
func (r GeometryRepair) Any() bool {
	return r.ClosedRings+r.DroppedRings+r.DroppedPolygon > 0
}

// ParseLatLngPath mengonversi kolom `path` dari dataset cahyadsn/wilayah_boundaries
// menjadi MultiPolygon GeoJSON. Dataset itu menyimpan titik sebagai [lintang, bujur]
// dan bisa berupa satu poligon (kedalaman 3) atau multipoligon (kedalaman 4).
func ParseLatLngPath(raw []byte) (MultiPolygon, GeometryRepair, error) {
	var repair GeometryRepair

	var probe []json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, repair, fmt.Errorf("%w: bukan array JSON: %w", ErrInvalidGeometry, err)
	}

	var polygons [][][][2]float64
	switch depth := arrayDepth(raw); depth {
	case 3:
		var poly [][][2]float64
		if err := json.Unmarshal(raw, &poly); err != nil {
			return nil, repair, fmt.Errorf("%w: %w", ErrInvalidGeometry, err)
		}
		polygons = [][][][2]float64{poly}
	case 4:
		if err := json.Unmarshal(raw, &polygons); err != nil {
			return nil, repair, fmt.Errorf("%w: %w", ErrInvalidGeometry, err)
		}
	default:
		return nil, repair, fmt.Errorf("%w: kedalaman array %d tidak didukung", ErrInvalidGeometry, depth)
	}

	out := make(MultiPolygon, 0, len(polygons))
	for _, poly := range polygons {
		converted, ok := convertPolygon(poly, &repair)
		if ok {
			out = append(out, converted)
		}
	}
	if len(out) == 0 {
		return nil, repair, fmt.Errorf("%w: tidak ada poligon yang tersisa", ErrInvalidGeometry)
	}
	return out, repair, nil
}

func convertPolygon(rings [][][2]float64, repair *GeometryRepair) (Polygon, bool) {
	if len(rings) == 0 {
		// Poligon kosong (`[]` di dalam multipoligon) tidak punya cincin luar.
		repair.DroppedPolygon++
		return nil, false
	}
	out := make(Polygon, 0, len(rings))
	for i, ring := range rings {
		converted, closed, ok := convertRing(ring)
		if !ok {
			if i == 0 {
				repair.DroppedPolygon++
				return nil, false
			}
			repair.DroppedRings++
			continue
		}
		if closed {
			repair.ClosedRings++
		}
		out = append(out, converted)
	}
	return out, true
}

// convertRing menukar [lat, lng] menjadi [lng, lat], memvalidasi rentang koordinat,
// dan menutup cincin bila perlu.
func convertRing(ring [][2]float64) (out Ring, closed, ok bool) {
	out = make(Ring, 0, len(ring)+1)
	for _, pt := range ring {
		lat, lng := pt[0], pt[1]
		if !validCoordinate(lat, lng) {
			return nil, false, false
		}
		out = append(out, Position{lng, lat})
	}
	if len(out) > 0 && out[0] != out[len(out)-1] {
		out = append(out, out[0])
		closed = true
	}
	if len(out) < minRingPositions {
		return nil, false, false
	}
	return out, closed, true
}

func validCoordinate(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

// arrayDepth menghitung kedalaman array JSON dengan mengikuti elemen pertama.
func arrayDepth(raw []byte) int {
	depth := 0
	for _, b := range raw {
		switch b {
		case '[':
			depth++
		case ' ', '\t', '\n', '\r':
		default:
			return depth
		}
	}
	return depth
}

// MarshalGeoJSON menghasilkan objek geometri GeoJSON MultiPolygon.
func (m MultiPolygon) MarshalGeoJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        string       `json:"type"`
		Coordinates MultiPolygon `json:"coordinates"`
	}{Type: "MultiPolygon", Coordinates: m})
}

// BBox mengembalikan kotak pembatas [minLng, minLat, maxLng, maxLat].
func (m MultiPolygon) BBox() [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, poly := range m {
		for _, ring := range poly {
			for _, p := range ring {
				b[0] = math.Min(b[0], p[0])
				b[1] = math.Min(b[1], p[1])
				b[2] = math.Max(b[2], p[0])
				b[3] = math.Max(b[3], p[1])
			}
		}
	}
	return b
}
