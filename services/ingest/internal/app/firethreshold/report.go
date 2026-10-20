package firethreshold

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/firecluster"
)

// Coverage adalah rentang arsip satu produk yang dianalisis.
type Coverage struct {
	Satellite, Product string
	From, To           time.Time
}

// TopEvents adalah jumlah kejadian terbesar yang dicantumkan di laporan.
const TopEvents = 25

// TopStatic adalah jumlah sumber panas tetap yang dirinci di laporan.
const TopStatic = 40

func wib(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(WIB).Format("2006-01-02 15.04")
}

func pct(a, b int) string {
	if b == 0 {
		return "-"
	}
	return strconv.FormatFloat(100*float64(a)/float64(b), 'f', 0, 64) + "%"
}

// WriteReport menulis laporan kalibrasi Markdown.
func WriteReport(w io.Writer, generated time.Time, box string, cov []Coverage, res Result) error {
	b := bufio.NewWriter(w)
	p := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	p("# Kalibrasi titik api dari arsip FIRMS\n\n")
	p("Dibuat `make fire-threshold` pada %s WIB (ADR 0023). Jangan diedit manual.\n\n", generated.In(WIB).Format("2006-01-02 15.04"))
	p("Kotak %s, deteksi %s s.d. %s WIB. Arsip standard processing (SP) dipakai selama tersedia, lalu near real-time (NRT).\n\n", box, wib(res.From), wib(res.To))
	p("| Wahana | Produk | Dari | Sampai |\n| --- | --- | --- | --- |\n")
	for _, c := range cov {
		p("| %s | `%s` | %s | %s |\n", c.Satellite, c.Product, c.From.Format(time.DateOnly), c.To.Format(time.DateOnly))
	}

	p("\n## Inventaris deteksi\n\n")
	p("Keyakinan VIIRS: rendah (glint/anomali < 15 K), nominal, tinggi (piksel jenuh). MODIS dipetakan dari persen (< 30, 30–79, ≥ 80). Kolom type hanya ada di SP: 0 vegetasi, 1 gunung api, 2 sumber tetap lain, 3 lepas pantai.\n\n")
	p("| Wahana | Tahun | Deteksi | Rendah | Nominal | Tinggi | Malam | type 0 | type 1 | type 2 | type 3 | tanpa type |\n")
	p("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, r := range res.Inventory {
		p("| %s | %d | %d | %d | %d | %d | %s | %d | %d | %d | %d | %d |\n", r.Satellite, r.Year, r.Detections,
			r.ByConfidence[fire.Low], r.ByConfidence[fire.Nominal], r.ByConfidence[fire.High], pct(r.Night, r.Detections),
			r.Typed[0], r.Typed[1], r.Typed[2], r.Typed[3], r.Untyped)
	}

	total := 0
	for _, m := range res.Monthly {
		total += m
	}
	p("\n## Sebaran bulanan (di luar sumber panas tetap, WIB)\n\n")
	p("| Jan | Feb | Mar | Apr | Mei | Jun | Jul | Agu | Sep | Okt | Nov | Des |\n| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n|")
	for _, m := range res.Monthly {
		p(" %s |", pct(m, total))
	}
	p("\n")

	r := res.StaticRule
	p("\n## Sumber panas tetap\n\n")
	p("Sel %.3f° dinilai bersama 8 tetangganya. Persisten: ≥ %d hari, ≥ %d tahun, ≥ %d bulan kalender berbeda, dan ≥ %.0f%% deteksi malam. Type FIRMS: ≥ %d deteksi SP dan ≥ %.0f%% di antaranya type 1/2. Sel kandidat yang bersebelahan digabung; jari-jari = anggota terjauh + %.2f km (minimal %.2f km).\n\n",
		r.CellDeg, r.MinDays, r.MinYears, r.MinMonths, 100*r.MinNightShare, r.MinTyped, 100*r.TypedShare, r.MarginKm, r.MinRadiusKm)
	all := 0
	for _, row := range res.Inventory {
		all += row.Detections
	}
	p("**%d sumber**, menyaring %d dari %d deteksi (%s). Daftar lengkap di `sumber-panas-tetap-32.csv`, peta di `sumber-panas-tetap-32.geojson` (bisa dibuka di GitHub atau geojson.io).\n\n",
		len(res.Static), res.Masked, all, pct(res.Masked, all))
	if len(res.Static) > 0 {
		p("| Pusat | Jari-jari | Alasan | Deteksi | Hari | Tahun | Bulan | type 1/2 | Malam | FRP rata | Pertama | Terakhir |\n")
		p("| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- |\n")
		for _, s := range res.Static[:min(TopStatic, len(res.Static))] {
			p("| %s | %.2f km | %s | %d | %d | %d | %d | %d | %s | %.1f MW | %s | %s |\n", s.ID(), s.RadiusKm, s.Reason, s.Detections, s.Days, s.Years, s.Months,
				s.Typed[fire.ActiveVolcano]+s.Typed[fire.StaticSource], pct(s.Night, s.Detections), s.MeanFRPMW,
				s.First.In(WIB).Format(time.DateOnly), s.Last.In(WIB).Format(time.DateOnly))
		}
	}

	p("\n## Kejadian per varian aturan\n\n")
	p("Deteksi dikelompokkan menjadi kejadian bila berjarak ≤ jarak sambung dan berselang ≤ jeda dari anggota lain (berantai). Tingkat dinilai di setiap deteksi atas jendela 24 jam: Info bila ada deteksi, Waspada bila ada deteksi dengan keyakinan minimal aturan, Siaga bila hitungan ≥ ambang. Tahun menurut awal kejadian (WIB).\n\n")
	p("| Varian | Wahana dibuang | Saringan | Sambung | Waspada mulai | Siaga | Deteksi dinilai | Kejadian | Info | Waspada | Siaga |\n")
	p("| --- | --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, vr := range res.Variants {
		v := vr.Variant
		t := vr.Total()
		mask := "ya"
		if v.NoMask {
			mask = "tidak"
		}
		p("| %s | %s | %s | %.1f km / %.0f jam | %s | ≥ %d %s | %d | %d | %d | %d | %d |\n", v.Name, orDash(strings.Join(v.Exclude, ", ")), mask,
			v.Cluster.LinkKm, v.Cluster.Gap.Hours(), v.Level.WaspadaMin, v.Level.SiagaCount, v.Level.Unit, vr.Points, len(vr.Events),
			t[firecluster.Info], t[firecluster.Waspada], t[firecluster.Siaga])
	}
	years := map[int]bool{}
	for _, vr := range res.Variants {
		for y := range vr.ByYear {
			years[y] = true
		}
	}
	if len(res.Variants) > 0 {
		p("\nKejadian Waspada / Siaga per tahun:\n\n| Tahun |")
		for i := range res.Variants {
			p(" V%d |", i+1)
		}
		p("\n| ---: |")
		for range res.Variants {
			p(" ---: |")
		}
		p("\n")
		for _, y := range slices.Sorted(maps.Keys(years)) {
			p("| %d |", y)
			for _, vr := range res.Variants {
				c := vr.ByYear[y]
				p(" %d / %d |", c[firecluster.Waspada], c[firecluster.Siaga])
			}
			p("\n")
		}
		p("\n")
		for i, vr := range res.Variants {
			p("V%d = %s. ", i+1, vr.Variant.Name)
		}
		p("\n")

		def := res.Variants[0]
		p("\n## %d kejadian terbesar (%s)\n\n", TopEvents, def.Variant.Name)
		p("| Mulai (WIB) | Selesai | Pusat | Deteksi | Puncak 24 jam | Tingkat | Wahana |\n| --- | --- | --- | ---: | ---: | --- | --- |\n")
		for _, ev := range def.Top(TopEvents) {
			p("| %s | %s | %.4f,%.4f | %d | %d | %s | %s |\n", wib(ev.Start), wib(ev.End), ev.Lat, ev.Lon, ev.Detections, ev.Peak, ev.Level, strings.Join(ev.Satellites, ", "))
		}
	}

	p("\n## Uji kebakaran tercatat\n\n")
	if len(res.Checks) == 0 {
		p("Tidak ada daftar kebakaran tercatat.\n")
	} else {
		p("Kejadian dihitung bila punya deteksi di dalam radius dari sehari sebelum tanggal mulai sampai dua hari setelah tanggal selesai (WIB). Isi sel: tingkat tertinggi, deteksi di radius, deteksi pertama; \"tersaring\" adalah deteksi di radius yang jatuh di sumber panas tetap.\n\n")
		p("| Kebakaran | Tanggal | Luas |")
		for i := range res.Variants {
			p(" V%d |", i+1)
		}
		p("\n| --- | --- | ---: |")
		for range res.Variants {
			p(" --- |")
		}
		p("\n")
		for _, c := range res.Checks {
			f := c.Fire
			area := "-"
			if f.AreaHa > 0 {
				area = strconv.FormatFloat(f.AreaHa, 'f', -1, 64) + " ha"
			}
			p("| [%s](%s) | %s s.d. %s | %s |", f.ID, f.Source, f.From.Format("2006-01-02"), f.To.Format("01-02"), area)
			for _, cr := range c.Results {
				cell := "tidak terdeteksi"
				if cr.Found {
					cell = fmt.Sprintf("%s, %d deteksi, pertama %s", cr.Level, cr.Detections, wib(cr.FirstAt))
				}
				if cr.Masked > 0 {
					cell += fmt.Sprintf(" (%d tersaring)", cr.Masked)
				}
				p(" %s |", cell)
			}
			p("\n")
		}
	}
	return b.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// staticColumns adalah header sumber-panas-tetap-32.csv.
var staticColumns = []string{
	"id", "lintang", "bujur", "radius_km", "alasan", "sel", "deteksi", "hari", "tahun", "bulan",
	"type_vegetasi", "type_gunung_api", "type_sumber_tetap", "type_lepas_pantai", "malam", "frp_rata_mw", "pertama", "terakhir",
}

func ff(v float64, prec int) string { return strconv.FormatFloat(v, 'f', prec, 64) }

// WriteStatic menulis daftar sumber panas tetap sebagai CSV (urut deteksi).
func WriteStatic(w io.Writer, static []firecluster.Source) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(staticColumns); err != nil {
		return err
	}
	for _, s := range static {
		rec := []string{
			s.ID(), ff(s.Lat, 5), ff(s.Lon, 5), ff(s.RadiusKm, 2), string(s.Reason), strconv.Itoa(s.Cells),
			strconv.Itoa(s.Detections), strconv.Itoa(s.Days), strconv.Itoa(s.Years), strconv.Itoa(s.Months),
			strconv.Itoa(s.Typed[0]), strconv.Itoa(s.Typed[1]), strconv.Itoa(s.Typed[2]), strconv.Itoa(s.Typed[3]),
			strconv.Itoa(s.Night), ff(s.MeanFRPMW, 1), s.First.UTC().Format(time.DateOnly), s.Last.UTC().Format(time.DateOnly),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteGeoJSON menulis sumber panas tetap sebagai titik GeoJSON dengan gaya
// simplestyle (geojson.io, GitHub): merah persisten, ungu type FIRMS, hitam
// keduanya.
func WriteGeoJSON(w io.Writer, static []firecluster.Source) error {
	type feature struct {
		Type       string         `json:"type"`
		Geometry   map[string]any `json:"geometry"`
		Properties map[string]any `json:"properties"`
	}
	color := map[firecluster.Reason]string{
		firecluster.ReasonPersistent: "#d62728", firecluster.ReasonFIRMS: "#9467bd", firecluster.ReasonBoth: "#111111",
	}
	fc := struct {
		Type     string    `json:"type"`
		Features []feature `json:"features"`
	}{Type: "FeatureCollection", Features: []feature{}}
	for _, s := range static {
		fc.Features = append(fc.Features, feature{
			Type:     "Feature",
			Geometry: map[string]any{"type": "Point", "coordinates": []float64{s.Lon, s.Lat}},
			Properties: map[string]any{
				"id": s.ID(), "radius_km": s.RadiusKm, "alasan": string(s.Reason), "deteksi": s.Detections, "hari": s.Days,
				"tahun": s.Years, "bulan": s.Months, "malam": s.Night, "frp_rata_mw": s.MeanFRPMW,
				"type_gunung_api": s.Typed[fire.ActiveVolcano], "type_sumber_tetap": s.Typed[fire.StaticSource],
				"pertama": s.First.UTC().Format(time.DateOnly), "terakhir": s.Last.UTC().Format(time.DateOnly),
				"marker-color": color[s.Reason], "marker-size": markerSize(s.Detections),
			},
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(fc)
}

func markerSize(n int) string {
	switch {
	case n >= 200:
		return "large"
	case n >= 50:
		return "medium"
	}
	return "small"
}
