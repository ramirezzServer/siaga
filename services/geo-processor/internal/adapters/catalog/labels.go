package catalog

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// labelColumns adalah kolom wajib set berlabel (make dedup-labels di ingest
// menulis docs/calibration/dedup-berlabel.csv; docs/calibration/dedup-arsip.csv
// disusun manual dengan kolom yang sama).
var labelColumns = []string{
	"dasar_label", "kejadian_label", "sumber", "id", "waktu_utc", "lintang", "bujur",
	"kedalaman_km", "magnitudo", "tipe_magnitudo", "magnitudo_lain", "catatan",
}

// ReadLabels membaca set berlabel. Baris berawalan # adalah komentar. Tidak
// ada baris yang dilewati: set berlabel kecil dan dibuat mesin atau tangan
// dengan hati-hati, jadi setiap baris rusak adalah galat.
func ReadLabels(r io.Reader) ([]ports.LabeledOrigin, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("membaca header: %w", err)
	}
	c := header(head)
	if !c.has(labelColumns...) {
		return nil, fmt.Errorf("%w: header set berlabel %v", ErrFormat, head)
	}
	var out []ports.LabeledOrigin
	seen := map[quake.IdentityKey]bool{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		line, _ := cr.FieldPos(0)
		o, err := labeled(rec, c)
		if err != nil {
			return nil, fmt.Errorf("baris %d: %w", line, err)
		}
		if seen[o.Key] {
			return nil, fmt.Errorf("baris %d: %w: %s muncul dua kali", line, ErrFormat, o.Key)
		}
		seen[o.Key] = true
		out = append(out, o)
	}
}

func labeled(rec []string, c columns) (ports.LabeledOrigin, error) {
	o := ports.LabeledOrigin{
		Basis: c.get(rec, "dasar_label"), Label: c.get(rec, "kejadian_label"),
		MagnitudeType: c.get(rec, "tipe_magnitudo"), Note: c.get(rec, "catatan"), DepthKm: math.NaN(),
	}
	if o.Basis == "" || o.Label == "" {
		return o, fmt.Errorf("%w: dasar_label dan kejadian_label wajib", ErrFormat)
	}
	src := quake.Source(c.get(rec, "sumber"))
	if src != quake.SourceBMKG && src != quake.SourceUSGS {
		return o, fmt.Errorf("%w: sumber %q bukan bmkg/usgs", ErrFormat, src)
	}
	o.Key = quake.IdentityKey{Source: src, EventID: c.get(rec, "id")}
	if o.Key.EventID == "" {
		return o, fmt.Errorf("%w: id kosong", ErrFormat)
	}
	t, err := time.Parse(time.RFC3339Nano, c.get(rec, "waktu_utc"))
	if err != nil {
		return o, fmt.Errorf("%w: waktu_utc: %w", ErrFormat, err)
	}
	v, err := floats(rec, c, "lintang", "bujur", "magnitudo")
	if err != nil {
		return o, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	o.Origin = quake.Origin{OccurredAt: t.UTC(), Latitude: v[0], Longitude: v[1], Magnitude: v[2]}
	if d := c.get(rec, "kedalaman_km"); d != "" {
		if o.DepthKm, err = strconv.ParseFloat(strings.TrimSpace(d), 64); err != nil {
			return o, fmt.Errorf("%w: kedalaman_km: %w", ErrFormat, err)
		}
	}
	if !(o.Latitude >= -90 && o.Latitude <= 90 && o.Longitude >= -180 && o.Longitude <= 180) {
		return o, fmt.Errorf("%w: koordinat %v,%v", ErrFormat, o.Latitude, o.Longitude)
	}
	return o, nil
}
