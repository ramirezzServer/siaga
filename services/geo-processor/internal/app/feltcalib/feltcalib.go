// Package feltcalib mengkalibrasi rumus radius dirasakan gempa
// R = 10^(a·M − b) km (PRD, Aturan bisnis; ADR 0026) dari dua bahan:
//
//   - kolom "Dirasakan" BMKG: setiap wilayah yang disebut dicocokkan ke kode
//     Kemendagri (gazetteer), lalu jarak episenter ke tepi wilayah itu menjadi
//     batas bawah jarak orang yang merasakan;
//   - sel DYFI USGS: intensitas per sel 10 km dengan jarak hiposenter; sel
//     CDI ≥ 2 dirasakan, sel CDI < 2 tidak dirasakan.
//
// Rumus dipilih dari BMKG (PRD: "dikalibrasi dengan data gempa dirasakan
// BMKG"): regresi kuantil τ pada log10 jarak wilayah dirasakan terjauh per
// gempa, dengan a > 0 supaya radius dan tingkat tidak pernah turun saat
// magnitudo naik. DYFI dipakai sebagai pemeriksa, terutama M ≥ 6 yang belum
// ada di arsip BMKG, dan satu-satunya sumber pengamatan "tidak dirasakan".
package feltcalib

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/gazetteer"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Formula adalah R = 10^(Slope·M − Intercept) km (jarak hiposenter).
type Formula struct {
	Slope, Intercept float64
}

// PRDFormula adalah heuristik awal PRD (a = 0,5, b = 1).
var PRDFormula = Formula{Slope: 0.5, Intercept: 1}

// FromPolicy mengambil rumus dari aturan gempa geo-processor.
func FromPolicy(p quake.Policy) Formula {
	return Formula{Slope: p.FeltSlope, Intercept: p.FeltIntercept}
}

// RadiusKm adalah radius dirasakan hiposenter untuk magnitudo m.
func (f Formula) RadiusKm(m float64) float64 { return math.Pow(10, f.Slope*m-f.Intercept) }

// Covers melaporkan apakah lokasi berjarak hiposenter hypoKm ada di dalam
// radius dirasakan (sama dengan Policy.LevelAt: √(d² + h²) ≤ R).
func (f Formula) Covers(m, hypoKm float64) bool { return hypoKm <= f.RadiusKm(m)+1e-9 }

// FeltMMI adalah skala terendah yang dihitung dirasakan (MMI II, sama dengan
// CDI 2 di DYFI). Penyebutan BMKG berskala I (misal "I-II") tidak dipakai.
const FeltMMI = 2

// FeltCDI adalah intensitas DYFI terendah yang dihitung dirasakan.
const FeltCDI = 2.0

// Mention adalah satu penyebutan wilayah BMKG beserta hasil pencocokan.
type Mention struct {
	Event ports.BMKGFelt
	quake.FeltMention
	Matched bool
	Match   gazetteer.Match
	// HypocentralKm adalah √(jarak tepi² + kedalaman²): batas bawah jarak
	// hiposenter orang yang merasakan di wilayah itu.
	HypocentralKm float64
}

// Unparsed adalah bagian kolom "Dirasakan" yang tidak berbentuk "MMI nama".
type Unparsed struct {
	EventID string
	Text    string
}

// Boundaries memberi batas wilayah per kode Kemendagri.
type Boundaries map[string]region.MultiPolygon

// MatchBMKG mengurai kolom "Dirasakan" setiap gempa dan mencocokkan setiap
// wilayah ke kode Kemendagri, memilih nama ganda yang terdekat ke episenter.
func MatchBMKG(events []ports.BMKGFelt, ix *gazetteer.Index, b Boundaries) ([]Mention, []Unparsed) {
	var out []Mention
	var bad []Unparsed
	for _, e := range events {
		mentions, unparsed := quake.ParseFelt(e.Felt)
		for _, u := range unparsed {
			bad = append(bad, Unparsed{EventID: e.ID, Text: u})
		}
		dist := func(code string) (float64, bool) {
			p, ok := b[code]
			if !ok {
				return 0, false
			}
			return p.DistanceKm(e.Latitude, e.Longitude), true
		}
		for _, m := range mentions {
			mm := Mention{Event: e, FeltMention: m}
			mm.Match, mm.Matched = ix.Resolve(ix.Lookup(m.Name), dist)
			if mm.Matched {
				mm.HypocentralKm = math.Hypot(mm.Match.DistanceKm, math.Max(e.DepthKm, 0))
			}
			out = append(out, mm)
		}
	}
	return out, bad
}

// Extent adalah wilayah dirasakan terjauh satu gempa BMKG.
type Extent struct {
	Event ports.BMKGFelt
	// Farthest adalah penyebutan dirasakan (MMI ≥ II) dengan jarak
	// hiposenter terbesar.
	Farthest Mention
	// Felt adalah jumlah penyebutan dirasakan yang cocok.
	Felt int
}

// Extents mengambil wilayah dirasakan terjauh per gempa. Gempa tanpa
// penyebutan dirasakan yang cocok tidak ikut.
func Extents(mentions []Mention) []Extent {
	var out []Extent
	idx := map[string]int{}
	for _, m := range mentions {
		if !m.Matched || m.MMI() < FeltMMI {
			continue
		}
		i, ok := idx[m.Event.ID]
		if !ok {
			idx[m.Event.ID] = len(out)
			out = append(out, Extent{Event: m.Event, Farthest: m, Felt: 1})
			continue
		}
		out[i].Felt++
		if m.HypocentralKm > out[i].Farthest.HypocentralKm {
			out[i].Farthest = m
		}
	}
	return out
}

// Fit adalah hasil regresi kuantil.
type Fit struct {
	Formula
	Tau    float64
	Events int
	// Loss adalah jumlah check loss τ (log10 km).
	Loss float64
}

// Grid pencarian kemiringan. Untuk setiap kemiringan, intersep terbaik
// adalah kuantil (1 − τ) dari a·M − log10 jarak, jadi pencarian hanya satu
// dimensi dan hasilnya pasti (tidak bergantung titik awal).
const (
	slopeMin  = 0.05
	slopeMax  = 1.0
	slopeStep = 0.005
)

// ErrNoData berarti bahan kalibrasi kosong.
var ErrNoData = errors.New("tidak ada gempa dirasakan untuk kalibrasi")

// FitQuantile mencari a > 0 dan b yang meminimalkan check loss τ pada
// log10(jarak hiposenter wilayah dirasakan terjauh) terhadap a·M − b, satu
// titik per gempa: kira-kira τ bagian gempa punya wilayah terjauh di dalam R.
func FitQuantile(ext []Extent, tau float64) (Fit, error) {
	if !(tau > 0 && tau < 1) {
		return Fit{}, fmt.Errorf("τ %v harus di antara 0 dan 1", tau)
	}
	if len(ext) < 2 {
		return Fit{}, ErrNoData
	}
	m := make([]float64, len(ext))
	y := make([]float64, len(ext))
	for i, e := range ext {
		m[i] = e.Event.Magnitude
		y[i] = math.Log10(math.Max(e.Farthest.HypocentralKm, 1))
	}
	return fitXY(m, y, tau), nil
}

func fitXY(m, y []float64, tau float64) Fit {
	best := Fit{Tau: tau, Events: len(m), Loss: math.Inf(1)}
	shift := make([]float64, len(m))
	steps := int(math.Round((slopeMax - slopeMin) / slopeStep))
	for s := 0; s <= steps; s++ {
		a := slopeMin + float64(s)*slopeStep
		for i := range m {
			shift[i] = a*m[i] - y[i]
		}
		b := quantile(shift, 1-tau)
		loss := 0.0
		for i := range m {
			r := y[i] - (a*m[i] - b)
			if r >= 0 {
				loss += tau * r
			} else {
				loss += (tau - 1) * r
			}
		}
		if loss < best.Loss-1e-12 {
			best.Formula, best.Loss = Formula{Slope: a, Intercept: b}, loss
		}
	}
	return best
}

// quantile adalah kuantil empiris (invers CDF, tanpa interpolasi).
func quantile(v []float64, p float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

// Interval adalah selang bootstrap 5–95%.
type Interval struct{ Low, High float64 }

// Bootstrap adalah sebaran hasil FitQuantile pada sampel ulang gempa.
type Bootstrap struct {
	Samples int
	Slope   Interval
	// Radius per magnitudo di Magnitudes.
	Radius []Interval
}

// Magnitudes adalah magnitudo yang dilaporkan di tabel.
var Magnitudes = []float64{3, 4, 5, 6, 7}

// BootstrapFit mengulang FitQuantile pada n sampel ulang gempa (dengan
// pengembalian, benih tetap supaya laporan bisa diulang persis).
func BootstrapFit(ext []Extent, tau float64, n int, seed uint64) (Bootstrap, error) {
	if len(ext) < 2 || n < 1 {
		return Bootstrap{}, ErrNoData
	}
	// Sampel ulang statistik, bukan kriptografi; benih tetap supaya laporan bisa diulang.
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // G404: bukan keperluan keamanan
	m := make([]float64, len(ext))
	y := make([]float64, len(ext))
	slopes := make([]float64, n)
	radius := make([][]float64, len(Magnitudes))
	for k := range n {
		for i := range ext {
			j := rng.IntN(len(ext))
			m[i] = ext[j].Event.Magnitude
			y[i] = math.Log10(math.Max(ext[j].Farthest.HypocentralKm, 1))
		}
		f := fitXY(m, y, tau)
		slopes[k] = f.Slope
		for i, mag := range Magnitudes {
			radius[i] = append(radius[i], f.RadiusKm(mag))
		}
	}
	out := Bootstrap{Samples: n, Slope: interval(slopes)}
	for _, r := range radius {
		out.Radius = append(out.Radius, interval(r))
	}
	return out, nil
}

func interval(v []float64) Interval {
	return Interval{Low: quantile(v, 0.05), High: quantile(v, 0.95)}
}

// Score adalah kinerja satu rumus.
type Score struct {
	Formula
	Name string
	// BMKGEvents: bagian gempa BMKG yang wilayah dirasakan terjauhnya di dalam R.
	BMKGEvents float64
	// BMKGMentions: bagian penyebutan dirasakan di dalam R (bobot sama per gempa).
	BMKGMentions float64
	// DYFIFelt: bagian sel dirasakan (CDI ≥ 2) di dalam R; DYFINotFelt:
	// bagian sel tidak dirasakan (CDI < 2) di luar R. Bobot sama per kejadian,
	// sel mencurigakan tidak dipakai. Large: hanya kejadian M ≥ 6.
	DYFIFelt, DYFINotFelt, DYFIFeltLarge float64
}

// LargeMagnitude adalah batas subset "gempa besar" DYFI.
const LargeMagnitude = 6.0

// Evaluate menilai rumus pada kedua bahan.
func Evaluate(name string, f Formula, ext []Extent, mentions []Mention, cells []ports.DYFICell) Score {
	s := Score{Formula: f, Name: name}
	var in, all float64
	for _, e := range ext {
		all++
		if f.Covers(e.Event.Magnitude, e.Farthest.HypocentralKm) {
			in++
		}
	}
	s.BMKGEvents = ratio(in, all)

	weight := map[string]float64{}
	for _, m := range mentions {
		if m.Matched && m.MMI() >= FeltMMI {
			weight[m.Event.ID]++
		}
	}
	in, all = 0, 0
	for _, m := range mentions {
		if !m.Matched || m.MMI() < FeltMMI {
			continue
		}
		w := 1 / weight[m.Event.ID]
		all += w
		if f.Covers(m.Event.Magnitude, m.HypocentralKm) {
			in += w
		}
	}
	s.BMKGMentions = ratio(in, all)

	// Bobot per kejadian dihitung terpisah untuk sel dirasakan dan tidak
	// dirasakan, supaya setiap kejadian bernilai sama di kedua ukuran.
	type key struct {
		id   string
		felt bool
	}
	cellWeight := map[key]float64{}
	for _, c := range cells {
		if !c.Suspect {
			cellWeight[key{c.ID, c.CDI >= FeltCDI}]++
		}
	}
	var feltIn, feltAll, notOut, notAll, bigIn, bigAll float64
	for _, c := range cells {
		if c.Suspect {
			continue
		}
		felt := c.CDI >= FeltCDI
		w := 1 / cellWeight[key{c.ID, felt}]
		inside := f.Covers(c.Magnitude, c.HypocentralKm)
		if felt {
			feltAll += w
			if inside {
				feltIn += w
			}
			if c.Magnitude >= LargeMagnitude {
				bigAll += w
				if inside {
					bigIn += w
				}
			}
		} else {
			notAll += w
			if !inside {
				notOut += w
			}
		}
	}
	s.DYFIFelt, s.DYFINotFelt, s.DYFIFeltLarge = ratio(feltIn, feltAll), ratio(notOut, notAll), ratio(bigIn, bigAll)
	return s
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return math.NaN()
	}
	return a / b
}

// Bin adalah ringkasan per rentang magnitudo untuk rumus terpilih.
type Bin struct {
	From, To                       float64
	BMKGEvents, BMKGCovered        int
	DYFIEvents                     int
	DYFIFelt, DYFIFeltIn           int
	DYFINotFelt, DYFINotFeltIn     int
	MedianFarthestKm, MedianDYFIKm float64
}

// Bins merangkum bahan per satu magnitudo (sel tidak berbobot).
func Bins(f Formula, ext []Extent, cells []ports.DYFICell) []Bin {
	edges := []float64{math.Inf(-1), 3, 4, 5, 6, 7, math.Inf(1)}
	out := make([]Bin, len(edges)-1)
	far := make([][]float64, len(out))
	dy := make([][]float64, len(out))
	events := make([]map[string]bool, len(out))
	idx := func(m float64) int {
		for i := range out {
			if m >= edges[i] && m < edges[i+1] {
				return i
			}
		}
		return len(out) - 1
	}
	for i := range out {
		out[i].From, out[i].To = edges[i], edges[i+1]
		events[i] = map[string]bool{}
	}
	for _, e := range ext {
		i := idx(e.Event.Magnitude)
		out[i].BMKGEvents++
		far[i] = append(far[i], e.Farthest.HypocentralKm)
		if f.Covers(e.Event.Magnitude, e.Farthest.HypocentralKm) {
			out[i].BMKGCovered++
		}
	}
	for _, c := range cells {
		if c.Suspect {
			continue
		}
		i := idx(c.Magnitude)
		events[i][c.ID] = true
		inside := f.Covers(c.Magnitude, c.HypocentralKm)
		if c.CDI >= FeltCDI {
			out[i].DYFIFelt++
			dy[i] = append(dy[i], c.HypocentralKm)
			if inside {
				out[i].DYFIFeltIn++
			}
		} else {
			out[i].DYFINotFelt++
			if inside {
				out[i].DYFINotFeltIn++
			}
		}
	}
	for i := range out {
		out[i].DYFIEvents = len(events[i])
		out[i].MedianFarthestKm, out[i].MedianDYFIKm = median(far[i]), median(dy[i])
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	return quantile(v, 0.5)
}

// Outside adalah gempa BMKG yang wilayah dirasakan terjauhnya di luar R,
// urut selisih terbesar, untuk diperiksa.
func Outside(f Formula, ext []Extent) []Extent {
	var out []Extent
	for _, e := range ext {
		if !f.Covers(e.Event.Magnitude, e.Farthest.HypocentralKm) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Extent) int {
		ra := a.Farthest.HypocentralKm / f.RadiusKm(a.Event.Magnitude)
		rb := b.Farthest.HypocentralKm / f.RadiusKm(b.Event.Magnitude)
		return cmp.Or(cmp.Compare(rb, ra), cmp.Compare(a.Event.ID, b.Event.ID))
	})
	return out
}
