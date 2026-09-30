package riversnap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/riversnap"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const src = `# Titik uji
# 2 titik
id,sungai,nama,lat,lon,radius,catatan
citarum-a,Citarum,Hulu,-7.0,107.6,0.05,
citarum-b,Citarum,Hilir,-6.9,107.5,0,diverifikasi di OSM
`

// fakeFlood menjawab dengan sel = titik yang diminta dibulatkan ke 0,05°,
// debit = 10 di sel bujur 107,65 (alur utama uji), 1 di sel lain, untuk
// setiap hari start_date..end_date. Sel di lintang halfLat hanya berisi
// separuh hari.
type fakeFlood struct {
	requests, locations int
	fail, empty, short  bool
	halfLat             float64
	models              []string
}

func (f *fakeFlood) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	if f.fail {
		return ports.Response{}, errors.New("jaringan putus")
	}
	f.requests++
	u, _ := url.Parse(r.URL)
	q := u.Query()
	from, _ := time.Parse(time.DateOnly, q.Get("start_date"))
	to, _ := time.Parse(time.DateOnly, q.Get("end_date"))
	days := int(to.Sub(from)/(24*time.Hour)) + 1
	if f.short {
		days--
	}
	f.models = append(f.models, q.Get("models"))
	lats := strings.Split(q.Get("latitude"), ",")
	lons := strings.Split(q.Get("longitude"), ",")
	var out []map[string]any
	for i := range lats {
		la, _ := strconv.ParseFloat(lats[i], 64)
		lo, _ := strconv.ParseFloat(lons[i], 64)
		cellLo := math.Round(lo/0.05) * 0.05
		v := 1.0
		if cellLo > 107.64 && cellLo < 107.66 {
			v = 10
		}
		vals := make([]any, days)
		for d := range vals {
			switch {
			case f.empty, d%2 == 1 && math.Abs(la-f.halfLat) < 1e-6:
			default:
				vals[d] = v
			}
		}
		out = append(out, map[string]any{
			"latitude": la, "longitude": cellLo,
			"daily": map[string]any{"time": make([]int, days), "river_discharge": vals},
		})
		f.locations++
	}
	b, _ := json.Marshal(out)
	return ports.Response{Body: b}, nil
}

type fakeClock struct{ slept time.Duration }

func (c *fakeClock) Now() time.Time { return time.Unix(0, 0) }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.slept += d
	return ctx.Err()
}

// shortWindow berbobot 1 per lokasi.
var shortWindow = Window{
	Model: DefaultModel,
	From:  time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	To:    time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
}

func newSnapper(fl *fakeFlood, clk *fakeClock, w Window) *Snapper {
	return &Snapper{Endpoint: "https://flood.example/v1/flood", Fetch: fl, Clock: clk, Window: w, Cache: NewCache(w)}
}

func TestRunWritesSitesAndReport(t *testing.T) {
	stations, err := ReadStations(strings.NewReader(src))
	if err != nil || len(stations) != 2 || stations[1].Radius != 0 {
		t.Fatalf("%v %v", stations, err)
	}
	fl, clk := &fakeFlood{}, &fakeClock{}
	s := newSnapper(fl, clk, shortWindow)
	var progress, checkpoints int
	s.Progress = func(int, int) { progress++ }
	s.Checkpoint = func(c *Cache) error {
		checkpoints++
		if c != s.Cache {
			t.Fatal("checkpoint harus menerima cache snapper")
		}
		return nil
	}
	if p := s.Plan(stations); p.Points != 10 || p.Cached != 0 || p.Calls != 10 {
		t.Fatalf("%+v", p)
	}
	choices, err := s.Run(context.Background(), stations)
	if err != nil {
		t.Fatal(err)
	}
	// Titik uji satu lokasi dulu, lalu sisanya.
	if fl.locations != 10 || fl.requests != 2 || progress != 2 || checkpoints != 2 {
		t.Fatalf("%d lokasi dalam %d request, %d progress, %d checkpoint", fl.locations, fl.requests, progress, checkpoints)
	}
	if !slices.Equal(fl.models, []string{DefaultModel, DefaultModel}) {
		t.Fatalf("model %v", fl.models)
	}
	if !near(choices[0].Cell, -7.05, 107.65) || choices[0].Mean != 10 || choices[1].Flags[0] != riversnap.FlagPinned {
		t.Fatalf("%+v", choices)
	}

	// Jalan ulang dari cache: tanpa request, hasil sama persis, juga setelah
	// cache ditulis dan dibaca ulang dari file.
	if p := s.Plan(stations); p.Cached != 10 || p.Calls != 0 {
		t.Fatalf("%+v", p)
	}
	again, err := s.Run(context.Background(), stations)
	if err != nil || fl.requests != 2 || !reflect.DeepEqual(again, choices) {
		t.Fatalf("jalan ulang: %v, %d request", err, fl.requests)
	}
	var file bytes.Buffer
	if err := s.Cache.Write(&file); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadCache(bytes.NewReader(file.Bytes()), shortWindow)
	if err != nil || loaded.Len() != 10 {
		t.Fatalf("%v %d", err, loaded.Len())
	}
	s.Cache = loaded
	if again, err = s.Run(context.Background(), stations); err != nil || fl.requests != 2 || !reflect.DeepEqual(again, choices) {
		t.Fatalf("dari file: %v, %d request", err, fl.requests)
	}
	var file2 bytes.Buffer
	if err := loaded.Write(&file2); err != nil || !bytes.Equal(file.Bytes(), file2.Bytes()) {
		t.Fatalf("tulis ulang cache harus identik: %v", err)
	}

	var sites, report bytes.Buffer
	if err := WriteSites(&sites, "32", "docs/calibration/titik-sungai-32.csv", choices); err != nil {
		t.Fatal(err)
	}
	parsed, err := sitelist.ParseRivers(sites.Bytes())
	if err != nil || len(parsed) != 2 || parsed[1].Name != "Hilir" {
		t.Fatalf("%v %v\n%s", parsed, err, sites.String())
	}
	if err := WriteReport(&report, "x.csv", time.Unix(0, 0), shortWindow, choices); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| Hulu (`citarum-a`) | Citarum |", "`manual`", "`models=consolidated_v4`", "2020-01-01 sampai 2020-01-02 (2 hari)", "kurang dari 90%"} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("laporan tanpa %q:\n%s", want, report.String())
		}
	}

	// Peta pemeriksaan: kandidat unik per titik, tepat satu sel terpilih.
	choices[0].Candidates = append(choices[0].Candidates, choices[0].Candidates[0])
	var geo bytes.Buffer
	if err := WriteGeoJSON(&geo, choices); err != nil {
		t.Fatal(err)
	}
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Geometry struct {
				Type string `json:"type"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(geo.Bytes(), &fc); err != nil || fc.Type != "FeatureCollection" {
		t.Fatalf("%v\n%s", err, geo.String())
	}
	count := map[string]int{}
	for _, f := range fc.Features {
		p := f.Properties
		key := p["titik"].(string) + "/" + p["jenis"].(string)
		count[key]++
		if p["terpilih"] == true {
			count[p["titik"].(string)+"/terpilih"]++
			if p["stroke"] != colorChosen || f.Geometry.Type != "Polygon" {
				t.Fatalf("%+v", f)
			}
		}
	}
	want := map[string]int{
		"citarum-a/kandidat": 9, "citarum-a/perkiraan": 1, "citarum-a/geser": 1, "citarum-a/terpilih": 1,
		"citarum-b/kandidat": 1, "citarum-b/perkiraan": 1, "citarum-b/geser": 1, "citarum-b/terpilih": 1,
	}
	for k, v := range want {
		if count[k] != v {
			t.Errorf("%s: %d, ingin %d", k, count[k], v)
		}
	}
	if len(count) != len(want) {
		t.Errorf("%v", count)
	}
}

func TestRunBatchesAndPaces(t *testing.T) {
	var b strings.Builder
	b.WriteString("id,sungai,nama,lat,lon,radius,catatan\n")
	for i := range 6 {
		b.WriteString("s-" + strconv.Itoa(i) + ",Citarum,S" + strconv.Itoa(i) + ",-7.0,107.6,0.15,\n")
	}
	stations, err := ReadStations(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	win := DefaultWindow()
	w := win.Weight()
	fl, clk := &fakeFlood{}, &fakeClock{}
	s := newSnapper(fl, clk, win)
	// 6 titik di tempat yang sama: 49 titik permintaan unik, bukan 294.
	if p := s.Plan(stations); p.Points != 49 || math.Abs(p.Calls-49*w) > 1e-9 {
		t.Fatalf("%+v", p)
	}
	choices, err := s.Run(context.Background(), stations)
	if err != nil || len(choices) != 6 {
		t.Fatal(err)
	}
	// Bobot 5,2 per lokasi: titik uji 1, lalu 38 + 10 (≤ 200 panggilan per request).
	if fl.requests != 3 || fl.locations != 49 {
		t.Fatalf("%d request, %d lokasi", fl.requests, fl.locations)
	}
	want := time.Duration(1*w*float64(time.Minute)/CallsPerMinute) + time.Duration(38*w*float64(time.Minute)/CallsPerMinute)
	if clk.slept != want {
		t.Fatalf("tidur %v, ingin %v", clk.slept, want)
	}

	fl.fail = true
	if _, err := newSnapper(fl, clk, win).Run(context.Background(), stations); err == nil {
		t.Fatal("galat jaringan harus diteruskan")
	}
	fl.fail = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newSnapper(fl, clk, win).Run(ctx, stations); !errors.Is(err, context.Canceled) {
		t.Fatalf("pembatalan: %v", err)
	}
	s = newSnapper(fl, clk, win)
	s.Checkpoint = func(*Cache) error { return errors.New("disk penuh") }
	if _, err := s.Run(context.Background(), stations); err == nil || !strings.Contains(err.Error(), "disk penuh") {
		t.Fatalf("galat checkpoint: %v", err)
	}
}

func TestRunBudget(t *testing.T) {
	stations, err := ReadStations(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	fl := &fakeFlood{}
	s := newSnapper(fl, &fakeClock{}, shortWindow)
	s.MaxCalls = 5
	_, err = s.Run(context.Background(), stations)
	var be *BudgetError
	// Titik uji 1, lalu request kedua dipotong jadi 4 lokasi.
	if !errors.As(err, &be) || be.Remaining != 5 || be.Calls != 5 || s.Cache.Len() != 5 || fl.requests != 2 {
		t.Fatalf("%v, cache %d", err, s.Cache.Len())
	}
	if !strings.Contains(err.Error(), "5 titik permintaan") {
		t.Fatal(err)
	}
	// Batas habis tepat sebelum request berikutnya.
	s.Cache = NewCache(shortWindow)
	s.MaxCalls = 0.5
	if _, err := s.Run(context.Background(), stations); !errors.As(err, &be) || be.Remaining != 10 || s.Cache.Len() != 0 {
		t.Fatalf("%v", err)
	}
	// Dilanjutkan dari cache pada jalan berikutnya.
	s.Cache = NewCache(shortWindow)
	s.MaxCalls = 5
	_, _ = s.Run(context.Background(), stations)
	s.MaxCalls = 5
	if _, err := s.Run(context.Background(), stations); err != nil || fl.locations != 15 {
		t.Fatalf("%v, %d lokasi", err, fl.locations)
	}
}

func TestRunRejectsEmptyAndMalformed(t *testing.T) {
	stations, err := ReadStations(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	fl := &fakeFlood{empty: true}
	s := newSnapper(fl, &fakeClock{}, shortWindow)
	s.Checkpoint = func(*Cache) error { t.Fatal("cache kosong tidak boleh disimpan"); return nil }
	if _, err := s.Run(context.Background(), stations); !errors.Is(err, ErrEmptyReanalysis) || fl.requests != 1 || s.Cache.Len() != 0 {
		t.Fatalf("%v, %d request", err, fl.requests)
	}
	fl = &fakeFlood{short: true}
	if _, err := newSnapper(fl, &fakeClock{}, shortWindow).Run(context.Background(), stations); !errors.Is(err, openmeteo.ErrStructure) {
		t.Fatalf("hari kurang: %v", err)
	}

	s = newSnapper(&fakeFlood{}, &fakeClock{}, shortWindow)
	s.Window.Model = "Konsolidasi V4"
	if _, err := s.Run(context.Background(), stations); err == nil {
		t.Fatal("model tidak valid harus ditolak")
	}
	s = newSnapper(&fakeFlood{}, &fakeClock{}, shortWindow)
	s.Cache = NewCache(DefaultWindow())
	if _, err := s.Run(context.Background(), stations); !errors.Is(err, ErrCacheWindow) {
		t.Fatalf("cache periode lain: %v", err)
	}
	s.Cache = nil
	if _, err := s.Run(context.Background(), stations); !errors.Is(err, ErrCacheWindow) {
		t.Fatalf("tanpa cache: %v", err)
	}
}

func TestRunSkipsSparseCells(t *testing.T) {
	stations, err := ReadStations(strings.NewReader("id,sungai,nama,lat,lon,radius,catatan\na,Citarum,A,-7.0,107.6,0.05,\n"))
	if err != nil {
		t.Fatal(err)
	}
	// Tanpa data jarang, sel -7,05 yang dipilih (lihat test di atas). Baris
	// lintang -7,05 hanya berisi separuh hari: sel itu tidak dipakai, sel
	// alur utama berikutnya yang dipilih.
	win := Window{Model: DefaultModel, From: shortWindow.From, To: shortWindow.From.AddDate(0, 0, 19)}
	choices, err := newSnapper(&fakeFlood{halfLat: -7.05}, &fakeClock{}, win).Run(context.Background(), stations)
	if err != nil {
		t.Fatal(err)
	}
	if got := choices[0].Cell; !near(got, -7, 107.65) || len(choices[0].Candidates) != 6 {
		t.Fatalf("%+v", choices[0])
	}
}

func TestWindow(t *testing.T) {
	d := DefaultWindow()
	if err := d.Validate(); err != nil || d.Days() != 730 || d.MinValid() != 657 || math.Abs(d.Weight()-730.0/140) > 1e-12 {
		t.Fatalf("%v %d %d %v", err, d.Days(), d.MinValid(), d.Weight())
	}
	if shortWindow.Weight() != 1 || d.String() != "consolidated_v4 2020-07-01..2022-06-30" {
		t.Fatal(shortWindow.Weight(), d.String())
	}
	day := 24 * time.Hour
	for name, w := range map[string]Window{
		"model":    {Model: "", From: d.From, To: d.To},
		"jam":      {Model: d.Model, From: d.From.Add(time.Hour), To: d.To},
		"zona":     {Model: d.Model, From: d.From.In(time.FixedZone("WIB", 7*3600)), To: d.To},
		"awal":     {Model: d.Model, From: ReanalysisStart.Add(-day), To: d.To},
		"terbalik": {Model: d.Model, From: d.To, To: d.From},
	} {
		if err := w.Validate(); err == nil {
			t.Errorf("%s: seharusnya ditolak", name)
		}
	}
}

func TestReadCacheErrors(t *testing.T) {
	head := `{"versi":1,"model":"consolidated_v4","dari":"2020-01-01","sampai":"2020-01-02","titik":[`
	for name, body := range map[string]string{
		"json":    "{",
		"versi":   `{"versi":2,"model":"consolidated_v4","dari":"2020-01-01","sampai":"2020-01-02","titik":[]}`,
		"asing":   `{"versi":1,"model":"consolidated_v4","dari":"2020-01-01","sampai":"2020-01-02","titik":[],"x":1}`,
		"periode": `{"versi":1,"model":"consolidated_v4","dari":"2020-07-01","sampai":"2022-06-30","titik":[]}`,
		"luar":    head + `{"lat":-7,"lon":107,"sel_lat":50,"sel_lon":107,"debit_rata2_m3s":1,"hari_berisi":2}]}`,
		"kosong":  head + `{"lat":-7,"lon":107,"sel_lat":-7,"sel_lon":107,"debit_rata2_m3s":null,"hari_berisi":2}]}`,
		"hari":    head + `{"lat":-7,"lon":107,"sel_lat":-7,"sel_lon":107,"debit_rata2_m3s":1,"hari_berisi":3}]}`,
		"negatif": head + `{"lat":-7,"lon":107,"sel_lat":-7,"sel_lon":107,"debit_rata2_m3s":-1,"hari_berisi":2}]}`,
	} {
		if _, err := ReadCache(strings.NewReader(body), shortWindow); err == nil {
			t.Errorf("%s: seharusnya gagal", name)
		} else if name == "periode" && !errors.Is(err, ErrCacheWindow) {
			t.Errorf("periode: %v", err)
		}
	}
	c, err := ReadCache(strings.NewReader(head+`{"lat":-7,"lon":107,"sel_lat":-7.025,"sel_lon":107.025,"debit_rata2_m3s":null,"hari_berisi":0}]}`), shortWindow)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Get(series.LatLon{Lat: -7.00001, Lon: 107}); !ok || !math.IsNaN(v.Mean) || v.Cell.Lat != -7.025 {
		t.Fatalf("%+v %v", v, ok)
	}
}

func near(p series.LatLon, lat, lon float64) bool {
	return math.Abs(p.Lat-lat) < 1e-9 && math.Abs(p.Lon-lon) < 1e-9
}

func TestReadStationsErrors(t *testing.T) {
	head := "id,sungai,nama,lat,lon,radius,catatan\n"
	for name, body := range map[string]string{
		"header": "id,sungai,nama,lat,lon\n",
		"kosong": "",
		"kolom":  head + "a,B,C,-7,107\n",
		"angka":  head + "a,B,C,x,107,0.1,\n",
		"slug":   head + "A B,B,C,-7,107,0.1,\n",
		"ganda":  head + "a,B,C,-7,107,0.1,\na,B,D,-7,107,0.1,\n",
		"nama":   head + "a,B,,-7,107,0.1,\n",
		"radius": head + "a,B,C,-7,107,0.5,\n",
		"luar":   head + "a,B,C,50,107,0.1,\n",
	} {
		if _, err := ReadStations(strings.NewReader(body)); err == nil {
			t.Errorf("%s: seharusnya gagal", name)
		}
	}
}

func TestSourceFileMatchesSites(t *testing.T) {
	// Daftar titik di repo harus sama dengan file sumber kalibrasinya.
	stations, err := readRepoStations()
	if err != nil {
		t.Fatal(err)
	}
	sites, err := sitelist.Rivers("32")
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != len(sites) {
		t.Fatalf("%d titik sumber, %d titik ingest", len(stations), len(sites))
	}
	for i, st := range stations {
		if sites[i].ID != "river:"+st.ID || sites[i].River != st.River || sites[i].Name != st.Name {
			t.Errorf("titik %d: sumber %s/%s/%s, ingest %+v", i, st.ID, st.River, st.Name, sites[i])
		}
	}
}
