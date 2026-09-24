package quake

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestDistanceKmKnownPairs(t *testing.T) {
	cases := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		want, tol              float64
	}{
		{"titik sama", -6.9, 107.6, -6.9, 107.6, 0, 1e-9},
		{"Bandung–Jakarta", -6.9175, 107.6191, -6.2088, 106.8456, 116, 2},
		{"satu derajat bujur di khatulistiwa", 0, 100, 0, 101, 111.195, 0.01},
		{"antipoda", 0, 0, 0, 180, math.Pi * EarthRadiusKm, 1e-6},
	}
	for _, c := range cases {
		got := DistanceKm(c.lat1, c.lon1, c.lat2, c.lon2)
		if math.Abs(got-c.want) > c.tol {
			t.Errorf("%s: %v km, ingin %v ± %v", c.name, got, c.want, c.tol)
		}
	}
}

func TestRuleMatchBoundaries(t *testing.T) {
	r := PRDInitialRule
	a := Origin{OccurredAt: t0, Latitude: -7, Longitude: 107, Magnitude: 4.7}
	b := a
	b.Magnitude = 5.4 // 5,4 − 4,7 = 0,7000000000000002 di float64
	if ok, _ := r.Match(a, b); !ok {
		t.Error("selisih magnitudo tepat 0,7 harus cocok")
	}
	b.Magnitude = 5.5
	if ok, score := r.Match(a, b); ok || !math.IsInf(score, 1) {
		t.Error("selisih 0,8 tidak boleh cocok")
	}
	b = a
	b.OccurredAt = t0.Add(90 * time.Second)
	if ok, _ := r.Match(a, b); !ok {
		t.Error("tepat 90 dtk harus cocok")
	}
	b.OccurredAt = t0.Add(-90*time.Second - time.Millisecond)
	if ok, _ := r.Match(a, b); ok {
		t.Error("lewat 90 dtk tidak boleh cocok")
	}
	b = a
	b.Latitude = a.Latitude + 76.0/111.195 // ±76 km ke selatan
	if ok, _ := r.Match(a, b); ok {
		t.Error("76 km tidak boleh cocok")
	}
	if ok, score := r.Match(a, a); !ok || score != 0 {
		t.Errorf("origin identik: %v %v", ok, score)
	}
	zero := Rule{MaxTimeDelta: time.Second, MaxDistanceKm: 1}
	if ok, score := zero.Match(a, a); !ok || score != 0 {
		t.Error("ambang magnitudo nol tidak boleh membagi dengan nol")
	}
}

func TestRuleValidate(t *testing.T) {
	for _, r := range []Rule{PRDInitialRule, DefaultCrossSourceRule, DefaultRevisionRule} {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	if err := DefaultRules().Validate(); err != nil {
		t.Error(err)
	}
	for _, r := range []Rule{
		{},
		{MaxTimeDelta: time.Hour, MaxDistanceKm: 10, MaxMagnitudeDelta: 1},
		{MaxTimeDelta: time.Second, MaxDistanceKm: math.NaN(), MaxMagnitudeDelta: 1},
		{MaxTimeDelta: time.Second, MaxDistanceKm: 10, MaxMagnitudeDelta: -1},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v seharusnya ditolak", r)
		}
	}
}

// Properti: Match simetris dan skornya dalam [0, 3] bila cocok.
func FuzzRuleMatchSymmetric(f *testing.F) {
	f.Add(int64(0), -6.9, 107.6, 5.0, int64(3), -6.8, 107.7, 5.3)
	f.Add(int64(0), 0.0, 179.9, 4.0, int64(-20), 0.1, -179.9, 4.1)
	f.Fuzz(func(t *testing.T, s1 int64, lat1, lon1, m1 float64, s2 int64, lat2, lon2, m2 float64) {
		if !inRange(lat1, -90, 90) || !inRange(lat2, -90, 90) || !inRange(lon1, -180, 180) || !inRange(lon2, -180, 180) ||
			!inRange(m1, MinMagnitude, MaxMagnitude) || !inRange(m2, MinMagnitude, MaxMagnitude) {
			t.Skip()
		}
		a := Origin{OccurredAt: t0.Add(time.Duration(s1%3600) * time.Second), Latitude: lat1, Longitude: lon1, Magnitude: m1}
		b := Origin{OccurredAt: t0.Add(time.Duration(s2%3600) * time.Second), Latitude: lat2, Longitude: lon2, Magnitude: m2}
		for _, r := range []Rule{PRDInitialRule, DefaultCrossSourceRule, DefaultRevisionRule} {
			ok1, sc1 := r.Match(a, b)
			ok2, sc2 := r.Match(b, a)
			if ok1 != ok2 || (ok1 && math.Abs(sc1-sc2) > 1e-9) {
				t.Fatalf("tidak simetris: %v/%v %v/%v", ok1, ok2, sc1, sc2)
			}
			if ok1 && (sc1 < 0 || sc1 > 3+1e-6) {
				t.Fatalf("skor %v di luar [0,3]", sc1)
			}
			d := DistanceKm(lat1, lon1, lat2, lon2)
			if d < 0 || d > math.Pi*EarthRadiusKm+1e-6 || math.IsNaN(d) {
				t.Fatalf("jarak %v tidak masuk akal", d)
			}
		}
	})
}

func TestFeltRadiusMatchesPRDExamples(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		m, h, wantR, wantS float64
	}{
		{5, 0, 31.62, 31.62}, // PRD: M5 dangkal ≈ 32 km
		{6, 0, 100, 100},     // PRD: M6 ≈ 100 km
		{5, 10, 31.62, 30},
		{4, 20, 10, 0},        // lebih dalam dari R: tidak dirasakan
		{5, -3, 31.62, 31.62}, // kedalaman negatif dihitung nol
	} {
		r, s := p.FeltRadius(c.m, c.h)
		if math.Abs(r-c.wantR) > 0.01 || math.Abs(s-c.wantS) > 0.01 {
			t.Errorf("M%v h%v: R=%v R_permukaan=%v, ingin %v %v", c.m, c.h, r, s, c.wantR, c.wantS)
		}
	}
}

func TestLevelAtFollowsPRDTable(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		name    string
		m, h, d float64
		want    Level
	}{
		{"M3 jauh", 3, 10, 200, LevelInfo},
		{"M4 di dalam radius dirasakan", 4, 1, 5, LevelWaspada},
		{"M4 di luar radius", 4, 1, 20, LevelInfo},
		{"M5,0 di dalam radius", 5.0, 10, 25, LevelSiaga},
		{"M4,5 ≤ 50 km walau di luar radius", 4.5, 10, 50, LevelSiaga},
		{"M4,4 dekat", 4.4, 10, 3, LevelWaspada},
		{"M6 ≤ 100 km", 6.0, 10, 100, LevelBahaya},
		{"M6 lewat 100 km", 6.0, 10, 101, LevelInfo},
		{"M6,5 150 km di dalam radius", 6.5, 10, 150, LevelSiaga},
		{"M5,9 50 km", 5.9, 10, 50, LevelSiaga},
		{"gempa dalam tidak dirasakan", 5.5, 300, 10, LevelSiaga},
		{"gempa dalam M4", 4.0, 300, 10, LevelInfo},
	}
	for _, c := range cases {
		if got := p.LevelAt(c.m, c.h, c.d); got != c.want {
			t.Errorf("%s: %v, ingin %v", c.name, got, c.want)
		}
	}
}

// Properti: tingkat tidak turun saat magnitudo naik dan tidak naik saat
// lokasi menjauh; radius permukaan tidak pernah melebihi radius hiposenter.
func FuzzLevelMonotone(f *testing.F) {
	f.Add(4.0, 10.0, 20.0, 0.5, 5.0)
	f.Add(5.9, 0.0, 100.0, 0.2, 1.0)
	f.Fuzz(func(t *testing.T, m, h, d, dm, dd float64) {
		if !inRange(m, MinMagnitude, MaxMagnitude) || !inRange(h, MinDepthKm, MaxDepthKm) || !inRange(d, 0, 2000) ||
			!inRange(dm, 0, 3) || !inRange(dd, 0, 500) {
			t.Skip()
		}
		p := DefaultPolicy()
		p.MaxQueryRadiusKm = math.Inf(1) // properti berlaku untuk radius tanpa batas atas
		r, s := p.FeltRadius(m, h)
		if s < 0 || s > r+1e-9 || math.IsNaN(s) {
			t.Fatalf("R=%v R_permukaan=%v", r, s)
		}
		base := p.LevelAt(m, h, d)
		if base < LevelInfo || base > LevelBahaya {
			t.Fatalf("tingkat %v di luar rentang", base)
		}
		if bigger := p.LevelAt(math.Min(m+dm, MaxMagnitude), h, d); bigger < base {
			t.Fatalf("M%v→M%v di %v km: %v turun ke %v", m, m+dm, d, base, bigger)
		}
		if farther := p.LevelAt(m, h, d+dd); farther > base {
			t.Fatalf("%v→%v km: %v naik ke %v", d, d+dd, base, farther)
		}
		if base > LevelInfo && d > p.QueryRadiusKm(m, h)+1e-6 {
			t.Fatalf("tingkat %v di %v km melewati radius pencarian %v", base, d, p.QueryRadiusKm(m, h))
		}
	})
}

func TestAssessSortsFiltersAndEscalatesTsunami(t *testing.T) {
	p := DefaultPolicy()
	regions := []RegionDistance{
		{Code: "32.03.01.2001", Name: "Jauh", DistanceKm: 80},
		{Code: "32.03.01.2003", Name: "Dekat B", DistanceKm: 12},
		{Code: "32.03.01.2002", Name: "Dekat A", DistanceKm: 12},
		{Code: "32.03.01.2004", Name: "Pusat", DistanceKm: 0},
	}
	level, impacts := p.Assess(5.6, 11, TsunamiNone, regions)
	if level != LevelSiaga {
		t.Errorf("tingkat %v, ingin Siaga", level)
	}
	if len(impacts) != 3 || impacts[0].Code != "32.03.01.2004" || impacts[1].Code != "32.03.01.2002" || impacts[2].Code != "32.03.01.2003" {
		t.Fatalf("urutan/penyaringan salah: %+v", impacts)
	}
	if !impacts[0].WithinFelt {
		t.Error("episenter di dalam radius dirasakan")
	}

	level, _ = p.Assess(5.6, 11, TsunamiPotential, regions)
	if level != LevelBahaya {
		t.Errorf("potensi tsunami dengan wilayah terdampak harus Bahaya, dapat %v", level)
	}
	level, impacts = p.Assess(5.6, 11, TsunamiPotential, []RegionDistance{{Code: "32", DistanceKm: 400}})
	if level != LevelInfo || len(impacts) != 0 {
		t.Errorf("tsunami jauh dari wilayah pantauan tetap Info: %v %v", level, impacts)
	}
}

func TestPolicyValidateRejects(t *testing.T) {
	for name, mutate := range map[string]func(*Policy){
		"slope":      func(p *Policy) { p.FeltSlope = 0 },
		"intercept":  func(p *Policy) { p.FeltIntercept = math.Inf(1) },
		"jarak":      func(p *Policy) { p.BahayaMaxDistanceKm = 0 },
		"urutan M":   func(p *Policy) { p.SiagaFeltMinMagnitude = 7 },
		"masa aktif": func(p *Policy) { p.ActiveFor = 0 },
		"radius":     func(p *Policy) { p.MaxQueryRadiusKm = 10 },
	} {
		p := DefaultPolicy()
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: seharusnya ditolak", name)
		}
	}
	if !errors.Is(DefaultRules().Validate(), nil) {
		t.Error("aturan bawaan valid")
	}
}

func TestLevelString(t *testing.T) {
	for l, want := range map[Level]string{LevelInfo: "info", LevelWaspada: "waspada", LevelSiaga: "siaga", LevelBahaya: "bahaya", 9: "Level(9)"} {
		if l.String() != want {
			t.Errorf("%d: %q", l, l.String())
		}
	}
}
