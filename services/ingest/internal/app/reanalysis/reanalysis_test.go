package reanalysis_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo/openmeteotest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

func day(s string) time.Time {
	t, err := reanalysis.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return t
}

// daily adalah sumber debit harian 280 hari: bobot 2, jadi 100 sel per request.
func daily() reanalysis.Source {
	return reanalysis.Source{
		Endpoint: "https://flood.test/v1/flood", Model: "consolidated_v4", Variable: "river_discharge", Unit: "m³/s",
		Resolution: reanalysis.Daily, From: day("2000-01-01"), To: day("2000-10-06"),
	}
}

// discharge = 10 × lintang absolut + langkah, kosong di lintang 0.
func discharge(lat, _ float64, _ time.Time, step int) float64 {
	if lat == 0 {
		return math.NaN()
	}
	return math.Abs(lat)*10 + float64(step)
}

func cells(n int) []series.LatLon {
	out := make([]series.LatLon, n)
	for i := range out {
		out[i] = series.LatLon{Lat: -6 - float64(i%50)*0.05, Lon: 107.5 + float64(i/50)*0.05}
	}
	return out
}

func TestSource(t *testing.T) {
	s := daily()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Days() != 280 || s.Steps() != 280 || s.Weight() != 2 || s.Key() != "consolidated_v4-river_discharge-2000-01-01-2000-10-06" {
		t.Fatalf("%d %d %v %s", s.Days(), s.Steps(), s.Weight(), s.Key())
	}
	h := s
	h.Resolution, h.To = reanalysis.Hourly, day("2000-01-02")
	if h.Steps() != 48 || h.Weight() != 1 || !strings.Contains(h.String(), "hourly") {
		t.Fatalf("%d %v %s", h.Steps(), h.Weight(), h)
	}
	for name, mut := range map[string]func(*reanalysis.Source){
		"endpoint": func(s *reanalysis.Source) { s.Endpoint = "ftp://x" },
		"model":    func(s *reanalysis.Source) { s.Model = "Consolidated V4" },
		"variabel": func(s *reanalysis.Source) { s.Variable = "a&b" },
		"satuan":   func(s *reanalysis.Source) { s.Unit = " " },
		"langkah":  func(s *reanalysis.Source) { s.Resolution = "monthly" },
		"jam":      func(s *reanalysis.Source) { s.From = s.From.Add(time.Hour) },
		"terbalik": func(s *reanalysis.Source) { s.To = s.From.AddDate(0, 0, -1) },
	} {
		bad := daily()
		mut(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s harus ditolak", name)
		}
	}
}

func newLoader(f *openmeteotest.Fake, clk *openmeteotest.Clock, src reanalysis.Source) *reanalysis.Loader {
	return &reanalysis.Loader{Fetch: f, Clock: clk, Cache: reanalysis.NewCache(src)}
}

func TestLoadBatchesAndCache(t *testing.T) {
	f, clk := &openmeteotest.Fake{Value: discharge}, &openmeteotest.Clock{}
	l := newLoader(f, clk, daily())
	var saves, progress int
	l.Checkpoint = func(*reanalysis.Cache) error { saves++; return nil }
	l.Progress = func(done, total int) { progress = done*100 + total }
	pts := append(cells(150), cells(3)...) // titik ganda diminta sekali
	if p := l.Plan(pts); p.Cells != 150 || p.Cached != 0 || p.Calls != 300 {
		t.Fatalf("%+v", p)
	}
	got, err := l.Load(context.Background(), pts)
	if err != nil {
		t.Fatal(err)
	}
	// Titik uji 1 sel, lalu 100 (batas 200 panggilan / bobot 2), lalu 49.
	if f.Requests != 3 || f.Locations != 150 || saves != 3 || progress != 303 {
		t.Fatalf("request %d lokasi %d simpan %d progres %d", f.Requests, f.Locations, saves, progress)
	}
	// Jeda mengikuti bobot request sebelumnya: (1 + 100) × 2 panggilan / 400 per menit.
	if want := time.Duration(202 * float64(time.Minute) / 400); clk.Slept != want {
		t.Fatalf("tidur %v, ingin %v", clk.Slept, want)
	}
	q := f.Queries[1]
	if q.Get("daily") != "river_discharge" || q.Get("models") != "consolidated_v4" || q.Get("cell_selection") != "nearest" ||
		q.Get("start_date") != "2000-01-01" || q.Get("end_date") != "2000-10-06" || q.Get("timezone") != "GMT" {
		t.Fatalf("query %v", q)
	}
	vs := got[reanalysis.Key(series.LatLon{Lat: -6.05, Lon: 107.5})]
	if len(got) != 150 || len(vs) != 280 || vs[0] != 60.5 || vs[279] != 339.5 {
		t.Fatalf("%d sel, %d nilai, %v..%v", len(got), len(vs), vs[0], vs[279])
	}

	// Jalan ulang dari cache: tanpa request.
	var buf bytes.Buffer
	if err := l.Cache.Write(&buf); err != nil {
		t.Fatal(err)
	}
	c2, err := reanalysis.ReadCache(bytes.NewReader(buf.Bytes()), daily())
	if err != nil {
		t.Fatal(err)
	}
	f2 := &openmeteotest.Fake{Value: discharge}
	l2 := &reanalysis.Loader{Fetch: f2, Clock: clk, Cache: c2}
	got2, err := l2.Load(context.Background(), pts)
	if err != nil || f2.Requests != 0 || len(got2) != 150 {
		t.Fatalf("%v request %d", err, f2.Requests)
	}
	for k, v := range got {
		w := got2[k]
		for i := range v {
			if v[i] != w[i] {
				t.Fatalf("cache berbeda di %v langkah %d", k, i)
			}
		}
	}
	var again bytes.Buffer
	if err := c2.Write(&again); err != nil || !bytes.Equal(buf.Bytes(), again.Bytes()) {
		t.Fatal("tulis ulang cache harus identik")
	}
}

func TestLoadBudget(t *testing.T) {
	f, clk := &openmeteotest.Fake{Value: discharge}, &openmeteotest.Clock{}
	l := newLoader(f, clk, daily())
	l.MaxCalls = 41 // titik uji 2 + 19 sel (38), sisa 1 tidak cukup untuk 1 sel
	var be *reanalysis.BudgetError
	if _, err := l.Load(context.Background(), cells(30)); !errors.As(err, &be) || be.Remaining != 10 || be.Calls != 20 {
		t.Fatalf("%v", err)
	}
	if l.Cache.Len() != 20 || !strings.Contains(be.Error(), "10 sel") {
		t.Fatalf("cache %d: %v", l.Cache.Len(), be)
	}
	// Lanjut dari cache tanpa titik uji, batas tepat habis di awal.
	l.MaxCalls = 1
	if _, err := l.Load(context.Background(), cells(30)); !errors.As(err, &be) || be.Remaining != 10 {
		t.Fatalf("%v", err)
	}
	l.MaxCalls = 0
	if _, err := l.Load(context.Background(), cells(30)); err != nil || l.Cache.Len() != 30 {
		t.Fatalf("%v %d", err, l.Cache.Len())
	}
}

func TestLoadErrors(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		fake *openmeteotest.Fake
		pts  []series.LatLon
		want error
	}{
		"kosong": {&openmeteotest.Fake{Value: func(float64, float64, time.Time, int) float64 { return math.NaN() }}, cells(3), reanalysis.ErrEmpty},
		"tidak utuh": {&openmeteotest.Fake{Value: func(lat, _ float64, _ time.Time, step int) float64 {
			if lat < -6.01 && step%50 == 0 {
				return math.NaN()
			}
			return 1
		}}, cells(3), reanalysis.ErrCoverage},
		"sel lain": {&openmeteotest.Fake{Value: discharge, Shift: 0.05}, cells(2), reanalysis.ErrCellMismatch},
		"satuan":   {&openmeteotest.Fake{Value: discharge, Unit: "m3/s"}, cells(2), openmeteo.ErrStructure},
		"langkah":  {&openmeteotest.Fake{Value: discharge, DropStep: true}, cells(2), openmeteo.ErrStructure},
		"negatif":  {&openmeteotest.Fake{Value: func(float64, float64, time.Time, int) float64 { return -1 }}, cells(2), openmeteo.ErrStructure},
		"jaringan": {&openmeteotest.Fake{Value: discharge, Fail: context.DeadlineExceeded}, cells(2), context.DeadlineExceeded},
	}
	for name, c := range cases {
		l := newLoader(c.fake, &openmeteotest.Clock{}, daily())
		if _, err := l.Load(ctx, c.pts); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Titik uji kosong hanya memakai satu request.
	f := &openmeteotest.Fake{Value: func(float64, float64, time.Time, int) float64 { return math.NaN() }}
	if _, err := newLoader(f, &openmeteotest.Clock{}, daily()).Load(ctx, cells(50)); !errors.Is(err, reanalysis.ErrEmpty) || f.Requests != 1 {
		t.Fatalf("%v request %d", err, f.Requests)
	}
	// Cache wajib, sumber harus valid, checkpoint gagal menghentikan.
	if _, err := (&reanalysis.Loader{}).Load(ctx, cells(1)); err == nil {
		t.Fatal("tanpa cache")
	}
	bad := daily()
	bad.Model = ""
	if _, err := newLoader(&openmeteotest.Fake{Value: discharge}, &openmeteotest.Clock{}, bad).Load(ctx, cells(1)); err == nil {
		t.Fatal("sumber tidak valid")
	}
	l := newLoader(&openmeteotest.Fake{Value: discharge}, &openmeteotest.Clock{}, daily())
	boom := errors.New("disk penuh")
	l.Checkpoint = func(*reanalysis.Cache) error { return boom }
	if _, err := l.Load(ctx, cells(1)); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	// Konteks batal saat jeda.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := newLoader(&openmeteotest.Fake{Value: discharge}, &openmeteotest.Clock{}, daily()).Load(cctx, cells(5)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLoadHourlySplitsBySteps(t *testing.T) {
	src := daily()
	src.Variable, src.Unit, src.Resolution = "precipitation", "mm", reanalysis.Hourly
	src.From, src.To = day("2017-01-01"), day("2024-12-31") // 70.128 langkah, bobot ±20,9
	f := &openmeteotest.Fake{Value: func(_, _ float64, _ time.Time, step int) float64 { return float64(step % 3) }}
	l := newLoader(f, &openmeteotest.Clock{}, src)
	got, err := l.Load(context.Background(), cells(12))
	if err != nil {
		t.Fatal(err)
	}
	// Titik uji, lalu 5 sel per request (400 ribu langkah / 70.128), bukan 9 (bobot).
	if f.Requests != 4 || f.Queries[1].Get("hourly") != "precipitation" || len(got) != 12 {
		t.Fatalf("request %d, %v", f.Requests, f.Queries[1])
	}
	if n := len(strings.Split(f.Queries[1].Get("latitude"), ",")); n != 5 {
		t.Fatalf("%d lokasi per request", n)
	}
}

func TestReadCacheErrors(t *testing.T) {
	src := daily()
	src.To = day("2000-01-02")
	good := `{"versi":1,"model":"consolidated_v4","variabel":"river_discharge","satuan":"m³/s","langkah":"daily","dari":"2000-01-01","sampai":"2000-01-02","sel":[{"lat":-6.1,"lon":107.5,"nilai":[1.5,null]}]}`
	c, err := reanalysis.ReadCache(strings.NewReader(good), src)
	if err != nil {
		t.Fatal(err)
	}
	if vs, ok := c.Get(series.LatLon{Lat: -6.10001, Lon: 107.5}); !ok || vs[0] != 1.5 || !math.IsNaN(vs[1]) {
		t.Fatalf("%v %v", vs, ok)
	}
	for name, body := range map[string]string{
		"json":    `{`,
		"versi":   strings.Replace(good, `"versi":1`, `"versi":2`, 1),
		"periode": strings.Replace(good, `"sampai":"2000-01-02"`, `"sampai":"2000-01-03"`, 1),
		"jumlah":  strings.Replace(good, `[1.5,null]`, `[1.5]`, 1),
		"negatif": strings.Replace(good, `1.5`, `-1.5`, 1),
		"lokasi":  strings.Replace(good, `"lat":-6.1`, `"lat":40`, 1),
		"kolom":   strings.Replace(good, `"versi":1`, `"versi":1,"x":1`, 1),
	} {
		if _, err := reanalysis.ReadCache(strings.NewReader(body), src); err == nil {
			t.Errorf("%s harus ditolak", name)
		}
	}
	if _, err := reanalysis.ReadCache(strings.NewReader(strings.Replace(good, "consolidated_v4", "seamless_v4", 1)), src); !errors.Is(err, reanalysis.ErrCacheSource) {
		t.Fatal(err)
	}
}
