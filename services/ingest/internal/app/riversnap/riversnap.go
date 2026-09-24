// Package riversnap adalah use case kalibrasi titik pantau sungai (make
// river-snap): membaca daftar titik berkoordinat perkiraan, meminta debit
// GloFAS untuk sel di sekitarnya, memilih sel per titik (domain/riversnap),
// lalu menulis daftar titik untuk ingest dan laporan pemeriksaan.
package riversnap

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/riversnap"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Batas Open-Meteo dihitung per lokasi: 600 per menit. Alat ini memakai
// paling banyak LocationsPerMinute supaya ingest yang sedang jalan tetap
// punya sisa kuota.
const (
	MaxLocationsPerRequest = 100
	LocationsPerMinute     = 400
	pastDays, aheadDays    = 7, 7
)

var slug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ReadStations membaca CSV sumber id,sungai,nama,lat,lon,radius,catatan
// (baris komentar # dilewati), urut hulu ke hilir per sungai.
func ReadStations(r io.Reader) ([]riversnap.Station, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	for line := range strings.SplitSeq(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	cr := csv.NewReader(&body)
	cr.FieldsPerRecord = 7
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV titik sungai: %w", err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != "id,sungai,nama,lat,lon,radius,catatan" {
		return nil, errors.New("header CSV titik sungai harus id,sungai,nama,lat,lon,radius,catatan")
	}
	var out []riversnap.Station
	seen := map[string]bool{}
	for _, rec := range recs[1:] {
		lat, err1 := strconv.ParseFloat(rec[3], 64)
		lon, err2 := strconv.ParseFloat(rec[4], 64)
		rad, err3 := strconv.ParseFloat(rec[5], 64)
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("titik %s: %w", rec[0], err)
		}
		st := riversnap.Station{
			ID: rec[0], River: strings.TrimSpace(rec[1]), Name: strings.TrimSpace(rec[2]),
			Approx: series.LatLon{Lat: lat, Lon: lon}, Radius: rad,
		}
		switch {
		case !slug.MatchString(st.ID) || seen[st.ID]:
			return nil, fmt.Errorf("id titik %q bukan slug atau ganda", st.ID)
		case st.River == "" || st.Name == "":
			return nil, fmt.Errorf("titik %s: sungai dan nama wajib diisi", st.ID)
		}
		if err := st.Validate(); err != nil {
			return nil, err
		}
		seen[st.ID] = true
		out = append(out, st)
	}
	return out, nil
}

// Snapper menjalankan kalibrasi.
type Snapper struct {
	Endpoint string
	Fetch    ports.Fetcher
	Clock    ports.Clock
	// Progress dipanggil setelah tiap request (boleh nil).
	Progress func(done, total int)
}

type batch struct {
	stations []int // indeks titik
	points   []series.LatLon
}

// Run meminta sel di sekitar setiap titik lalu memilih satu sel per titik.
func (s *Snapper) Run(ctx context.Context, stations []riversnap.Station) ([]riversnap.Choice, error) {
	batches := plan(stations)
	cands := make([][]riversnap.Candidate, len(stations))
	for bi, b := range batches {
		if bi > 0 {
			wait := time.Duration(len(b.points)) * time.Minute / LocationsPerMinute
			if err := s.Clock.Sleep(ctx, wait); err != nil {
				return nil, err
			}
		}
		cells, err := s.fetch(ctx, b.points)
		if err != nil {
			return nil, err
		}
		pos := 0
		for _, si := range b.stations {
			n := len(stations[si].Offsets())
			for _, c := range cells[pos : pos+n] {
				cands[si] = append(cands[si], riversnap.Candidate{Cell: c.Cell, Mean: mean(c.Values)})
			}
			pos += n
		}
		if s.Progress != nil {
			s.Progress(bi+1, len(batches))
		}
	}
	choices := make([]riversnap.Choice, len(stations))
	for i, st := range stations {
		ch, err := riversnap.Choose(st, cands[i])
		if err != nil {
			return nil, err
		}
		choices[i] = ch
	}
	return riversnap.Review(choices), nil
}

// plan mengelompokkan titik ke request berisi paling banyak
// MaxLocationsPerRequest lokasi, tanpa memecah satu titik.
func plan(stations []riversnap.Station) []batch {
	var out []batch
	var cur batch
	for i, st := range stations {
		pts := st.Offsets()
		if len(cur.points)+len(pts) > MaxLocationsPerRequest && len(cur.points) > 0 {
			out = append(out, cur)
			cur = batch{}
		}
		cur.stations = append(cur.stations, i)
		cur.points = append(cur.points, pts...)
	}
	if len(cur.points) > 0 {
		out = append(out, cur)
	}
	return out
}

func (s *Snapper) fetch(ctx context.Context, pts []series.LatLon) ([]openmeteo.CellValues, error) {
	lats := make([]string, len(pts))
	lons := make([]string, len(pts))
	for i, p := range pts {
		lats[i] = strconv.FormatFloat(p.Lat, 'f', -1, 64)
		lons[i] = strconv.FormatFloat(p.Lon, 'f', -1, 64)
	}
	q := url.Values{
		"latitude": {strings.Join(lats, ",")}, "longitude": {strings.Join(lons, ",")},
		"daily": {"river_discharge"}, "timeformat": {"unixtime"},
		"past_days": {strconv.Itoa(pastDays)}, "forecast_days": {strconv.Itoa(aheadDays)},
	}
	resp, err := s.Fetch.Fetch(ctx, ports.Request{
		URL: s.Endpoint + "?" + strings.ReplaceAll(q.Encode(), "%2C", ","), Accept: "application/json", MaxBytes: 16 << 20,
	})
	if err != nil {
		return nil, fmt.Errorf("meminta debit %d lokasi: %w", len(pts), err)
	}
	cells, err := openmeteo.ParseDaily(resp.Body, "river_discharge")
	if err != nil {
		return nil, err
	}
	if len(cells) != len(pts) {
		return nil, fmt.Errorf("%w: %d lokasi dijawab untuk %d", openmeteo.ErrStructure, len(cells), len(pts))
	}
	return cells, nil
}

// mean adalah rata-rata nilai yang terisi; NaN bila semuanya kosong.
func mean(vs []*float64) float64 {
	var sum float64
	n := 0
	for _, v := range vs {
		if v != nil {
			sum += *v
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	return sum / float64(n)
}

// WriteSites menulis daftar titik untuk ingest (sitelist/data/rivers_<PP>.csv).
func WriteSites(w io.Writer, province, source string, choices []riversnap.Choice) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Titik pantau debit sungai provinsi %s (PRD, bagian Sungai yang dipantau).\n", province)
	fmt.Fprintf(&b, "# Koordinat = pusat sel GloFAS terpilih. Dibuat `make river-snap` dari %s; jangan diedit manual.\n", source)
	fmt.Fprintf(&b, "# %d titik\n", len(choices))
	cw := csv.NewWriter(&b)
	_ = cw.Write([]string{"id", "sungai", "nama", "lat", "lon"})
	for _, c := range choices {
		_ = cw.Write([]string{c.ID, c.River, c.Name, coord(c.Cell.Lat), coord(c.Cell.Lon)})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteReport menulis laporan kalibrasi dalam Markdown.
func WriteReport(w io.Writer, source string, at time.Time, choices []riversnap.Choice) error {
	var b strings.Builder
	suspects := slices.IndexFunc(choices, riversnap.Choice.Suspect) >= 0
	fmt.Fprintf(&b, "# Kalibrasi titik pantau sungai\n\n")
	fmt.Fprintf(&b, "Dibuat `make river-snap` pada %s dari `%s`. Jangan diedit manual; ubah file sumber lalu jalankan ulang.\n\n", at.UTC().Format("2006-01-02 15:04 UTC"), source)
	b.WriteString("Setiap titik pantau PRD adalah nama lokasi. Alat ini meminta debit GloFAS v4 (Open-Meteo, grid 0,05°) harian 7 hari lalu sampai 7 hari ke depan untuk sel di sekitar koordinat perkiraan. ")
	b.WriteString("Di dalam radius pencarian, sel alur utama adalah sel yang debit rata-ratanya paling sedikit 30% dari debit terbesar; dari sel itu dipilih yang paling dekat dengan koordinat perkiraan (memilih debit terbesar saja akan selalu menggeser titik ke hilir). ")
	b.WriteString("Aturan ini bisa salah saat sungai lain yang lebih besar ada di dalam radius atau saat debit musim kemarau sangat kecil, jadi pilihan yang meragukan ditandai untuk diperiksa di peta (PRD, bagian Batas kemampuan data).\n\n")
	b.WriteString("Tanda: `tepi` sel terpilih di tepi radius, `kecil` debit rata-rata < 0,5 m³/s, `hilir-lebih-kecil` debit lebih kecil dari titik hulu sebelumnya di sungai yang sama (wajar di hilir bendungan), `sel-ganda` sel dipakai titik lain, `manual` titik sudah diverifikasi (radius 0).\n\n")
	b.WriteString("Setelah memeriksa satu titik di peta OSM, tulis koordinat sel yang benar di file sumber dengan radius 0 dan catatan asal verifikasinya.\n\n")
	b.WriteString("| Titik | Sungai | Perkiraan | Radius | Sel terpilih | Geser (km) | Debit rata-rata (m³/s) | Tanda |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range choices {
		flags := make([]string, len(c.Flags))
		for i, f := range c.Flags {
			flags[i] = "`" + string(f) + "`"
		}
		fmt.Fprintf(&b, "| %s (`%s`) | %s | %s, %s | %s° | %s, %s | %s | %s | %s |\n",
			c.Name, c.ID, c.River, coord(c.Approx.Lat), coord(c.Approx.Lon), num(c.Radius, 2),
			coord(c.Cell.Lat), coord(c.Cell.Lon), num(c.DistanceKm, 1), num(c.Mean, 2), strings.Join(flags, " "))
	}
	if suspects {
		b.WriteString("\nTitik bertanda (selain `manual`) belum boleh dipakai untuk ambang banjir sebelum diverifikasi.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func coord(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// num menulis angka dengan koma desimal (bahasa Indonesia).
func num(v float64, decimals int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', decimals, 64), ".", ",", 1)
}
