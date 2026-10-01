// Package rainthreshold adalah use case kalibrasi indeks hujan sub-DAS (make
// rain-threshold). Tujuh sub-DAS Citarum Hulu terlalu kecil untuk sel GloFAS
// 5 km (PRD, Batas kemampuan data), jadi potensi banjirnya diindikasikan dari
// akumulasi hujan 3, 6, dan 24 jam rata-rata wilayah sub-DAS dibandingkan
// persentil historisnya. Alat ini meminta hujan per jam model ECMWF IFS dari
// arsip Open-Meteo untuk setiap sel yang menutup sub-DAS, merata-ratakannya
// dengan bobot luas, lalu menghitung ambang dari puncak harian akumulasi
// (ADR 0020).
package rainthreshold

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/threshold"
)

// DefaultArchiveURL adalah Open-Meteo Historical Weather API.
const DefaultArchiveURL = "https://archive-api.open-meteo.com/v1/archive"

// DefaultModel adalah ECMWF IFS 9 km. Arsipnya mulai 2017-01-01 di sel yang
// sama dengan prakiraannya (dicek 2026-10-01), jadi klimatologi dan prakiraan
// berasal dari model yang sama. ERA5-Land di Open-Meteo tidak berisi hujan.
const DefaultModel = "ecmwf_ifs"

// Periode klimatologi bawaan: 8 tahun kalender penuh arsip ECMWF IFS.
var (
	DefaultFrom = time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)
	DefaultTo   = time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
)

// Windows adalah panjang jendela akumulasi (jam) indeks hujan (PRD).
var Windows = []int{3, 6, 24}

// MinDays adalah hari berisi paling sedikit per sub-DAS dan jendela: tujuh
// tahun, jadi persentil 99,5 masih ditopang ±13 hari.
const MinDays = 2555

// WeightTolerance adalah selisih terbesar jumlah bobot sel satu sub-DAS dari
// 1 (bobot ditulis 4 desimal).
const WeightTolerance = 0.002

// Source mengembalikan sumber hujan per jam model untuk periode from..to.
func Source(endpoint, model string, from, to time.Time) reanalysis.Source {
	return reanalysis.Source{
		Endpoint: endpoint, Model: model, Variable: "precipitation", Unit: "mm",
		Resolution: reanalysis.Hourly, From: from, To: to,
	}
}

// Basin adalah satu sub-DAS: sel model yang menutupinya dan bagian luasnya.
type Basin struct {
	ID, Name string
	Cells    []series.LatLon
	Weights  []float64
}

var slug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ReadBasins membaca CSV subdas,nama,sel_lat,sel_lon,bobot (baris komentar
// # dilewati), satu baris per sel per sub-DAS.
func ReadBasins(r io.Reader) ([]Basin, error) {
	const header = "subdas,nama,sel_lat,sel_lon,bobot"
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
		return nil, fmt.Errorf("CSV sub-DAS: %w", err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != header {
		return nil, fmt.Errorf("header CSV sub-DAS harus %s", header)
	}
	var out []Basin
	index := map[string]int{}
	for _, rec := range recs[1:] {
		lat, err1 := strconv.ParseFloat(rec[2], 64)
		lon, err2 := strconv.ParseFloat(rec[3], 64)
		w, err3 := strconv.ParseFloat(rec[4], 64)
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("sub-DAS %s: %w", rec[0], err)
		}
		cell := series.LatLon{Lat: lat, Lon: lon}
		switch {
		case !slug.MatchString(rec[0]) || strings.TrimSpace(rec[1]) == "":
			return nil, fmt.Errorf("sub-DAS %q: id bukan slug atau nama kosong", rec[0])
		case !series.InIndonesia(cell):
			return nil, fmt.Errorf("sub-DAS %s: sel %v,%v di luar Indonesia", rec[0], lat, lon)
		case !(w > 0 && w <= 1):
			return nil, fmt.Errorf("sub-DAS %s: bobot %v di luar (0, 1]", rec[0], w)
		}
		i, ok := index[rec[0]]
		if !ok {
			i = len(out)
			index[rec[0]] = i
			out = append(out, Basin{ID: rec[0], Name: strings.TrimSpace(rec[1])})
		}
		b := &out[i]
		if b.Name != strings.TrimSpace(rec[1]) {
			return nil, fmt.Errorf("sub-DAS %s: nama berbeda antarbaris", rec[0])
		}
		if slices.Contains(b.Cells, reanalysis.Key(cell)) {
			return nil, fmt.Errorf("sub-DAS %s: sel %v,%v ganda", rec[0], lat, lon)
		}
		b.Cells = append(b.Cells, reanalysis.Key(cell))
		b.Weights = append(b.Weights, w)
	}
	for _, b := range out {
		total := 0.0
		for _, w := range b.Weights {
			total += w
		}
		if math.Abs(total-1) > WeightTolerance {
			return nil, fmt.Errorf("sub-DAS %s: jumlah bobot %.4f, harus 1", b.ID, total)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("CSV sub-DAS kosong")
	}
	return out, nil
}

// Cells mengembalikan sel unik semua sub-DAS, urutan tetap.
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

// Result adalah ambang satu sub-DAS untuk satu jendela akumulasi.
type Result struct {
	Basin      Basin
	Hours      int
	Thresholds threshold.Set
	// Valid adalah jumlah hari berisi.
	Valid int
	// Top adalah hari dengan akumulasi terbesar, menurun (bahan pencocokan
	// dengan berita banjir).
	Top []threshold.Peak
}

// TopDays adalah jumlah hari terbesar yang dilaporkan per sub-DAS.
const TopDays = 5

// Compute menghitung ambang setiap sub-DAS dan jendela dari deret hujan per
// jam setiap sel (kunci reanalysis.Key).
func Compute(src reanalysis.Source, basins []Basin, data map[series.LatLon][]float64) ([]Result, error) {
	var out []Result
	for _, b := range basins {
		cells := make([][]float64, len(b.Cells))
		for i, c := range b.Cells {
			vs, ok := data[reanalysis.Key(c)]
			if !ok || len(vs) != src.Steps() {
				return nil, fmt.Errorf("sub-DAS %s: deret sel %v,%v tidak ada", b.ID, c.Lat, c.Lon)
			}
			cells[i] = vs
		}
		areal, err := threshold.WeightedMean(cells, b.Weights)
		if err != nil {
			return nil, fmt.Errorf("sub-DAS %s: %w", b.ID, err)
		}
		for _, h := range Windows {
			daily := threshold.DailyMaxSum(areal, h)
			set, err := threshold.Compute(daily, MinDays)
			if err != nil {
				return nil, fmt.Errorf("sub-DAS %s, %d jam: %w", b.ID, h, err)
			}
			r := Result{Basin: b, Hours: h, Thresholds: set}
			for i, v := range daily {
				if !math.IsNaN(v) {
					r.Valid++
					r.Top = append(r.Top, threshold.Peak{Day: src.From.AddDate(0, 0, i), Value: v})
				}
			}
			slices.SortStableFunc(r.Top, func(a, b threshold.Peak) int { return cmp.Compare(b.Value, a.Value) })
			r.Top = r.Top[:min(TopDays, len(r.Top))]
			out = append(out, r)
		}
	}
	return out, nil
}

// WriteThresholds menulis daftar ambang per sub-DAS dan jendela.
func WriteThresholds(w io.Writer, src reanalysis.Source, results []Result) error {
	var b strings.Builder
	b.WriteString("# Ambang indeks hujan sub-DAS Citarum Hulu (PRD, Batas kemampuan data; tingkat seperti debit: p80, p90, p98, p99,5).\n")
	fmt.Fprintf(&b, "# Puncak harian (UTC) akumulasi hujan rata-rata wilayah (mm), Open-Meteo arsip models=%s, %s..%s.\n", src.Model, reanalysis.Date(src.From), reanalysis.Date(src.To))
	b.WriteString("# Dibuat `make rain-threshold` dari docs/calibration/sub-das-citarum-hulu.csv; jangan diedit manual (ADR 0020).\n")
	fmt.Fprintf(&b, "# %d baris\n", len(results))
	cw := csv.NewWriter(&b)
	_ = cw.Write([]string{"subdas", "nama", "jam", "p50", "p80", "p90", "p98", "p99_5", "hari_berisi"})
	for _, r := range results {
		t := r.Thresholds
		_ = cw.Write([]string{
			r.Basin.ID, r.Basin.Name, strconv.Itoa(r.Hours),
			val(t.P50), val(t.Levels[0]), val(t.Levels[1]), val(t.Levels[2]), val(t.Levels[3]), strconv.Itoa(r.Valid),
		})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteReport menulis laporan kalibrasi dalam Markdown.
func WriteReport(w io.Writer, at time.Time, src reanalysis.Source, results []Result) error {
	var b strings.Builder
	b.WriteString("# Ambang indeks hujan sub-DAS Citarum Hulu\n\n")
	fmt.Fprintf(&b, "Dibuat `make rain-threshold` pada %s. Jangan diedit manual; ubah daftar sel lalu jalankan ulang (ADR 0020).\n\n", at.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Hujan per jam model ECMWF IFS 9 km (arsip Open-Meteo `models=%s`, `cell_selection=nearest`) %s sampai %s dirata-rata per sub-DAS dengan bobot luas (`docs/calibration/sub-das-citarum-hulu.csv`). ",
		src.Model, reanalysis.Date(src.From), reanalysis.Date(src.To))
	b.WriteString("Untuk setiap hari UTC diambil akumulasi 3, 6, dan 24 jam terbesar yang berakhir di hari itu; ambang adalah persentil nilai harian itu, jadi frekuensinya sama dengan ambang debit (73, 37, 7, dan 1,8 hari per tahun). ")
	b.WriteString("Hasilnya berlabel \"indikasi potensi banjir\", bukan pengukuran debit (PRD).\n\n")
	b.WriteString("| Sub-DAS | Jendela | p50 | p80 | p90 | p98 | p99,5 | Hari berisi |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range results {
		t := r.Thresholds
		fmt.Fprintf(&b, "| %s | %d jam | %s | %s | %s | %s | %s | %d |\n",
			r.Basin.Name, r.Hours, num(t.P50), num(t.Levels[0]), num(t.Levels[1]), num(t.Levels[2]), num(t.Levels[3]), r.Valid)
	}
	b.WriteString("\nSatuan mm.\n\n## Hari dengan hujan 24 jam terbesar\n\n")
	b.WriteString("Bahan pencocokan dengan berita banjir cekungan Bandung: bila hari-hari ini tidak pernah tercatat banjir, ambang perlu ditinjau.\n\n")
	b.WriteString("| Sub-DAS | Hari (akumulasi 24 jam, mm) |\n| --- | --- |\n")
	for _, r := range results {
		if r.Hours != 24 {
			continue
		}
		days := make([]string, len(r.Top))
		for i, p := range r.Top {
			days[i] = fmt.Sprintf("%s (%s)", reanalysis.Date(p.Day), num(p.Value))
		}
		fmt.Fprintf(&b, "| %s | %s |\n", r.Basin.Name, strings.Join(days, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func val(v float64) string { return fmt.Sprintf("%.2f", v) }

func num(v float64) string {
	if math.IsNaN(v) {
		return "-"
	}
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}
