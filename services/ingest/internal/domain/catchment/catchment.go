// Package catchment berisi aturan murni sub-DAS indeks hujan (ADR 0020):
// daftar sel model yang menutupi tiap sub-DAS beserta bobot luasnya, ID
// titiknya di SIAGA, dan hujan rata-rata wilayah per jam dari nilai sel.
// Alat kalibrasi (make rain-threshold) dan konektor prakiraan
// (openmeteo-hujan) memakai fungsi yang sama, jadi indeks hujan prakiraan
// sebanding dengan ambangnya.
package catchment

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/threshold"
)

// ErrInvalid menandai daftar sub-DAS atau nilai sel yang melanggar invarian.
var ErrInvalid = errors.New("sub-DAS tidak valid")

// WeightTolerance adalah selisih terbesar jumlah bobot sel satu sub-DAS dari
// 1 (bobot ditulis 4 desimal).
const WeightTolerance = 0.002

// Header adalah baris kolom CSV sub-DAS.
const Header = "subdas,nama,sel_lat,sel_lon,bobot"

// Basin adalah satu sub-DAS: sel model yang menutupinya dan bagian luasnya.
// Cells dan Weights sejajar.
type Basin struct {
	ID, Name string
	Cells    []series.LatLon
	Weights  []float64
}

var slug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SiteID mengembalikan ID titik sub-DAS, misal "catchment:cikapundung".
func (b Basin) SiteID() string { return series.CatchmentID(b.ID) }

// Centroid adalah titik berat sel-sel sub-DAS menurut bobot luasnya,
// dibulatkan 4 desimal. Dipakai sebagai lokasi titik sub-DAS.
func (b Basin) Centroid() series.LatLon {
	var lat, lon, total float64
	for i, c := range b.Cells {
		lat += b.Weights[i] * c.Lat
		lon += b.Weights[i] * c.Lon
		total += b.Weights[i]
	}
	return Key(series.LatLon{Lat: lat / total, Lon: lon / total})
}

// Key membulatkan koordinat sel ke 4 desimal, ketelitian daftar sel.
func Key(p series.LatLon) series.LatLon {
	return series.LatLon{Lat: math.Round(p.Lat*1e4) / 1e4, Lon: math.Round(p.Lon*1e4) / 1e4}
}

// Parse membaca CSV subdas,nama,sel_lat,sel_lon,bobot (baris komentar #
// dilewati), satu baris per sel per sub-DAS. Urutan sub-DAS dan sel
// mengikuti file.
func Parse(r io.Reader) ([]Basin, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	for line := range strings.SplitSeq(string(raw), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	cr := csv.NewReader(&body)
	cr.FieldsPerRecord = 5
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: CSV: %w", ErrInvalid, err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != Header {
		return nil, fmt.Errorf("%w: header CSV harus %s", ErrInvalid, Header)
	}
	var out []Basin
	index := map[string]int{}
	for _, rec := range recs[1:] {
		lat, err1 := strconv.ParseFloat(rec[2], 64)
		lon, err2 := strconv.ParseFloat(rec[3], 64)
		w, err3 := strconv.ParseFloat(rec[4], 64)
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, rec[0], err)
		}
		cell := series.LatLon{Lat: lat, Lon: lon}
		switch {
		case !slug.MatchString(rec[0]) || strings.TrimSpace(rec[1]) == "":
			return nil, fmt.Errorf("%w: %q: id bukan slug atau nama kosong", ErrInvalid, rec[0])
		case !series.InIndonesia(cell):
			return nil, fmt.Errorf("%w: %s: sel %v,%v di luar Indonesia", ErrInvalid, rec[0], lat, lon)
		case !(w > 0 && w <= 1):
			return nil, fmt.Errorf("%w: %s: bobot %v di luar (0, 1]", ErrInvalid, rec[0], w)
		}
		i, ok := index[rec[0]]
		if !ok {
			i = len(out)
			index[rec[0]] = i
			out = append(out, Basin{ID: rec[0], Name: strings.TrimSpace(rec[1])})
		}
		b := &out[i]
		if b.Name != strings.TrimSpace(rec[1]) {
			return nil, fmt.Errorf("%w: %s: nama berbeda antarbaris", ErrInvalid, rec[0])
		}
		if slices.Contains(b.Cells, Key(cell)) {
			return nil, fmt.Errorf("%w: %s: sel %v,%v ganda", ErrInvalid, rec[0], lat, lon)
		}
		b.Cells = append(b.Cells, Key(cell))
		b.Weights = append(b.Weights, w)
	}
	for _, b := range out {
		total := 0.0
		for _, w := range b.Weights {
			total += w
		}
		if math.Abs(total-1) > WeightTolerance {
			return nil, fmt.Errorf("%w: %s: jumlah bobot %.4f, harus 1", ErrInvalid, b.ID, total)
		}
		if !series.ValidSiteID(b.SiteID()) {
			return nil, fmt.Errorf("%w: ID titik %s terlalu panjang", ErrInvalid, b.SiteID())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: CSV kosong", ErrInvalid)
	}
	return out, nil
}

// Cells mengembalikan sel unik semua sub-DAS, urutan tetap (urutan pertama
// muncul di daftar).
func Cells(basins []Basin) []series.LatLon {
	var out []series.LatLon
	for _, b := range basins {
		for _, c := range b.Cells {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// Areal menghitung hujan rata-rata wilayah satu sub-DAS per langkah dari deret
// setiap sel (kunci Key). Langkah yang salah satu selnya kosong (NaN) bernilai
// NaN. Rata-ratanya threshold.WeightedMean, fungsi yang juga dipakai
// kalibrasi.
func (b Basin) Areal(data map[series.LatLon][]float64) ([]float64, error) {
	cells := make([][]float64, len(b.Cells))
	for i, c := range b.Cells {
		vs, ok := data[Key(c)]
		if !ok {
			return nil, fmt.Errorf("%w: %s: deret sel %v,%v tidak ada", ErrInvalid, b.ID, c.Lat, c.Lon)
		}
		cells[i] = vs
	}
	out, err := threshold.WeightedMean(cells, b.Weights)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, b.ID, err)
	}
	return out, nil
}
