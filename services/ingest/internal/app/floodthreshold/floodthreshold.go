// Package floodthreshold adalah use case kalibrasi ambang banjir (make
// flood-threshold): meminta debit harian reanalisis GloFAS periode
// klimatologi untuk sel setiap titik pantau sungai, menghitung ambang
// persentil per titik (domain/threshold), menguji ambang terhadap kejadian
// banjir tercatat, lalu menulis daftar ambang dan laporannya (ADR 0020).
package floodthreshold

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/threshold"
)

// DefaultFloodURL adalah Open-Meteo Flood API.
const DefaultFloodURL = "https://flood-api.open-meteo.com/v1/flood"

// Model reanalisis dan gabungan Open-Meteo Flood API. Reanalisis
// consolidated_v4 berisi 1997-01-01 sampai 2025-05-31 (dicek 2026-10-01;
// dokumentasi Open-Meteo menyebut 1984 sampai Juli 2022). seamless_v4
// (bawaan API, dipakai ingest) menyambung reanalisis dengan data
// intermediate dan prakiraan, jadi dipakai untuk kejadian setelah reanalisis.
const (
	ModelReanalysis = "consolidated_v4"
	ModelSeamless   = "seamless_v4"
)

// ReanalysisStart adalah hari pertama reanalisis consolidated_v4 yang berisi
// di Open-Meteo (sama dengan riversnap.ReanalysisStart).
var ReanalysisStart = time.Date(1997, 1, 1, 0, 0, 0, 0, time.UTC)

// Periode klimatologi bawaan: 28 tahun kalender penuh di dalam reanalisis.
// Januari–Mei 2025 sengaja di luar periode supaya banjir Bekasi Maret 2025
// menjadi uji di luar sampel.
var (
	DefaultFrom = time.Date(1997, 1, 1, 0, 0, 0, 0, time.UTC)
	DefaultTo   = time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
)

// MinYearDays adalah hari berisi paling sedikit supaya puncak tahunan dihitung.
const MinYearDays = 330

// Source mengembalikan sumber debit harian model untuk periode from..to.
func Source(endpoint, model string, from, to time.Time) reanalysis.Source {
	return reanalysis.Source{
		Endpoint: endpoint, Model: model, Variable: "river_discharge", Unit: "m³/s",
		Resolution: reanalysis.Daily, From: from, To: to,
	}
}

// Result adalah ambang satu titik pantau.
type Result struct {
	Site       series.Site
	Thresholds threshold.Set
	// Valid adalah jumlah hari berisi.
	Valid int
	// MedianAnnualMax adalah median puncak tahunan; Years jumlah tahunnya.
	MedianAnnualMax float64
	Years           int
	// Max adalah debit harian terbesar periode klimatologi.
	Max threshold.Peak
	// SeamlessRatio adalah p98 seamless_v4 dibagi p98 reanalisis di periode
	// tumpang tindih (NaN bila tidak dihitung). Di bawah 1 berarti data yang
	// dibandingkan dengan ambang cenderung lebih rendah dari klimatologinya.
	SeamlessRatio float64
}

// Compute menghitung ambang setiap titik dari deret sel (kunci
// reanalysis.Key dari koordinat sel titik).
func Compute(src reanalysis.Source, sites []series.Site, data map[series.LatLon][]float64) ([]Result, error) {
	out := make([]Result, 0, len(sites))
	for _, s := range sites {
		vs, ok := data[reanalysis.Key(s.Requested)]
		if !ok || len(vs) != src.Steps() {
			return nil, fmt.Errorf("titik %s: deret sel %v,%v tidak ada", s.ID, s.Requested.Lat, s.Requested.Lon)
		}
		set, err := threshold.Compute(vs, threshold.MinSamples)
		if err != nil {
			return nil, fmt.Errorf("titik %s: %w", s.ID, err)
		}
		r := Result{Site: s, Thresholds: set, SeamlessRatio: math.NaN()}
		peaks := threshold.AnnualMax(src.From, vs, MinYearDays)
		maxes := make([]float64, len(peaks))
		for i, p := range peaks {
			maxes[i] = p.Value
			if i == 0 || p.Value > r.Max.Value {
				r.Max = p
			}
		}
		r.Years, r.MedianAnnualMax = len(peaks), threshold.Median(maxes)
		for _, v := range vs {
			if !math.IsNaN(v) {
				r.Valid++
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Event adalah kejadian banjir tercatat untuk menguji ambang.
type Event struct {
	ID string
	// Stations adalah slug titik pantau yang dilewati banjir.
	Stations []string
	From, To time.Time
	Model    string
	Source   string
}

// MaxEventDays membatasi jendela kejadian supaya permintaan uji tetap satu
// panggilan per titik (bobot ≤ 1 sampai 140 hari, dibatasi lebih ketat).
const MaxEventDays = 31

var (
	slug    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	httpURL = regexp.MustCompile(`^https://\S+$`)
)

// ReadEvents membaca CSV id,titik,dari,sampai,model,sumber (baris komentar #
// dilewati; titik dipisah titik koma).
func ReadEvents(r io.Reader) ([]Event, error) {
	recs, err := readCSV(r, "id,titik,dari,sampai,model,sumber")
	if err != nil {
		return nil, fmt.Errorf("CSV kejadian banjir: %w", err)
	}
	var out []Event
	seen := map[string]bool{}
	for _, rec := range recs {
		from, err1 := reanalysis.ParseDate(rec[2])
		to, err2 := reanalysis.ParseDate(rec[3])
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("kejadian %s: %w", rec[0], err)
		}
		ev := Event{ID: rec[0], Stations: strings.Split(rec[1], ";"), From: from, To: to, Model: rec[4], Source: rec[5]}
		switch {
		case !slug.MatchString(ev.ID) || seen[ev.ID]:
			return nil, fmt.Errorf("id kejadian %q bukan slug atau ganda", ev.ID)
		case ev.Model != ModelReanalysis && ev.Model != ModelSeamless:
			return nil, fmt.Errorf("kejadian %s: model %q harus %s atau %s", ev.ID, ev.Model, ModelReanalysis, ModelSeamless)
		case to.Before(from) || to.Sub(from) >= MaxEventDays*24*time.Hour:
			return nil, fmt.Errorf("kejadian %s: jendela %s..%s harus 1..%d hari", ev.ID, rec[2], rec[3], MaxEventDays)
		case !httpURL.MatchString(ev.Source):
			return nil, fmt.Errorf("kejadian %s: sumber harus URL https", ev.ID)
		}
		for _, s := range ev.Stations {
			if !slug.MatchString(s) {
				return nil, fmt.Errorf("kejadian %s: titik %q bukan slug", ev.ID, s)
			}
		}
		seen[ev.ID] = true
		out = append(out, ev)
	}
	return out, nil
}

// InSample menyatakan jendela kejadian ada di dalam periode klimatologi src
// dengan model yang sama: ambang ikut dihitung dari hari kejadian itu.
func (e Event) InSample(src reanalysis.Source) bool {
	return e.Model == src.Model && !e.From.Before(src.From) && !e.To.After(src.To)
}

// Check adalah hasil uji satu titik untuk satu kejadian.
type Check struct {
	Event    Event
	Site     series.Site
	InSample bool
	Peak     threshold.Peak
	Level    threshold.Level
}

// Evaluate menguji kejadian terhadap ambang. data adalah deret sel untuk
// periode kejadian (Source model kejadian, ev.From..ev.To).
func Evaluate(ev Event, results []Result, inSample bool, data map[series.LatLon][]float64) ([]Check, error) {
	byID := map[string]Result{}
	for _, r := range results {
		byID[r.Site.ID] = r
	}
	var out []Check
	for _, st := range ev.Stations {
		r, ok := byID[series.RiverID(st)]
		if !ok {
			return nil, fmt.Errorf("kejadian %s: titik %s tidak ada di daftar titik pantau", ev.ID, st)
		}
		vs, ok := data[reanalysis.Key(r.Site.Requested)]
		if !ok {
			return nil, fmt.Errorf("kejadian %s: deret titik %s tidak ada", ev.ID, st)
		}
		c := Check{Event: ev, Site: r.Site, InSample: inSample, Peak: threshold.Peak{Value: math.NaN()}}
		for i, v := range vs {
			if !math.IsNaN(v) && (math.IsNaN(c.Peak.Value) || v > c.Peak.Value) {
				c.Peak = threshold.Peak{Day: ev.From.AddDate(0, 0, i), Value: v}
			}
		}
		c.Level = r.Thresholds.Level(c.Peak.Value)
		out = append(out, c)
	}
	return out, nil
}

// Cells mengembalikan sel setiap titik pantau yang dilewati kejadian.
func (e Event) Cells(sites []series.Site) []series.LatLon {
	var out []series.LatLon
	for _, s := range sites {
		for _, st := range e.Stations {
			if s.ID == series.RiverID(st) {
				out = append(out, s.Requested)
			}
		}
	}
	return out
}

// Opener membuka Loader dengan cache untuk satu sumber (diisi cmd: cache di
// file, Fetcher HTTP).
type Opener func(reanalysis.Source) (*reanalysis.Loader, error)

// Calibrator menjalankan kalibrasi.
type Calibrator struct {
	Endpoint string
	Source   reanalysis.Source
	Open     Opener
	// OverlapFrom..OverlapTo adalah periode pembanding seamless_v4 dengan
	// reanalisis, di dalam Source (nol = tidak dibandingkan).
	OverlapFrom, OverlapTo time.Time
}

// Periode tumpang tindih bawaan: setelah akhir reanalisis yang
// didokumentasikan (Juli 2022) sampai akhir periode klimatologi. Di periode
// ini seamless_v4 memakai data intermediate GloFAS, sumber yang sama dengan
// awal deret prakiraan, jadi rasionya mengukur bias data pembanding.
var (
	DefaultOverlapFrom = time.Date(2022, 8, 1, 0, 0, 0, 0, time.UTC)
	DefaultOverlapTo   = DefaultTo
)

// Run mengambil deret klimatologi, menghitung ambang, lalu menguji setiap
// kejadian. Kejadian di dalam periode klimatologi memakai deret yang sama;
// kejadian lain diminta dengan model kejadian untuk jendelanya saja.
func (c *Calibrator) Run(ctx context.Context, sites []series.Site, events []Event) ([]Result, []Check, error) {
	if c.Source.Model == ModelReanalysis && c.Source.From.Before(ReanalysisStart) {
		return nil, nil, fmt.Errorf("periode klimatologi dimulai %s, sebelum reanalisis %s di Open-Meteo (%s)",
			reanalysis.Date(c.Source.From), ModelReanalysis, reanalysis.Date(ReanalysisStart))
	}
	cells := make([]series.LatLon, len(sites))
	for i, s := range sites {
		cells[i] = s.Requested
	}
	main, err := c.Open(c.Source)
	if err != nil {
		return nil, nil, err
	}
	data, err := main.Load(ctx, cells)
	if err != nil {
		return nil, nil, err
	}
	results, err := Compute(c.Source, sites, data)
	if err != nil {
		return nil, nil, err
	}
	var checks []Check
	for _, ev := range events {
		in := ev.InSample(c.Source)
		evData := map[series.LatLon][]float64{}
		if in {
			off := int(ev.From.Sub(c.Source.From) / (24 * time.Hour))
			n := int(ev.To.Sub(ev.From)/(24*time.Hour)) + 1
			for p, vs := range data {
				evData[p] = vs[off : off+n]
			}
		} else {
			l, err := c.Open(Source(c.Endpoint, ev.Model, ev.From, ev.To))
			if err != nil {
				return nil, nil, err
			}
			if evData, err = l.Load(ctx, ev.Cells(sites)); err != nil {
				return nil, nil, fmt.Errorf("kejadian %s: %w", ev.ID, err)
			}
		}
		cs, err := Evaluate(ev, results, in, evData)
		if err != nil {
			return nil, nil, err
		}
		checks = append(checks, cs...)
	}
	if !c.OverlapFrom.IsZero() {
		if err := c.compare(ctx, sites, results, data); err != nil {
			return nil, nil, err
		}
	}
	return results, checks, nil
}

// compare mengisi SeamlessRatio: p98 seamless_v4 dibagi p98 reanalisis di
// periode tumpang tindih.
func (c *Calibrator) compare(ctx context.Context, sites []series.Site, results []Result, data map[series.LatLon][]float64) error {
	if c.OverlapFrom.Before(c.Source.From) || c.OverlapTo.After(c.Source.To) || c.OverlapTo.Before(c.OverlapFrom) {
		return fmt.Errorf("periode pembanding %s..%s harus di dalam periode klimatologi", reanalysis.Date(c.OverlapFrom), reanalysis.Date(c.OverlapTo))
	}
	l, err := c.Open(Source(c.Endpoint, ModelSeamless, c.OverlapFrom, c.OverlapTo))
	if err != nil {
		return err
	}
	cells := make([]series.LatLon, len(sites))
	for i, s := range sites {
		cells[i] = s.Requested
	}
	seamless, err := l.Load(ctx, cells)
	if err != nil {
		return fmt.Errorf("pembanding %s: %w", ModelSeamless, err)
	}
	off := int(c.OverlapFrom.Sub(c.Source.From) / (24 * time.Hour))
	n := int(c.OverlapTo.Sub(c.OverlapFrom)/(24*time.Hour)) + 1
	for i := range results {
		k := reanalysis.Key(results[i].Site.Requested)
		ref := p98(data[k][off : off+n])
		if ref > 0 {
			results[i].SeamlessRatio = p98(seamless[k]) / ref
		}
	}
	return nil
}

func p98(vs []float64) float64 {
	c := make([]float64, 0, len(vs))
	for _, v := range vs {
		if !math.IsNaN(v) {
			c = append(c, v)
		}
	}
	slices.Sort(c)
	return threshold.Percentile(c, 0.98)
}

// WriteThresholds menulis daftar ambang per titik pantau.
func WriteThresholds(w io.Writer, province string, src reanalysis.Source, results []Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ambang banjir titik pantau sungai provinsi %s (PRD, Aturan bisnis: Info p80, Waspada p90, Siaga p98, Bahaya p99,5).\n", province)
	fmt.Fprintf(&b, "# Debit harian (m³/s) Open-Meteo Flood API models=%s, %s..%s (%d hari), sel = kolom lat/lon.\n", src.Model, reanalysis.Date(src.From), reanalysis.Date(src.To), src.Days())
	b.WriteString("# Dibuat `make flood-threshold`; jangan diedit manual (ADR 0020).\n")
	fmt.Fprintf(&b, "# %d titik\n", len(results))
	cw := csv.NewWriter(&b)
	_ = cw.Write([]string{"id", "sungai", "nama", "lat", "lon", "p50", "p80", "p90", "p98", "p99_5", "maks_tahunan_median", "hari_berisi", "rasio_p98_seamless"})
	for _, r := range results {
		t := r.Thresholds
		_ = cw.Write([]string{
			strings.TrimPrefix(r.Site.ID, "river:"), r.Site.River, r.Site.Name, coord(r.Site.Requested.Lat), coord(r.Site.Requested.Lon),
			val(t.P50), val(t.Levels[0]), val(t.Levels[1]), val(t.Levels[2]), val(t.Levels[3]), val(r.MedianAnnualMax), fmt.Sprint(r.Valid), ratio(r.SeamlessRatio),
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
func WriteReport(w io.Writer, at time.Time, src reanalysis.Source, results []Result, checks []Check) error {
	var b strings.Builder
	b.WriteString("# Ambang banjir titik pantau sungai\n\n")
	fmt.Fprintf(&b, "Dibuat `make flood-threshold` pada %s. Jangan diedit manual; ubah daftar titik atau kejadian lalu jalankan ulang (ADR 0020).\n\n", at.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "Ambang adalah persentil debit harian reanalisis GloFAS v4 (Open-Meteo `models=%s`) %s sampai %s (%d hari) di sel setiap titik pantau. ",
		src.Model, reanalysis.Date(src.From), reanalysis.Date(src.To), src.Days())
	b.WriteString("Tingkat (PRD, Aturan bisnis): Info ≥ p80, Waspada ≥ p90, Siaga ≥ p98, Bahaya ≥ p99,5. Karena ambang dihitung dari hari kalender, rata-rata 73 hari per tahun di atas p80, 37 di atas p90, 7 di atas p98, dan 1,8 di atas p99,5 di setiap titik. ")
	b.WriteString("Debit GloFAS di sungai kecil dan di hilir waduk jauh dari debit terukur, jadi ambang hanya bermakna terhadap debit model yang sama, bukan terhadap pengukuran lapangan.\n\n")
	fmt.Fprintf(&b, "Kolom rasio adalah p98 `%s` dibagi p98 reanalisis di periode tumpang tindih: di bawah 1 berarti data prakiraan dan intermediate cenderung lebih rendah dari klimatologi sel itu, jadi tingkat bisa terlambat naik.\n\n", ModelSeamless)
	b.WriteString("| Titik | Sungai | Sel | p50 | p80 | p90 | p98 | p99,5 | Median puncak tahunan | Puncak tertinggi | Rasio p98 seamless |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range results {
		t := r.Thresholds
		fmt.Fprintf(&b, "| %s (`%s`) | %s | %s, %s | %s | %s | %s | %s | %s | %s | %s (%s) | %s |\n",
			r.Site.Name, strings.TrimPrefix(r.Site.ID, "river:"), r.Site.River, coord(r.Site.Requested.Lat), coord(r.Site.Requested.Lon),
			num(t.P50), num(t.Levels[0]), num(t.Levels[1]), num(t.Levels[2]), num(t.Levels[3]), num(r.MedianAnnualMax),
			num(r.Max.Value), reanalysis.Date(r.Max.Day), strings.Replace(ratio(r.SeamlessRatio), ".", ",", 1))
	}
	b.WriteString("\nSatuan m³/s.\n\n## Uji terhadap kejadian banjir tercatat\n\n")
	b.WriteString("Kejadian dari `docs/calibration/banjir-tercatat.csv`. Kejadian **di luar sampel** tidak ikut dipakai menghitung ambang. ")
	fmt.Fprintf(&b, "Kejadian dengan model `%s` memakai data gabungan Open-Meteo (intermediate dan prakiraan), bukan reanalisis: debitnya bisa berbeda dari reanalisis di hari yang sama, jadi hasilnya indikatif.\n\n", ModelSeamless)
	if len(checks) == 0 {
		b.WriteString("Tidak ada kejadian.\n")
	} else {
		b.WriteString("| Kejadian | Titik | Model | Sampel | Puncak (m³/s) | Tanggal puncak | Tingkat |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
		for _, c := range checks {
			sample := "di luar sampel"
			if c.InSample {
				sample = "di dalam sampel"
			}
			day := "-"
			if !math.IsNaN(c.Peak.Value) {
				day = reanalysis.Date(c.Peak.Day)
			}
			fmt.Fprintf(&b, "| [%s](%s) | `%s` | `%s` | %s | %s | %s | %s |\n",
				c.Event.ID, c.Event.Source, strings.TrimPrefix(c.Site.ID, "river:"), c.Event.Model, sample, num(c.Peak.Value), day, c.Level)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func readCSV(r io.Reader, header string) ([][]string, error) {
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
	cr.FieldsPerRecord = strings.Count(header, ",") + 1
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != header {
		return nil, fmt.Errorf("header harus %s", header)
	}
	return recs[1:], nil
}

// ratio menulis rasio dengan 2 desimal, kosong bila tidak dihitung.
func ratio(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	return fmt.Sprintf("%.2f", v)
}

func coord(v float64) string { return fmt.Sprintf("%.4f", v) }

// val menulis ambang dengan 2 desimal (titik desimal, untuk dibaca mesin).
func val(v float64) string { return fmt.Sprintf("%.2f", v) }

// num menulis angka dengan 1 desimal dan koma desimal (bahasa Indonesia).
func num(v float64) string {
	if math.IsNaN(v) {
		return "-"
	}
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}
