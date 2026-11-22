package feltcalib

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/gazetteer"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func box(lon0, lat0, lon1, lat1 float64) region.MultiPolygon {
	return region.MultiPolygon{{region.Ring{{lon0, lat0}, {lon1, lat0}, {lon1, lat1}, {lon0, lat1}, {lon0, lat0}}}}
}

// Wilayah uji: kabupaten 1°×1° di sekitar episenter (0, 100), kecamatan
// "Timur" 1° di timur tepi kabupaten, desa "Ganda" di dua kecamatan.
func fixture(t *testing.T) (*gazetteer.Index, Boundaries) {
	t.Helper()
	ix, err := gazetteer.New([]gazetteer.Entry{
		{Code: "11", Name: "Prov Uji"},
		{Code: "11.01", Name: "Kabupaten Pusat"},
		{Code: "11.01.01", Name: "Timur"},
		{Code: "11.01.01.2001", Name: "Ganda"},
		{Code: "11.02", Name: "Kabupaten Jauh"},
		{Code: "11.02.01", Name: "Barat"},
		{Code: "11.02.01.2001", Name: "Ganda"},
	}, []gazetteer.Alias{{Name: "Kota Lama", Code: "11.01.01", Reason: "uji"}})
	if err != nil {
		t.Fatal(err)
	}
	return ix, Boundaries{
		"11.01":    box(99.5, -0.5, 100.5, 0.5),
		"11.01.01": box(101.5, -0.5, 102, 0.5),
		"11.02":    box(110, -0.5, 111, 0.5),
		"11.02.01": box(110, -0.5, 110.5, 0.5),
	}
}

func event(id string, m, h float64, felt string) ports.BMKGFelt {
	return ports.BMKGFelt{FeltEvent: ports.FeltEvent{ID: id, OccurredAt: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), Magnitude: m, DepthKm: h, Longitude: 100}, Felt: felt}
}

func TestMatchAndExtents(t *testing.T) {
	ix, b := fixture(t)
	evs := []ports.BMKGFelt{
		event("a", 5, 10, "III Pusat, II Ganda, I-II Kota Lama, II Tidak Ada, rusak"),
		event("b", 3, 30, "II Kab. Pusat"),
		event("c", 2, 5, "I Pusat"),
	}
	mentions, bad := MatchBMKG(evs, ix, b)
	if len(mentions) != 6 || len(bad) != 1 || bad[0].EventID != "a" || bad[0].Text != "rusak" {
		t.Fatalf("%d penyebutan, tidak terbaca %+v", len(mentions), bad)
	}
	ganda := mentions[1]
	east := region.HaversineKm(0, 100, 0, 101.5)
	if !ganda.Matched || ganda.Match.Code != "11.01.01.2001" || ganda.Match.BoundaryCode != "11.01.01" || ganda.Match.Candidates != 2 ||
		math.Abs(ganda.Match.DistanceKm-east) > 0.01 || math.Abs(ganda.HypocentralKm-math.Hypot(east, 10)) > 0.01 {
		t.Fatalf("nama ganda: %+v", ganda)
	}
	if mentions[0].HypocentralKm != 10 || mentions[3].Matched || mentions[2].Match.Kind != gazetteer.KindAlias {
		t.Fatalf("%+v", mentions)
	}
	ext := Extents(mentions)
	if len(ext) != 2 || ext[0].Event.ID != "a" || ext[0].Felt != 2 || ext[0].Farthest.Name != "Ganda" || ext[1].Farthest.HypocentralKm != 30 {
		t.Fatalf("%+v", ext)
	}
}

// synthetic membuat gempa M 2–7 dengan wilayah terjauh tepat di garis
// log10 R = 0,4M − 0,2, kecuali sebagian kecil di atasnya.
func synthetic(n int) []Extent {
	var out []Extent
	for i := range n {
		m := 2 + 5*float64(i)/float64(n-1)
		r := math.Pow(10, 0.4*m-0.2)
		if i%10 == 0 {
			r *= 3
		}
		out = append(out, Extent{Event: event("e", m, 0, ""), Farthest: Mention{HypocentralKm: r}})
	}
	return out
}

func TestFitQuantile(t *testing.T) {
	f, err := FitQuantile(synthetic(101), 0.85)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(f.Slope-0.4) > 0.006 || math.Abs(f.Intercept-0.2) > 0.03 || f.Events != 101 || f.Tau != 0.85 {
		t.Fatalf("%+v", f)
	}
	if _, err := FitQuantile(synthetic(10), 1); err == nil {
		t.Fatal("τ = 1 harus galat")
	}
	if _, err := FitQuantile(nil, 0.9); err == nil {
		t.Fatal("tanpa data harus galat")
	}
	bs, err := BootstrapFit(synthetic(101), 0.85, 50, 26)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := BootstrapFit(synthetic(101), 0.85, 50, 26)
	if bs.Samples != 50 || len(bs.Radius) != len(Magnitudes) || bs.Slope.Low > 0.4 || bs.Slope.High < 0.39 || bs.Radius[2] != again.Radius[2] {
		t.Fatalf("%+v", bs)
	}
	if _, err := BootstrapFit(nil, 0.9, 10, 1); err == nil {
		t.Fatal("tanpa data harus galat")
	}
}

func TestEvaluateBinsOutside(t *testing.T) {
	ix, b := fixture(t)
	mentions, _ := MatchBMKG([]ports.BMKGFelt{event("a", 5, 10, "III Pusat, II Ganda"), event("b", 3, 30, "II Kab. Pusat")}, ix, b)
	ext := Extents(mentions)
	cell := func(id string, m, cdi, r float64, suspect bool) ports.DYFICell {
		return ports.DYFICell{FeltEvent: ports.FeltEvent{ID: id, Magnitude: m}, CDI: cdi, HypocentralKm: r, Responses: 1, Suspect: suspect}
	}
	cells := []ports.DYFICell{
		cell("u1", 5, 3, 50, false), cell("u1", 5, 2, 500, false), cell("u1", 5, 1, 600, false), cell("u1", 5, 4, 1, true),
		cell("u2", 6.5, 2.5, 100, false), cell("u2", 6.5, 1, 20, false),
	}
	f := FromPolicy(quake.DefaultPolicy())
	s := Evaluate("Layanan", f, ext, mentions, cells)
	// R(5) ≈ 162 km: wilayah terjauh gempa a (±167 km) di luar, gempa b di dalam.
	if s.BMKGEvents != 0.5 || s.BMKGMentions != 0.75 || math.Abs(s.DYFIFelt-0.75) > 1e-9 || s.DYFIFeltLarge != 1 || s.DYFINotFelt != 0.5 {
		t.Fatalf("%+v", s)
	}
	if z := Evaluate("x", f, nil, nil, nil); !math.IsNaN(z.BMKGEvents) || !math.IsNaN(z.DYFIFelt) {
		t.Fatalf("tanpa data harus NaN: %+v", z)
	}
	bins := Bins(f, ext, cells)
	if len(bins) != 6 || bins[1].BMKGEvents != 1 || bins[3].DYFIFelt != 2 || bins[3].DYFIFeltIn != 1 || bins[3].DYFINotFelt != 1 ||
		bins[4].DYFIEvents != 1 || bins[4].DYFINotFeltIn != 1 || !math.IsNaN(bins[0].MedianFarthestKm) {
		t.Fatalf("%+v", bins)
	}
	out := Outside(f, ext)
	if len(out) != 1 || out[0].Event.ID != "a" {
		t.Fatalf("%+v", out)
	}

	rep := Report{
		Input:    Input{BMKGEvents: 2, DYFIEvents: 2, DYFICells: len(cells), DYFISuspect: 1, Names: ix.Len(), Boundaries: len(b), NamesSource: "uji", BoundariesSource: "uji"},
		Mentions: append(mentions, Mention{Event: event("c", 4, 0, ""), FeltMention: quake.FeltMention{MMIMin: 2, MMIMax: 2, Name: "Entah | Mana"}}),
		Unparsed: []Unparsed{{EventID: "c", Text: "rusak"}}, Extents: ext,
		Chosen: Fit{Formula: f, Tau: 0.9, Events: 2}, Bootstrap: Bootstrap{Samples: 2, Slope: Interval{0.3, 0.4}, Radius: make([]Interval, len(Magnitudes))},
		Service: f, Scores: []Score{s}, Bins: bins,
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	md := buf.String()
	for _, want := range []string{
		"# Kalibrasi radius dirasakan gempa", "**a = 0,350, b = -0,460**", "| Layanan | 0,350 | -0,460 | 162 | 50,0% | 75,0% | 75,0% | 100,0% | 50,0% |",
		"| ≥ 7 |", "| < 3 |", "Entah \\| Mana (1×)", `bagian tidak terbaca di c: "rusak"`, "Bootstrap kemiringan a: 0,300–0,400.", "| Ganda | II |", "nama 3",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("laporan tanpa %q:\n%s", want, md)
		}
	}
	buf.Reset()
	if err := WriteMatches(&buf, rep.Mentions); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "a,5.0,10,2,2,Ganda,nama,11.01.01.2001,Ganda,2,11.01.01,") || !strings.Contains(buf.String(), "c,4.0,0,2,2,Entah | Mana,tidak-cocok,,,0,,,") {
		t.Fatal(buf.String())
	}
	if surfaceKm(f, 3, 1000) != 0 || kindSummary(nil) != "tidak ada" || mmiText(3, 3) != "III" || mmiText(2, 3) != "II–III" || frac(0, 0) != "–" {
		t.Fatal("pembantu laporan")
	}
	buf.Reset()
	rep.Unparsed, rep.Mentions = nil, mentions
	rep.Service = Formula{Slope: 1, Intercept: -5}
	if err := WriteMarkdown(&buf, rep); err != nil || strings.Count(buf.String(), "Tidak ada.") != 2 {
		t.Fatalf("laporan tanpa temuan: %v\n%s", err, buf.String())
	}
}
