package catalog

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/gazetteer"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Kolom bahan kalibrasi radius dirasakan; ditulis make felt-radius (ingest,
// cmd/felt-data) ke docs/calibration/dirasakan-dyfi.csv dan dirasakan-bmkg.csv.
var (
	dyfiColumns = []string{
		"usgs_id", "waktu_utc", "lintang", "bujur", "kedalaman_km", "magnitudo", "tipe_magnitudo",
		"laporan_kejadian", "sel", "cdi", "laporan", "jarak_km", "jenis_jarak", "lintang_sel", "bujur_sel",
		"mencurigakan", "kota",
	}
	bmkgFeltColumns = []string{"bmkg_id", "waktu_utc", "lintang", "bujur", "kedalaman_km", "magnitudo", "wilayah", "dirasakan"}
	aliasColumns    = []string{"nama", "kode", "alasan"}
)

// readRows membaca CSV berkomentar # dengan kolom wajib; setiap baris rusak
// adalah galat (bahan dibuat mesin atau tangan dengan hati-hati).
func readRows(r io.Reader, want []string, fn func(rec []string, c columns) error) error {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	head, err := cr.Read()
	if err != nil {
		return fmt.Errorf("membaca header: %w", err)
	}
	c := header(head)
	if !c.has(want...) {
		return fmt.Errorf("%w: header %v, butuh %v", ErrFormat, head, want)
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		line, _ := cr.FieldPos(0)
		if err := fn(rec, c); err != nil {
			return fmt.Errorf("baris %d: %w", line, err)
		}
	}
}

func feltEvent(rec []string, c columns, idCol string) (ports.FeltEvent, error) {
	v, err := floats(rec, c, "lintang", "bujur", "kedalaman_km", "magnitudo")
	if err != nil {
		return ports.FeltEvent{}, err
	}
	t, err := time.Parse(time.RFC3339, c.get(rec, "waktu_utc"))
	if err != nil {
		return ports.FeltEvent{}, fmt.Errorf("kolom waktu_utc: %w", err)
	}
	e := ports.FeltEvent{ID: c.get(rec, idCol), OccurredAt: t.UTC(), Latitude: v[0], Longitude: v[1], DepthKm: v[2], Magnitude: v[3]}
	switch {
	case e.ID == "":
		return e, fmt.Errorf("%w: %s kosong", ErrFormat, idCol)
	case math.Abs(e.Latitude) > 90 || math.Abs(e.Longitude) > 180:
		return e, fmt.Errorf("%w: koordinat di luar rentang", ErrFormat)
	case e.Magnitude < -1 || e.Magnitude > 10:
		return e, fmt.Errorf("%w: magnitudo %v", ErrFormat, e.Magnitude)
	}
	return e, nil
}

// ReadDYFICells membaca dirasakan-dyfi.csv. Jarak episenter (berkas DYFI
// lama) diubah menjadi jarak hiposenter dengan kedalaman kejadian.
func ReadDYFICells(r io.Reader) ([]ports.DYFICell, error) {
	var out []ports.DYFICell
	err := readRows(r, dyfiColumns, func(rec []string, c columns) error {
		e, err := feltEvent(rec, c, "usgs_id")
		if err != nil {
			return err
		}
		v, err := floats(rec, c, "cdi", "laporan", "jarak_km")
		if err != nil {
			return err
		}
		cell := ports.DYFICell{
			FeltEvent: e, MagnitudeType: c.get(rec, "tipe_magnitudo"), CDI: v[0], Responses: int(v[1]),
			HypocentralKm: v[2], Suspect: c.get(rec, "mencurigakan") == "1", Place: c.get(rec, "kota"),
		}
		switch c.get(rec, "jenis_jarak") {
		case "hiposenter":
		case "episenter":
			cell.HypocentralKm = math.Hypot(v[2], math.Max(e.DepthKm, 0))
		default:
			return fmt.Errorf("%w: jenis_jarak %q", ErrFormat, c.get(rec, "jenis_jarak"))
		}
		if cell.CDI < 1 || cell.CDI > 12 || cell.Responses < 1 || cell.HypocentralKm < 0 {
			return fmt.Errorf("%w: sel %s CDI %v, laporan %d, jarak %v", ErrFormat, c.get(rec, "sel"), cell.CDI, cell.Responses, cell.HypocentralKm)
		}
		out = append(out, cell)
		return nil
	})
	return out, err
}

// ReadBMKGFelt membaca dirasakan-bmkg.csv.
func ReadBMKGFelt(r io.Reader) ([]ports.BMKGFelt, error) {
	var out []ports.BMKGFelt
	seen := map[string]bool{}
	err := readRows(r, bmkgFeltColumns, func(rec []string, c columns) error {
		e, err := feltEvent(rec, c, "bmkg_id")
		if err != nil {
			return err
		}
		if seen[e.ID] {
			return fmt.Errorf("%w: %s muncul dua kali", ErrFormat, e.ID)
		}
		seen[e.ID] = true
		out = append(out, ports.BMKGFelt{FeltEvent: e, Region: c.get(rec, "wilayah"), Felt: c.get(rec, "dirasakan")})
		return nil
	})
	return out, err
}

// ReadAliases membaca gazetir-alias.csv (nama, kode, alasan).
func ReadAliases(r io.Reader) ([]gazetteer.Alias, error) {
	var out []gazetteer.Alias
	err := readRows(r, aliasColumns, func(rec []string, c columns) error {
		out = append(out, gazetteer.Alias{Name: c.get(rec, "nama"), Code: c.get(rec, "kode"), Reason: c.get(rec, "alasan")})
		return nil
	})
	return out, err
}
