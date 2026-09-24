package quake

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func stored(r Report) Stored { return Stored{Report: r, FirstSeenAt: r.FetchedAt} }

func TestMergeTakesNumbersFromNewestAndTextFromNewestFilled(t *testing.T) {
	latest := bmkgReport(FeedBMKGLatest)
	latest.ShakemapURL = "https://data.bmkg.go.id/DataMKG/TEWS/20260923121656.mmi.jpg"
	latest.Felt = ""
	felt := bmkgReport(FeedBMKGFelt)
	felt.FetchedAt = latest.FetchedAt.Add(time.Minute)
	felt.Magnitude = 5.5
	felt.Tsunami = TsunamiUnknown
	felt.PotentialText = ""
	recent := bmkgReport(FeedBMKGRecent)
	recent.FetchedAt = latest.FetchedAt.Add(-time.Minute)

	for _, order := range [][]Stored{
		{stored(latest), stored(felt), stored(recent)},
		{stored(recent), stored(felt), stored(latest)},
	} {
		s, err := Merge(order)
		if err != nil {
			t.Fatal(err)
		}
		if s.Magnitude != 5.5 || s.Felt != "V Cianjur" || s.ShakemapURL == "" || s.Tsunami != TsunamiNone ||
			!s.FirstSeenAt.Equal(recent.FetchedAt) || s.PotentialText == "" {
			t.Fatalf("Merge = %+v", s)
		}
	}
	if _, err := Merge(nil); !errors.Is(err, ErrNoReports) {
		t.Error("Merge kosong")
	}
	other := bmkgReport(FeedBMKGFelt)
	other.EventID = "20260923121700"
	if _, err := Merge([]Stored{stored(latest), stored(other)}); !errors.Is(err, ErrInvalid) {
		t.Error("laporan beda kejadian harus ditolak")
	}
}

func TestDeriveUsesBMKGAsPrimary(t *testing.T) {
	p := DefaultPolicy()
	b, _ := Merge([]Stored{stored(bmkgReport(FeedBMKGLatest))})
	u, _ := Merge([]Stored{stored(usgsReport())})
	id := NewEventID(u.Key, 0)
	v, err := p.Derive(id, []Solution{u, b})
	if err != nil {
		t.Fatal(err)
	}
	if v.Primary.Key != b.Key || len(v.Corroborating) != 1 || v.Corroborating[0].Key != u.Key {
		t.Fatalf("primary %v corroborating %v", v.Primary.Key, v.Corroborating)
	}
	if !v.DetectedAt.Equal(b.FirstSeenAt) || !v.ExpiresAt.Equal(b.OccurredAt.Add(6*time.Hour)) {
		t.Errorf("waktu turunan salah: %+v", v)
	}
	if v.Title != "Gempa M5,6 — darat 10 km barat daya Kab. Cianjur" {
		t.Errorf("judul %q", v.Title)
	}
	for _, want := range []string{"Kedalaman 11 km.", "Dirasakan (skala MMI): V Cianjur.", "Estimasi radius dirasakan 62 km.", "Tidak berpotensi tsunami.", "Sumber: BMKG."} {
		if !strings.Contains(v.Summary, want) {
			t.Errorf("ringkasan %q tanpa %q", v.Summary, want)
		}
	}

	vu, _ := p.Derive(id, []Solution{u})
	if vu.Title != "Gempa M5,6 — 11 km NE of Sukabumi, Indonesia" || !strings.Contains(vu.Summary, "Sumber: USGS.") {
		t.Errorf("USGS saja: %q / %q", vu.Title, vu.Summary)
	}
	deep := u
	deep.DepthKm = 400
	deep.Tsunami = TsunamiPotential
	vd, _ := p.Derive(id, []Solution{deep})
	if !strings.Contains(vd.Summary, "tidak dirasakan di permukaan") || !strings.Contains(vd.Summary, "berpotensi tsunami") {
		t.Errorf("ringkasan gempa dalam %q", vd.Summary)
	}

	gone := u
	gone.Deleted = true
	if _, err := p.Derive(id, []Solution{gone}); !errors.Is(err, ErrNoCurrent) {
		t.Error("kejadian tanpa solusi berlaku harus gagal")
	}
}

func TestAssessmentDigestReflectsPublishedContent(t *testing.T) {
	p := DefaultPolicy()
	b, _ := Merge([]Stored{stored(bmkgReport(FeedBMKGLatest))})
	v, _ := p.Derive(NewEventID(b.Key, 0), []Solution{b})
	level, impacts := p.Assess(v.Primary.Magnitude, v.Primary.DepthKm, v.Primary.Tsunami,
		[]RegionDistance{{Code: "32.03.01.2001", Name: "A", DistanceKm: 3}})
	a := Assessment{View: v, Level: level, Impacts: impacts}
	same := Assessment{View: v, Level: level, Impacts: impacts}
	if a.Digest() != same.Digest() {
		t.Fatal("digest harus deterministik")
	}
	for name, mutate := range map[string]func(*Assessment){
		"tingkat": func(x *Assessment) { x.Level = LevelBahaya },
		"wilayah": func(x *Assessment) { x.Impacts = nil },
		"jarak": func(x *Assessment) {
			x.Impacts = []Impact{{RegionDistance: RegionDistance{Code: "32.03.01.2001", Name: "A", DistanceKm: 4}, Level: LevelSiaga}}
		},
		"shakemap": func(x *Assessment) { x.Primary.ShakemapURL = "https://x.test/a.jpg" },
		"korob.":   func(x *Assessment) { x.Corroborating = []Solution{b} },
	} {
		c := Assessment{View: v, Level: level, Impacts: impacts}
		mutate(&c)
		if c.Digest() == a.Digest() {
			t.Errorf("%s: perubahan tidak tercermin di digest", name)
		}
	}
}

func TestFormatMagnitudeAndPlace(t *testing.T) {
	for m, want := range map[float64]string{5: "5,0", 4.75: "4,8", -0.5: "-0,5"} {
		if got := FormatMagnitude(m); got != want {
			t.Errorf("FormatMagnitude(%v) = %q", m, got)
		}
	}
	if placeText("  Pusat gempa berada di laut 48 km utara Ruteng ") != "laut 48 km utara Ruteng" ||
		placeText("Pusat gempa berada 5 km TL Garut") != "5 km TL Garut" || placeText("") != "" {
		t.Error("placeText")
	}
}
