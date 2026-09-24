package quake

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func sol(src Source, id string, dt time.Duration, lat, lon, mag float64, seen time.Duration) Solution {
	return Solution{
		Key:         IdentityKey{Source: src, EventID: id},
		OccurredAt:  t0.Add(dt),
		Latitude:    lat,
		Longitude:   lon,
		Magnitude:   mag,
		DepthKm:     10,
		FirstSeenAt: t0.Add(seen),
	}
}

func TestEventIDFormat(t *testing.T) {
	id := NewEventID(IdentityKey{Source: SourceBMKG, EventID: "20260923121656"}, 0)
	s := id.String()
	if len(s) != 36 || s[14] != '8' || !strings.ContainsAny(s[19:20], "89ab") {
		t.Fatalf("bukan UUIDv8: %s", s)
	}
	back, err := ParseEventID(s)
	if err != nil || back != id {
		t.Fatalf("ParseEventID(%s) = %v, %v", s, back, err)
	}
	if NewEventID(IdentityKey{Source: SourceBMKG, EventID: "20260923121656"}, 1) == id {
		t.Error("generasi berbeda harus menghasilkan ID berbeda")
	}
	if id.IsZero() || !(EventID{}).IsZero() {
		t.Error("IsZero")
	}
	for _, bad := range []string{"", "x", strings.Repeat("g", 36), "0123456789ab-cdef-0123-4567-89abcdef0123", "01234567-89ab-cdef-0123-456789abcdeg"} {
		if _, err := ParseEventID(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseEventID(%q) harus gagal", bad)
		}
	}
}

func TestCurrentPrefersLatestSeenPerSourceAndOrdersBySource(t *testing.T) {
	old := sol(SourceBMKG, "20260923121656", 0, -7, 107, 5, 0)
	rev := sol(SourceBMKG, "20260923121658", 2*time.Second, -7, 107, 5.1, time.Minute)
	us := sol(SourceUSGS, "us1", time.Second, -7, 107, 5, 30*time.Second)
	gone := sol(SourceUSGS, "us2", time.Second, -7, 107, 5, time.Hour)
	gone.Deleted = true
	cur := Current([]Solution{us, rev, gone, old})
	if len(cur) != 2 || cur[0].Key != rev.Key || cur[1].Key != us.Key {
		t.Fatalf("Current = %+v", cur)
	}
	tie := sol(SourceBMKG, "20260923121659", 3*time.Second, -7, 107, 5, time.Minute)
	if cur := Current([]Solution{rev, tie}); cur[0].Key != tie.Key {
		t.Error("waktu terlihat kembar diputus dengan ID")
	}
}

func place(t *testing.T, self Solution, home EventID, clusters ...Cluster) (Placement, []Change) {
	t.Helper()
	p, ch, err := Place(self, home, clusters, DefaultRules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, ch
}

func TestPlaceJoinsCrossSourceReport(t *testing.T) {
	b := sol(SourceBMKG, "20260923121656", 0, -6.85, 107.03, 5.6, 0)
	e := Cluster{ID: NewEventID(b.Key, 0), Members: []Solution{b}}
	u := sol(SourceUSGS, "us7000ir9t", -2800*time.Millisecond, -6.836, 106.9968, 5.6, time.Minute)

	p, ch := place(t, u, EventID{}, e)
	if p.Target != e.ID || p.Create || p.Moved() {
		t.Fatalf("placement %+v", p)
	}
	if len(ch) != 1 || len(ch[0].Members) != 2 {
		t.Fatalf("changes %+v", ch)
	}
}

func TestPlaceCreatesEventWhenNothingMatches(t *testing.T) {
	b := sol(SourceBMKG, "20260923121656", 0, -6.85, 107.03, 5.6, 0)
	e := Cluster{ID: NewEventID(b.Key, 0), Members: []Solution{b}}
	far := sol(SourceUSGS, "us1", 0, -8.5, 107.03, 5.6, 0) // ±183 km
	p, ch := place(t, far, EventID{}, e)
	if !p.Create || p.Target != NewEventID(far.Key, 0) || len(ch) != 1 || !ch[0].Created {
		t.Fatalf("placement %+v changes %+v", p, ch)
	}
}

func TestPlaceKeepsDoubletsApart(t *testing.T) {
	// Dua gempa berbeda 40 detik terpisah; BMKG melaporkan keduanya, USGS satu.
	b1 := sol(SourceBMKG, "A", 0, -7, 107, 4.5, 0)
	u1 := sol(SourceUSGS, "u1", time.Second, -7.05, 107, 4.6, time.Minute)
	e := Cluster{ID: NewEventID(b1.Key, 0), Members: []Solution{b1, u1}}
	b2 := sol(SourceBMKG, "B", 25*time.Second, -7.1, 107, 4.4, 2*time.Minute)
	p, _ := place(t, b2, EventID{}, e)
	if p.Target == e.ID {
		t.Fatal("BMKG kedua bukan revisi BMKG pertama, tidak boleh bergabung walau cocok dengan USGS")
	}
}

func TestPlaceMergesSameSourceRevision(t *testing.T) {
	b1 := sol(SourceBMKG, "20260923121656", 0, -7, 107, 5.0, 0)
	e := Cluster{ID: NewEventID(b1.Key, 0), Members: []Solution{b1}}
	b2 := sol(SourceBMKG, "20260923121658", 2*time.Second, -7.05, 107.02, 4.8, 5*time.Minute)
	p, ch := place(t, b2, EventID{}, e)
	if p.Target != e.ID || len(ch[0].Members) != 2 {
		t.Fatalf("revisi BMKG harus bergabung: %+v", p)
	}
	cur := Current(ch[0].Members)
	if len(cur) != 1 || cur[0].Key != b2.Key {
		t.Errorf("revisi terbaru harus berlaku: %+v", cur)
	}

	// USGS yang saling menyebut ID alternatif bergabung walau berbeda parameter.
	u1 := sol(SourceUSGS, "us1", 0, -7, 107, 5.0, 0)
	u1.AlternateIDs = []string{"at1"}
	eu := Cluster{ID: NewEventID(u1.Key, 0), Members: []Solution{u1}}
	u2 := sol(SourceUSGS, "at1", 9*time.Second, -7.3, 107, 5.0, time.Minute)
	if p, _ := place(t, u2, EventID{}, eu); p.Target != eu.ID {
		t.Errorf("ID alternatif harus bergabung: %+v", p)
	}
}

func TestPlaceStickyHomeAndMergeWhenRevisedIntoRange(t *testing.T) {
	b := sol(SourceBMKG, "B", 0, -7, 107, 5.0, 0)
	eb := Cluster{ID: NewEventID(b.Key, 0), Members: []Solution{b}}
	// USGS otomatis awal meleset 150 km: berdiri sendiri.
	u := sol(SourceUSGS, "u", 0, -8.35, 107, 5.0, time.Minute)
	eu := Cluster{ID: NewEventID(u.Key, 0), Members: []Solution{u}}

	// Revisi USGS masuk jangkauan: pindah ke kejadian BMKG, kejadian lamanya kosong.
	u2 := u
	u2.Latitude = -7.2
	p, ch := place(t, u2, eu.ID, eb, eu)
	if p.Target != eb.ID || !p.Moved() || p.Create {
		t.Fatalf("placement %+v", p)
	}
	if len(ch) != 2 || ch[0].ID != eu.ID || !ch[0].Empty() || ch[1].ID != eb.ID || len(ch[1].Members) != 2 {
		t.Fatalf("changes %+v", ch)
	}

	// Kejadian gabungan: revisi kecil tetap di tempat (lengket).
	merged := Cluster{ID: eb.ID, Members: []Solution{b, u2}}
	u3 := u2
	u3.Magnitude = 5.2
	p, ch = place(t, u3, eb.ID, merged, eu)
	if p.Target != eb.ID || p.Moved() || len(ch) != 1 {
		t.Fatalf("revisi kecil harus tetap: %+v %+v", p, ch)
	}

	// Revisi yang tidak lagi cocok memisahkan diri ke kejadian baru.
	u4 := u2
	u4.Latitude = -9
	p, ch = place(t, u4, eb.ID, merged)
	if !p.Create || !p.Moved() || len(ch) != 2 || len(ch[0].Members) != 1 {
		t.Fatalf("revisi menjauh harus berpisah: %+v %+v", p, ch)
	}
	// ID generasi 0 milik u sudah terpakai (kejadian lama), jadi generasi berikutnya.
	p, _, err := Place(u4, eb.ID, []Cluster{merged}, DefaultRules(), func(id EventID) bool { return id == eu.ID })
	if err != nil || p.Target != NewEventID(u.Key, 1) {
		t.Fatalf("ID baru harus melewati yang terpakai: %+v %v", p, err)
	}

	// Solusi tunggal yang tidak cocok ke mana pun tetap di kejadiannya sendiri.
	lonely := Cluster{ID: eu.ID, Members: []Solution{u4}}
	u5 := u4
	u5.Magnitude = 5.1
	if p, _ := place(t, u5, eu.ID, lonely, eb); p.Target != eu.ID || p.Moved() {
		t.Fatalf("solusi tunggal harus tetap: %+v", p)
	}
}

func TestPlaceDetachesDeletedReport(t *testing.T) {
	b := sol(SourceBMKG, "B", 0, -7, 107, 5, 0)
	u := sol(SourceUSGS, "u", 0, -7, 107, 5, time.Minute)
	e := Cluster{ID: NewEventID(b.Key, 0), Members: []Solution{b, u}}
	gone := u
	gone.Deleted = true
	p, ch := place(t, gone, e.ID, e)
	if !p.Target.IsZero() || !p.Moved() || len(ch) != 1 || len(ch[0].Members) != 1 {
		t.Fatalf("placement %+v changes %+v", p, ch)
	}
	alone := Cluster{ID: NewEventID(u.Key, 0), Members: []Solution{u}}
	_, ch = place(t, gone, alone.ID, alone)
	if len(ch) != 1 || !ch[0].Empty() {
		t.Fatalf("kejadian tanpa anggota harus kosong: %+v", ch)
	}
	if p, ch := place(t, gone, EventID{}); !p.Target.IsZero() || len(ch) != 0 {
		t.Fatal("laporan ditarik tanpa kejadian tidak mengubah apa pun")
	}
}

func TestPlaceChoosesBestScoreAndRequiresHome(t *testing.T) {
	u := sol(SourceUSGS, "u", 0, -7, 107, 5, 0)
	near := sol(SourceBMKG, "near", time.Second, -7.01, 107, 5, 0)
	far := sol(SourceBMKG, "far", 5*time.Second, -7.5, 107, 5.4, 0)
	cn := Cluster{ID: NewEventID(near.Key, 0), Members: []Solution{near}}
	cf := Cluster{ID: NewEventID(far.Key, 0), Members: []Solution{far}}
	if p, _ := place(t, u, EventID{}, cf, cn); p.Target != cn.ID {
		t.Errorf("harus memilih kandidat terdekat: %+v", p)
	}
	if _, _, err := Place(u, cn.ID, []Cluster{cf}, DefaultRules(), nil); !errors.Is(err, ErrHomeMissing) {
		t.Errorf("err = %v, ingin ErrHomeMissing", err)
	}
}

func TestPlaceGivesUpWhenEveryIDIsTaken(t *testing.T) {
	u := sol(SourceUSGS, "u", 0, -7, 107, 5, 0)
	_, _, err := Place(u, EventID{}, nil, DefaultRules(), func(EventID) bool { return true })
	if !errors.Is(err, ErrNoFreshID) {
		t.Fatalf("err = %v, ingin ErrNoFreshID", err)
	}
	if _, err := Settle(u, EventID{}, nil, DefaultRules(), func(EventID) bool { return true }); !errors.Is(err, ErrNoFreshID) {
		t.Fatalf("Settle err = %v", err)
	}
}
