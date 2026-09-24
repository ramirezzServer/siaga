package quake

import (
	"cmp"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// sim menjalankan alur geo-processor di memori memakai fungsi domain yang sama
// dengan use case: Apply → Merge → Place → keanggotaan baru. Dipakai untuk
// property test pengelompokan tanpa database.
type sim struct {
	t       testing.TB
	rules   Rules
	reports map[ReportKey]Stored
	home    map[IdentityKey]EventID
	members map[EventID][]IdentityKey
	retired map[EventID]bool
	changes int
}

func newSim(t testing.TB) *sim {
	return &sim{
		t: t, rules: DefaultRules(),
		reports: map[ReportKey]Stored{}, home: map[IdentityKey]EventID{},
		members: map[EventID][]IdentityKey{}, retired: map[EventID]bool{},
	}
}

func (s *sim) solution(k IdentityKey) Solution {
	var rs []Stored
	for rk, st := range s.reports {
		if rk.IdentityKey == k {
			rs = append(rs, st)
		}
	}
	sol, err := Merge(rs)
	if err != nil {
		s.t.Fatal(err)
	}
	return sol
}

func (s *sim) apply(r Report) {
	s.t.Helper()
	var prev *Stored
	if st, ok := s.reports[r.Key()]; ok {
		prev = &st
	}
	next, write, changed := Apply(prev, r)
	if write {
		s.reports[r.Key()] = next
	}
	if !changed {
		return
	}
	s.changes++
	self := s.solution(r.Identity())
	var clusters []Cluster
	for id, keys := range s.members {
		c := Cluster{ID: id}
		for _, k := range keys {
			c.Members = append(c.Members, s.solution(k))
		}
		clusters = append(clusters, c)
	}
	slices.SortFunc(clusters, func(a, b Cluster) int { return a.ID.Compare(b.ID) })
	taken := func(id EventID) bool { return s.retired[id] }
	out, err := Settle(self, s.home[self.Key], clusters, s.rules, taken)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, ch := range out.Changes {
		for _, keys := range [][]IdentityKey{s.members[ch.ID]} {
			for _, k := range keys {
				if s.home[k] == ch.ID {
					delete(s.home, k)
				}
			}
		}
	}
	for _, ch := range out.Changes {
		if ch.Empty() {
			delete(s.members, ch.ID)
			s.retired[ch.ID] = true
			continue
		}
		keys := make([]IdentityKey, len(ch.Members))
		for i, m := range ch.Members {
			keys[i] = m.Key
			s.home[m.Key] = ch.ID
		}
		s.members[ch.ID] = keys
	}
}

// partition mengembalikan pengelompokan sebagai teks kanonis, lepas dari ID kejadian.
func (s *sim) partition() string {
	var groups []string
	for _, keys := range s.members {
		var names []string
		for _, k := range keys {
			names = append(names, k.String())
		}
		slices.Sort(names)
		groups = append(groups, strings.Join(names, "+"))
	}
	slices.Sort(groups)
	return strings.Join(groups, " | ")
}

// checkInvariants memeriksa invarian pengelompokan setelah setiap langkah.
func (s *sim) checkInvariants() {
	s.t.Helper()
	identities := map[IdentityKey]bool{}
	for rk := range s.reports {
		identities[rk.IdentityKey] = true
	}
	seen := map[IdentityKey]EventID{}
	for id, keys := range s.members {
		if len(keys) == 0 {
			s.t.Fatalf("kejadian %s aktif tanpa anggota", id)
		}
		var sols []Solution
		for _, k := range keys {
			if other, dup := seen[k]; dup {
				s.t.Fatalf("%s ada di dua kejadian %s dan %s", k, other, id)
			}
			seen[k] = id
			if s.home[k] != id {
				s.t.Fatalf("home %s = %s, bukan %s", k, s.home[k], id)
			}
			sols = append(sols, s.solution(k))
		}
		cur := Current(sols)
		if len(cur) == 0 {
			s.t.Fatalf("kejadian %s tanpa solusi berlaku", id)
		}
		for i := range cur {
			for j := i + 1; j < len(cur); j++ {
				if ok, _ := s.rules.CrossSource.Match(cur[i].Origin(), cur[j].Origin()); !ok {
					s.t.Fatalf("kejadian %s: %s dan %s tidak memenuhi ambang lintas sumber", id, cur[i].Key, cur[j].Key)
				}
			}
		}
	}
	for k := range identities {
		sol := s.solution(k)
		if _, in := seen[k]; in == sol.Deleted {
			s.t.Fatalf("%s: tergabung=%v padahal deleted=%v", k, in, sol.Deleted)
		}
	}
}

// scenario membangkitkan gempa-gempa "sebenarnya" yang terpisah jauh
// (≥ 5 menit), lalu laporan BMKG dan USGS dengan galat dalam batas yang
// realistis menurut kalibrasi: BMKG ≤ 3 dtk/15 km/0,3 dari kebenaran
// (termasuk revisi dengan ID baru), USGS ≤ 12 dtk/45 km/0,35 (termasuk revisi
// dengan ID sama dan sesekali ditarik).
type truth struct {
	at        time.Time
	lat, lon  float64
	magnitude float64
}

func jitter(rng *rand.Rand, q truth, dt time.Duration, km, dm float64) (time.Time, float64, float64, float64) {
	at := q.at.Add(time.Duration(rng.Int64N(int64(2*dt+1))) - dt).Truncate(time.Millisecond)
	bearing := rng.Float64() * 2 * math.Pi
	dist := rng.Float64() * km / EarthRadiusKm
	lat := q.lat + dist*math.Cos(bearing)*180/math.Pi
	lon := q.lon + dist*math.Sin(bearing)*180/math.Pi/math.Cos(q.lat*math.Pi/180)
	mag := math.Round((q.magnitude+(rng.Float64()*2-1)*dm)*10) / 10
	return at.UTC(), math.Round(lat*100) / 100, math.Round(lon*100) / 100, mag
}

func scenario(rng *rand.Rand, quakes int) ([]Report, map[IdentityKey]int) {
	var out []Report
	owner := map[IdentityKey]int{}
	fetch := t0
	for qi := range quakes {
		q := truth{
			at:        t0.Add(time.Duration(qi) * 7 * time.Minute),
			lat:       -8 + rng.Float64()*2.5,
			lon:       106 + rng.Float64()*3,
			magnitude: 3 + rng.Float64()*3,
		}
		if fetch.Before(q.at) {
			fetch = q.at
		}
		// BMKG: 1–2 solusi (revisi dengan ID baru), tiap solusi di 1–3 feed.
		if rng.IntN(10) < 9 {
			for range 1 + rng.IntN(2) {
				at, lat, lon, mag := jitter(rng, q, 1500*time.Millisecond, 7, 0.15)
				at = at.Truncate(time.Second)
				id := at.Format("20060102150405")
				owner[IdentityKey{SourceBMKG, id}] = qi
				fetch = fetch.Add(time.Duration(1+rng.IntN(30)) * time.Second)
				for _, feed := range []Feed{FeedBMKGLatest, FeedBMKGRecent, FeedBMKGFelt}[:1+rng.IntN(3)] {
					out = append(out, Report{
						Source: SourceBMKG, Feed: feed, EventID: id, OccurredAt: at,
						Latitude: lat, Longitude: lon, Magnitude: mag, DepthKm: 10,
						Place: "Pusat gempa berada di darat " + string(feed), Tsunami: TsunamiNone,
						FetchedAt: fetch.Add(time.Duration(rng.IntN(60)) * time.Second),
					})
				}
			}
		}
		// USGS: 0–1 ID dengan 1–3 revisi; revisi terakhir kadang "deleted".
		if rng.IntN(10) < 7 {
			id := fmt.Sprintf("us%04d", qi)
			owner[IdentityKey{SourceUSGS, id}] = qi
			revs := 1 + rng.IntN(3)
			for rv := range revs {
				at, lat, lon, mag := jitter(rng, q, 6*time.Second, 22, 0.175)
				status := "automatic"
				if rv == revs-1 && rng.IntN(8) == 0 {
					status = ReviewDeleted
				}
				fetch = fetch.Add(time.Duration(1+rng.IntN(30)) * time.Second)
				out = append(out, Report{
					Source: SourceUSGS, Feed: FeedUSGSSummary, EventID: id, OccurredAt: at,
					Latitude: lat, Longitude: lon, Magnitude: mag, MagnitudeType: "mb", DepthKm: 10,
					SourceUpdatedAt: at.Add(time.Duration(rv+1) * time.Minute), ReviewStatus: status,
					FetchedAt: fetch,
				})
			}
		}
	}
	return out, owner
}

// expected adalah pengelompokan yang benar: satu kejadian per gempa
// sebenarnya, berisi semua ID yang tidak ditarik.
func expected(s *sim, owner map[IdentityKey]int) string {
	byQuake := map[int][]string{}
	for k, qi := range owner {
		if !s.solution(k).Deleted {
			byQuake[qi] = append(byQuake[qi], k.String())
		}
	}
	var groups []string
	for _, names := range byQuake {
		slices.Sort(names)
		groups = append(groups, strings.Join(names, "+"))
	}
	slices.Sort(groups)
	return strings.Join(groups, " | ")
}

// Properti utama deduplikasi (PRD T4): untuk gempa yang terpisah jauh, setiap
// urutan kedatangan laporan — termasuk revisi, ulangan, dan laporan yang
// ditarik — menghasilkan tepat satu kejadian per gempa, tanpa gabungan salah
// dan tanpa kejadian ganda; invarian dicek di setiap langkah; memproses ulang
// semua laporan tidak mengubah apa pun.
func FuzzClusteringOrderIndependent(f *testing.F) {
	for _, seed := range []uint64{1, 2, 3, 7, 42, 2022} {
		f.Add(seed, uint8(6))
	}
	f.Fuzz(func(t *testing.T, seed uint64, n uint8) {
		rng := rand.New(rand.NewPCG(seed, seed*31+7))
		reports, owner := scenario(rng, 1+int(n%8))
		var want string
		for round := range 4 {
			order := slices.Clone(reports)
			if round > 0 {
				rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			}
			s := newSim(t)
			for _, r := range order {
				if err := r.Validate(); err != nil {
					t.Fatal(err)
				}
				s.apply(r)
				s.checkInvariants()
			}
			got := s.partition()
			if exp := expected(s, owner); got != exp {
				t.Fatalf("urutan %d:\n dapat %s\n ingin %s", round, got, exp)
			}
			if round == 0 {
				want = got
			} else if got != want {
				t.Fatalf("hasil bergantung urutan:\n %s\n %s", want, got)
			}
			before := s.changes
			for _, r := range order {
				s.apply(r)
			}
			if s.changes != before || s.partition() != got {
				t.Fatal("memproses ulang laporan yang sama mengubah keadaan")
			}
		}
	})
}

// Kejadian yang berdekatan (gempa susulan dalam hitungan detik) tidak dijamin
// terpisah sempurna, tetapi invarian tetap wajib berlaku untuk urutan apa pun.
func FuzzClusteringInvariantsUnderCrowding(f *testing.F) {
	f.Add(uint64(9), uint8(10))
	f.Fuzz(func(t *testing.T, seed uint64, n uint8) {
		rng := rand.New(rand.NewPCG(seed, ^seed))
		reports, _ := scenario(rng, 2+int(n%10))
		// Rapatkan semua gempa ke jendela 2 menit supaya kandidat saling tumpang tindih.
		for i := range reports {
			off := reports[i].OccurredAt.Sub(t0)
			squeeze := time.Duration(int64(off) % int64(2*time.Minute))
			shift := squeeze - off
			reports[i].OccurredAt = reports[i].OccurredAt.Add(shift)
			if reports[i].Source == SourceBMKG {
				reports[i].EventID = reports[i].OccurredAt.Format("20060102150405")
			}
			if !reports[i].SourceUpdatedAt.IsZero() {
				reports[i].SourceUpdatedAt = reports[i].SourceUpdatedAt.Add(shift)
			}
		}
		rng.Shuffle(len(reports), func(i, j int) { reports[i], reports[j] = reports[j], reports[i] })
		s := newSim(t)
		for _, r := range reports {
			s.apply(r)
			s.checkInvariants()
		}
	})
}

func TestScenarioSanity(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	reports, owner := scenario(rng, 8)
	if len(reports) < 8 || len(owner) < 8 {
		t.Fatalf("skenario terlalu kecil: %d laporan, %d ID", len(reports), len(owner))
	}
	s := newSim(t)
	for _, r := range reports {
		s.apply(r)
	}
	if s.partition() != expected(s, owner) {
		t.Fatalf("\n%s\n%s", s.partition(), expected(s, owner))
	}
	sizes := map[int]int{}
	for _, keys := range s.members {
		sizes[len(keys)]++
	}
	keys := slices.Collect(func(yield func(int) bool) {
		for k := range sizes {
			if !yield(k) {
				return
			}
		}
	})
	slices.SortFunc(keys, cmp.Compare)
	t.Logf("ukuran kejadian: %v", sizes)
}
