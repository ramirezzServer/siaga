package fire

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var fetched = time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)

func viirs() Detection {
	bg, frp := 297.1, 5.8
	return Detection{
		Product: VIIRSSNPP, Satellite: "N", Instrument: VIIRS, Lat: -6.83712, Lon: 107.44156,
		DetectedAt: time.Date(2026, 9, 24, 6, 12, 0, 0, time.UTC), Confidence: Nominal,
		BrightnessK: 338.5, BackgroundK: &bg, FRPMW: &frp, ScanKm: 0.41, TrackKm: 0.61, Daytime: true, Version: "2.0NRT",
	}
}

func modis() Detection {
	d := viirs()
	pct := 85
	d.Product, d.Instrument, d.Satellite, d.Version = MODISProduct, MODIS, "Aqua", "6.1NRT"
	d.ConfidencePct, d.Confidence, d.ScanKm, d.TrackKm = &pct, High, 1.2, 1.1
	return d
}

func TestDetectionValid(t *testing.T) {
	for _, d := range []Detection{viirs(), modis()} {
		if err := d.Validate(fetched); err != nil {
			t.Fatal(err)
		}
	}
	if got := viirs().ID(); got != "VIIRS_SNPP_NRT:20260924T0612:-6.83712:107.44156" {
		t.Fatalf("ID %s", got)
	}
}

func TestDetectionInvalid(t *testing.T) {
	p := func(v float64) *float64 { return &v }
	cases := map[string]func(*Detection){
		"produk":        func(d *Detection) { d.Product = "LANDSAT_NRT" },
		"instrumen":     func(d *Detection) { d.Instrument = MODIS },
		"satelit":       func(d *Detection) { d.Satellite = "" },
		"versi":         func(d *Detection) { d.Version = "2.0 NRT" },
		"lokasi":        func(d *Detection) { d.Lat = 20 },
		"bukan UTC":     func(d *Detection) { d.DetectedAt = d.DetectedAt.In(time.FixedZone("WIB", 7*3600)) },
		"detik":         func(d *Detection) { d.DetectedAt = d.DetectedAt.Add(time.Second) },
		"masa depan":    func(d *Detection) { d.DetectedAt = fetched.Add(time.Hour) },
		"terlalu tua":   func(d *Detection) { d.DetectedAt = fetched.Add(-11 * 24 * time.Hour) },
		"kelas":         func(d *Detection) { d.Confidence = 0 },
		"pct VIIRS":     func(d *Detection) { pct := 50; d.ConfidencePct = &pct },
		"kecerahan":     func(d *Detection) { d.BrightnessK = 100 },
		"latar":         func(d *Detection) { d.BackgroundK = p(500) },
		"frp":           func(d *Detection) { d.FRPMW = p(-1) },
		"piksel":        func(d *Detection) { d.ScanKm = 0 },
		"MODIS tanpa %": func(d *Detection) { *d = modis(); d.ConfidencePct = nil },
		"MODIS % salah": func(d *Detection) { *d = modis(); pct := 20; d.ConfidencePct = &pct },
		"MODIS % > 100": func(d *Detection) { *d = modis(); pct := 101; d.ConfidencePct = &pct },
	}
	for name, mut := range cases {
		d := viirs()
		mut(&d)
		if err := d.Validate(fetched); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestConfidence(t *testing.T) {
	for pct, want := range map[int]Confidence{0: Low, 29: Low, 30: Nominal, 79: Nominal, 80: High, 100: High} {
		if got := ConfidenceFromPct(pct); got != want {
			t.Errorf("%d → %v", pct, got)
		}
	}
	for s, want := range map[string]Confidence{"l": Low, "N": Nominal, " h ": High, "high": High, "nominal": Nominal, "low": Low} {
		if got, ok := ParseVIIRSConfidence(s); !ok || got != want {
			t.Errorf("%q → %v %v", s, got, ok)
		}
	}
	if _, ok := ParseVIIRSConfidence("x"); ok {
		t.Error("kelas x diterima")
	}
	if Confidence(0).String() != "unknown" || High.String() != "high" || Low.String() != "low" || Nominal.String() != "nominal" {
		t.Error("String")
	}
	for _, p := range Products {
		if _, ok := p.Instrument(); !ok || !strings.HasSuffix(string(p), "_NRT") {
			t.Errorf("produk %s", p)
		}
	}
}
