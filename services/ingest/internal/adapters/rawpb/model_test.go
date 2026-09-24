package rawpb

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func pf(v float64) *float64 { return &v }

func seriesOf(spec series.Spec, site series.Site, vals ...*float64) series.Series {
	s := series.Series{
		Site: site, Cell: series.LatLon{Lat: site.Requested.Lat + 0.01, Lon: site.Requested.Lon}, Elevation: pf(661),
		Model: "m", Times: []time.Time{time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)},
		Values: make([][]*float64, len(spec.Vars)),
	}
	for v := range spec.Vars {
		var x *float64
		if v < len(vals) {
			x = vals[v]
		}
		s.Values[v] = []*float64{x}
	}
	return s
}

var gridSite = series.Site{ID: "grid:-7.00:107.50", Requested: series.LatLon{Lat: -7, Lon: 107.5}}

func TestGridWeatherEvent(t *testing.T) {
	s := seriesOf(series.WeatherSpec, gridSite, pf(21.8), pf(70), pf(0), pf(3), nil, pf(5.1), pf(135), pf(14), pf(922.5), pf(280))
	ev, err := NewGridWeatherEvent(s)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Subject() != "raw.forecast.openmeteo" || ev.Key() != gridSite.ID {
		t.Fatalf("%s %s", ev.Subject(), ev.Key())
	}
	msg := ev.(*ModelEvent).Message().(*rawv1.GridWeatherForecast)
	st := msg.GetSteps()[0]
	if msg.GetSource() != hazardv1.Source_SOURCE_OPEN_METEO || msg.GetSite().GetElevationM() != 661 || st.GetTemperatureC() != 21.8 ||
		st.GetWeatherCode() != 3 || st.CloudCoverPct != nil || st.GetBoundaryLayerHeightM() != 280 || st.GetSurfacePressureHpa() != 922.5 {
		t.Fatalf("%v", msg)
	}
	// Mengubah deret setelah event dibuat tidak mengubah event.
	*s.Values[0][0] = 99
	if ev.(*ModelEvent).Message().(*rawv1.GridWeatherForecast).GetSteps()[0].GetTemperatureC() != 21.8 {
		t.Fatal("event berbagi memori dengan deret")
	}
	content, _ := ev.Content()
	data, err := ev.Encode(ports.FetchMeta{Connector: "openmeteo-cuaca", FetchedAt: time.Unix(1790253600, 0).UTC(), PayloadSHA256: "ab"})
	if err != nil {
		t.Fatal(err)
	}
	var full rawv1.GridWeatherForecast
	if err := proto.Unmarshal(data, &full); err != nil || full.GetMeta().GetConnector() != "openmeteo-cuaca" {
		t.Fatal(err)
	}
	after, _ := ev.Content()
	if !bytes.Equal(content, after) {
		t.Fatal("Encode mengubah isi event")
	}
}

func TestAirQualityAndDischargeEvents(t *testing.T) {
	aq, err := NewAirQualityEvent(seriesOf(series.AirQualitySpec, gridSite, pf(12.2), pf(13), pf(900), pf(5), pf(6), pf(40), pf(0.3), pf(0)))
	if err != nil {
		t.Fatal(err)
	}
	a := aq.(*ModelEvent).Message().(*rawv1.AirQualityForecast).GetSteps()[0]
	if aq.Subject() != "raw.aq.openmeteo" || a.GetPm2_5Ugm3() != 12.2 || a.GetAerosolOpticalDepth() != 0.3 || a.DustUgm3 == nil {
		t.Fatalf("%v", a)
	}
	river := series.Site{ID: "river:citarum-dayeuhkolot", River: "Citarum", Name: "Dayeuhkolot", Requested: series.LatLon{Lat: -6.975, Lon: 107.625}}
	fl, err := NewDischargeEvent(seriesOf(series.DischargeSpec, river, pf(8.79), pf(9.36), pf(8.77), pf(17.79), pf(5.3), pf(7.53), pf(10.4)))
	if err != nil {
		t.Fatal(err)
	}
	d := fl.(*ModelEvent).Message().(*rawv1.RiverDischargeForecast)
	if fl.Subject() != "raw.flood.openmeteo" || d.GetSite().GetRiver() != "Citarum" || d.GetSteps()[0].GetEnsembleMaxM3S() != 17.79 {
		t.Fatalf("%v", d)
	}
}

func TestModelEventRejectsShapeMismatch(t *testing.T) {
	s := seriesOf(series.WeatherSpec, gridSite, pf(1))
	if _, err := NewAirQualityEvent(s); err == nil {
		t.Fatal("deret cuaca sebagai kualitas udara harus ditolak")
	}
	s.Values[3] = nil
	if _, err := NewGridWeatherEvent(s); err == nil {
		t.Fatal("kolom yang panjangnya salah harus ditolak")
	}
	if code(pf(2)) == nil || *code(pf(2)) != 2 || code(nil) != nil {
		t.Fatal("code")
	}
}

func TestFireEventRejectsUnknownConfidence(t *testing.T) {
	d := fire.Detection{Product: fire.VIIRSSNPP, Confidence: 9}
	if _, err := NewFireEvent(d); err == nil {
		t.Fatal("kelas keyakinan tak dikenal diterima")
	}
	pct := 150
	d = fire.Detection{Product: fire.MODISProduct, Confidence: fire.High, ConfidencePct: &pct}
	ev, err := NewFireEvent(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.(*ModelEvent).Message().(*rawv1.FireDetection).GetConfidencePct(); got != 100 {
		t.Fatalf("persentase %d", got)
	}
	for c, want := range map[fire.Confidence]rawv1.FireConfidence{
		fire.Low: rawv1.FireConfidence_FIRE_CONFIDENCE_LOW, fire.Nominal: rawv1.FireConfidence_FIRE_CONFIDENCE_NOMINAL,
		fire.High: rawv1.FireConfidence_FIRE_CONFIDENCE_HIGH,
	} {
		if fireConfidence(c) != want {
			t.Errorf("%v", c)
		}
	}
}
