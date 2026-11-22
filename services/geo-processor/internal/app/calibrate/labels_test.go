package calibrate

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

func lab(basis, label string, src quake.Source, id string, dt time.Duration, lat, lon, mag float64, typ string) Labeled {
	return Labeled{
		Basis: basis, Label: label, Key: quake.IdentityKey{Source: src, EventID: id},
		Origin:  quake.Origin{OccurredAt: t0.Add(dt), Latitude: lat, Longitude: lon, Magnitude: mag},
		DepthKm: 10, MagnitudeType: typ,
	}
}

// labelScenario: A cocok (Jawa Barat), B berselisih 167 km, C dan D gempa
// susulan berselang 140 dtk, E USGS tanpa pasangan dengan BMKG gempa lain
// di dekatnya, G USGS dengan dua BMKG dari gempa berbeda yang lolos ambang
// revisi (simulasi memilih revisi terbaru yang salah), H dan I gempa kembar
// berselang 12 dan 25 dtk yang hanya pertamanya dilaporkan BMKG, J gempa
// M 7,1 yang waktu BMKG-nya berselisih 22 dtk, dan satu pasangan arsip.
func labelScenario() []Labeled {
	b, u := quake.SourceBMKG, quake.SourceUSGS
	h := time.Hour
	return []Labeled{
		lab("isc", "A", b, "bA", 0, -7, 107, 5.0, "Mw"),
		lab("isc", "A", u, "uA", 2*time.Second, -7.1, 107.1, 5.2, "Mww"),
		lab("isc", "B", b, "bB", h, 3, 96, 4.8, "MLv"),
		lab("isc", "B", u, "uB", h+3*time.Second, 3, 97.5, 4.6, "mb"),
		lab("isc", "C", b, "bC", 2*h, 2.8, 127.1, 5.5, "MLv"),
		lab("isc", "C", u, "uC", 2*h+time.Second, 2.82, 127.0, 5.1, "mb"),
		lab("isc", "D", b, "bD", 2*h+140*time.Second, 2.83, 127.11, 4.7, "MLv"),
		lab("isc", "D", u, "uD", 2*h+141*time.Second, 2.6, 126.9, 4.4, "mb"),
		lab("isc", "E", u, "uE", 3*h, -9, 115, 4.5, "mb"),
		lab("isc", "F", b, "bF", 3*h+5*time.Second, -9.2, 115.1, 4.3, "MLv"),
		lab("isc", "G1", b, "bG1", 4*h, -3, 120, 5.0, "MLv"),
		lab("isc", "G1", u, "uG", 4*h+time.Second, -3, 120.045, 5.0, "mb"),
		lab("isc", "G2", b, "bG2", 4*h+4*time.Second, -3, 120.18, 5.0, "MLv"),
		lab("isc", "H1", b, "bH1", 6*h, 4, 96, 4.9, "MLv"),
		lab("isc", "H1", u, "uH1", 6*h+time.Second, 4, 96.05, 4.8, "mb"),
		lab("isc", "H2", u, "uH2", 6*h+12*time.Second, 4.05, 96.1, 4.7, "mb"),
		lab("isc", "I1", b, "bI1", 7*h, -5, 130, 5.3, "Mw"),
		lab("isc", "I1", u, "uI1", 7*h+time.Second, -5, 130.05, 5.2, "Mww"),
		lab("isc", "I2", u, "uI2", 7*h+25*time.Second, -5.05, 130.1, 5.0, "mb"),
		lab("isc", "J", b, "bJ", 8*h, 6.2, 127, 7.15, "Mw"),
		lab("isc", "J", u, "uJ", 8*h+22*time.Second, 6.18, 127.01, 7.1, "Mww"),
		lab("arsip", "arsip-01", b, "20260924002635", 5*h, -1.6, 138.93, 4.3, ""),
		lab("arsip", "arsip-01", u, "us6000tx7g", 5*h+3*time.Second, -1.81, 138.78, 4.6, "mb"),
	}
}

func TestEvaluateLabels(t *testing.T) {
	rules := DefaultLabelRules()
	rep, err := EvaluateLabels(labelScenario(), rules, DefaultSubsets(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Bases) != 2 || rep.Bases[0].Basis != "isc" || rep.Bases[1].Basis != "arsip" {
		t.Fatalf("basis %+v", rep.Bases)
	}
	isc := rep.Bases[0]
	if isc.BMKG != 10 || isc.USGS != 11 || isc.Positive != 8 || isc.Labels != 13 {
		t.Fatalf("ringkasan %+v", isc)
	}
	def := isc.Results[0]
	if def.Rule.Rules.CrossSource != quake.DefaultCrossSourceRule {
		t.Fatalf("ambang pertama %+v", def.Rule)
	}
	if p := def.Pair[0]; p != (Score{USGS: 11, Positive: 8, TP: 7, FP: 2, FN: 1}) {
		t.Fatalf("pasangan %+v", p)
	}
	if s := def.Sim[0]; s != (Score{USGS: 11, Positive: 8, TP: 6, FP: 2, FN: 2}) {
		t.Fatalf("simulasi %+v", s)
	}
	if def.SameSourceBMKG != 1 || def.SameSourceUSGS != 0 {
		t.Fatalf("sumber sama %d %d", def.SameSourceBMKG, def.SameSourceUSGS)
	}
	if def.FalsePerThousandMinutes <= 0 {
		t.Fatal("geseran menit harus menemukan gempa susulan C/D")
	}
	if jb := def.Pair[3]; jb != (Score{USGS: 1, Positive: 1, TP: 1}) {
		t.Fatalf("Jawa Barat %+v", jb)
	}
	if m45 := def.Pair[2]; m45.USGS != 10 || m45.TP != 6 {
		t.Fatalf("M ≥ 4,5 %+v", m45)
	}
	byName := map[string]LabelResult{}
	for _, r := range isc.Results {
		byName[r.Rule.Name] = r
	}
	if far := byName["jarak 200 km"]; far.Pair[0] != (Score{USGS: 11, Positive: 8, TP: 8, FP: 2}) {
		t.Fatalf("200 km %+v", far.Pair[0])
	}
	// Gempa kembar I (25 dtk) lolos jendela 30 dtk sebagai FP, tidak di bawaan.
	if wide := byName["waktu 30 dtk"]; wide.Pair[0] != (Score{USGS: 11, Positive: 8, TP: 7, FP: 3, FN: 1}) {
		t.Fatalf("30 dtk %+v", wide.Pair[0])
	}
	// Gempa besar J (22 dtk) hanya cocok berkat jendela M ≥ 6.
	if flat := byName["tanpa jendela M ≥ 6"]; flat.Pair[0] != (Score{USGS: 11, Positive: 8, TP: 6, FP: 2, FN: 2}) {
		t.Fatalf("tanpa jendela M ≥ 6 %+v", flat.Pair[0])
	}
	if old := byName["bawaan lama (ADR 0008)"]; old.Rule.Rules.CrossSource != quake.ADR0008CrossSourceRule {
		t.Fatalf("bawaan lama %+v", old.Rule)
	}
	if got := def.Pair[0].Precision(); math.Abs(got-7.0/9) > 1e-12 {
		t.Fatalf("presisi %v", got)
	}
	if !math.IsNaN((Score{}).Recall()) {
		t.Fatal("recall tanpa positif harus NaN")
	}
	if isc.MissesTotal != 3 || len(isc.Misses) != 3 || isc.Misses[0].USGS.Key.EventID != "uB" || isc.Misses[1].Got.Key.EventID != "bF" ||
		isc.Misses[1].GotUSGS != nil || isc.Misses[2].GotUSGS == nil || isc.Misses[2].GotUSGS.Key.EventID != "uH1" {
		t.Fatalf("contoh %+v", isc.Misses)
	}
	if isc.FalsePositives != 2 || isc.FalseOther != 1 {
		t.Fatalf("FP %d, gempa lain %d", isc.FalsePositives, isc.FalseOther)
	}
	if isc.ByType[0].Type != "MLv" || isc.ByType[0].Positive != 5 || isc.ByType[0].TP != 4 {
		t.Fatalf("tipe %+v", isc.ByType)
	}
	if a := rep.Bases[1]; a.Results[0].Pair[0] != (Score{USGS: 1, Positive: 1, TP: 1}) || a.Results[0].Sim[0] != a.Results[0].Pair[0] {
		t.Fatalf("arsip %+v", a.Results[0])
	}

	md := LabelMarkdown(rep, LabelMeta{Sources: []string{"`uji.csv`"}, Command: "make dedup-labels"})
	for _, want := range []string{
		"## Basis `isc`", "## Basis `arsip`", "### semua (11 origin USGS, 8 positif)",
		"| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 87,5% | 77,8% | 7/2/1 | 75,0% | 75,0% | 6/2/2 |",
		"| uB | ", "FN", "FP", "bF (label F;", "| uH1 (11,0) |", "Semua 2 FP ditampilkan; 1 di antaranya", "| (tidak disebut) |", "10.31905/D808B830",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("laporan tanpa %q", want)
		}
	}
	if strings.Contains(md, "### Jawa Barat") == false {
		t.Error("subset Jawa Barat hilang")
	}
}

func TestEvaluateLabelsRejects(t *testing.T) {
	rows := labelScenario()
	if _, err := EvaluateLabels(append(rows, rows[0]), DefaultLabelRules(), DefaultSubsets(), 1); err == nil {
		t.Fatal("origin ganda harus galat")
	}
	bad := labelScenario()
	bad[0].Key.Source = "jma"
	if _, err := EvaluateLabels(bad, DefaultLabelRules(), DefaultSubsets(), 1); err == nil {
		t.Fatal("sumber asing harus galat")
	}
	if _, err := EvaluateLabels(rows, []LabelRule{{Name: "rusak"}}, DefaultSubsets(), 1); err == nil {
		t.Fatal("ambang tidak valid harus galat")
	}
}

// Pasangan berlabel sama di luar jendela kandidat tetap positif (FN).
func TestEvaluateLabelsTruthOutsideCandidate(t *testing.T) {
	b, u := quake.SourceBMKG, quake.SourceUSGS
	rows := []Labeled{
		lab("isc", "X", b, "bX", 0, -7, 107, 5, "MLv"),
		lab("isc", "X", u, "uX", 8*time.Minute, -7, 107, 5, "mb"),
	}
	rep, err := EvaluateLabels(rows, DefaultLabelRules()[:1], DefaultSubsets(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if p := rep.Bases[0].Results[0].Pair[0]; p != (Score{USGS: 1, Positive: 1, FN: 1}) {
		t.Fatalf("%+v", p)
	}
}
