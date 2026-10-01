package reanalysis

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

// ErrCacheSource berarti file cache dibuat untuk sumber atau periode lain.
var ErrCacheSource = errors.New("cache untuk sumber atau periode lain")

// cacheVersion naik bila format file cache berubah.
const cacheVersion = 1

// Cache menyimpan deret per sel untuk satu Source. Data historis tidak
// berubah, jadi jalan ulang (misal setelah metode ambang diubah) tidak
// memakai kuota dan hasilnya sama.
type Cache struct {
	Source Source
	cells  map[series.LatLon][]float64
}

// NewCache membuat cache kosong untuk src.
func NewCache(src Source) *Cache {
	return &Cache{Source: src, cells: map[series.LatLon][]float64{}}
}

// Get mengembalikan deret sel p (NaN untuk langkah kosong).
func (c *Cache) Get(p series.LatLon) ([]float64, bool) {
	v, ok := c.cells[Key(p)]
	return v, ok
}

// Put menyimpan deret sel p, dibulatkan seperti di file.
func (c *Cache) Put(p series.LatLon, vs []float64) {
	out := make([]float64, len(vs))
	for i, v := range vs {
		out[i] = round2(v)
	}
	c.cells[Key(p)] = out
}

// Len adalah jumlah sel yang tersimpan.
func (c *Cache) Len() int { return len(c.cells) }

// Key membulatkan koordinat sel ke 4 desimal, ketelitian daftar titik pantau.
func Key(p series.LatLon) series.LatLon {
	return series.LatLon{Lat: math.Round(p.Lat*1e4) / 1e4, Lon: math.Round(p.Lon*1e4) / 1e4}
}

// round2 membulatkan ke 2 desimal: ketelitian yang dijawab Open-Meteo untuk
// debit (m³/s) dan hujan (mm), jadi jalan pertama sama dengan jalan dari cache.
func round2(v float64) float64 {
	if math.IsNaN(v) {
		return v
	}
	return math.Round(v*100) / 100
}

type cacheFile struct {
	Version    int         `json:"versi"`
	Model      string      `json:"model"`
	Variable   string      `json:"variabel"`
	Unit       string      `json:"satuan"`
	Resolution string      `json:"langkah"`
	From       string      `json:"dari"`
	To         string      `json:"sampai"`
	Cells      []cacheCell `json:"sel"`
}

type cacheCell struct {
	Lat    float64    `json:"lat"`
	Lon    float64    `json:"lon"`
	Values []*float64 `json:"nilai"`
}

// ReadCache membaca file cache untuk src.
func ReadCache(r io.Reader, src Source) (*Cache, error) {
	var f cacheFile
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("cache %s: %w", src.Key(), err)
	}
	if f.Version != cacheVersion {
		return nil, fmt.Errorf("cache %s versi %d, ingin %d", src.Key(), f.Version, cacheVersion)
	}
	if f.Model != src.Model || f.Variable != src.Variable || f.Unit != src.Unit ||
		f.Resolution != string(src.Resolution) || f.From != Date(src.From) || f.To != Date(src.To) {
		return nil, fmt.Errorf("%w: file %s %s %s..%s, diminta %s", ErrCacheSource, f.Model, f.Variable, f.From, f.To, src)
	}
	c := NewCache(src)
	for _, cell := range f.Cells {
		p := series.LatLon{Lat: cell.Lat, Lon: cell.Lon}
		if !series.InIndonesia(p) || len(cell.Values) != src.Steps() {
			return nil, fmt.Errorf("cache %s: sel %v,%v tidak valid (%d nilai, ingin %d)", src.Key(), cell.Lat, cell.Lon, len(cell.Values), src.Steps())
		}
		vs := make([]float64, len(cell.Values))
		for i, v := range cell.Values {
			vs[i] = math.NaN()
			if v != nil {
				if *v < 0 || math.IsInf(*v, 0) {
					return nil, fmt.Errorf("cache %s: sel %v,%v nilai %v", src.Key(), cell.Lat, cell.Lon, *v)
				}
				vs[i] = *v
			}
		}
		c.Put(p, vs)
	}
	return c, nil
}

// Write menulis cache dengan urutan sel tetap.
func (c *Cache) Write(w io.Writer) error {
	f := cacheFile{
		Version: cacheVersion, Model: c.Source.Model, Variable: c.Source.Variable, Unit: c.Source.Unit,
		Resolution: string(c.Source.Resolution), From: Date(c.Source.From), To: Date(c.Source.To),
	}
	for p, vs := range c.cells {
		cell := cacheCell{Lat: p.Lat, Lon: p.Lon, Values: make([]*float64, len(vs))}
		for i, v := range vs {
			if !math.IsNaN(v) {
				cell.Values[i] = &v
			}
		}
		f.Cells = append(f.Cells, cell)
	}
	slices.SortFunc(f.Cells, func(a, b cacheCell) int {
		return cmp.Or(cmp.Compare(a.Lat, b.Lat), cmp.Compare(a.Lon, b.Lon))
	})
	return json.NewEncoder(w).Encode(f)
}
