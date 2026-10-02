package openmeteo

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/catchment"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

// Rekaman hujan sub-DAS 2026-10-02 sekitar 01.10 UTC (testdata/README.md).
var rainFetched = time.Date(2026, 10, 2, 1, 10, 0, 0, time.UTC)

func mustRain(t *testing.T, basins []catchment.Basin) *RainConnector {
	t.Helper()
	c, err := NewRainConnector(DefaultWeatherURL, basins, func() time.Time { return rainFetched })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func repoBasins(t *testing.T) []catchment.Basin {
	t.Helper()
	b, err := sitelist.Catchments("32")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRainRequest(t *testing.T) {
	c := mustRain(t, repoBasins(t))
	if c.Name() != "openmeteo-hujan" || c.ArchiveExt() != "json" || c.Sites() != 47 {
		t.Fatalf("%s %s %d", c.Name(), c.ArchiveExt(), c.Sites())
	}
	req := c.Request()
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"hourly": "precipitation", "models": "ecmwf_ifs", "cell_selection": "nearest", "timeformat": "unixtime",
		"timezone": "GMT", "start_date": "2026-10-01", "end_date": "2026-10-05",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, harus %q", k, got, want)
		}
	}
	if lats := strings.Split(q.Get("latitude"), ","); len(lats) != 47 || lats[0] != "-6.9244" {
		t.Fatalf("lintang %v", lats[:1])
	}
	if !strings.Contains(req.URL, "longitude=107.6813,107.6202,") || req.MaxBytes != maxPayload {
		t.Fatalf("URL %s", req.URL)
	}
}

func TestNewRainConnectorRejects(t *testing.T) {
	one := catchment.Basin{ID: "a", Name: "A", Cells: []series.LatLon{{Lat: -7, Lon: 107}}, Weights: []float64{1}}
	for name, tc := range map[string]struct {
		basins   []catchment.Basin
		endpoint string
	}{
		"kosong":   {nil, DefaultWeatherURL},
		"ganda":    {[]catchment.Basin{one, one}, DefaultWeatherURL},
		"id":       {[]catchment.Basin{{ID: "A b", Name: "A", Cells: one.Cells, Weights: one.Weights}}, DefaultWeatherURL},
		"sel":      {[]catchment.Basin{{ID: "a", Name: "A"}}, DefaultWeatherURL},
		"endpoint": {[]catchment.Basin{one}, ""},
	} {
		if _, err := NewRainConnector(tc.endpoint, tc.basins, fixed); err == nil {
			t.Errorf("%s harus ditolak", name)
		}
	}
}

// TestParseRainRecording memutar rekaman asli: tujuh event, satu per sub-DAS,
// dengan hujan rata-rata wilayah yang sama dengan perhitungan langsung.
func TestParseRainRecording(t *testing.T) {
	basins := repoBasins(t)
	c := mustRain(t, basins)
	events, rejections, err := c.Parse(read(t, "hujan-sub-das-47.json"), rainFetched)
	if err != nil || len(rejections) != 0 {
		t.Fatalf("%v %v", err, rejections)
	}
	if len(events) != 7 {
		t.Fatalf("%d event", len(events))
	}
	var raw []struct {
		Latitude, Longitude float64
		Hourly              struct{ Precipitation []float64 }
	}
	if err := json.Unmarshal(read(t, "hujan-sub-das-47.json"), &raw); err != nil {
		t.Fatal(err)
	}
	cell := map[series.LatLon][]float64{}
	for i, p := range catchment.Cells(basins) {
		cell[p] = raw[i].Hourly.Precipitation
	}
	for i, ev := range events {
		b := basins[i]
		if ev.Subject() != "raw.rain.openmeteo" || ev.Key() != b.SiteID() {
			t.Fatalf("%s %s", ev.Subject(), ev.Key())
		}
		m := ev.(*rawpb.ModelEvent).Message().(*rawv1.CatchmentRainfallForecast)
		if m.GetModel() != "ecmwf_ifs" || int(m.GetCellCount()) != len(b.Cells) || len(m.GetSteps()) != 120 ||
			m.GetSite().GetName() != b.Name || m.GetSite().GetRiver() != "" {
			t.Fatalf("%s: %v sel, %d langkah", b.ID, m.GetCellCount(), len(m.GetSteps()))
		}
		c0 := b.Centroid()
		if m.GetSite().GetCell().GetLatitude() != c0.Lat || m.GetSite().GetRequested().GetLongitude() != c0.Lon {
			t.Fatalf("%s: titik %v", b.ID, m.GetSite())
		}
		if got := m.GetSteps()[0].GetValidTime().AsTime(); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("langkah pertama %v", got)
		}
		for k, st := range m.GetSteps() {
			want := 0.0
			for j, p := range b.Cells {
				want += b.Weights[j] * cell[p][k]
			}
			total := 0.0
			for _, w := range b.Weights {
				total += w
			}
			want /= total
			if math.Abs(st.GetPrecipitationMm()-want) > 0.0005+1e-12 {
				t.Fatalf("%s jam %d: %v, harus %.4f", b.ID, k, st.GetPrecipitationMm(), want)
			}
		}
	}
}

// rainPayload membuat respons sintetis untuk sel-sel cells: 3 jam mulai
// 2026-10-02T00:00Z dengan nilai vals[i] untuk sel i (nil = kosong).
func rainPayload(cells []series.LatLon, vals [][]*float64, edit func(i int, loc map[string]any)) []byte {
	var out []map[string]any
	for i, c := range cells {
		loc := map[string]any{
			"latitude": c.Lat, "longitude": c.Lon, "utc_offset_seconds": 0, "elevation": 700,
			"hourly_units": map[string]string{"time": "unixtime", "precipitation": "mm"},
			"hourly":       map[string]any{"time": []int64{1791158400, 1791162000, 1791165600}, "precipitation": vals[i]},
		}
		if edit != nil {
			edit(i, loc)
		}
		out = append(out, loc)
	}
	b, _ := json.Marshal(out)
	return b
}

func f(v float64) *float64 { return &v }

var twoBasins = []catchment.Basin{
	{ID: "hulu", Name: "Hulu", Cells: []series.LatLon{{Lat: -7, Lon: 107}, {Lat: -7.1, Lon: 107.2}}, Weights: []float64{0.5, 0.5}},
	{ID: "hilir", Name: "Hilir", Cells: []series.LatLon{{Lat: -7.1, Lon: 107.2}}, Weights: []float64{1}},
}

func TestParseRainNullsAndRejections(t *testing.T) {
	c := mustRain(t, twoBasins)
	cells := catchment.Cells(twoBasins)
	// Sel pertama kosong di jam kedua: hanya sub-DAS hulu yang kehilangan jam itu.
	body := rainPayload(cells, [][]*float64{{f(1), nil, f(3)}, {f(2), f(4), f(0.1)}}, nil)
	events, rejections, err := c.Parse(body, rainFetched)
	if err != nil || len(rejections) != 0 || len(events) != 2 {
		t.Fatalf("%v %v %d", err, rejections, len(events))
	}
	hulu := events[0].(*rawpb.ModelEvent).Message().(*rawv1.CatchmentRainfallForecast)
	hilir := events[1].(*rawpb.ModelEvent).Message().(*rawv1.CatchmentRainfallForecast)
	if len(hulu.GetSteps()) != 2 || hulu.GetSteps()[0].GetPrecipitationMm() != 1.5 || hulu.GetSteps()[1].GetPrecipitationMm() != 1.55 {
		t.Fatalf("hulu %v", hulu.GetSteps())
	}
	if len(hilir.GetSteps()) != 3 || hilir.GetSteps()[1].GetPrecipitationMm() != 4 {
		t.Fatalf("hilir %v", hilir.GetSteps())
	}

	// Nilai di luar batas hanya menolak sub-DAS yang memakai sel itu.
	body = rainPayload(cells, [][]*float64{{f(900), f(900), f(900)}, {f(1), f(1), f(1)}}, nil)
	events, rejections, err = c.Parse(body, rainFetched)
	if err != nil || len(events) != 1 || len(rejections) != 1 || rejections[0].Key != "catchment:hulu" ||
		!errors.Is(rejections[0].Reason, series.ErrInvalid) {
		t.Fatalf("%v %d %v", err, len(events), rejections)
	}

	// Semua jam kosong: sub-DAS ditolak, bukan event tanpa langkah.
	body = rainPayload(cells, [][]*float64{{nil, nil, nil}, {f(1), f(1), f(1)}}, nil)
	if _, rejections, err = c.Parse(body, rainFetched); err != nil || len(rejections) != 1 {
		t.Fatalf("%v %v", err, rejections)
	}
}

func TestParseRainStructureErrors(t *testing.T) {
	c := mustRain(t, twoBasins)
	cells := catchment.Cells(twoBasins)
	vals := [][]*float64{{f(1), f(1), f(1)}, {f(1), f(1), f(1)}}
	for name, body := range map[string][]byte{
		"jumlah lokasi": rainPayload(cells[:1], vals, nil),
		"sel bergeser": rainPayload(cells, vals, func(i int, l map[string]any) {
			if i == 1 {
				l["latitude"] = -7.11
			}
		}),
		"satuan": rainPayload(cells, vals, func(_ int, l map[string]any) {
			l["hourly_units"] = map[string]string{"time": "unixtime", "precipitation": "inch"}
		}),
		"satuan waktu": rainPayload(cells, vals, func(_ int, l map[string]any) {
			l["hourly_units"] = map[string]string{"time": "iso8601", "precipitation": "mm"}
		}),
		"zona waktu": rainPayload(cells, vals, func(_ int, l map[string]any) { l["utc_offset_seconds"] = 25200 }),
		"waktu beda": rainPayload(cells, vals, func(i int, l map[string]any) {
			if i == 1 {
				l["hourly"] = map[string]any{"time": []int64{1791158400, 1791162000, 1791169200}, "precipitation": vals[1]}
			}
		}),
		"langkah beda": rainPayload(cells, vals, func(i int, l map[string]any) {
			if i == 1 {
				l["hourly"] = map[string]any{"time": []int64{1791158400, 1791162000}, "precipitation": vals[1][:2]}
			}
		}),
		"kolom pendek": rainPayload(cells, vals, func(i int, l map[string]any) {
			if i == 0 {
				l["hourly"] = map[string]any{"time": []int64{1791158400, 1791162000, 1791165600}, "precipitation": vals[0][:1]}
			}
		}),
		"waktu rusak": rainPayload(cells, vals, func(i int, l map[string]any) {
			l["hourly"] = map[string]any{"time": []any{1791158400.5, 1791162000, 1791165600}, "precipitation": vals[i]}
		}),
		"tanpa koordinat": rainPayload(cells, vals, func(i int, l map[string]any) {
			if i == 0 {
				delete(l, "latitude")
			}
		}),
		"galat sumber": []byte(`[{"error":true,"reason":"Cannot initialize"}]`),
		"bukan JSON":   []byte(`<html>`),
	} {
		if _, _, err := c.Parse(body, rainFetched); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
