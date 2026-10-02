package flood

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

func f(v float64) *float64 { return &v }

var (
	fetched = time.Date(2026, 10, 2, 1, 10, 0, 0, time.UTC)
	today   = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
)

// nanjung adalah baris asli docs/calibration/ambang-banjir-32.csv.
var nanjung = DischargeRow{
	SiteID: "river:citarum-nanjung", River: "Citarum", Name: "Nanjung", Cell: series.Point{Lat: -6.975, Lon: 107.525},
	P50: 95.83, Climatology: Set{215.77, 271.90, 378.73, 479.70}, SeamlessRatio: 0.87,
}

func mustDischarge(t *testing.T, p Policy, r DischargeRow) DischargeThreshold {
	t.Helper()
	th, err := p.Discharge(r)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestSetLevelAndValidate(t *testing.T) {
	s := Set{10, 20, 30, 40}
	for v, want := range map[float64]hazard.Level{9.99: None, 10: hazard.LevelInfo, 25: hazard.LevelWaspada, 30: hazard.LevelSiaga, 1e6: hazard.LevelBahaya} {
		if got := s.Level(v); got != want {
			t.Errorf("Level(%v) = %d, harus %d", v, got, want)
		}
	}
	if s.Level(math.NaN()) != None {
		t.Error("NaN harus None")
	}
	for _, bad := range []Set{{0, 1, 2, 3}, {1, 1, 2, 3}, {1, 2, 3, math.Inf(1)}, {1, 2, 3, math.NaN()}, {1, 2, 3, 5000}} {
		if err := bad.Validate(1000); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v harus ditolak", bad)
		}
	}
	if s.Scale(0.5) != (Set{5, 10, 15, 20}) || s != (Set{10, 20, 30, 40}) {
		t.Error("Scale tidak boleh mengubah aslinya")
	}
}

func TestPolicyDischargeCorrectionAndCap(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	th := mustDischarge(t, p, nanjung)
	if th.Correction != 0.87 || th.MaxLevel != hazard.LevelBahaya {
		t.Fatalf("%+v", th)
	}
	if e := th.Effective(); math.Abs(e[2]-329.4951) > 1e-9 {
		t.Fatalf("p98 efektif %v", e[2])
	}
	// Rasio 0,9 ke atas tidak dikoreksi (Majalaya 0,92; Dayeuhkolot 0,93).
	r := nanjung
	r.SeamlessRatio = 0.9
	if th := mustDischarge(t, p, r); th.Correction != 1 || th.Effective() != r.Climatology {
		t.Fatalf("%+v", th)
	}
	r.SeamlessRatio = 1.06
	if th := mustDischarge(t, p, r); th.Correction != 1 {
		t.Fatalf("rasio di atas 1 tidak menaikkan ambang: %+v", th)
	}
	r.SiteID = "river:citarum-karawang"
	if th := mustDischarge(t, p, r); th.MaxLevel != hazard.LevelSiaga {
		t.Fatalf("titik hilir waduk: %+v", th)
	}
	for name, edit := range map[string]func(*DischargeRow){
		"id":     func(r *DischargeRow) { r.SiteID = "catchment:citarum" },
		"sungai": func(r *DischargeRow) { r.River = "" },
		"nama":   func(r *DischargeRow) { r.Name = " Nanjung" },
		"sel":    func(r *DischargeRow) { r.Cell = series.Point{Lat: 50, Lon: 107} },
		"ambang": func(r *DischargeRow) { r.Climatology[1] = 1 },
		"median": func(r *DischargeRow) { r.P50 = 300 },
		"rasio":  func(r *DischargeRow) { r.SeamlessRatio = 0 },
	} {
		r := nanjung
		edit(&r)
		if _, err := p.Discharge(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s harus ditolak: %v", name, err)
		}
	}
}

func TestPolicyValidate(t *testing.T) {
	for name, edit := range map[string]func(*Policy){
		"horizon": func(p *Policy) { p.Horizon = 10 },
		"linger":  func(p *Policy) { p.Linger = time.Minute },
		"bias":    func(p *Policy) { p.BiasCutoff = 0 },
		"hujan":   func(p *Policy) { p.RainfallMax = hazard.LevelBahaya },
		"waduk":   func(p *Policy) { p.Regulated = map[string]hazard.Level{"river:x": 5} },
	} {
		p := DefaultPolicy()
		edit(&p)
		if err := p.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s harus ditolak", name)
		}
	}
}

// cikapundung adalah baris asli docs/calibration/ambang-hujan-sub-das.csv.
var cikapundung = []RainfallRow{
	{SiteID: "catchment:cikapundung", Name: "Cikapundung", Hours: 3, P50: 1.74, Levels: Set{6.06, 9.55, 17.53, 29.41}},
	{SiteID: "catchment:cikapundung", Name: "Cikapundung", Hours: 6, P50: 2.64, Levels: Set{8.40, 13.08, 25.66, 40.56}},
	{SiteID: "catchment:cikapundung", Name: "Cikapundung", Hours: 24, P50: 5.78, Levels: Set{15.39, 23.21, 48.69, 76.70}},
}

func mustRainfall(t *testing.T) RainfallThreshold {
	t.Helper()
	m, err := DefaultPolicy().Rainfall(cikapundung)
	if err != nil {
		t.Fatal(err)
	}
	return m["catchment:cikapundung"]
}

func TestPolicyRainfall(t *testing.T) {
	th := mustRainfall(t)
	if th.MaxLevel != hazard.LevelSiaga || th.Windows[2][2] != 48.69 || th.P50[0] != 1.74 || th.Name != "Cikapundung" {
		t.Fatalf("%+v", th)
	}
	edit := func(i int, fn func(*RainfallRow)) []RainfallRow {
		rows := append([]RainfallRow(nil), cikapundung...)
		fn(&rows[i])
		return rows
	}
	for name, rows := range map[string][]RainfallRow{
		"jendela": edit(0, func(r *RainfallRow) { r.Hours = 12 }),
		"ganda":   edit(0, func(r *RainfallRow) { r.Hours = 6 }),
		"id":      edit(1, func(r *RainfallRow) { r.SiteID = "river:cikapundung" }),
		"nama":    edit(2, func(r *RainfallRow) { r.Name = "Lain" }),
		"kosong":  edit(2, func(r *RainfallRow) { r.Name = "" }),
		"ambang":  edit(2, func(r *RainfallRow) { r.Levels[3] = 1 }),
		"median":  edit(2, func(r *RainfallRow) { r.P50 = -1 }),
		"kurang":  cikapundung[:2],
	} {
		if _, err := DefaultPolicy().Rainfall(rows); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s harus ditolak: %v", name, err)
		}
	}
}

func dischargeRun(steps ...series.DischargeStep) series.DischargeRun {
	return series.DischargeRun{Run: series.Run{
		Site:    series.Site{ID: "river:citarum-nanjung", Kind: series.SiteRiver, Name: "Nanjung", River: "Citarum", Location: series.Point{Lat: -6.941, Lon: 107.537}},
		Dataset: series.Discharge, Source: series.SourceOpenMeteo, Model: "glofas_v4", Cell: series.Point{Lat: -6.975, Lon: 107.525},
		IssuedAt: fetched, FetchedAt: fetched,
	}, Steps: steps}
}

func TestAssessDischarge(t *testing.T) {
	p := DefaultPolicy()
	th := mustDischarge(t, p, nanjung) // efektif ≈ 187,7 / 236,6 / 329,5 / 417,3
	d := func(k int) time.Time { return today.AddDate(0, 0, k) }
	run := dischargeRun(
		series.DischargeStep{ValidDate: d(-1), Discharge: f(500)}, // kemarin tidak dinilai
		series.DischargeStep{ValidDate: d(0), Discharge: f(150)},  // tanpa ensemble
		series.DischargeStep{ValidDate: d(1), Discharge: f(150), Median: f(240), P75: f(340), Max: f(900)},
		series.DischargeStep{ValidDate: d(2), Discharge: f(150), Median: f(340), P75: f(500), Max: f(600)},
		series.DischargeStep{ValidDate: d(4), Discharge: f(150), Median: f(1000), P75: f(1000)}, // di luar horizon
	)
	o, err := p.AssessDischarge(run, th)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Days) != 4 || !o.Days[0].Date.Equal(today) || !o.Days[3].Date.Equal(d(3)) {
		t.Fatalf("hari %+v", o.Days)
	}
	want := []struct {
		level    hazard.Level
		possible bool
	}{{None, false}, {hazard.LevelSiaga, true}, {hazard.LevelBahaya, true}, {None, false}}
	for i, w := range want {
		if o.Days[i].Level != w.level || o.Days[i].Possible != w.possible {
			t.Errorf("hari %d: %d %v, harus %d %v", i, o.Days[i].Level, o.Days[i].Possible, w.level, w.possible)
		}
	}
	if o.Days[0].FromEnsemble || !o.Days[1].FromEnsemble || *o.Days[1].Max != 900 || o.Days[3].Discharge != nil {
		t.Fatalf("%+v", o.Days)
	}
	if o.Level != hazard.LevelBahaya || !o.Possible || !o.Peak.Equal(d(2)) || o.Indicator != Discharge || o.River != "Citarum" {
		t.Fatalf("%+v", o)
	}
	// Maksimum ensemble sendiri tidak pernah menaikkan tingkat (T3).
	o, _ = p.AssessDischarge(dischargeRun(series.DischargeStep{ValidDate: d(0), Median: f(100), P75: f(150), Max: f(5000)}), th)
	if o.Level != None {
		t.Fatalf("maksimum ensemble menaikkan tingkat: %d", o.Level)
	}
	// Debit tunggal di atas Siaga tanpa ensemble: Siaga, bukan kemungkinan.
	o, _ = p.AssessDischarge(dischargeRun(series.DischargeStep{ValidDate: d(0), Discharge: f(340), P75: f(1000)}), th)
	if o.Level != hazard.LevelSiaga || o.Possible {
		t.Fatalf("%+v", o)
	}
	// Hari dengan tingkat sama tanpa P75 membuat kejadian bukan "kemungkinan".
	o, _ = p.AssessDischarge(dischargeRun(
		series.DischargeStep{ValidDate: d(0), Median: f(240), P75: f(340)},
		series.DischargeStep{ValidDate: d(1), Median: f(340), P75: f(340)},
	), th)
	if o.Level != hazard.LevelSiaga || o.Possible || !o.Peak.Equal(d(0)) {
		t.Fatalf("%+v", o)
	}
	if _, err := p.AssessDischarge(dischargeRun(), mustDischarge(t, p, DischargeRow{
		SiteID: "river:x", River: "X", Name: "X", Cell: nanjung.Cell, P50: 1, Climatology: Set{2, 3, 4, 5}, SeamlessRatio: 1,
	})); !errors.Is(err, ErrInvalid) {
		t.Fatal("ambang titik lain harus ditolak")
	}
}

func TestAssessDischargeRegulatedCap(t *testing.T) {
	p := DefaultPolicy()
	r := nanjung
	r.SiteID, r.SeamlessRatio = "river:citarum-karawang", 1
	th := mustDischarge(t, p, r)
	run := dischargeRun(series.DischargeStep{ValidDate: today, Median: f(1000), P75: f(1000)})
	run.Site.ID = r.SiteID
	o, err := p.AssessDischarge(run, th)
	if err != nil || o.Level != hazard.LevelSiaga || o.Possible || o.MaxLevel != hazard.LevelSiaga {
		t.Fatalf("%+v %v", o, err)
	}
	// Median sudah di batas: P75 tidak bisa menaikkan lagi.
	run.Steps[0].Median = f(400)
	if o, _ := p.AssessDischarge(run, th); o.Level != hazard.LevelSiaga || o.Possible {
		t.Fatalf("%+v", o)
	}
}

func rainRun(start time.Time, vals ...float64) series.WeatherRun {
	r := series.WeatherRun{Run: series.Run{
		Site:    series.Site{ID: "catchment:cikapundung", Kind: series.SiteCatchment, Name: "Cikapundung", Location: series.Point{Lat: -6.82, Lon: 107.62}},
		Dataset: series.Weather, Source: series.SourceOpenMeteo, Model: "ecmwf_ifs", Cell: series.Point{Lat: -6.82, Lon: 107.62},
		IssuedAt: fetched, FetchedAt: fetched,
	}}
	for i, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		r.Steps = append(r.Steps, series.WeatherStep{ValidTime: start.Add(time.Duration(i) * time.Hour), PrecipitationMM: f(v)})
	}
	return r
}

func TestAssessRainfall(t *testing.T) {
	p := DefaultPolicy()
	th := mustRainfall(t)
	yesterday := today.AddDate(0, 0, -1)
	vals := make([]float64, 5*24)
	// Kemarin 22.00–23.00 dan hari ini 00.00–01.00: 4 × 10 mm. Akumulasi
	// terbesar yang berakhir hari ini: 3 jam 30 mm (Bahaya ≥ 29,41, dibatasi
	// Siaga), 6 jam 40 mm (Siaga ≥ 25,66), 24 jam 40 mm (Waspada). Tingkat sama
	// di 3 dan 6 jam: jendela lebih panjang yang dilaporkan.
	vals[22], vals[23], vals[24], vals[25] = 10, 10, 10, 10
	// +2 hari: 20 mm sejam (3 jam: 20 → Siaga).
	vals[24*3+12] = 20
	// +3 hari: jam hilang di tengah, sisanya hujan ringan.
	for i := 24 * 4; i < 24*5; i++ {
		vals[i] = 0.5
	}
	vals[24*4+5] = math.NaN()
	o, err := p.AssessRainfall(rainRun(yesterday, vals...), th)
	if err != nil {
		t.Fatal(err)
	}
	d0, d1, d2, d3 := o.Days[0], o.Days[1], o.Days[2], o.Days[3]
	if d0.Level != hazard.LevelSiaga || *d0.Rain[0] != 30 || *d0.Rain[1] != 40 || *d0.Rain[2] != 40 || d0.Window != 6 || d0.Possible {
		t.Fatalf("hari ini %+v rain %v %v %v", d0, *d0.Rain[0], *d0.Rain[1], *d0.Rain[2])
	}
	if d1.Level != None || *d1.Rain[2] != 10 {
		// 24 jam yang berakhir pukul 00.00 besok masih memuat jam 01.00 hari ini.
		t.Fatalf("besok %+v %v", d1, *d1.Rain[2])
	}
	if d2.Level != hazard.LevelSiaga || d2.Window != 3 {
		t.Fatalf("+2 %+v", d2)
	}
	// +3: jendela 24 jam yang memuat jam hilang (05.00) dilewati; yang berakhir
	// 00.00–04.00 masih memuat 20 mm hari sebelumnya: 20 + 5 × 0,5 = 22,5 (Info).
	if d3.Rain[2] == nil || *d3.Rain[2] != 22.5 || *d3.Rain[0] != 1.5 || d3.Level != hazard.LevelInfo || d3.Window != 24 {
		t.Fatalf("+3 %+v", d3)
	}
	if o.Level != hazard.LevelSiaga || !o.Peak.Equal(today) || o.MaxLevel != hazard.LevelSiaga || o.Indicator != Rainfall {
		t.Fatalf("%+v", o)
	}
	// Jendela sama tingkatnya: jendela terpanjang dipakai.
	vals = make([]float64, 2*24)
	for i := 24; i < 48; i++ {
		vals[i] = 1.0
	}
	vals[24+23] = 15 // 3 jam 17 → Info (≥6,06, <9,55)? 1+1+15=17 → Waspada; 24 jam 38 → Waspada
	o, _ = p.AssessRainfall(rainRun(yesterday, vals...), th)
	if o.Days[0].Level != hazard.LevelWaspada || o.Days[0].Window != 24 {
		t.Fatalf("%+v", o.Days[0])
	}
	if _, err := p.AssessRainfall(series.WeatherRun{Run: series.Run{Site: series.Site{ID: "catchment:lain"}}}, th); !errors.Is(err, ErrInvalid) {
		t.Fatal("ambang sub-DAS lain harus ditolak")
	}
}

// dailyMaxSum adalah salinan threshold.DailyMaxSum ingest (jendela geser
// atas deret per jam mulai pukul 00.00 UTC), definisi yang dipakai menghitung
// ambang indeks hujan.
func dailyMaxSum(hourly []float64, hours int) []float64 {
	days := (len(hourly) + 23) / 24
	out := make([]float64, days)
	for i := range out {
		out[i] = math.NaN()
	}
	if hours <= 0 {
		return out
	}
	sum, bad := 0.0, 0
	for i, v := range hourly {
		if math.IsNaN(v) {
			bad++
		} else {
			sum += v
		}
		if i >= hours {
			old := hourly[i-hours]
			if math.IsNaN(old) {
				bad--
			} else {
				sum -= old
			}
		}
		if i < hours-1 || bad > 0 {
			continue
		}
		s := math.Round(sum*1e9) / 1e9
		if d := i / 24; math.IsNaN(out[d]) || s > out[d] {
			out[d] = s
		}
	}
	return out
}

// FuzzDayMaxSumMatchesCalibration: indeks prakiraan dihitung sama dengan
// klimatologinya, untuk deret berlubang dan semua jendela.
func FuzzDayMaxSumMatchesCalibration(fz *testing.F) {
	fz.Add([]byte{0, 3, 200, 7, 9, 255, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, uint8(3))
	fz.Add([]byte{10, 0, 0, 0, 255, 0, 0, 30}, uint8(24))
	fz.Fuzz(func(t *testing.T, raw []byte, w uint8) {
		hours := int(w%30) + 1
		if len(raw) > 24*6 {
			raw = raw[:24*6]
		}
		start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		hourly := make([]float64, len(raw))
		m := map[time.Time]float64{}
		for i, b := range raw {
			if b == 255 {
				hourly[i] = math.NaN()
				continue
			}
			hourly[i] = float64(b) / 10
			m[start.Add(time.Duration(i)*time.Hour)] = hourly[i]
		}
		want := dailyMaxSum(hourly, hours)
		for d, wv := range want {
			got, ok := DayMaxSum(m, start.AddDate(0, 0, d), hours)
			if ok != !math.IsNaN(wv) || (ok && math.Abs(got-wv) > 1e-9) {
				t.Fatalf("hari %d jendela %d: %v %v, kalibrasi %v", d, hours, got, ok, wv)
			}
		}
		if _, ok := DayMaxSum(m, start, 0); ok {
			t.Fatal("jendela 0 jam")
		}
	})
}

// FuzzLevelMonotone: menaikkan debit atau hujan tidak pernah menurunkan
// tingkat hari mana pun.
func FuzzLevelMonotone(fz *testing.F) {
	fz.Add(200.0, 250.0, 340.0, 10.0, 2.0)
	fz.Fuzz(func(t *testing.T, median, p75, single, rain, delta float64) {
		for _, v := range []float64{median, p75, single, rain, delta} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e5 {
				return
			}
		}
		p := DefaultPolicy()
		th := mustDischarge(t, p, nanjung)
		run := func(add float64) series.DischargeRun {
			lo, hi := min(median, p75)+add, max(median, p75)+add
			return dischargeRun(
				series.DischargeStep{ValidDate: today, Median: f(lo), P75: f(hi)},
				series.DischargeStep{ValidDate: today.AddDate(0, 0, 1), Discharge: f(single + add)},
			)
		}
		a, _ := p.AssessDischarge(run(0), th)
		b, _ := p.AssessDischarge(run(delta), th)
		for i := range a.Days {
			if b.Days[i].Level < a.Days[i].Level {
				t.Fatalf("debit +%v menurunkan tingkat hari %d: %d → %d", delta, i, a.Days[i].Level, b.Days[i].Level)
			}
		}
		rt := mustRainfall(t)
		ra, _ := p.AssessRainfall(rainRun(today, rain, rain, rain), rt)
		rb, _ := p.AssessRainfall(rainRun(today, rain+delta, rain+delta, rain+delta), rt)
		if rb.Level < ra.Level || rb.Level > hazard.LevelSiaga {
			t.Fatalf("hujan +%v: %d → %d", delta, ra.Level, rb.Level)
		}
	})
}

func TestAssessmentAndDigest(t *testing.T) {
	p := DefaultPolicy()
	th := mustDischarge(t, p, nanjung)
	o, _ := p.AssessDischarge(dischargeRun(
		series.DischargeStep{ValidDate: today, Median: f(240), P75: f(340), Max: f(600)},
	), th)
	id := EventIDFor(o.SiteID, fetched)
	if id != EventIDFor("river:citarum-nanjung", fetched.In(time.FixedZone("WIB", 7*3600))) || id == EventIDFor(o.SiteID, fetched.Add(time.Second)) {
		t.Fatal("ID harus deterministik per titik dan waktu buka")
	}
	a := p.Assess(id, o, fetched, fetched)
	if a.Level != hazard.LevelSiaga || !a.ExpiresAt.Equal(fetched.Add(24*time.Hour)) || a.Title != "Potensi banjir Citarum di Nanjung" {
		t.Fatalf("%+v", a)
	}
	for _, want := range []string{"240,0 m³/s pada 2 Okt 2026 (tingkat Waspada, ambang 236,6 m³/s)", "P75 ensemble 340,0 m³/s, jadi kemungkinan Siaga", "Skenario terburuk ensemble 600,0", "bukan pengukuran lapangan"} {
		if !strings.Contains(a.Summary, want) {
			t.Errorf("ringkasan tanpa %q:\n%s", want, a.Summary)
		}
	}
	d0 := a.Digest()
	if d0 != p.Assess(id, o, fetched, fetched).Digest() {
		t.Fatal("digest tidak deterministik")
	}
	later := p.Assess(id, o, fetched, fetched.Add(time.Hour))
	if later.Digest() == d0 {
		t.Fatal("perpanjangan masa aktif harus mengubah digest")
	}
	changed := o
	changed.Days = append([]Day(nil), o.Days...)
	changed.Days[0].Max = f(601)
	if p.Assess(id, changed, fetched, fetched).Digest() == d0 {
		t.Fatal("skenario terburuk harus ikut digest")
	}

	// Keluaran terbaru di bawah ambang: kejadian tetap Info sampai kedaluwarsa.
	below, _ := p.AssessDischarge(dischargeRun(series.DischargeStep{ValidDate: today, Median: f(100)}), th)
	b := p.Assess(id, below, fetched.Add(-6*time.Hour), fetched.Add(-6*time.Hour))
	if b.Level != hazard.LevelInfo || !strings.Contains(b.Summary, "di bawah ambang Info; kejadian berakhir 3 Okt 2026 02.10 WIB") {
		t.Fatalf("%d %s", b.Level, b.Summary)
	}

	// Titik hilir waduk dan median di bawah Info yang dinaikkan P75.
	r := nanjung
	r.SiteID, r.SeamlessRatio = "river:citarum-muara", 1
	reg := mustDischarge(t, p, r)
	run := dischargeRun(series.DischargeStep{ValidDate: today, Median: f(200), P75: f(220)})
	run.Site.ID = r.SiteID
	o, _ = p.AssessDischarge(run, reg)
	a = p.Assess(EventIDFor(r.SiteID, fetched), o, fetched, fetched)
	if a.Level != hazard.LevelInfo || !strings.Contains(a.Summary, "di bawah ambang Info 215,8 m³/s); P75 ensemble 220,0 m³/s, jadi kemungkinan Info") ||
		!strings.Contains(a.Summary, "diatur waduk; tingkat dibatasi Siaga") {
		t.Fatalf("%s", a.Summary)
	}
	run.Steps[0] = series.DischargeStep{ValidDate: today, Discharge: f(400)}
	o, _ = p.AssessDischarge(run, reg)
	if a := p.Assess(id, o, fetched, fetched); !strings.Contains(a.Summary, "400,0 m³/s pada 2 Okt 2026, tingkat Siaga (ambang 378,7 m³/s)") {
		t.Fatalf("%s", a.Summary)
	}

	// Indeks hujan.
	ro, _ := p.AssessRainfall(rainRun(today, 10, 10, 10), mustRainfall(t))
	ra := p.Assess(EventIDFor(ro.SiteID, fetched), ro, fetched, fetched)
	if ra.Title != "Indikasi potensi banjir sub-DAS Cikapundung" || ra.Level != hazard.LevelSiaga ||
		!strings.Contains(ra.Summary, "30,0 mm dalam 3 jam pada 2 Okt 2026, tingkat Siaga (ambang 17,5 mm)") ||
		!strings.Contains(ra.Summary, "bukan pengukuran debit") {
		t.Fatalf("%s %s", ra.Title, ra.Summary)
	}
	if ra.Digest() == d0 {
		t.Fatal("digest indeks hujan sama dengan debit")
	}
}

func TestLabelsAndFormats(t *testing.T) {
	if Label(None) != "di bawah Info" || Label(hazard.LevelBahaya) != "Bahaya" || Label(7) != "Level(7)" {
		t.Fatal("Label")
	}
	if FormatDate(time.Date(2026, 1, 9, 23, 0, 0, 0, time.UTC)) != "9 Jan 2026" || num(329.4951) != "329,5" {
		t.Fatal("format")
	}
	if threshold(Set{1, 2, 3, 4}, None) != "-" {
		t.Fatal("threshold")
	}
}

func TestCalibrationValidate(t *testing.T) {
	p := DefaultPolicy()
	rain, _ := p.Rainfall(cikapundung)
	src := Source{Period: Period{From: time.Date(1997, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)}, SHA256: [32]byte{1}}
	c := Calibration{
		Discharge: map[string]DischargeThreshold{nanjung.SiteID: mustDischarge(t, p, nanjung)}, Rainfall: rain,
		DischargeSource: src, RainfallSource: src,
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.RainfallSource.SHA256 = [32]byte{}
	if err := c.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("asal ambang kosong harus ditolak")
	}
	c.RainfallSource, c.Discharge = src, nil
	if err := c.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("tanpa titik debit harus ditolak")
	}
}
