package quake

import (
	"errors"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 23, 12, 16, 56, 0, time.UTC)

func bmkgReport(feed Feed) Report {
	return Report{
		Source:        SourceBMKG,
		Feed:          feed,
		EventID:       "20260923121656",
		OccurredAt:    t0,
		Latitude:      -6.85,
		Longitude:     107.03,
		Magnitude:     5.6,
		DepthKm:       11,
		Place:         "Pusat gempa berada di darat 10 km barat daya Kab. Cianjur",
		Felt:          "V Cianjur",
		Tsunami:       TsunamiNone,
		PotentialText: "Tidak berpotensi tsunami",
		FetchedAt:     t0.Add(3 * time.Minute),
	}
}

func usgsReport() Report {
	return Report{
		Source:          SourceUSGS,
		Feed:            FeedUSGSSummary,
		EventID:         "us7000ir9t",
		OccurredAt:      t0.Add(-2800 * time.Millisecond),
		Latitude:        -6.836,
		Longitude:       106.9968,
		Magnitude:       5.6,
		MagnitudeType:   "mww",
		DepthKm:         10,
		Place:           "11 km NE of Sukabumi, Indonesia",
		SourceUpdatedAt: t0.Add(20 * time.Minute),
		ReviewStatus:    "reviewed",
		AlternateIDs:    []string{"at00rlqzvj"},
		SourceURL:       "https://earthquake.usgs.gov/earthquakes/eventpage/us7000ir9t",
		FetchedAt:       t0.Add(21 * time.Minute),
	}
}

func TestValidateAcceptsRealReports(t *testing.T) {
	for _, r := range []Report{bmkgReport(FeedBMKGLatest), bmkgReport(FeedBMKGFelt), usgsReport()} {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.Feed, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	cases := map[string]func(*Report){
		"feed asing":           func(r *Report) { r.Feed = "bmkg-lain" },
		"feed sumber lain":     func(r *Report) { r.Feed = FeedUSGSSummary },
		"ID kosong":            func(r *Report) { r.EventID = "" },
		"ID berspasi":          func(r *Report) { r.EventID = "2026 09" },
		"ID alternatif rusak":  func(r *Report) { r.AlternateIDs = []string{"a\tb"} },
		"ID alternatif banyak": func(r *Report) { r.AlternateIDs = make([]string, maxAltIDs+1) },
		"waktu kosong":         func(r *Report) { r.OccurredAt = time.Time{} },
		"waktu bukan UTC":      func(r *Report) { r.OccurredAt = r.OccurredAt.In(wib) },
		"ambil kosong":         func(r *Report) { r.FetchedAt = time.Time{} },
		"revisi bukan UTC":     func(r *Report) { r.SourceUpdatedAt = t0.In(wib) },
		"masa depan":           func(r *Report) { r.OccurredAt = r.FetchedAt.Add(MaxClockSkew + time.Second) },
		"lintang":              func(r *Report) { r.Latitude = 91 },
		"bujur NaN":            func(r *Report) { r.Longitude = math.NaN() },
		"magnitudo":            func(r *Report) { r.Magnitude = 10.1 },
		"kedalaman":            func(r *Report) { r.DepthKm = 801 },
		"tsunami":              func(r *Report) { r.Tsunami = 9 },
		"teks bukan UTF-8":     func(r *Report) { r.Place = "\xff" },
		"teks NUL":             func(r *Report) { r.Felt = "III\x00" },
		"teks panjang":         func(r *Report) { r.Place = strings.Repeat("a", maxTextLen+1) },
		"URL http":             func(r *Report) { r.ShakemapURL = "http://bmkg.go.id/x.jpg" },
		"URL relatif":          func(r *Report) { r.SourceURL = "/x" },
		"URL rusak":            func(r *Report) { r.SourceURL = "https://%zz" },
	}
	for name, mutate := range cases {
		r := bmkgReport(FeedBMKGLatest)
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, ingin ErrInvalid", name, err)
		}
	}
}

func TestContentDigestIgnoresFetchMeta(t *testing.T) {
	a := usgsReport()
	b := a
	b.FetchedAt = a.FetchedAt.Add(time.Hour)
	b.ArchiveKey = "usgs-summary/2026/09/23/x.json.gz"
	if a.ContentDigest() != b.ContentDigest() {
		t.Error("waktu ambil dan kunci arsip tidak boleh mengubah digest")
	}
	b.Magnitude = 5.7
	if a.ContentDigest() == b.ContentDigest() {
		t.Error("magnitudo berbeda harus mengubah digest")
	}
	c := a
	c.AlternateIDs = []string{"at00", "rlqzvj"}
	d := a
	d.AlternateIDs = []string{"at00rlqzvj"}
	if c.ContentDigest() == d.ContentDigest() {
		t.Error("pemisahan ID alternatif harus tercermin di digest")
	}
}

func TestApplyKeepsNewestAndEarliestFirstSeen(t *testing.T) {
	first := usgsReport()
	st, write, changed := Apply(nil, first)
	if !write || !changed || !st.FirstSeenAt.Equal(first.FetchedAt) {
		t.Fatalf("laporan pertama: %+v %v %v", st, write, changed)
	}

	same := first
	same.FetchedAt = first.FetchedAt.Add(time.Minute)
	st2, write, changed := Apply(&st, same)
	if !write || changed {
		t.Fatalf("isi sama yang lebih baru: write=%v changed=%v, ingin true,false", write, changed)
	}
	if !st2.FirstSeenAt.Equal(first.FetchedAt) {
		t.Error("FirstSeenAt harus tetap yang paling awal")
	}

	older := first
	older.Magnitude = 5.4
	older.SourceUpdatedAt = first.SourceUpdatedAt.Add(-time.Minute)
	older.FetchedAt = first.FetchedAt.Add(-time.Minute)
	st3, write, changed := Apply(&st2, older)
	if !write || !changed || st3.Magnitude != first.Magnitude || !st3.FirstSeenAt.Equal(older.FetchedAt) {
		t.Fatalf("revisi lama: isi harus tetap, FirstSeenAt turun: %+v write=%v changed=%v", st3, write, changed)
	}

	if _, write, changed := Apply(&st3, older); write || changed {
		t.Error("pesan ulangan tidak boleh mengubah apa pun")
	}
}

// Properti: hasil Apply atas sekumpulan laporan satu feed tidak bergantung
// urutan kedatangan maupun pengulangan pesan.
func FuzzApplyOrderIndependent(f *testing.F) {
	f.Add(uint64(1), uint8(5))
	f.Add(uint64(42), uint8(12))
	f.Fuzz(func(t *testing.T, seed uint64, n uint8) {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		count := int(n%16) + 1
		reports := make([]Report, count)
		for i := range reports {
			r := usgsReport()
			// Sedikit variasi supaya ada revisi, ulangan, dan waktu kembar.
			r.SourceUpdatedAt = t0.Add(time.Duration(rng.IntN(4)) * time.Minute)
			r.FetchedAt = t0.Add(time.Duration(rng.IntN(6)) * time.Minute)
			r.Magnitude = 5 + float64(rng.IntN(3))/10
			reports[i] = r
		}
		run := func(order []int) Stored {
			var st *Stored
			for _, i := range order {
				next, write, _ := Apply(st, reports[i])
				if write {
					st = &next
				}
			}
			return *st
		}
		base := make([]int, count)
		for i := range base {
			base[i] = i
		}
		want := run(base)
		for range 5 {
			order := append(base[:0:0], base...)
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			order = append(order, order[:rng.IntN(len(order))]...) // ulangan
			got := run(order)
			if got.ContentDigest() != want.ContentDigest() || !got.FetchedAt.Equal(want.FetchedAt) || !got.FirstSeenAt.Equal(want.FirstSeenAt) {
				t.Fatalf("urutan %v menghasilkan %+v, ingin %+v", order, got, want)
			}
		}
	})
}

func TestParseTsunamiRoundTrip(t *testing.T) {
	for _, v := range []Tsunami{TsunamiUnknown, TsunamiNone, TsunamiPotential} {
		got, err := ParseTsunami(v.String())
		if err != nil || got != v {
			t.Errorf("ParseTsunami(%q) = %v, %v", v.String(), got, err)
		}
	}
	if _, err := ParseTsunami("mungkin"); !errors.Is(err, ErrInvalid) {
		t.Errorf("nilai asing harus ErrInvalid, dapat %v", err)
	}
	if Tsunami(7).String() != "Tsunami(7)" {
		t.Error("String nilai asing")
	}
}

func TestFeedSourceAndIdentity(t *testing.T) {
	if Feed("x").Source() != "" || FeedBMKGFelt.Source() != SourceBMKG {
		t.Error("Feed.Source")
	}
	r := bmkgReport(FeedBMKGRecent)
	if r.Key().Feed != FeedBMKGRecent || r.Identity().String() != "bmkg:20260923121656" {
		t.Errorf("kunci laporan %+v", r.Key())
	}
	if r.Deleted() {
		t.Error("BMKG tidak pernah deleted")
	}
	if Source("x").rank() <= SourceUSGS.rank() {
		t.Error("sumber asing harus berprioritas paling rendah")
	}
}
