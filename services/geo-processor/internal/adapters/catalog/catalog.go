// Package catalog membaca katalog gempa historis untuk kalibrasi:
// RepoGempa BMKG (versi CSV lama dan TSV v2 dari kekavigi/repo-gempa) dan
// CSV layanan FDSN USGS (earthquake.usgs.gov/fdsnws/event/1/query?format=csv).
package catalog

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// ErrFormat berarti header file tidak dikenali.
var ErrFormat = errors.New("format katalog tidak dikenali")

// bom adalah byte order mark UTF-8 yang kadang ada di awal file CSV.
const bom = "\xef\xbb\xbf"

type columns map[string]int

func header(rec []string) columns {
	c := columns{}
	for i, name := range rec {
		c[strings.TrimSpace(strings.TrimPrefix(name, bom))] = i
	}
	return c
}

func (c columns) has(names ...string) bool {
	for _, n := range names {
		if _, ok := c[n]; !ok {
			return false
		}
	}
	return true
}

func (c columns) get(rec []string, name string) string {
	if i, ok := c[name]; ok && i < len(rec) {
		return strings.TrimSpace(rec[i])
	}
	return ""
}

func floats(rec []string, c columns, names ...string) ([]float64, error) {
	out := make([]float64, len(names))
	for i, n := range names {
		v, err := strconv.ParseFloat(c.get(rec, n), 64)
		if err != nil {
			return nil, fmt.Errorf("kolom %s: %w", n, err)
		}
		out[i] = v
	}
	return out, nil
}

// ReadBMKG membaca katalog RepoGempa: CSV lama (tgl, ot, lat, lon, depth, mag)
// atau TSV v2 (eventID, datetime, latitude, longitude, magnitude, ...). Waktu
// katalog sudah UTC. Baris yang tidak lengkap dilewati dan dihitung.
func ReadBMKG(r io.Reader) (events []ports.CatalogEvent, skipped int, err error) {
	br := newSniffer(r)
	sep := ','
	if br.hasTab() {
		sep = '\t'
	}
	cr := csv.NewReader(br)
	cr.Comma = sep
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("membaca header: %w", err)
	}
	c := header(head)
	var parse func(rec []string, line int) (ports.CatalogEvent, error)
	switch {
	case c.has("tgl", "ot", "lat", "lon", "mag"):
		parse = func(rec []string, line int) (ports.CatalogEvent, error) {
			t, err := time.Parse("2006/01/02 15:04:05.999999", c.get(rec, "tgl")+" "+c.get(rec, "ot"))
			if err != nil {
				return ports.CatalogEvent{}, err
			}
			v, err := floats(rec, c, "lat", "lon", "mag")
			if err != nil {
				return ports.CatalogEvent{}, err
			}
			return event(fmt.Sprintf("bmkg-v1-%d", line), t, v), nil
		}
	case c.has("eventID", "datetime", "latitude", "longitude", "magnitude"):
		parse = func(rec []string, _ int) (ports.CatalogEvent, error) {
			t, err := time.Parse("2006-01-02 15:04:05.999999-07:00", c.get(rec, "datetime"))
			if err != nil {
				return ports.CatalogEvent{}, err
			}
			v, err := floats(rec, c, "latitude", "longitude", "magnitude")
			if err != nil {
				return ports.CatalogEvent{}, err
			}
			return event(c.get(rec, "eventID"), t, v), nil
		}
	default:
		return nil, 0, fmt.Errorf("%w: header BMKG %v", ErrFormat, head)
	}
	return readAll(cr, parse)
}

// ReadUSGS membaca CSV FDSN USGS; hanya baris bertipe "earthquake".
func ReadUSGS(r io.Reader) (events []ports.CatalogEvent, skipped int, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("membaca header: %w", err)
	}
	c := header(head)
	if !c.has("time", "latitude", "longitude", "mag", "id") {
		return nil, 0, fmt.Errorf("%w: header USGS %v", ErrFormat, head)
	}
	parse := func(rec []string, _ int) (ports.CatalogEvent, error) {
		if typ := c.get(rec, "type"); typ != "" && typ != "earthquake" {
			return ports.CatalogEvent{}, errNotQuake
		}
		t, err := time.Parse(time.RFC3339Nano, c.get(rec, "time"))
		if err != nil {
			return ports.CatalogEvent{}, err
		}
		v, err := floats(rec, c, "latitude", "longitude", "mag")
		if err != nil {
			return ports.CatalogEvent{}, err
		}
		return event(c.get(rec, "id"), t, v), nil
	}
	return readAll(cr, parse)
}

var errNotQuake = errors.New("bukan gempa bumi")

func event(id string, t time.Time, v []float64) ports.CatalogEvent {
	return ports.CatalogEvent{ID: id, Origin: quake.Origin{OccurredAt: t.UTC(), Latitude: v[0], Longitude: v[1], Magnitude: v[2]}}
}

func readAll(cr *csv.Reader, parse func([]string, int) (ports.CatalogEvent, error)) ([]ports.CatalogEvent, int, error) {
	var out []ports.CatalogEvent
	skipped := 0
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, skipped, nil
		}
		if err != nil {
			return nil, skipped, fmt.Errorf("baris %d: %w", line, err)
		}
		e, err := parse(rec, line)
		if err != nil {
			skipped++
			continue
		}
		out = append(out, e)
	}
}
