package hotspot

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)

func viirs() Detection {
	at := time.Date(2026, 9, 24, 6, 12, 0, 0, time.UTC)
	bg, frp := 297.1, 5.8
	return Detection{
		ID: "VIIRS_SNPP_NRT:20260924T0612:-6.83712:107.44157", Product: "VIIRS_SNPP_NRT", Satellite: "N", Instrument: "VIIRS",
		Lat: -6.83712, Lon: 107.44157, DetectedAt: at, Confidence: Nominal, BrightnessK: 338.5, BackgroundK: &bg, FRPMW: &frp,
		ScanKm: 0.41, TrackKm: 0.61, Daytime: true, Version: "2.0NRT", FetchedAt: now, ArchiveKey: "firms/k.csv.gz",
	}
}

func modis() Detection {
	d := viirs()
	pct := 85
	d.ID = "MODIS_NRT:20260924T0612:-6.83712:107.44157"
	d.Product, d.Instrument, d.Satellite, d.ConfidencePct, d.Confidence = "MODIS_NRT", "MODIS", "Aqua", &pct, High
	return d
}

func TestValid(t *testing.T) {
	for _, d := range []Detection{viirs(), modis()} {
		if err := d.Validate(now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalid(t *testing.T) {
	p := func(v float64) *float64 { return &v }
	cases := map[string]func(*Detection){
		"produk":      func(d *Detection) { d.Product = "LANDSAT_NRT" },
		"instrumen":   func(d *Detection) { d.Instrument = "MODIS" },
		"ID format":   func(d *Detection) { d.ID = "x" },
		"ID isi":      func(d *Detection) { d.Lat = -6.83713 },
		"satelit":     func(d *Detection) { d.Satellite = "N 20" },
		"versi":       func(d *Detection) { d.Version = "" },
		"lokasi":      func(d *Detection) { d.Lon, d.ID = 150, "VIIRS_SNPP_NRT:20260924T0612:-6.83712:150.00000" },
		"bukan UTC":   func(d *Detection) { d.FetchedAt = now.In(time.FixedZone("WIB", 7*3600)) },
		"detik":       func(d *Detection) { d.DetectedAt = d.DetectedAt.Add(time.Second) },
		"terlalu tua": func(d *Detection) { d.FetchedAt = d.DetectedAt.Add(11 * 24 * time.Hour) },
		"ambil depan": func(d *Detection) { d.FetchedAt = now.Add(2 * time.Hour) },
		"kelas":       func(d *Detection) { d.Confidence = "yakin" },
		"pct VIIRS":   func(d *Detection) { pct := 40; d.ConfidencePct = &pct },
		"MODIS tanpa": func(d *Detection) { *d = modis(); d.ConfidencePct = nil },
		"pct kelas":   func(d *Detection) { *d = modis(); pct := 10; d.ConfidencePct = &pct },
		"pct batas":   func(d *Detection) { *d = modis(); pct := 120; d.ConfidencePct = &pct },
		"kecerahan":   func(d *Detection) { d.BrightnessK = 900 },
		"latar":       func(d *Detection) { d.BackgroundK = p(100) },
		"frp":         func(d *Detection) { d.FRPMW = p(-3) },
		"piksel":      func(d *Detection) { d.TrackKm = 20 },
		"arsip":       func(d *Detection) { d.ArchiveKey = "a b" },
	}
	for name, mut := range cases {
		d := viirs()
		mut(&d)
		if err := d.Validate(now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for pct, want := range map[int]Confidence{0: Low, 29: Low, 30: Nominal, 79: Nominal, 80: High} {
		if ConfidenceFromPct(pct) != want {
			t.Errorf("%d", pct)
		}
	}
}
