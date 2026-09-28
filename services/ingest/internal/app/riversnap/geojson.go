package riversnap

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/riversnap"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

// Warna peta pemeriksaan (simplestyle-spec, dibaca geojson.io dan uMap).
const (
	colorChosen   = "#d7191c" // sel terpilih
	colorApprox   = "#2c7bb6" // koordinat perkiraan
	colorMainStem = "#1a9641" // sel alur utama (>= MainStemFraction puncak)
	colorOther    = "#a6a6a6" // sel lain di radius
)

type feature struct {
	Type       string         `json:"type"`
	Geometry   geometry       `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type geometry struct {
	Type        string `json:"type"`
	Coordinates any    `json:"coordinates"`
}

// WriteGeoJSON menulis peta pemeriksaan titik sungai: untuk setiap titik,
// kotak sel GloFAS kandidat di dalam radius (warna menurut debit relatif
// terhadap sel terbesar), sel terpilih, koordinat perkiraan, dan garis
// pergeserannya. Dibuka di geojson.io di atas peta OSM untuk memeriksa
// apakah sel terpilih benar berada di alur sungai yang dimaksud.
func WriteGeoJSON(w io.Writer, choices []riversnap.Choice) error {
	var feats []feature
	for _, c := range choices {
		flags := make([]string, len(c.Flags))
		for i, f := range c.Flags {
			flags[i] = string(f)
		}
		base := func(kind string) map[string]any {
			return map[string]any{
				"jenis": kind, "titik": c.ID, "nama": c.Name, "sungai": c.River,
				"perlu_cek": c.Suspect(), "tanda": strings.Join(flags, " "),
			}
		}
		peak := 0.0
		for _, k := range c.Candidates {
			peak = math.Max(peak, k.Mean)
		}
		drawn := map[series.LatLon]bool{}
		for _, k := range c.Candidates {
			// Sumber bisa menjawab sel yang sama untuk dua titik permintaan.
			if drawn[k.Cell] {
				continue
			}
			drawn[k.Cell] = true
			p := base("kandidat")
			frac := 0.0
			if peak > 0 {
				frac = k.Mean / peak
			}
			color := colorOther
			if frac >= riversnap.MainStemFraction {
				color = colorMainStem
			}
			chosen := k.Cell == c.Cell
			p["sel"] = cellLabel(k.Cell)
			p["debit_rata2_m3s"] = round2(k.Mean)
			p["persen_puncak"] = math.Round(frac * 100)
			p["terpilih"] = chosen
			p["fill"], p["fill-opacity"] = color, 0.15+0.45*frac
			p["stroke"], p["stroke-width"] = color, 1
			if chosen {
				p["stroke"], p["stroke-width"] = colorChosen, 3
			}
			feats = append(feats, feature{Type: "Feature", Geometry: square(k.Cell), Properties: p})
		}
		p := base("perkiraan")
		p["lat"], p["lon"], p["radius_derajat"] = c.Approx.Lat, c.Approx.Lon, c.Radius
		p["marker-color"], p["marker-symbol"] = colorApprox, "water"
		feats = append(feats, feature{Type: "Feature", Geometry: point(c.Approx), Properties: p})
		p = base("geser")
		p["sel_terpilih"] = cellLabel(c.Cell)
		p["debit_rata2_m3s"] = round2(c.Mean)
		p["geser_km"] = math.Round(c.DistanceKm*10) / 10
		p["stroke"], p["stroke-width"] = colorChosen, 2
		feats = append(feats, feature{Type: "Feature", Properties: p, Geometry: geometry{
			Type: "LineString", Coordinates: [][2]float64{lonLat(c.Approx), lonLat(c.Cell)},
		}})
	}
	// Satu fitur per baris: berkas tetap kecil tetapi mudah dibandingkan.
	if _, err := io.WriteString(w, "{\"type\":\"FeatureCollection\",\"features\":[\n"); err != nil {
		return err
	}
	for i, f := range feats {
		b, err := json.Marshal(f)
		if err != nil {
			return fmt.Errorf("fitur %d: %w", i, err)
		}
		sep := ",\n"
		if i == len(feats)-1 {
			sep = "\n"
		}
		if _, err := io.WriteString(w, string(b)+sep); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "]}\n")
	return err
}

func lonLat(p series.LatLon) [2]float64 { return [2]float64{p.Lon, p.Lat} }

func point(p series.LatLon) geometry { return geometry{Type: "Point", Coordinates: lonLat(p)} }

// square adalah kotak sel selebar CellStep dengan pusat c.
func square(c series.LatLon) geometry {
	h := riversnap.CellStep / 2
	r := func(v float64) float64 { return math.Round(v*1e6) / 1e6 }
	ring := [][2]float64{
		{r(c.Lon - h), r(c.Lat - h)},
		{r(c.Lon + h), r(c.Lat - h)},
		{r(c.Lon + h), r(c.Lat + h)},
		{r(c.Lon - h), r(c.Lat + h)},
		{r(c.Lon - h), r(c.Lat - h)},
	}
	return geometry{Type: "Polygon", Coordinates: [][][2]float64{ring}}
}

func cellLabel(c series.LatLon) string { return coord(c.Lat) + ", " + coord(c.Lon) }

func round2(v float64) float64 { return math.Round(v*100) / 100 }
