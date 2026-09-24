package riversnap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/riversnap"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const src = `# Titik uji
# 2 titik
id,sungai,nama,lat,lon,radius,catatan
citarum-a,Citarum,Hulu,-7.0,107.6,0.05,
citarum-b,Citarum,Hilir,-6.9,107.5,0,diverifikasi di OSM
`

// fakeFlood menjawab dengan sel = titik yang diminta dibulatkan ke 0,05°,
// debit = 10 di sel bujur 107,65 (alur utama uji), 1 di sel lain.
type fakeFlood struct {
	requests  int
	locations int
	fail      bool
}

func (f *fakeFlood) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	if f.fail {
		return ports.Response{}, errors.New("jaringan putus")
	}
	f.requests++
	u, _ := url.Parse(r.URL)
	q := u.Query()
	lats := strings.Split(q.Get("latitude"), ",")
	lons := strings.Split(q.Get("longitude"), ",")
	var out []map[string]any
	for i := range lats {
		la, _ := strconv.ParseFloat(lats[i], 64)
		lo, _ := strconv.ParseFloat(lons[i], 64)
		cellLo := float64(int(lo/0.05+0.5)) * 0.05
		v := 1.0
		if cellLo > 107.64 && cellLo < 107.66 {
			v = 10
		}
		out = append(out, map[string]any{
			"latitude": la, "longitude": cellLo,
			"daily": map[string]any{"time": []int{1, 2}, "river_discharge": []any{v, nil}},
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

func TestRunWritesSitesAndReport(t *testing.T) {
	stations, err := ReadStations(strings.NewReader(src))
	if err != nil || len(stations) != 2 || stations[1].Radius != 0 {
		t.Fatalf("%v %v", stations, err)
	}
	fl, clk := &fakeFlood{}, &fakeClock{}
	var progress int
	s := &Snapper{Endpoint: "https://flood.example/v1/flood", Fetch: fl, Clock: clk, Progress: func(int, int) { progress++ }}
	choices, err := s.Run(context.Background(), stations)
	if err != nil {
		t.Fatal(err)
	}
	if fl.locations != 10 || fl.requests != 1 || progress != 1 {
		t.Fatalf("%d lokasi dalam %d request", fl.locations, fl.requests)
	}
	if choices[0].Cell.Lon < 107.64 || choices[0].Mean != 10 || choices[1].Flags[0] != riversnap.FlagPinned {
		t.Fatalf("%+v", choices)
	}
	var sites, report bytes.Buffer
	if err := WriteSites(&sites, "32", "docs/calibration/titik-sungai-32.csv", choices); err != nil {
		t.Fatal(err)
	}
	parsed, err := sitelist.ParseRivers(sites.Bytes())
	if err != nil || len(parsed) != 2 || parsed[1].Name != "Hilir" {
		t.Fatalf("%v %v\n%s", parsed, err, sites.String())
	}
	if err := WriteReport(&report, "x.csv", time.Unix(0, 0), choices); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "| Hulu (`citarum-a`) | Citarum |") || !strings.Contains(report.String(), "`manual`") {
		t.Fatal(report.String())
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
	fl, clk := &fakeFlood{}, &fakeClock{}
	if _, err := (&Snapper{Endpoint: "https://x", Fetch: fl, Clock: clk}).Run(context.Background(), stations); err != nil {
		t.Fatal(err)
	}
	// 6 titik × 49 lokasi, paling banyak 100 lokasi per request.
	if fl.requests != 3 || fl.locations != 294 {
		t.Fatalf("%d request, %d lokasi", fl.requests, fl.locations)
	}
	if want := time.Duration(196) * time.Minute / LocationsPerMinute; clk.slept != want {
		t.Fatalf("tidur %v, ingin %v", clk.slept, want)
	}
	fl.fail = true
	if _, err := (&Snapper{Endpoint: "https://x", Fetch: fl, Clock: clk}).Run(context.Background(), stations); err == nil {
		t.Fatal("galat jaringan harus diteruskan")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fl.fail = false
	if _, err := (&Snapper{Endpoint: "https://x", Fetch: fl, Clock: clk}).Run(ctx, stations); !errors.Is(err, context.Canceled) {
		t.Fatalf("pembatalan: %v", err)
	}
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
