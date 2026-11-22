package feltcalib

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/gazetteer"
)

// Input adalah keterangan bahan untuk laporan.
type Input struct {
	BMKGEvents, DYFIEvents, DYFICells, DYFISuspect int
	Names, Boundaries, Aliases                     int
	NamesSource, BoundariesSource                  string
}

// Report adalah isi laporan kalibrasi.
type Report struct {
	Input     Input
	Mentions  []Mention
	Unparsed  []Unparsed
	Extents   []Extent
	Chosen    Fit
	Bootstrap Bootstrap
	// Service adalah rumus yang dipakai geo-processor (quake.DefaultPolicy).
	Service Formula
	Scores  []Score
	Bins    []Bin
}

func pct(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}
	return num(100*v, 1) + "%"
}

func km(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

func num(v float64, d int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', d, 64), ".", ",", 1)
}

// surfaceKm adalah radius permukaan √(R² − h²) (0 bila h ≥ R).
func surfaceKm(f Formula, m, h float64) float64 {
	r, hh := f.RadiusKm(m), math.Max(h, 0)
	if hh >= r {
		return 0
	}
	return math.Sqrt(r*r - hh*hh)
}

// WriteMarkdown menulis laporan docs/calibration/radius-dirasakan.md.
func WriteMarkdown(w io.Writer, r Report) error {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	in := r.Input
	p("# Kalibrasi radius dirasakan gempa")
	p("")
	p("Dihasilkan `make felt-radius` (ADR 0026); jangan diedit manual. Rumus: R = 10^(a·M − b) km (jarak hiposenter), R_permukaan = √(R² − h²).")
	p("")
	p("## Bahan")
	p("")
	matched, felt := 0, 0
	kinds := map[gazetteer.Kind]int{}
	for _, m := range r.Mentions {
		if m.Matched {
			matched++
			kinds[m.Match.Kind]++
			if m.MMI() >= FeltMMI {
				felt++
			}
		}
	}
	p("- BMKG: %d gempa dirasakan dari arsip `gempadirasakan`, %d penyebutan wilayah (%d cocok, %d berskala ≥ II), %d bagian tidak terbaca; %d gempa dengan wilayah dirasakan yang cocok dipakai untuk kalibrasi.",
		in.BMKGEvents, len(r.Mentions), matched, felt, len(r.Unparsed), len(r.Extents))
	p("- Gazetir: %d nama Kemendagri (%s), %d batas provinsi/kab/kota/kecamatan (%s), %d alias (`gazetir-alias.csv`). Desa/kelurahan memakai batas kecamatan induknya, jadi jaraknya batas bawah.",
		in.Names, in.NamesSource, in.Boundaries, in.BoundariesSource, in.Aliases)
	p("- DYFI USGS: %d kejadian, %d sel (%d mencurigakan, tidak dipakai). Sel CDI ≥ 2 dirasakan, CDI < 2 tidak dirasakan.", in.DYFIEvents, in.DYFICells, in.DYFISuspect)
	p("")
	p("Cara cocok nama: %s.", kindSummary(kinds))
	p("")

	p("## Rumus terpilih")
	p("")
	c := r.Chosen
	p("Regresi kuantil τ = %s pada log10 jarak hiposenter wilayah dirasakan terjauh per gempa BMKG (%d gempa), a dicari di [%s; %s] langkah %s: **a = %s, b = %s**. Rumus layanan (`quake.DefaultPolicy`): **a = %s, b = %s**.",
		num(c.Tau, 2), c.Events, num(slopeMin, 2), num(slopeMax, 2), num(slopeStep, 3), num(c.Slope, 3), num(c.Intercept, 3), num(r.Service.Slope, 2), num(r.Service.Intercept, 2))
	p("")
	head := []string{"Magnitudo"}
	for _, m := range Magnitudes {
		head = append(head, "M"+num(m, 0))
	}
	p("| %s |", strings.Join(head, " | "))
	p("|%s", strings.Repeat(" --- |", len(head)))
	row := func(name string, f func(m float64) string) {
		cells := []string{name}
		for _, m := range Magnitudes {
			cells = append(cells, f(m))
		}
		p("| %s |", strings.Join(cells, " | "))
	}
	row("PRD (km)", func(m float64) string { return km(PRDFormula.RadiusKm(m)) })
	row("Hasil fit (km)", func(m float64) string { return km(c.RadiusKm(m)) })
	row("Layanan (km)", func(m float64) string { return km(r.Service.RadiusKm(m)) })
	if bs := r.Bootstrap; bs.Samples > 0 {
		i := 0
		row(fmt.Sprintf("Bootstrap 5–95%% (%d sampel)", bs.Samples), func(float64) string {
			iv := bs.Radius[i]
			i++
			return km(iv.Low) + "–" + km(iv.High)
		})
		p("")
		p("Bootstrap kemiringan a: %s–%s.", num(bs.Slope.Low, 3), num(bs.Slope.High, 3))
	}
	p("")

	p("## Pembanding rumus")
	p("")
	p("BMKG kejadian: bagian gempa yang wilayah dirasakan terjauhnya di dalam R. BMKG penyebutan: bagian penyebutan dirasakan di dalam R (bobot sama per gempa). DYFI: bobot sama per kejadian; \"tidak dirasakan di luar\" adalah satu-satunya ukuran radius yang kebesaran.")
	p("")
	p("| Rumus | a | b | R M5 (km) | BMKG kejadian | BMKG penyebutan | DYFI dirasakan di dalam | DYFI M ≥ 6 dirasakan di dalam | DYFI tidak dirasakan di luar |")
	p("| --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, s := range r.Scores {
		p("| %s | %s | %s | %s | %s | %s | %s | %s | %s |", s.Name, num(s.Slope, 3), num(s.Intercept, 3), km(s.RadiusKm(5)),
			pct(s.BMKGEvents), pct(s.BMKGMentions), pct(s.DYFIFelt), pct(s.DYFIFeltLarge), pct(s.DYFINotFelt))
	}
	p("")

	p("## Per magnitudo (rumus layanan)")
	p("")
	p("| M | Gempa BMKG | Terjauh di dalam R | Median terjauh (km) | Kejadian DYFI | Sel dirasakan di dalam R | Median sel dirasakan (km) | Sel tidak dirasakan di dalam R |")
	p("| --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, bn := range r.Bins {
		label := binLabel(bn)
		p("| %s | %d | %s | %s | %d | %s | %s | %s |", label, bn.BMKGEvents, frac(bn.BMKGCovered, bn.BMKGEvents), km(bn.MedianFarthestKm),
			bn.DYFIEvents, frac(bn.DYFIFeltIn, bn.DYFIFelt), km(bn.MedianDYFIKm), frac(bn.DYFINotFeltIn, bn.DYFINotFelt))
	}
	p("")

	out := Outside(r.Service, r.Extents)
	p("## Gempa BMKG dengan wilayah dirasakan di luar R (rumus layanan)")
	p("")
	if len(out) == 0 {
		p("Tidak ada.")
	} else {
		p("| Gempa (UTC) | M | Kedalaman (km) | Wilayah terjauh | MMI | Kode | Jarak tepi (km) | Jarak hiposenter (km) | R (km) | R permukaan (km) |")
		p("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
		for _, e := range out {
			m := e.Farthest
			p("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |", e.Event.OccurredAt.Format("2006-01-02 15:04"), num(e.Event.Magnitude, 1), km(e.Event.DepthKm),
				escape(m.Name), mmiText(m.MMIMin, m.MMIMax), m.Match.Code, km(m.Match.DistanceKm), km(m.HypocentralKm),
				km(r.Service.RadiusKm(e.Event.Magnitude)), km(surfaceKm(r.Service, e.Event.Magnitude, e.Event.DepthKm)))
		}
	}
	p("")

	p("## Nama yang tidak cocok")
	p("")
	unmatched := map[string]int{}
	for _, m := range r.Mentions {
		if !m.Matched {
			unmatched[m.Name]++
		}
	}
	if len(unmatched) == 0 && len(r.Unparsed) == 0 {
		p("Tidak ada.")
	} else {
		names := make([]string, 0, len(unmatched))
		for n := range unmatched {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			p("- %s (%d×): tambahkan ke `gazetir-alias.csv` bila wilayahnya jelas", escape(n), unmatched[n])
		}
		for _, u := range r.Unparsed {
			p("- bagian tidak terbaca di %s: %q", u.EventID, u.Text)
		}
	}
	p("")
	p("## Sumber")
	p("")
	p("- BMKG, gempa dirasakan (`gempadirasakan.json`), https://data.bmkg.go.id/gempabumi/")
	p("- U.S. Geological Survey, Did You Feel It? (domain publik), https://earthquake.usgs.gov/data/dyfi/")
	p("- Nama dan batas wilayah: cahyadsn/wilayah dan cahyadsn/wilayah_boundaries (MIT), mengacu Kepmendagri No. 300.2.2-2430 Tahun 2025")
	_, err := io.WriteString(w, b.String())
	return err
}

func kindSummary(k map[gazetteer.Kind]int) string {
	order := []gazetteer.Kind{gazetteer.KindExact, gazetteer.KindCompact, gazetteer.KindTownAsSub, gazetteer.KindQualified, gazetteer.KindAlias}
	var parts []string
	for _, kind := range order {
		if k[kind] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", kind, k[kind]))
		}
	}
	if len(parts) == 0 {
		return "tidak ada"
	}
	return strings.Join(parts, ", ")
}

func binLabel(b Bin) string {
	switch {
	case math.IsInf(b.From, -1):
		return "< " + num(b.To, 0)
	case math.IsInf(b.To, 1):
		return "≥ " + num(b.From, 0)
	default:
		return num(b.From, 0) + "–" + num(b.To, 0)
	}
}

func frac(a, n int) string {
	if n == 0 {
		return "–"
	}
	return fmt.Sprintf("%d/%d (%s)", a, n, pct(float64(a)/float64(n)))
}

func mmiText(lo, hi int) string {
	roman := []string{"", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII"}
	if lo == hi {
		return roman[lo]
	}
	return roman[lo] + "–" + roman[hi]
}

func escape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// MatchHeader adalah kolom dirasakan-bmkg-cocok.csv.
var MatchHeader = []string{
	"bmkg_id", "magnitudo", "kedalaman_km", "mmi_min", "mmi_maks", "nama", "cara", "kode", "nama_kemendagri",
	"kandidat", "kode_batas", "jarak_tepi_km", "jarak_hiposenter_km",
}

// WriteMatches menulis hasil pencocokan setiap penyebutan, untuk diperiksa.
func WriteMatches(w io.Writer, mentions []Mention) error {
	if _, err := io.WriteString(w, "# Hasil pencocokan wilayah dirasakan BMKG ke kode Kemendagri (make felt-radius, ADR 0026). Jangan diedit manual.\n"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(MatchHeader); err != nil {
		return err
	}
	f := func(v float64, d int) string { return strconv.FormatFloat(v, 'f', d, 64) }
	sorted := slices.Clone(mentions)
	slices.SortStableFunc(sorted, func(a, b Mention) int { return cmp.Compare(a.Event.ID, b.Event.ID) })
	for _, m := range sorted {
		rec := []string{m.Event.ID, f(m.Event.Magnitude, 1), f(m.Event.DepthKm, 0), strconv.Itoa(m.MMIMin), strconv.Itoa(m.MMIMax), m.Name}
		if m.Matched {
			rec = append(rec, string(m.Match.Kind), m.Match.Code, m.Match.Name, strconv.Itoa(m.Match.Candidates), m.Match.BoundaryCode,
				f(m.Match.DistanceKm, 1), f(m.HypocentralKm, 1))
		} else {
			rec = append(rec, "tidak-cocok", "", "", "0", "", "", "")
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
