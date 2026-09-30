package riversnap

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

// ErrCacheWindow berarti file cache dibuat untuk periode acuan lain.
var ErrCacheWindow = errors.New("cache river-snap untuk periode acuan lain")

// cacheVersion naik bila format file cache berubah.
const cacheVersion = 1

// CachedCell adalah jawaban sumber untuk satu titik permintaan.
type CachedCell struct {
	// Cell adalah pusat sel GloFAS yang dijawab.
	Cell series.LatLon
	// Mean adalah rata-rata nilai berisi (NaN bila tidak ada).
	Mean float64
	// Valid adalah jumlah hari yang berisi.
	Valid int
}

// Cache menyimpan debit rata-rata per titik permintaan untuk satu periode
// acuan. Reanalisis tidak berubah, jadi menjalankan ulang river-snap (misal
// setelah titik diverifikasi manual) tidak memakai kuota dan hasilnya sama.
type Cache struct {
	Window Window
	cells  map[series.LatLon]CachedCell
}

// NewCache membuat cache kosong untuk w.
func NewCache(w Window) *Cache {
	return &Cache{Window: w, cells: map[series.LatLon]CachedCell{}}
}

// Get mengembalikan jawaban untuk titik permintaan p.
func (c *Cache) Get(p series.LatLon) (CachedCell, bool) {
	v, ok := c.cells[key(p)]
	return v, ok
}

// Put menyimpan jawaban untuk titik permintaan p.
func (c *Cache) Put(p series.LatLon, v CachedCell) { c.cells[key(p)] = v }

// Len adalah jumlah titik permintaan yang tersimpan.
func (c *Cache) Len() int { return len(c.cells) }

// key membulatkan titik permintaan ke 4 desimal seperti Station.Offsets.
func key(p series.LatLon) series.LatLon {
	return series.LatLon{Lat: round4(p.Lat), Lon: round4(p.Lon)}
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

type cacheFile struct {
	Version int          `json:"versi"`
	Model   string       `json:"model"`
	From    string       `json:"dari"`
	To      string       `json:"sampai"`
	Points  []cachePoint `json:"titik"`
}

type cachePoint struct {
	Lat     float64  `json:"lat"`
	Lon     float64  `json:"lon"`
	CellLat float64  `json:"sel_lat"`
	CellLon float64  `json:"sel_lon"`
	Mean    *float64 `json:"debit_rata2_m3s"`
	Valid   int      `json:"hari_berisi"`
}

// ReadCache membaca file cache untuk periode acuan w.
func ReadCache(r io.Reader, w Window) (*Cache, error) {
	var f cacheFile
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("cache river-snap: %w", err)
	}
	if f.Version != cacheVersion {
		return nil, fmt.Errorf("cache river-snap versi %d, ingin %d", f.Version, cacheVersion)
	}
	if f.Model != w.Model || f.From != date(w.From) || f.To != date(w.To) {
		return nil, fmt.Errorf("%w: file %s %s..%s, diminta %s", ErrCacheWindow, f.Model, f.From, f.To, w)
	}
	c := NewCache(w)
	for _, p := range f.Points {
		mean := math.NaN()
		if p.Mean != nil {
			mean = *p.Mean
		}
		cell := series.LatLon{Lat: p.CellLat, Lon: p.CellLon}
		if !series.InIndonesia(cell) || p.Valid < 0 || p.Valid > w.Days() || (p.Mean == nil) != (p.Valid == 0) || mean < 0 {
			return nil, fmt.Errorf("cache river-snap: titik %v,%v tidak valid", p.Lat, p.Lon)
		}
		c.Put(series.LatLon{Lat: p.Lat, Lon: p.Lon}, CachedCell{Cell: cell, Mean: mean, Valid: p.Valid})
	}
	return c, nil
}

// Write menulis cache dengan urutan tetap, jadi isi file hanya bergantung
// pada titik yang tersimpan.
func (c *Cache) Write(w io.Writer) error {
	f := cacheFile{Version: cacheVersion, Model: c.Window.Model, From: date(c.Window.From), To: date(c.Window.To)}
	for p, v := range c.cells {
		cp := cachePoint{Lat: p.Lat, Lon: p.Lon, CellLat: v.Cell.Lat, CellLon: v.Cell.Lon, Valid: v.Valid}
		if !math.IsNaN(v.Mean) {
			m := math.Round(v.Mean*1e4) / 1e4
			cp.Mean = &m
		}
		f.Points = append(f.Points, cp)
	}
	slices.SortFunc(f.Points, func(a, b cachePoint) int {
		return cmp.Or(cmp.Compare(a.Lat, b.Lat), cmp.Compare(a.Lon, b.Lon))
	})
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(f)
}
