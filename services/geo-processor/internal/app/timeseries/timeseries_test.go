package timeseries

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

var now = time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)

type fakeStore struct {
	calls int
	res   ports.SeriesResult
	err   error
}

func (s *fakeStore) SaveWeather(context.Context, series.WeatherRun) (ports.SeriesResult, error) {
	s.calls++
	return s.res, s.err
}

func (s *fakeStore) SaveAirQuality(context.Context, series.AirQualityRun) (ports.SeriesResult, error) {
	s.calls++
	return s.res, s.err
}

func (s *fakeStore) SaveDischarge(context.Context, series.DischargeRun) (ports.SeriesResult, error) {
	s.calls++
	return s.res, s.err
}

func f(v float64) *float64 { return &v }

func run(ds series.Dataset, model string) series.Run {
	r := series.Run{
		Site:    series.Site{ID: "grid:-7.00:107.50", Kind: series.SiteGrid, Location: series.Point{Lat: -7, Lon: 107.5}},
		Dataset: ds, Source: series.SourceOpenMeteo, Model: model, Cell: series.Point{Lat: -7, Lon: 107.5},
		IssuedAt: now, FetchedAt: now,
	}
	if ds == series.Discharge {
		r.Site = series.Site{ID: "river:x", Kind: series.SiteRiver, Name: "X", River: "Y", Location: r.Site.Location}
	}
	return r
}

func TestServiceStoresValidRuns(t *testing.T) {
	st := &fakeStore{res: ports.SeriesResult{Inserted: 2, Updated: 1}}
	s := New(st, func() time.Time { return now })
	ctx := context.Background()
	w := series.WeatherRun{Run: run(series.Weather, "best_match"), Steps: []series.WeatherStep{{ValidTime: now, TemperatureC: f(20)}}}
	a := series.AirQualityRun{Run: run(series.AirQuality, "cams_global"), Steps: []series.AirQualityStep{{ValidTime: now, PM25: f(10)}}}
	d := series.DischargeRun{Run: run(series.Discharge, "glofas_v4"), Steps: []series.DischargeStep{{ValidDate: now.Truncate(24 * time.Hour), Discharge: f(3)}}}
	for name, call := range map[string]func() (Result, error){
		"cuaca": func() (Result, error) { return s.Weather(ctx, w) },
		"udara": func() (Result, error) { return s.AirQuality(ctx, a) },
		"debit": func() (Result, error) { return s.Discharge(ctx, d) },
	} {
		res, err := call()
		if err != nil || res != (Result{Changed: true, Rows: 3}) {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	if st.calls != 3 {
		t.Fatalf("%d panggilan", st.calls)
	}
	st.res = ports.SeriesResult{}
	if res, err := s.Weather(ctx, w); err != nil || res.Changed {
		t.Fatalf("ulangan: %+v %v", res, err)
	}
}

func TestServiceRejectsInvalidAndPassesStoreErrors(t *testing.T) {
	st := &fakeStore{err: errors.New("koneksi putus")}
	s := New(st, func() time.Time { return now })
	ctx := context.Background()
	bad := series.WeatherRun{Run: run(series.Weather, "best_match")}
	if _, err := s.Weather(ctx, bad); !errors.Is(err, series.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.AirQuality(ctx, series.AirQualityRun{Run: run(series.AirQuality, "x")}); !errors.Is(err, series.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Discharge(ctx, series.DischargeRun{Run: run(series.Discharge, "x")}); !errors.Is(err, series.ErrInvalid) {
		t.Fatal(err)
	}
	if st.calls != 0 {
		t.Fatal("deret tidak valid tidak boleh disimpan")
	}
	ok := series.WeatherRun{Run: run(series.Weather, "best_match"), Steps: []series.WeatherStep{{ValidTime: now, TemperatureC: f(20)}}}
	if _, err := s.Weather(ctx, ok); err == nil || errors.Is(err, series.ErrInvalid) {
		t.Fatalf("galat penyimpanan harus diteruskan apa adanya: %v", err)
	}
}
