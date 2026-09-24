package quake

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

var fetched = time.Date(2026, 9, 23, 12, 17, 30, 0, time.UTC)

func valid() Report {
	return Report{
		Source:     SourceBMKG,
		Feed:       FeedBMKGLatest,
		EventID:    "20260923121656",
		OccurredAt: time.Date(2026, 9, 23, 12, 16, 56, 0, time.UTC),
		Latitude:   -8.51, Longitude: 123.27,
		Magnitude: 1.8, DepthKm: 12,
		Place:       "Pusat gempa berada di darat 24 km barat daya Lembata",
		Felt:        "II-III Kab. Lembata",
		ShakemapURL: "https://data.bmkg.go.id/DataMKG/TEWS/20260923191656.mmi.jpg",
	}
}

func TestValidateAcceptsRealReport(t *testing.T) {
	if err := valid().Validate(fetched); err != nil {
		t.Fatal(err)
	}
	usgs := valid()
	usgs.Source, usgs.Feed, usgs.DepthKm = SourceUSGS, FeedUSGSSummary, -1.5
	usgs.SourceUpdatedAt = fetched
	usgs.AlternateIDs = []string{"at00abc"}
	if err := usgs.Validate(fetched); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Report){
		"feed tidak dikenal":        func(r *Report) { r.Feed = "lain" },
		"feed bukan milik sumber":   func(r *Report) { r.Source = SourceUSGS },
		"ID kosong":                 func(r *Report) { r.EventID = "" },
		"ID berspasi":               func(r *Report) { r.EventID = "a b" },
		"ID non-ASCII":              func(r *Report) { r.EventID = "gempaé" },
		"ID terlalu panjang":        func(r *Report) { r.EventID = strings.Repeat("x", 129) },
		"ID alternatif rusak":       func(r *Report) { r.AlternateIDs = []string{""} },
		"waktu kosong":              func(r *Report) { r.OccurredAt = time.Time{} },
		"waktu bukan UTC":           func(r *Report) { r.OccurredAt = r.OccurredAt.In(time.FixedZone("WIB", 7*3600)) },
		"waktu terlalu lampau":      func(r *Report) { r.OccurredAt = time.Date(1899, 1, 1, 0, 0, 0, 0, time.UTC) },
		"waktu di masa depan":       func(r *Report) { r.OccurredAt = fetched.Add(MaxClockSkew + time.Second) },
		"waktu revisi bukan UTC":    func(r *Report) { r.SourceUpdatedAt = fetched.Local().In(time.FixedZone("X", 3600)) },
		"lintang di luar batas":     func(r *Report) { r.Latitude = 91 },
		"bujur NaN":                 func(r *Report) { r.Longitude = math.NaN() },
		"magnitudo terlalu besar":   func(r *Report) { r.Magnitude = 10.1 },
		"magnitudo tak hingga":      func(r *Report) { r.Magnitude = math.Inf(1) },
		"kedalaman terlalu dalam":   func(r *Report) { r.DepthKm = 801 },
		"kedalaman terlalu dangkal": func(r *Report) { r.DepthKm = -11 },
		"tsunami tidak dikenal":     func(r *Report) { r.Tsunami = 9 },
		"teks bukan UTF-8":          func(r *Report) { r.Place = "\xff" },
		"teks terlalu panjang":      func(r *Report) { r.Felt = strings.Repeat("a", 1025) },
		"URL bukan https":           func(r *Report) { r.ShakemapURL = "http://data.bmkg.go.id/x.jpg" },
		"URL relatif":               func(r *Report) { r.SourceURL = "/earthquakes/x" },
		"URL rusak":                 func(r *Report) { r.SourceURL = "https://%zz" },
		"URL terlalu panjang":       func(r *Report) { r.SourceURL = "https://a.id/" + strings.Repeat("a", 1024) },
	}
	for name, mutate := range cases {
		r := valid()
		mutate(&r)
		if err := r.Validate(fetched); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, ingin ErrInvalid", name, err)
		}
	}
}

func TestValidateReportsAllViolations(t *testing.T) {
	r := valid()
	r.Latitude, r.Magnitude = 100, 11
	err := r.Validate(fetched)
	if err == nil || !strings.Contains(err.Error(), "lintang") || !strings.Contains(err.Error(), "magnitudo") {
		t.Fatalf("semua pelanggaran harus dilaporkan: %v", err)
	}
}

func TestClockSkewBoundary(t *testing.T) {
	r := valid()
	r.OccurredAt = fetched.Add(MaxClockSkew)
	if err := r.Validate(fetched); err != nil {
		t.Fatalf("tepat di batas toleransi harus diterima: %v", err)
	}
}

func TestBMKGEventID(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	got := BMKGEventID(time.Date(2026, 9, 23, 19, 16, 56, 0, wib))
	if got != "20260923121656" {
		t.Fatalf("ID = %s, ingin waktu UTC 20260923121656", got)
	}
}

func TestParseTsunamiText(t *testing.T) {
	cases := map[string]Tsunami{
		"Tidak berpotensi tsunami":                             TsunamiNone,
		"tidak  berpotensi   TSUNAMI":                          TsunamiNone,
		"Gempa ini tidak berpotensi menimbulkan tsunami":       TsunamiNone,
		"Berpotensi tsunami":                                   TsunamiPotential,
		"Berpotensi tsunami untuk diteruskan pada masyarakat":  TsunamiPotential,
		"Peringatan dini tsunami telah diakhiri":               TsunamiPotential,
		"Gempa ini dirasakan untuk diteruskan pada masyarakat": TsunamiUnknown,
		"":        TsunamiUnknown,
		"Tsunami": TsunamiUnknown,
	}
	for in, want := range cases {
		if got := ParseTsunamiText(in); got != want {
			t.Errorf("ParseTsunamiText(%q) = %d, ingin %d", in, got, want)
		}
	}
}

func TestInIndonesiaBox(t *testing.T) {
	cases := []struct {
		lat, lon float64
		in       bool
	}{
		{-6.9, 107.6, true},      // Bandung
		{1.217, 126.5263, true},  // dekat Ternate
		{57.2, -155.5, false},    // Alaska
		{-12.5, 110, false},      // selatan kotak
		{math.NaN(), 110, false}, // data rusak
	}
	for _, c := range cases {
		if got := InIndonesiaBox(c.lat, c.lon); got != c.in {
			t.Errorf("InIndonesiaBox(%v, %v) = %v", c.lat, c.lon, got)
		}
	}
}

func TestFeedSource(t *testing.T) {
	for f, s := range map[Feed]Source{
		FeedBMKGLatest: SourceBMKG, FeedBMKGRecent: SourceBMKG, FeedBMKGFelt: SourceBMKG,
		FeedUSGSSummary: SourceUSGS, "x": "",
	} {
		if f.Source() != s {
			t.Errorf("%s.Source() = %q, ingin %q", f, f.Source(), s)
		}
	}
}
