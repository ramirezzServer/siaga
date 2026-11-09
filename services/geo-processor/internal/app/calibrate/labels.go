package calibrate

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Labeled adalah satu origin di set berlabel.
type Labeled = ports.LabeledOrigin

// Pengukuran terhadap set berlabel (PRD T4, ADR 0024). Dua cara dihitung
// untuk setiap ambang:
//
//   - Pasangan: untuk setiap origin USGS, origin BMKG dengan skor
//     quake.Rule.Match terbaik yang lolos ambang lintas sumber. Benar bila
//     label keduanya sama.
//   - Simulasi: semua origin diputar melalui quake.Settle (fungsi yang sama
//     dengan geo-processor) menurut urutan kedatangan, dengan ambang lintas
//     sumber dan ambang revisi; origin BMKG yang berlaku di kejadian akhir
//     origin USGS adalah pasangannya. Simulasi juga menangkap gabungan salah
//     dua origin BMKG (atau dua USGS) dari gempa berbeda lewat ambang revisi.
//
// Untuk origin USGS u: positif bila set berisi origin BMKG berlabel sama.
// TP: pasangan ada dan labelnya sama. FP: pasangan ada tetapi labelnya lain
// (gabungan salah). FN: positif tetapi bukan TP. Presisi = TP/(TP+FP),
// recall = TP/positif.

// Arrival meniru keterlambatan terbit di feed real-time: BMKG beberapa menit,
// USGS untuk Indonesia ±16–20 menit (arsip ingest 24 Sep–20 Okt 2026).
// FirstSeenAt menentukan revisi mana yang berlaku saat ambang revisi
// menggabungkan dua origin BMKG.
var Arrival = map[quake.Source]time.Duration{quake.SourceBMKG: 3 * time.Minute, quake.SourceUSGS: 20 * time.Minute}

// LabelRule adalah satu pasang ambang yang diuji.
type LabelRule struct {
	Name  string
	Rules quake.Rules
}

// Subset menyaring origin USGS yang dihitung.
type Subset struct {
	Name string
	Keep func(u Labeled) bool
}

// Score adalah hitungan satu ambang di satu subset.
type Score struct {
	USGS, Positive, TP, FP, FN int
}

// Recall = TP / Positive.
func (s Score) Recall() float64 { return ratio(s.TP, s.Positive) }

// Precision = TP / (TP + FP).
func (s Score) Precision() float64 { return ratio(s.TP, s.TP+s.FP) }

func ratio(a, b int) float64 {
	if b == 0 {
		return math.NaN()
	}
	return float64(a) / float64(b)
}

func (s *Score) add(positive, matched, correct bool) {
	s.USGS++
	switch {
	case matched && correct:
		s.TP++
	case matched:
		s.FP++
	}
	if positive {
		s.Positive++
		if !matched || !correct {
			s.FN++
		}
	}
}

// LabelResult adalah hasil satu ambang di satu basis label.
type LabelResult struct {
	Rule LabelRule
	// Pair dan Sim sejajar dengan LabelReport.Subsets.
	Pair, Sim []Score
	// SameSourceBMKG dan SameSourceUSGS adalah kejadian simulasi yang berisi
	// dua origin satu sumber dari label berbeda.
	SameSourceBMKG, SameSourceUSGS int
	// FalsePerThousandMinutes: kecocokan pasangan per 1.000 origin USGS
	// setelah waktunya digeser ±2–10 menit (gempa susulan).
	FalsePerThousandMinutes float64
}

// TypeScore merangkum pasangan positif menurut tipe magnitudo BMKG.
type TypeScore struct {
	Type      string
	Positive  int
	TP        int
	DMag      Percentiles
	MaxDeltaM float64
}

// LabelMiss adalah satu origin USGS yang salah menurut ambang pertama
// (cara pasangan).
type LabelMiss struct {
	USGS Labeled
	// Truth adalah origin BMKG berlabel sama terdekat (kosong bila negatif).
	Truth *Labeled
	// Got adalah pasangan yang dipilih ambang (kosong bila tidak ada).
	Got *Labeled
}

// BasisReport adalah hasil satu basis label (isc atau arsip).
type BasisReport struct {
	Basis       string
	BMKG, USGS  int
	Positive    int
	Labels      int
	From, To    time.Time
	Offsets     Offsets
	Results     []LabelResult
	ByType      []TypeScore
	Misses      []LabelMiss
	MissesTotal int
}

// LabelReport adalah hasil seluruh set berlabel.
type LabelReport struct {
	Subsets []string
	Bases   []BasisReport
}

// JawaBarat adalah kotak Jawa Barat dan laut sekitarnya, sama dengan
// kalibrasi katalog (ADR 0008).
var JawaBarat = Box{MinLat: -9, MaxLat: -5.5, MinLon: 105, MaxLon: 109.5}

// DefaultSubsets berisi semua origin USGS, USGS M ≥ 4,3 dan ≥ 4,5 (mirip feed
// real-time: arsip ingest berisi USGS M 4,3–5,0 untuk Indonesia), dan
// episenter USGS di kotak Jawa Barat.
func DefaultSubsets() []Subset {
	return []Subset{
		{Name: "semua", Keep: func(Labeled) bool { return true }},
		{Name: "USGS M ≥ 4,3", Keep: func(u Labeled) bool { return u.Magnitude >= 4.3-1e-9 }},
		{Name: "USGS M ≥ 4,5", Keep: func(u Labeled) bool { return u.Magnitude >= 4.5-1e-9 }},
		{Name: "Jawa Barat", Keep: func(u Labeled) bool { return JawaBarat.Contains(u.Latitude, u.Longitude) }},
	}
}

// DefaultLabelRules adalah ambang yang dibandingkan; yang pertama adalah
// ambang bawaan layanan dan menjadi acuan contoh yang terlewat.
func DefaultLabelRules() []LabelRule {
	rev := quake.DefaultRevisionRule
	mk := func(name string, cross quake.Rule) LabelRule {
		return LabelRule{Name: name, Rules: quake.Rules{CrossSource: cross, Revision: rev}}
	}
	return []LabelRule{
		mk("bawaan (ADR 0008)", quake.DefaultCrossSourceRule),
		mk("PRD awal", quake.PRDInitialRule),
		mk("jarak 120 km", quake.Rule{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 120, MaxMagnitudeDelta: 1.0}),
		mk("jarak 150 km", quake.Rule{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 150, MaxMagnitudeDelta: 1.0}),
		mk("waktu 20 dtk", quake.Rule{MaxTimeDelta: 20 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 1.0}),
		mk("waktu 60 dtk", quake.Rule{MaxTimeDelta: 60 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 1.0}),
		mk("magnitudo 0,7", quake.Rule{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 0.7}),
		mk("magnitudo 1,3", quake.Rule{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 1.3}),
		mk("120 km + magnitudo 1,3", quake.Rule{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 120, MaxMagnitudeDelta: 1.3}),
	}
}

type group struct {
	basis      string
	bmkg, usgs []Labeled
}

// EvaluateLabels mengukur setiap ambang di setiap basis label. maxMisses
// membatasi contoh yang dicantumkan per basis.
func EvaluateLabels(rows []Labeled, rules []LabelRule, subsets []Subset, maxMisses int) (LabelReport, error) {
	rep := LabelReport{}
	for _, s := range subsets {
		rep.Subsets = append(rep.Subsets, s.Name)
	}
	groups := map[string]*group{}
	seen := map[quake.IdentityKey]bool{}
	for _, r := range rows {
		if seen[r.Key] {
			return rep, fmt.Errorf("origin %s muncul dua kali di set berlabel", r.Key)
		}
		seen[r.Key] = true
		g := groups[r.Basis]
		if g == nil {
			g = &group{basis: r.Basis}
			groups[r.Basis] = g
		}
		switch r.Key.Source {
		case quake.SourceBMKG:
			g.bmkg = append(g.bmkg, r)
		case quake.SourceUSGS:
			g.usgs = append(g.usgs, r)
		default:
			return rep, fmt.Errorf("origin %s: sumber tidak dikenal", r.Key)
		}
	}
	for _, r := range rules {
		if err := r.Rules.Validate(); err != nil {
			return rep, fmt.Errorf("ambang %s: %w", r.Name, err)
		}
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range groups {
			if !yield(k) {
				return
			}
		}
	})
	// isc dulu, lalu basis lain urut nama.
	isc := func(s string) int {
		if s == "isc" {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(names, func(a, b string) int { return cmp.Compare(isc(a), isc(b)) })
	for _, name := range names {
		br, err := evaluateBasis(groups[name], rules, subsets, maxMisses)
		if err != nil {
			return rep, err
		}
		rep.Bases = append(rep.Bases, br)
	}
	return rep, nil
}

func byTime(a, b Labeled) int {
	return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), a.Key.Compare(b.Key))
}

func evaluateBasis(g *group, rules []LabelRule, subsets []Subset, maxMisses int) (BasisReport, error) {
	slices.SortFunc(g.bmkg, byTime)
	slices.SortFunc(g.usgs, byTime)
	br := BasisReport{Basis: g.basis, BMKG: len(g.bmkg), USGS: len(g.usgs)}
	labels := map[string]bool{}
	for _, r := range append(slices.Clone(g.bmkg), g.usgs...) {
		labels[r.Label] = true
		if br.From.IsZero() || r.OccurredAt.Before(br.From) {
			br.From = r.OccurredAt
		}
		if r.OccurredAt.After(br.To) {
			br.To = r.OccurredAt
		}
	}
	br.Labels = len(labels)
	ix := labeledIndex(g.bmkg)

	// Pasangan sebenarnya: origin BMKG berlabel sama, terdekat menurut skor
	// jendela kandidat (dipakai untuk selisih dan contoh terlewat).
	truth := make([]*Labeled, len(g.usgs))
	var dts, kms, dms []float64
	types := map[string]*TypeScore{}
	byLabel := map[string][]*Labeled{}
	for j := range g.bmkg {
		byLabel[g.bmkg[j].Label] = append(byLabel[g.bmkg[j].Label], &g.bmkg[j])
	}
	for i, u := range g.usgs {
		best := math.Inf(1)
		for _, b := range byLabel[u.Label] {
			_, s := Candidate.Match(u.Origin, b.Origin)
			if math.IsInf(s, 1) {
				// Di luar jendela kandidat tetap pasangan bila labelnya sama;
				// skor dihitung dengan jendela lebar supaya tetap terpilih.
				_, s = wide.Match(u.Origin, b.Origin)
				s += 100
			}
			if s < best {
				best, truth[i] = s, b
			}
		}
		if t := truth[i]; t != nil {
			br.Positive++
			dt, km, dm := deltas(u.Origin, t.Origin)
			dts, kms, dms = append(dts, dt), append(kms, km), append(dms, dm)
			ts := types[t.MagnitudeType]
			if ts == nil {
				ts = &TypeScore{Type: t.MagnitudeType}
				types[t.MagnitudeType] = ts
			}
			ts.Positive++
		}
	}
	br.Offsets = Offsets{Seconds: percentiles(dts), Km: percentiles(kms), Magnitude: percentiles(dms)}

	for ri, r := range rules {
		res := LabelResult{Rule: r, Pair: make([]Score, len(subsets)), Sim: make([]Score, len(subsets))}
		pairs := make([]*Labeled, len(g.usgs))
		for i, u := range g.usgs {
			if b, ok := ix.best(u.Origin, r.Rules.CrossSource); ok {
				pairs[i] = b
			}
		}
		sim, err := simulate(g, r.Rules)
		if err != nil {
			return br, fmt.Errorf("simulasi %s %s: %w", g.basis, r.Name, err)
		}
		res.SameSourceBMKG, res.SameSourceUSGS = sim.sameSource()
		for i, u := range g.usgs {
			positive := truth[i] != nil
			p := pairs[i]
			s := sim.partner(u.Key)
			for si, sub := range subsets {
				if !sub.Keep(u) {
					continue
				}
				res.Pair[si].add(positive, p != nil, p != nil && p.Label == u.Label)
				res.Sim[si].add(positive, s != nil, s != nil && s.Label == u.Label)
			}
			if ri == 0 {
				if positive {
					ts := types[truth[i].MagnitudeType]
					if p != nil && p.Label == u.Label {
						ts.TP++
					}
				}
				if (positive && (p == nil || p.Label != u.Label)) || (!positive && p != nil) {
					br.MissesTotal++
					if len(br.Misses) < maxMisses {
						br.Misses = append(br.Misses, LabelMiss{USGS: u, Truth: truth[i], Got: p})
					}
				}
			}
		}
		res.FalsePerThousandMinutes = labeledFalseRate(ix, g.usgs, r.Rules.CrossSource, MinuteShifts)
		br.Results = append(br.Results, res)
	}
	for i, u := range g.usgs {
		if t := truth[i]; t != nil {
			_, _, dm := deltas(u.Origin, t.Origin)
			types[t.MagnitudeType].MaxDeltaM = math.Max(types[t.MagnitudeType].MaxDeltaM, dm)
		}
	}
	for _, ts := range types {
		var d []float64
		for i, u := range g.usgs {
			if t := truth[i]; t != nil && t.MagnitudeType == ts.Type {
				_, _, dm := deltas(u.Origin, t.Origin)
				d = append(d, dm)
			}
		}
		ts.DMag = percentiles(d)
		br.ByType = append(br.ByType, *ts)
	}
	slices.SortFunc(br.ByType, func(a, b TypeScore) int {
		return cmp.Or(cmp.Compare(b.Positive, a.Positive), cmp.Compare(a.Type, b.Type))
	})
	return br, nil
}

// wide adalah jendela untuk mengurutkan pasangan sebenarnya yang berada di
// luar jendela kandidat (label sama tetapi selisih sangat besar).
var wide = quake.Rule{MaxTimeDelta: 10 * time.Minute, MaxDistanceKm: 500, MaxMagnitudeDelta: 3}

type labeledIx []Labeled

func labeledIndex(sorted []Labeled) labeledIx { return labeledIx(sorted) }

func (ix labeledIx) within(t time.Time, d time.Duration) (int, int) {
	lo, _ := slices.BinarySearchFunc(ix, t.Add(-d), func(e Labeled, x time.Time) int { return e.OccurredAt.Compare(x) })
	hi, _ := slices.BinarySearchFunc(ix, t.Add(d+1), func(e Labeled, x time.Time) int { return e.OccurredAt.Compare(x) })
	return lo, hi
}

func (ix labeledIx) best(o quake.Origin, r quake.Rule) (*Labeled, bool) {
	lo, hi := ix.within(o.OccurredAt, r.MaxTimeDelta)
	var best *Labeled
	bestScore := math.Inf(1)
	for i := lo; i < hi; i++ {
		if ok, s := r.Match(o, ix[i].Origin); ok && (s < bestScore || (s == bestScore && ix[i].Key.Compare(best.Key) < 0)) {
			best, bestScore = &ix[i], s
		}
	}
	return best, best != nil
}

func labeledFalseRate(ix labeledIx, usgs []Labeled, r quake.Rule, shifts []time.Duration) float64 {
	if len(usgs) == 0 || len(shifts) == 0 {
		return 0
	}
	n := 0
	for _, s := range shifts {
		for _, u := range usgs {
			o := u.Origin
			o.OccurredAt = o.OccurredAt.Add(s)
			if _, ok := ix.best(o, r); ok {
				n++
			}
		}
	}
	return float64(n) / float64(len(shifts)) / float64(len(usgs)) * 1000
}

// simResult adalah keanggotaan akhir simulasi.
type simResult struct {
	rows    map[quake.IdentityKey]*Labeled
	home    map[quake.IdentityKey]quake.EventID
	members map[quake.EventID][]quake.Solution
}

// partner mengembalikan origin BMKG yang berlaku di kejadian akhir origin USGS.
func (s simResult) partner(k quake.IdentityKey) *Labeled {
	for _, m := range quake.Current(s.members[s.home[k]]) {
		if m.Key.Source == quake.SourceBMKG {
			return s.rows[m.Key]
		}
	}
	return nil
}

func (s simResult) sameSource() (bmkg, usgs int) {
	for _, ms := range s.members {
		labels := map[quake.Source]map[string]bool{}
		for _, m := range ms {
			if labels[m.Key.Source] == nil {
				labels[m.Key.Source] = map[string]bool{}
			}
			labels[m.Key.Source][s.rows[m.Key].Label] = true
		}
		if len(labels[quake.SourceBMKG]) > 1 {
			bmkg++
		}
		if len(labels[quake.SourceUSGS]) > 1 {
			usgs++
		}
	}
	return bmkg, usgs
}

// simulate memutar semua origin satu basis melalui quake.Settle menurut
// urutan kedatangan (waktu kejadian + Arrival), dengan kandidat seperti
// geo-processor: kejadian yang punya anggota dalam jendela
// max(lintas sumber, revisi) dari waktu kejadian origin baru.
func simulate(g *group, rules quake.Rules) (simResult, error) {
	res := simResult{
		rows:    map[quake.IdentityKey]*Labeled{},
		home:    map[quake.IdentityKey]quake.EventID{},
		members: map[quake.EventID][]quake.Solution{},
	}
	var sols []quake.Solution
	for _, list := range [][]Labeled{g.bmkg, g.usgs} {
		for i := range list {
			r := &list[i]
			res.rows[r.Key] = r
			sols = append(sols, quake.Solution{
				Key: r.Key, OccurredAt: r.OccurredAt, Latitude: r.Latitude, Longitude: r.Longitude,
				Magnitude: r.Magnitude, MagnitudeType: r.MagnitudeType, DepthKm: r.DepthKm,
				FirstSeenAt: r.OccurredAt.Add(Arrival[r.Key.Source]),
			})
		}
	}
	slices.SortFunc(sols, func(a, b quake.Solution) int {
		return cmp.Or(a.FirstSeenAt.Compare(b.FirstSeenAt), a.Key.Compare(b.Key))
	})
	w := max(rules.CrossSource.MaxTimeDelta, rules.Revision.MaxTimeDelta)
	bucket := func(t time.Time) int64 { return t.UnixNano() / int64(w) }
	buckets := map[int64][]quake.IdentityKey{}
	byKey := map[quake.IdentityKey]quake.Solution{}
	retired := map[quake.EventID]bool{}
	taken := func(id quake.EventID) bool { _, live := res.members[id]; return live || retired[id] }

	for _, self := range sols {
		ids := map[quake.EventID]bool{}
		b := bucket(self.OccurredAt)
		for k := b - 1; k <= b+1; k++ {
			for _, key := range buckets[k] {
				m := byKey[key]
				if m.OccurredAt.Sub(self.OccurredAt).Abs() <= w {
					ids[res.home[key]] = true
				}
			}
		}
		var clusters []quake.Cluster
		for id := range ids {
			clusters = append(clusters, quake.Cluster{ID: id, Members: res.members[id]})
		}
		slices.SortFunc(clusters, func(a, b quake.Cluster) int { return a.ID.Compare(b.ID) })
		out, err := quake.Settle(self, quake.EventID{}, clusters, rules, taken)
		if err != nil {
			return res, err
		}
		for _, ch := range out.Changes {
			if ch.Empty() {
				delete(res.members, ch.ID)
				retired[ch.ID] = true
				continue
			}
			res.members[ch.ID] = ch.Members
			for _, m := range ch.Members {
				res.home[m.Key] = ch.ID
			}
		}
		byKey[self.Key] = self
		buckets[b] = append(buckets[b], self.Key)
	}
	return res, nil
}

// LabelMeta adalah keterangan asal data untuk laporan.
type LabelMeta struct {
	Sources []string
	Command string
}

func pct(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}
	return num(v*100, 1) + "%"
}

// LabelMarkdown menulis laporan set berlabel.
func LabelMarkdown(rep LabelReport, m LabelMeta) string {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	f("# Deduplikasi gempa BMKG–USGS terhadap set berlabel (PRD T4)\n\n")
	f("Dihasilkan otomatis oleh `%s`. Jangan diedit manual; jalankan ulang perintahnya. Keputusan di ADR 0024.\n\n", m.Command)
	f("Set berlabel: %s. Label ISC berasal dari pengelompokan kejadian ISC Bulletin (International Seismological Centre, On-line Bulletin, https://doi.org/10.31905/D808B830): origin DJA (BMKG) dan NEIC (USGS) di kejadian ISC yang sama adalah pasangan. Label arsip disusun manual dari arsip payload real-time ingest.\n\n", strings.Join(m.Sources, ", "))
	f("Untuk setiap origin USGS: **positif** bila set berisi origin BMKG berlabel sama. **Pasangan**: origin BMKG dengan skor `quake.Rule.Match` terbaik yang lolos ambang lintas sumber. **Simulasi**: semua origin diputar lewat `quake.Settle` (sama dengan geo-processor) menurut urutan kedatangan (BMKG %s menit, USGS %s menit setelah kejadian), dengan ambang revisi %s, %s km, %s; pasangannya adalah origin BMKG yang berlaku di kejadian akhir. TP = pasangan berlabel sama, FP = pasangan berlabel lain (gabungan salah), FN = positif yang bukan TP. Presisi = TP/(TP+FP), recall = TP/positif.\n\n",
		num(Arrival[quake.SourceBMKG].Minutes(), 0), num(Arrival[quake.SourceUSGS].Minutes(), 0),
		quake.DefaultRevisionRule.MaxTimeDelta, num(quake.DefaultRevisionRule.MaxDistanceKm, 0), num(quake.DefaultRevisionRule.MaxMagnitudeDelta, 1))
	for _, br := range rep.Bases {
		f("## Basis `%s`\n\n", br.Basis)
		f("| | |\n| --- | --- |\n")
		f("| Periode origin | %s s.d. %s (UTC) |\n", br.From.Format(time.DateOnly), br.To.Format(time.DateOnly))
		f("| Origin BMKG | %d |\n| Origin USGS | %d |\n| Origin USGS positif | %d |\n| Label (kejadian) | %d |\n\n", br.BMKG, br.USGS, br.Positive, br.Labels)
		if br.Positive > 0 {
			f("Selisih origin USGS ke origin BMKG berlabel sama:\n\n")
			f("| Selisih | p50 | p90 | p95 | p99 | maks |\n| --- | --- | --- | --- | --- | --- |\n")
			row := func(name string, p Percentiles, digits int) {
				f("| %s | %s | %s | %s | %s | %s |\n", name, num(p.P50, digits), num(p.P90, digits), num(p.P95, digits), num(p.P99, digits), num(p.Max, digits))
			}
			row("Waktu (detik)", br.Offsets.Seconds, 1)
			row("Jarak episenter (km)", br.Offsets.Km, 1)
			row("Magnitudo", br.Offsets.Magnitude, 2)
			f("\n")
		}
		for si, sub := range rep.Subsets {
			n := 0
			if len(br.Results) > 0 {
				n = br.Results[0].Pair[si].USGS
			}
			if n == 0 {
				continue
			}
			f("### %s (%d origin USGS, %d positif)\n\n", sub, n, br.Results[0].Pair[si].Positive)
			f("| Ambang (waktu, jarak, magnitudo) | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |\n")
			f("| --- | --- | --- | --- | --- | --- | --- |\n")
			for _, r := range br.Results {
				p, s := r.Pair[si], r.Sim[si]
				f("| %s: %s | %s | %s | %d/%d/%d | %s | %s | %d/%d/%d |\n", r.Rule.Name, ruleText(r.Rule.Rules.CrossSource),
					pct(p.Recall()), pct(p.Precision()), p.TP, p.FP, p.FN,
					pct(s.Recall()), pct(s.Precision()), s.TP, s.FP, s.FN)
			}
			f("\n")
		}
		if len(br.Results) > 0 {
			f("### Gabungan salah lain\n\n")
			f("Kejadian simulasi berisi dua origin satu sumber dari label berbeda (lewat ambang revisi), dan kecocokan per 1.000 origin USGS setelah waktunya digeser ±2–10 menit. Set berlabel hanya memuat origin BMKG di sekitar origin USGS, jadi angka geser adalah batas bawah.\n\n")
			f("| Ambang | BMKG–BMKG | USGS–USGS | Geser ±2–10 mnt /1.000 |\n| --- | --- | --- | --- |\n")
			for _, r := range br.Results {
				f("| %s: %s | %d | %d | %s |\n", r.Rule.Name, ruleText(r.Rule.Rules.CrossSource), r.SameSourceBMKG, r.SameSourceUSGS, num(r.FalsePerThousandMinutes, 2))
			}
			f("\n")
		}
		if len(br.ByType) > 0 && len(br.Results) > 0 {
			f("### Menurut tipe magnitudo BMKG (ambang %s)\n\n", br.Results[0].Rule.Name)
			f("| Tipe | Positif | Recall pasangan | Selisih M p50 | p95 | p99 | maks |\n| --- | --- | --- | --- | --- | --- | --- |\n")
			for _, t := range br.ByType {
				name := t.Type
				if name == "" {
					name = "(tidak disebut)"
				}
				f("| %s | %d | %s | %s | %s | %s | %s |\n", name, t.Positive, pct(ratio(t.TP, t.Positive)),
					num(t.DMag.P50, 2), num(t.DMag.P95, 2), num(t.DMag.P99, 2), num(t.MaxDeltaM, 2))
			}
			f("\n")
		}
		if br.MissesTotal > 0 && len(br.Results) > 0 {
			f("### Salah menurut ambang %s (cara pasangan, %d dari %d ditampilkan)\n\n", br.Results[0].Rule.Name, len(br.Misses), br.MissesTotal)
			f("| USGS | Waktu (UTC) | M | Jenis | Ke BMKG berlabel sama (dtk, km, ΔM) | Pasangan terpilih |\n| --- | --- | --- | --- | --- | --- |\n")
			for _, mi := range br.Misses {
				kind, truth, got := "FN", "–", "–"
				if mi.Got != nil {
					kind = "FP"
					if mi.Truth != nil {
						kind = "FP+FN"
					}
					dt, km, dm := deltas(mi.USGS.Origin, mi.Got.Origin)
					got = fmt.Sprintf("%s (label %s; %s, %s, %s)", mi.Got.Key.EventID, mi.Got.Label, num(dt, 1), num(km, 1), num(dm, 2))
				}
				if mi.Truth != nil {
					dt, km, dm := deltas(mi.USGS.Origin, mi.Truth.Origin)
					truth = fmt.Sprintf("%s, %s, %s (%s)", num(dt, 1), num(km, 1), num(dm, 2), mi.Truth.MagnitudeType)
				}
				f("| %s | %s | %s | %s | %s | %s |\n", mi.USGS.Key.EventID, mi.USGS.OccurredAt.Format("2006-01-02 15:04:05"),
					num(mi.USGS.Magnitude, 1), kind, truth, got)
			}
			f("\n")
		}
	}
	return b.String()
}

func ruleText(r quake.Rule) string {
	return fmt.Sprintf("%s, %s km, %s", r.MaxTimeDelta, num(r.MaxDistanceKm, 0), num(r.MaxMagnitudeDelta, 1))
}
