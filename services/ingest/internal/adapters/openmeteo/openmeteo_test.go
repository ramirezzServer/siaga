package openmeteo

import (
	"bytes"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Rekaman 2026-09-24 12.40 UTC (testdata/README.md).
var fetched = time.Date(2026, 9, 24, 12, 40, 0, 0, time.UTC)

func fixed() time.Time { return fetched }

func read(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func grid(points ...[2]float64) []series.Site {
	out := make([]series.Site, len(points))
	for i, p := range points {
		ll := series.LatLon{Lat: p[0], Lon: p[1]}
		out[i] = series.Site{ID: series.GridID(ll), Requested: ll}
	}
	return out
}

var grid4 = grid([2]float64{-7.75, 106.5}, [2]float64{-7.75, 106.75}, [2]float64{-7.75, 107}, [2]float64{-7.75, 107.25})

var rivers3 = []series.Site{
	{ID: "river:citarum-majalaya", River: "Citarum", Name: "Majalaya", Requested: series.LatLon{Lat: -7.045, Lon: 107.755}},
	{ID: "river:citarum-dayeuhkolot", River: "Citarum", Name: "Dayeuhkolot", Requested: series.LatLon{Lat: -6.987, Lon: 107.625}},
	{ID: "river:citarum-nanjung", River: "Citarum", Name: "Nanjung", Requested: series.LatLon{Lat: -6.941, Lon: 107.537}},
}

func mustWeather(t *testing.T, sites []series.Site) *Connector {
	t.Helper()
	c, err := NewWeatherConnector(DefaultWeatherURL, sites, fixed)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRequest(t *testing.T) {
	c := mustWeather(t, grid4[:2])
	u, err := url.Parse(c.Request().URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"latitude": "-7.75,-7.75", "longitude": "106.5,106.75", "start_date": "2026-09-24", "end_date": "2026-09-27",
		"timezone": "GMT", "timeformat": "unixtime", "cell_selection": "nearest",
		"hourly": "temperature_2m,relative_humidity_2m,precipitation,weather_code,cloud_cover,wind_speed_10m,wind_direction_10m,wind_gusts_10m,surface_pressure,boundary_layer_height",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, ingin %q", k, got, v)
		}
	}
	if strings.Contains(c.Request().URL, "%2C") || u.Host != "api.open-meteo.com" || u.Path != "/v1/forecast" {
		t.Errorf("URL %s", c.Request().URL)
	}

	air, _ := NewAirQualityConnector(DefaultAirURL, grid4, fixed)
	if q := mustQuery(t, air); q.Get("domains") != "cams_global" || q.Get("hourly") == "" || !strings.HasPrefix(q.Get("hourly"), "pm2_5,") {
		t.Errorf("udara: %v", q)
	}
	flood, _ := NewDischargeConnector(DefaultFloodURL, rivers3, fixed)
	if q := mustQuery(t, flood); q.Get("past_days") != "4" || q.Get("forecast_days") != "10" || q.Get("daily") == "" || q.Get("start_date") != "" {
		t.Errorf("sungai: %v", q)
	}
	if air.Name() != "openmeteo-udara" || flood.Name() != "openmeteo-sungai" || c.Name() != "openmeteo-cuaca" || c.ArchiveExt() != "json" || flood.Sites() != 3 {
		t.Error("nama konektor")
	}
}

func mustQuery(t *testing.T, c *Connector) url.Values {
	t.Helper()
	u, err := url.Parse(c.Request().URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestNewConnectorRejectsBadSites(t *testing.T) {
	for name, sites := range map[string][]series.Site{
		"kosong": nil,
		"ganda":  {grid4[0], grid4[0]},
		"ID":     {{ID: "grid:-7.7:106.5"}},
	} {
		if _, err := NewWeatherConnector(DefaultWeatherURL, sites, fixed); err == nil {
			t.Errorf("%s: seharusnya ditolak", name)
		}
	}
	if _, err := NewWeatherConnector("", grid4, fixed); err == nil {
		t.Error("endpoint kosong seharusnya ditolak")
	}
}

func TestParseWeatherGrid(t *testing.T) {
	c := mustWeather(t, grid4)
	events, rej, err := c.Parse(read(t, "cuaca-grid-4.json"), fetched)
	if err != nil || len(rej) != 0 || len(events) != 4 {
		t.Fatalf("%d event, %v, %v", len(events), rej, err)
	}
	ev := events[0]
	if ev.Subject() != "raw.forecast.openmeteo" || ev.Key() != "grid:-7.75:106.50" {
		t.Fatalf("%s %s", ev.Subject(), ev.Key())
	}
	msg := ev.(*rawpb.ModelEvent).Message().(*rawv1.GridWeatherForecast)
	if len(msg.GetSteps()) != 96 || msg.GetModel() != ModelWeather || msg.GetSite().GetCell().GetLatitude() != -7.768014 {
		t.Fatalf("%d langkah, model %s, sel %v", len(msg.GetSteps()), msg.GetModel(), msg.GetSite().GetCell())
	}
	st := msg.GetSteps()[0]
	if st.GetValidTime().AsTime() != time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC) || st.GetTemperatureC() != 24.2 || st.WeatherCode == nil {
		t.Fatalf("langkah pertama %v", st)
	}
	data, err := ev.Encode(ports.FetchMeta{Connector: c.Name(), FetchedAt: fetched, ArchiveKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	var full rawv1.GridWeatherForecast
	if err := proto.Unmarshal(data, &full); err != nil || full.GetMeta().GetArchiveKey() != "k" {
		t.Fatalf("encode: %v %v", full.GetMeta(), err)
	}
	// Isi tanpa meta sama untuk payload yang sama: deteksi "tidak berubah" di poll.
	again, _, _ := c.Parse(read(t, "cuaca-grid-4.json"), fetched.Add(time.Hour))
	a, _ := ev.Content()
	b, _ := again[0].Content()
	if !bytes.Equal(a, b) {
		t.Fatal("isi berbeda untuk payload yang sama")
	}
}

func TestParseSingleObjectAndAir(t *testing.T) {
	c := mustWeather(t, grid([2]float64{-7, 107.5}))
	events, rej, err := c.Parse(read(t, "cuaca-1titik.json"), fetched)
	if err != nil || len(rej) != 0 || len(events) != 1 {
		t.Fatalf("%d event, %v, %v", len(events), rej, err)
	}
	air, _ := NewAirQualityConnector(DefaultAirURL, grid4, fixed)
	events, rej, err = air.Parse(read(t, "udara-grid-4.json"), fetched)
	if err != nil || len(rej) != 0 || len(events) != 4 || events[0].Subject() != "raw.aq.openmeteo" {
		t.Fatalf("%d event, %v, %v", len(events), rej, err)
	}
	msg := events[0].(*rawpb.ModelEvent).Message().(*rawv1.AirQualityForecast)
	if msg.GetSteps()[0].GetPm2_5Ugm3() != 12.2 || msg.GetSteps()[0].AerosolOpticalDepth == nil || msg.GetModel() != ModelAir {
		t.Fatalf("%v", msg.GetSteps()[0])
	}
}

func TestParseDischarge(t *testing.T) {
	c, _ := NewDischargeConnector(DefaultFloodURL, rivers3, fixed)
	events, rej, err := c.Parse(read(t, "sungai-3.json"), fetched)
	if err != nil || len(rej) != 0 || len(events) != 3 {
		t.Fatalf("%d event, %v, %v", len(events), rej, err)
	}
	msg := events[1].(*rawpb.ModelEvent).Message().(*rawv1.RiverDischargeForecast)
	if events[1].Subject() != "raw.flood.openmeteo" || msg.GetSite().GetName() != "Dayeuhkolot" || msg.GetSite().GetRiver() != "Citarum" ||
		len(msg.GetSteps()) != 14 || msg.GetSteps()[0].GetValidDate().AsTime() != time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC) ||
		msg.GetSteps()[0].EnsembleP75M3S == nil {
		t.Fatalf("%v", msg)
	}
}

func TestParseStructureErrors(t *testing.T) {
	c := mustWeather(t, grid4)
	grid := string(read(t, "cuaca-grid-4.json"))
	for name, body := range map[string]string{
		"bukan JSON":      "<html>",
		"JSON rusak":      "[{",
		"galat sumber":    string(read(t, "galat-lintang.json")),
		"jumlah lokasi":   string(read(t, "cuaca-1titik.json")),
		"satuan berubah":  strings.Replace(grid, `"temperature_2m":"°C"`, `"temperature_2m":"°F"`, 1),
		"satuan waktu":    strings.Replace(grid, `"time":"unixtime"`, `"time":"iso8601"`, 1),
		"variabel hilang": strings.Replace(grid, `"boundary_layer_height":"m"`, `"blh":"m"`, 1),
	} {
		if _, _, err := c.Parse([]byte(body), fetched); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseRejectsBadLocationOnly(t *testing.T) {
	c := mustWeather(t, grid4)
	body := string(read(t, "cuaca-grid-4.json"))
	// Kelembapan 70 di lokasi pertama menjadi 170: hanya lokasi itu yang ditolak.
	i := strings.Index(body, `"relative_humidity_2m":[`) + len(`"relative_humidity_2m":[`)
	j := i + strings.Index(body[i:], ",")
	bad := body[:i] + "170" + body[j:]
	events, rej, err := c.Parse([]byte(bad), fetched)
	if err != nil || len(events) != 3 || len(rej) != 1 || rej[0].Key != "grid:-7.75:106.50" || !errors.Is(rej[0].Reason, series.ErrInvalid) {
		t.Fatalf("%d event, %v, %v", len(events), rej, err)
	}
	// Payload yang diambil jauh setelah jendelanya: semua lokasi ditolak.
	_, rej, err = c.Parse([]byte(body), fetched.AddDate(0, 0, 10))
	if err != nil || len(rej) != 4 {
		t.Fatalf("%v, %v", rej, err)
	}
	// Sel terlalu jauh dari titik yang diminta.
	far := mustWeather(t, grid([2]float64{-6, 108}, [2]float64{-7.75, 106.75}, [2]float64{-7.75, 107}, [2]float64{-7.75, 107.25}))
	if _, rej, _ := far.Parse([]byte(body), fetched); len(rej) != 1 {
		t.Fatalf("sel jauh: %v", rej)
	}
}

func TestParseNullsAndOffset(t *testing.T) {
	c := mustWeather(t, grid([2]float64{-7, 107.5}))
	body := string(read(t, "cuaca-1titik.json"))
	// Semua suhu kosong tetap diterima; langkah yang semuanya kosong dibuang.
	nulls := strings.Repeat("null,", 95) + "null"
	i := strings.Index(body, `"temperature_2m":[`) + len(`"temperature_2m":[`)
	j := i + strings.Index(body[i:], "]")
	events, rej, err := c.Parse([]byte(body[:i]+nulls+body[j:]), fetched)
	if err != nil || len(rej) != 0 || len(events) != 1 {
		t.Fatalf("%v %v", rej, err)
	}
	if _, rej, _ := c.Parse([]byte(strings.Replace(body, `"utc_offset_seconds":0`, `"utc_offset_seconds":25200`, 1)), fetched); len(rej) != 1 {
		t.Fatalf("zona waktu bukan GMT harus ditolak: %v", rej)
	}
	if _, rej, _ := c.Parse([]byte(strings.Replace(body, `"latitude":-6.994727,`, ``, 1)), fetched); len(rej) != 1 {
		t.Fatalf("koordinat sel hilang harus ditolak: %v", rej)
	}
}

func TestParseDaily(t *testing.T) {
	cells, err := ParseDaily(read(t, "sungai-3.json"), "river_discharge")
	if err != nil || len(cells) != 3 || cells[1].Cell.Lat != -6.9749985 || len(cells[1].Values) != 14 {
		t.Fatalf("%v %v", cells, err)
	}
	if _, err := ParseDaily(read(t, "sungai-3.json"), "tidak_ada"); !errors.Is(err, ErrStructure) {
		t.Fatal(err)
	}
	if _, err := ParseDaily([]byte("x"), "river_discharge"); !errors.Is(err, ErrStructure) {
		t.Fatal(err)
	}
}

func FuzzParse(f *testing.F) {
	for _, name := range []string{"cuaca-1titik.json", "galat-lintang.json"} {
		f.Add(read(f, name))
	}
	f.Add([]byte(`{"latitude":-7,"longitude":107.5,"hourly_units":{"time":"unixtime"},"hourly":{"time":[1e30]}}`))
	c, err := NewWeatherConnector(DefaultWeatherURL, grid([2]float64{-7, 107.5}), fixed)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		events, rej, err := c.Parse(body, fetched)
		if err != nil {
			if !errors.Is(err, ErrStructure) {
				t.Fatalf("galat tanpa ErrStructure: %v", err)
			}
			return
		}
		if len(events)+len(rej) != 1 {
			t.Fatalf("%d event + %d penolakan untuk 1 titik", len(events), len(rej))
		}
		for _, ev := range events {
			if _, err := ev.Content(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
