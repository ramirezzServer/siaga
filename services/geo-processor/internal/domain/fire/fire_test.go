package fire

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
)

var t0 = time.Date(2023, 8, 25, 6, 30, 0, 0, time.UTC)

func d(id, product string, lat, lon float64, h time.Duration, c hotspot.Confidence) Detection {
	frp := 3.0
	return Detection{ID: id, Product: product, Lat: lat, Lon: lon, DetectedAt: t0.Add(h), Confidence: c, FRPMW: &frp, FetchedAt: t0.Add(h + 3*time.Hour)}
}

func TestDistanceAndLinked(t *testing.T) {
	// 0,01° lintang ±1,112 km.
	if km := DistanceKm(-6.8, 107.35, -6.81, 107.35); math.Abs(km-1.112) > 0.002 {
		t.Fatal(km)
	}
	p := DefaultPolicy()
	a := d("a", "MODIS_NRT", -6.8, 107.35, 0, hotspot.Low)
	for _, c := range []struct {
		b    Detection
		want bool
	}{
		{d("b", "MODIS_NRT", -6.81, 107.35, 24*time.Hour, hotspot.Low), true},
		{d("c", "MODIS_NRT", -6.81, 107.35, -24*time.Hour, hotspot.Low), true},
		{d("d", "MODIS_NRT", -6.81, 107.35, 24*time.Hour+time.Minute, hotspot.Low), false},
		{d("e", "MODIS_NRT", -6.82, 107.35, 0, hotspot.Low), false},
	} {
		if p.Linked(a, c.b) != c.want || p.Linked(c.b, a) != c.want {
			t.Errorf("%s: %v", c.b.ID, !c.want)
		}
	}
	if !p.Excludes("VIIRS_SNPP_NRT") || p.Excludes("VIIRS_NOAA20_NRT") {
		t.Fatal("Excludes")
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func(*Policy){
		"jarak":    func(p *Policy) { p.LinkKm = 0 },
		"jeda":     func(p *Policy) { p.Gap = 0 },
		"jendela":  func(p *Policy) { p.Window = 8 * 24 * time.Hour },
		"siaga":    func(p *Policy) { p.SiagaCount = 0 },
		"lintasan": func(p *Policy) { p.WaspadaPasses = -1 },
		"linger":   func(p *Policy) { p.Linger = time.Minute },
		"yakin":    func(p *Policy) { p.WaspadaMin = "x" },
	} {
		p := DefaultPolicy()
		f(&p)
		if err := p.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s diterima", name)
		}
	}
}

func TestEvaluate(t *testing.T) {
	p := DefaultPolicy()
	if o := p.Evaluate(nil); o.Level != 0 {
		t.Fatal(o)
	}
	one := []Detection{d("a", "VIIRS_NOAA20_NRT", -6.8, 107.35, 0, hotspot.Nominal), d("b", "VIIRS_NOAA20_NRT", -6.801, 107.35, 0, hotspot.Nominal)}
	if o := p.Evaluate(one); o.Level != hazard.LevelInfo || o.PeakDetections != 2 || o.Overpasses != 1 {
		t.Fatalf("%+v", o)
	}
	// Lintasan kedua (produk lain): Waspada.
	two := append(one, d("c", "VIIRS_NOAA21_NRT", -6.8, 107.35, 50*time.Minute, hotspot.Low))
	if o := p.Evaluate(two); o.Level != hazard.LevelWaspada || !o.ReachedAt.Equal(t0.Add(50*time.Minute)) || o.PeakOverpasses != 2 {
		t.Fatalf("%+v", o)
	}
	// Lintasan kedua di luar jendela: Info. Jendela (t − 24 jam, t].
	late := append(one, d("c", "VIIRS_NOAA21_NRT", -6.8, 107.35, 24*time.Hour, hotspot.Low))
	if o := p.Evaluate(late); o.Level != hazard.LevelInfo || o.Overpasses != 2 {
		t.Fatalf("%+v", o)
	}
	// Keyakinan tinggi: Waspada; lima deteksi: Siaga, urutan masukan bebas.
	high := []Detection{d("h", "MODIS_NRT", -6.8, 107.35, 0, hotspot.High)}
	if o := p.Evaluate(high); o.Level != hazard.LevelWaspada || o.HighConfidence != 1 {
		t.Fatalf("%+v", o)
	}
	five := []Detection{
		d("e", "MODIS_NRT", -6.8, 107.35, 3*time.Hour, hotspot.Low), two[0], two[1], two[2],
		d("f", "MODIS_NRT", -6.8, 107.35, 3*time.Hour, hotspot.Low),
	}
	o := p.Evaluate(five)
	if o.Level != hazard.LevelSiaga || o.PeakDetections != 5 || !o.ReachedAt.Equal(t0.Add(3*time.Hour)) || !o.First.Equal(t0) || o.Detections != 5 {
		t.Fatalf("%+v", o)
	}
	noPasses := p
	noPasses.WaspadaPasses = 0
	if o := noPasses.Evaluate(two); o.Level != hazard.LevelInfo {
		t.Fatalf("tanpa syarat lintasan %+v", o)
	}
}

func TestAssess(t *testing.T) {
	p := DefaultPolicy()
	if _, err := p.Assess(EventIDFor("x"), "x", nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("kejadian kosong diterima")
	}
	single := d("a", "VIIRS_NOAA20_NRT", -6.8, 107.35, 0, hotspot.Nominal)
	single.FRPMW = nil
	a, err := p.Assess(EventIDFor("a"), "a", []Detection{single})
	if err != nil || a.Title != "Titik api terdeteksi satelit" || a.MaxFRPMW != nil || !a.ExpiresAt.Equal(single.FetchedAt.Add(24*time.Hour)) ||
		strings.Contains(a.Summary, "Waspada") || !strings.Contains(a.Summary, "VIIRS NOAA-20") || !strings.Contains(a.Summary, "NASA FIRMS") {
		t.Fatalf("%+v %v", a, err)
	}
	pass := []Detection{single, d("b", "VIIRS_NOAA21_NRT", -6.81, 107.36, time.Hour, hotspot.Low), d("c", "LAIN", -6.8, 107.35, 2*time.Hour, hotspot.Low)}
	a, _ = p.Assess(EventIDFor("a"), "a", pass)
	if a.Level != hazard.LevelWaspada || !strings.Contains(a.Summary, "api terlihat di 3 lintasan") || a.Title != "Titik api: 3 deteksi satelit" ||
		a.Lat != -6.80333 || a.Lon != 107.35333 || *a.MaxFRPMW != 3 || len(a.Products) != 3 || !strings.Contains(a.Summary, "LAIN") ||
		!a.FirstFetchedAt.Equal(single.FetchedAt) || !a.LastFetchedAt.Equal(t0.Add(5*time.Hour)) {
		t.Fatalf("%+v", a)
	}
	high := d("h", "MODIS_NRT", -6.8, 107.35, 0, hotspot.High)
	a, _ = p.Assess(EventIDFor("h"), "h", []Detection{high})
	if !strings.Contains(a.Summary, "keyakinan tinggi") {
		t.Fatal(a.Summary)
	}
	var many []Detection
	for i := range 5 {
		many = append(many, d(fmt.Sprint(i), "MODIS_NRT", -6.8, 107.35, time.Duration(i)*time.Minute, hotspot.Low))
	}
	a, _ = p.Assess(EventIDFor("0"), "0", many)
	if a.Level != hazard.LevelSiaga || !strings.Contains(a.Summary, "Siaga: 5 deteksi dalam 24 jam (ambang 5)") || !strings.Contains(a.Summary, " s.d. ") {
		t.Fatal(a.Summary)
	}
	// Digest berubah bila isi berubah.
	b, _ := p.Assess(EventIDFor("0"), "0", many[:4])
	if a.Digest() == b.Digest() {
		t.Fatal("digest sama")
	}
	c, _ := p.Assess(EventIDFor("0"), "0", many)
	if a.Digest() != c.Digest() {
		t.Fatal("digest tidak deterministik")
	}
	if Label(hazard.LevelSiaga) != "Siaga" || Label(0) != "-" || Label(9) != hazard.Level(9).String() {
		t.Fatal("Label")
	}
	if EventIDFor("a") == EventIDFor("b") || EventIDFor("a").IsZero() {
		t.Fatal("EventIDFor")
	}
}

func TestFromHotspot(t *testing.T) {
	frp := 2.0
	h := hotspot.Detection{ID: "x", Product: "MODIS_NRT", Lat: -6.8, Lon: 107.3, DetectedAt: t0.In(time.FixedZone("WIB", 7*3600)), Confidence: hotspot.High, FRPMW: &frp, FetchedAt: t0}
	got := FromHotspot(h)
	if got.ID != "x" || got.DetectedAt.Location() != time.UTC || !got.DetectedAt.Equal(t0) || got.Confidence != hotspot.High || *got.FRPMW != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestStatic(t *testing.T) {
	ok := StaticSource{ID: "-6.37815,108.39848", Lat: -6.37815, Lon: 108.39848, RadiusKm: 2.43, Reason: ReasonBoth, Detections: 3, First: t0, Last: t0}
	c := Calibration{Static: Mask{ok}, Period: Period{From: t0, To: t0.Add(time.Hour)}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if s, hit := c.Static.Match(-6.38, 108.40); !hit || s.ID != ok.ID {
		t.Fatal("tidak cocok")
	}
	if _, hit := c.Static.Match(-6.5, 108.40); hit {
		t.Fatal("cocok di luar jari-jari")
	}
	for name, f := range map[string]func(*StaticSource){
		"id":      func(s *StaticSource) { s.ID = "-6.37816,108.39848" },
		"lokasi":  func(s *StaticSource) { s.Lat, s.ID = 20, "20.00000,108.39848" },
		"radius":  func(s *StaticSource) { s.RadiusKm = 0 },
		"alasan":  func(s *StaticSource) { s.Reason = "x" },
		"deteksi": func(s *StaticSource) { s.Detections = 0 },
		"tanggal": func(s *StaticSource) { s.Last = t0.Add(-time.Hour) },
	} {
		s := ok
		f(&s)
		if err := (Calibration{Static: Mask{s}, Period: c.Period}).Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s diterima", name)
		}
	}
	if err := (Calibration{Static: Mask{ok, ok}, Period: c.Period}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Error("ganda diterima")
	}
	if err := (Calibration{Static: Mask{ok}}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Error("tanpa periode diterima")
	}
}

func randomDetections(data []byte) []Detection {
	var ds []Detection
	at := time.Duration(0)
	products := []string{"VIIRS_NOAA20_NRT", "VIIRS_NOAA21_NRT", "MODIS_NRT"}
	confs := []hotspot.Confidence{hotspot.Low, hotspot.Nominal, hotspot.High}
	for i := 0; i+1 < len(data) && len(ds) < 40; i += 2 {
		at += time.Duration(data[i]) * 9 * time.Minute
		ds = append(ds, d(fmt.Sprint(i), products[data[i+1]%3], -6.8, 107.35, at, confs[(data[i+1]/3)%3]))
	}
	return ds
}

// FuzzLevelMonotone: menambah deteksi tidak pernah menurunkan tingkat atau
// puncak, dan urutan masukan tidak mengubah hasil.
func FuzzLevelMonotone(f *testing.F) {
	f.Add([]byte{1, 1, 2, 2, 3, 3}, byte(4), uint64(1))
	f.Add([]byte{0, 0, 200, 4, 0, 7, 90, 1}, byte(255), uint64(7))
	f.Fuzz(func(t *testing.T, data []byte, extra byte, seed uint64) {
		p := DefaultPolicy()
		ds := randomDetections(data)
		before := p.Evaluate(ds)
		shuffled := append([]Detection(nil), ds...)
		rand.New(rand.NewPCG(seed, seed)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if p.Evaluate(shuffled) != before {
			t.Fatal("urutan masukan mengubah hasil")
		}
		add := d("tambah", "MODIS_NRT", -6.8, 107.35, time.Duration(extra)*37*time.Minute, hotspot.Low)
		after := p.Evaluate(append(ds, add))
		if after.Level < before.Level || after.PeakDetections < before.PeakDetections || after.Level < hazard.LevelInfo || after.Level > hazard.LevelSiaga {
			t.Fatalf("%+v → %+v", before, after)
		}
	})
}
