package quake

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

// EventID adalah ID kejadian bahaya (UUIDv8 deterministik, lihat hazard.EventID).
type EventID = hazard.EventID

// NewEventID membentuk ID kejadian gempa yang didirikan founder. generation > 0
// dipakai bila ID generasi sebelumnya sudah terpakai (misal kejadian lama yang
// sudah digabung ke kejadian lain, lalu laporan pendirinya berpisah lagi).
func NewEventID(founder IdentityKey, generation int) EventID {
	return hazard.NewEventID("siaga/hazard/quake", generation, string(founder.Source), founder.EventID)
}

// ParseEventID membaca UUID berformat 8-4-4-4-12.
func ParseEventID(s string) (EventID, error) {
	id, err := hazard.ParseEventID(s)
	if err != nil {
		return EventID{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return id, nil
}

// Cluster adalah satu kejadian beserta semua solusi yang tergabung di dalamnya,
// termasuk solusi lama dari sumber yang sama yang sudah digantikan revisi.
type Cluster struct {
	ID      EventID
	Members []Solution
}

// Rules adalah ambang deduplikasi yang dipakai pengelompokan.
type Rules struct {
	// CrossSource membandingkan solusi dari sumber berbeda (BMKG vs USGS).
	CrossSource Rule
	// Revision membandingkan dua ID berbeda dari sumber yang sama.
	Revision Rule
}

// DefaultRules mengembalikan ambang bawaan hasil kalibrasi.
func DefaultRules() Rules {
	return Rules{CrossSource: DefaultCrossSourceRule, Revision: DefaultRevisionRule}
}

// Validate memastikan kedua ambang valid.
func (r Rules) Validate() error {
	return errors.Join(r.CrossSource.Validate(), r.Revision.Validate())
}

// Current mengembalikan solusi yang berlaku per sumber: di antara beberapa ID
// dari sumber yang sama, yang paling akhir terlihat adalah revisi terbaru.
// Solusi yang ditarik sumber tidak pernah berlaku. Hasil terurut menurut
// prioritas sumber (BMKG dulu).
func Current(members []Solution) []Solution {
	best := map[Source]Solution{}
	for _, m := range members {
		if m.Deleted {
			continue
		}
		b, ok := best[m.Key.Source]
		if !ok || m.FirstSeenAt.After(b.FirstSeenAt) ||
			(m.FirstSeenAt.Equal(b.FirstSeenAt) && m.Key.Compare(b.Key) > 0) {
			best[m.Key.Source] = m
		}
	}
	out := make([]Solution, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Solution) int { return cmp.Compare(a.Key.Source.rank(), b.Key.Source.rank()) })
	return out
}

// fits melaporkan apakah self boleh bergabung dengan anggota others, beserta
// skor terburuknya. Syaratnya: cocok dengan solusi yang berlaku dari setiap
// sumber lain (ambang lintas sumber), dan bila sumbernya sendiri sudah ada di
// kejadian, self harus revisi dari solusi itu. Syarat kedua mencegah dua gempa
// berbeda yang dilaporkan satu sumber tergabung karena sumber lain hanya
// melaporkan salah satunya.
func fits(self Solution, others []Solution, rules Rules) (bool, float64) {
	cur := Current(others)
	if len(cur) == 0 {
		return false, math.Inf(1)
	}
	worst := 0.0
	for _, m := range cur {
		var ok bool
		var score float64
		switch {
		case m.Key.Source != self.Key.Source:
			ok, score = rules.CrossSource.Match(self.Origin(), m.Origin())
		case self.linked(m):
			ok, score = true, 0
		default:
			ok, score = rules.Revision.Match(self.Origin(), m.Origin())
		}
		if !ok {
			return false, math.Inf(1)
		}
		worst = math.Max(worst, score)
	}
	return true, worst
}

func without(members []Solution, key IdentityKey) []Solution {
	return slices.DeleteFunc(slices.Clone(members), func(m Solution) bool { return m.Key == key })
}

// Placement adalah keputusan pengelompokan untuk satu solusi.
type Placement struct {
	// Target adalah kejadian tempat solusi berada setelah keputusan; kosong bila
	// solusi ditarik sumbernya dan tidak lagi menjadi bagian kejadian mana pun.
	Target EventID
	// Create true bila Target kejadian baru yang didirikan solusi ini.
	Create bool
	// Home adalah kejadian solusi sebelum keputusan (kosong bila belum ada).
	Home EventID
}

// Moved melaporkan apakah solusi meninggalkan kejadian lamanya.
func (p Placement) Moved() bool { return !p.Home.IsZero() && p.Home != p.Target }

// Change adalah keanggotaan baru satu kejadian yang terdampak keputusan.
type Change struct {
	ID      EventID
	Members []Solution
	Created bool
}

// Empty melaporkan apakah kejadian tidak lagi punya anggota.
func (c Change) Empty() bool { return len(c.Members) == 0 }

// ErrHomeMissing berarti kejadian asal solusi tidak ada di daftar kandidat;
// pemanggil wajib menyertakannya.
var ErrHomeMissing = errors.New("kejadian asal tidak ada di kandidat")

// Place menentukan kejadian untuk solusi self yang baru berubah, lalu
// mengembalikan keanggotaan baru semua kejadian yang terdampak (kejadian asal
// dan tujuan).
//
// clusters berisi kejadian di sekitar waktu self, termasuk kejadian asalnya
// (home) bila ada. taken melaporkan ID yang sudah terpakai di penyimpanan
// (termasuk kejadian yang sudah tidak aktif), untuk membentuk ID kejadian baru.
//
// Urutan keputusan:
//  1. Solusi yang ditarik sumbernya keluar dari kejadian.
//  2. Bila kejadian asal masih punya anggota lain dan self masih cocok, tetap
//     di sana (lengket, supaya kejadian tidak berpindah-pindah).
//  3. Bila tidak, gabung ke kandidat lain yang cocok dengan skor terbaik.
//  4. Bila tidak ada, tetap di kejadian asal bila self satu-satunya anggota,
//     atau dirikan kejadian baru.
func Place(self Solution, home EventID, clusters []Cluster, rules Rules, taken func(EventID) bool) (Placement, []Change, error) {
	p := Placement{Home: home}
	var homeC *Cluster
	for i := range clusters {
		if clusters[i].ID == home {
			homeC = &clusters[i]
		}
	}
	if !home.IsZero() && homeC == nil {
		return p, nil, fmt.Errorf("%w: %s", ErrHomeMissing, home)
	}
	var homeOthers []Solution
	if homeC != nil {
		homeOthers = without(homeC.Members, self.Key)
	}

	switch {
	case self.Deleted:
		// Target tetap kosong.
	case homeC != nil && len(homeOthers) > 0 && first(fits(self, homeOthers, rules)):
		p.Target = home
	default:
		bestScore := math.Inf(1)
		for _, c := range clusters {
			if c.ID == home {
				continue
			}
			ok, score := fits(self, without(c.Members, self.Key), rules)
			if ok && (score < bestScore || (score == bestScore && c.ID.Compare(p.Target) < 0)) {
				p.Target, bestScore = c.ID, score
			}
		}
		if p.Target.IsZero() {
			if homeC != nil && len(homeOthers) == 0 {
				p.Target = home
			} else {
				id, err := freshID(self.Key, clusters, taken)
				if err != nil {
					return p, nil, err
				}
				p.Target, p.Create = id, true
			}
		}
	}

	var changes []Change
	if homeC != nil && p.Moved() {
		changes = append(changes, Change{ID: home, Members: homeOthers})
	}
	if !p.Target.IsZero() {
		var members []Solution
		for _, c := range clusters {
			if c.ID == p.Target {
				members = without(c.Members, self.Key)
			}
		}
		members = append(members, self)
		slices.SortFunc(members, func(a, b Solution) int { return a.Key.Compare(b.Key) })
		changes = append(changes, Change{ID: p.Target, Members: members, Created: p.Create})
	}
	return p, changes, nil
}

func first(ok bool, _ float64) bool { return ok }

// maxGenerations membatasi pencarian ID baru; ribuan generasi untuk satu
// laporan berarti fungsi taken salah, bukan data nyata.
const maxGenerations = 1000

// ErrNoFreshID berarti tidak ada ID kejadian baru yang belum terpakai.
var ErrNoFreshID = errors.New("tidak ada ID kejadian yang belum terpakai")

func freshID(founder IdentityKey, clusters []Cluster, taken func(EventID) bool) (EventID, error) {
	for gen := range maxGenerations {
		id := NewEventID(founder, gen)
		inUse := slices.ContainsFunc(clusters, func(c Cluster) bool { return c.ID == id })
		if !inUse && (taken == nil || !taken(id)) {
			return id, nil
		}
	}
	return EventID{}, fmt.Errorf("%w untuk %s", ErrNoFreshID, founder)
}

// Outcome adalah hasil akhir Settle: keanggotaan baru semua kejadian yang
// terdampak, terurut menurut ID.
type Outcome struct {
	Changes []Change
}

// ErrUnsettled berarti pengelompokan tidak stabil dalam batas langkah; ini
// tanda bug, bukan kondisi data.
var ErrUnsettled = errors.New("pengelompokan tidak stabil")

// Settle menempatkan self lalu menstabilkan kejadian yang terdampak.
//
// Menempatkan satu solusi bisa mengubah solusi yang berlaku di kejadian lain:
// bila revisi BMKG terbaru keluar dari kejadian (atau waktu terlihat pertamanya
// berubah), revisi BMKG yang lebih lama kembali berlaku padahal belum pernah
// dicek terhadap solusi USGS di kejadian itu. Setiap solusi yang baru mulai
// berlaku seperti ini ditempatkan ulang dengan Place, sampai tidak ada lagi
// yang berubah. Dengan begitu invarian ini selalu terjaga: di setiap kejadian,
// setiap pasangan solusi berlaku dari sumber berbeda memenuhi ambang lintas
// sumber.
func Settle(self Solution, home EventID, clusters []Cluster, rules Rules, taken func(EventID) bool) (Outcome, error) {
	work := map[EventID][]Solution{}
	for _, c := range clusters {
		work[c.ID] = slices.Clone(c.Members)
	}
	created := map[EventID]bool{}
	touched := map[EventID]bool{}
	isTaken := func(id EventID) bool { return created[id] || (taken != nil && taken(id)) }
	snapshot := func() []Cluster {
		out := make([]Cluster, 0, len(work))
		for id, m := range work {
			out = append(out, Cluster{ID: id, Members: m})
		}
		slices.SortFunc(out, func(a, b Cluster) int { return a.ID.Compare(b.ID) })
		return out
	}
	currentKeys := func(members []Solution) map[IdentityKey]bool {
		keys := map[IdentityKey]bool{}
		for _, s := range Current(members) {
			keys[s.Key] = true
		}
		return keys
	}

	type task struct {
		sol  Solution
		home EventID
	}
	queue := []task{{self, home}}
	limit := 4 * (1 + len(clusters) + totalMembers(clusters))
	for steps := 0; len(queue) > 0; steps++ {
		if steps > limit {
			return Outcome{}, fmt.Errorf("%w setelah %d langkah (%s)", ErrUnsettled, steps, self.Key)
		}
		t := queue[0]
		queue = queue[1:]
		_, changes, err := Place(t.sol, t.home, snapshot(), rules, isTaken)
		if err != nil {
			return Outcome{}, err
		}
		for _, ch := range changes {
			before := currentKeys(work[ch.ID])
			touched[ch.ID] = true
			if ch.Created {
				created[ch.ID] = true
			}
			if ch.Empty() {
				delete(work, ch.ID)
				continue
			}
			work[ch.ID] = ch.Members
			if ch.Created {
				continue
			}
			// Langkah pertama: kandidat datang dari penyimpanan yang sudah memuat
			// laporan terbaru self, jadi "sebelum" tidak diketahui; semua solusi
			// berlaku di kejadian terdampak dicek ulang. Langkah berikutnya cukup
			// yang baru mulai berlaku.
			for _, s := range Current(ch.Members) {
				if s.Key != t.sol.Key && (steps == 0 || !before[s.Key]) {
					queue = append(queue, task{s, ch.ID})
				}
			}
		}
	}

	var out Outcome
	for id := range touched {
		members, alive := work[id]
		if !alive && created[id] {
			continue // dibuat lalu dikosongkan dalam langkah yang sama
		}
		out.Changes = append(out.Changes, Change{ID: id, Members: members, Created: created[id]})
	}
	slices.SortFunc(out.Changes, func(a, b Change) int { return a.ID.Compare(b.ID) })
	return out, nil
}

func totalMembers(clusters []Cluster) int {
	n := 0
	for _, c := range clusters {
		n += len(c.Members)
	}
	return n
}
