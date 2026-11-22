package feltdata

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
)

// DYFIHeader adalah kolom dirasakan-dyfi.csv; dibaca adapter catalog
// geo-processor. Satu baris per sel DYFI.
var DYFIHeader = []string{
	"usgs_id", "waktu_utc", "lintang", "bujur", "kedalaman_km", "magnitudo", "tipe_magnitudo",
	"laporan_kejadian", "sel", "cdi", "laporan", "jarak_km", "jenis_jarak", "lintang_sel", "bujur_sel",
	"mencurigakan", "kota",
}

// BMKGHeader adalah kolom dirasakan-bmkg.csv: satu baris per gempa dirasakan.
var BMKGHeader = []string{
	"bmkg_id", "waktu_utc", "lintang", "bujur", "kedalaman_km", "magnitudo", "wilayah", "dirasakan",
	"pertama_terlihat_utc", "terakhir_terlihat_utc", "versi_dirasakan",
}

// DYFIMeta adalah keterangan komentar dirasakan-dyfi.csv.
type DYFIMeta struct {
	From, To time.Time
	Box      area.Box
	MinFelt  int
	Stats    DYFIStats
}

func ff(v float64, digits int) string { return strconv.FormatFloat(v, 'f', digits, 64) }

func writeComments(w io.Writer, lines []string) error {
	for _, c := range lines {
		if _, err := io.WriteString(w, "# "+c+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// WriteDYFICSV menulis sel DYFI semua kejadian, urut kejadian lalu jarak.
func WriteDYFICSV(w io.Writer, m DYFIMeta, events []EventData) error {
	err := writeComments(w, []string{
		"Sel intensitas DYFI USGS (cdi_geo.txt, sel UTM 10 km) untuk kalibrasi radius dirasakan (make felt-radius, ADR 0026). Jangan diedit manual.",
		"Sumber: U.S. Geological Survey, Did You Feel It? (domain publik), https://earthquake.usgs.gov/data/dyfi/",
		fmt.Sprintf("Kejadian ber-DYFI %s..%s (UTC), kotak %s, >= %d laporan: %d kejadian, %d tanpa produk DYFI, %d tanpa cdi_geo, %d sel",
			m.From.Format(time.DateOnly), m.To.Format(time.DateOnly), m.Box, m.MinFelt, m.Stats.Events, m.Stats.NoProduct, m.Stats.NoCDI, m.Stats.Cells),
		"Episenter, kedalaman, dan magnitudo dari origin pilihan USGS; jarak_km dari cdi_geo (hiposenter, atau episenter untuk berkas lama).",
	})
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(DYFIHeader); err != nil {
		return err
	}
	for _, e := range events {
		for _, c := range e.Cells {
			kind := "hiposenter"
			if c.Epicentral {
				kind = "episenter"
			}
			suspect := "0"
			if c.Suspect {
				suspect = "1"
			}
			city := c.City
			if c.State != "" {
				city += ", " + c.State
			}
			rec := []string{
				e.ID, e.Time.Format("2006-01-02T15:04:05.000Z"), ff(e.Latitude, 4), ff(e.Longitude, 4), ff(e.DepthKm, 1),
				ff(e.Magnitude, 1), e.MagnitudeType, strconv.Itoa(e.Product.Responses()), c.Box, ff(c.CDI, 1),
				strconv.Itoa(c.Responses), ff(c.DistanceKm, 0), kind, ff(c.Latitude, 4), ff(c.Longitude, 4), suspect, oneLine(city),
			}
			if err := cw.Write(rec); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteBMKGCSV menulis gempa dirasakan BMKG, urut waktu kejadian.
func WriteBMKGCSV(w io.Writer, st BMKGStats, events []BMKGFelt) error {
	var first, last time.Time
	for _, e := range events {
		if first.IsZero() || e.FirstSeen.Before(first) {
			first = e.FirstSeen
		}
		if e.LastSeen.After(last) {
			last = e.LastSeen
		}
	}
	err := writeComments(w, []string{
		"Gempa dirasakan BMKG dari arsip payload gempadirasakan.json (make felt-radius, ADR 0026). Jangan diedit manual.",
		"Sumber: BMKG, https://data.bmkg.go.id/gempabumi/ (isi payload terakhir yang memuat gempa itu).",
		fmt.Sprintf("%d payload (%d tidak terbaca, %d gempa ditolak validasi) diambil %s..%s; %d gempa, %d dengan isi Dirasakan berubah",
			st.Payloads, st.Unreadable, st.Rejected, fmtTime(first), fmtTime(last), st.Events, st.Revised),
	})
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(BMKGHeader); err != nil {
		return err
	}
	for _, e := range events {
		rec := []string{
			e.EventID, e.OccurredAt.Format(time.RFC3339), ff(e.Latitude, 2), ff(e.Longitude, 2), ff(e.DepthKm, 0),
			ff(e.Magnitude, 1), oneLine(e.Place), oneLine(e.Felt), fmtTime(e.FirstSeen), fmtTime(e.LastSeen), strconv.Itoa(e.Versions),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
