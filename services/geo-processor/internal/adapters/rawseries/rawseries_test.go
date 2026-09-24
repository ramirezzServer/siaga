package rawseries

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

var (
	fetched = time.Date(2026, 9, 24, 12, 40, 0, 123456789, time.UTC)
	day     = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	now     = fetched.Add(time.Minute)
)

func pf(v float64) *float64 { return &v }

func meta() *rawv1.FetchMeta {
	return &rawv1.FetchMeta{Connector: "c", FetchedAt: timestamppb.New(fetched), ArchiveKey: "c/k.json.gz"}
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeRegionForecast(t *testing.T) {
	vis := 8000.0
	m := &rawv1.RegionForecast{
		Meta: meta(), Source: hazardv1.Source_SOURCE_BMKG, RegionCode: "32.73.01.1001", Village: " Cihapit ",
		Location: &commonv1.Point{Latitude: -6.9, Longitude: 107.61}, AnalysisTime: timestamppb.New(day),
		Steps: []*rawv1.ForecastStep{{
			ValidTime: timestamppb.New(day.Add(3 * time.Hour)), TemperatureC: 24, RelativeHumidityPct: 90,
			CloudCoverPct: 100, PrecipitationMm: 2.5, WeatherCode: 61, WeatherDesc: "Hujan Ringan", WindSpeedKmh: 5, WindFromDeg: 270, VisibilityM: &vis,
		}},
	}
	run, err := DecodeRegionForecast(marshal(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Validate(now); err != nil {
		t.Fatal(err)
	}
	st := run.Steps[0]
	if run.Site.ID != "adm4:32.73.01.1001" || run.Site.Name != "Cihapit" || run.Model != series.ModelBMKG || !run.IssuedAt.Equal(day) ||
		run.FetchedAt.Nanosecond() != 123456000 || *st.WeatherCode != 61 || *st.VisibilityM != 8000 || st.WindGustKmh != nil {
		t.Fatalf("%+v", run)
	}
	m.Source = hazardv1.Source_SOURCE_USGS
	if _, err := DecodeRegionForecast(marshal(t, m)); !errors.Is(err, series.ErrInvalid) {
		t.Fatal(err)
	}
}

func site(id, name, river string) *rawv1.ModelSite {
	return &rawv1.ModelSite{
		Id: id, Name: name, River: river,
		Requested: &commonv1.Point{Latitude: -6.975, Longitude: 107.625}, Cell: &commonv1.Point{Latitude: -6.9749985, Longitude: 107.625}, ElevationM: pf(661),
	}
}

func TestDecodeModelRuns(t *testing.T) {
	code := int32(3)
	g := &rawv1.GridWeatherForecast{
		Meta: meta(), Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: site("grid:-7.00:107.50", "", ""), Model: "best_match",
		Steps: []*rawv1.GridWeatherStep{{ValidTime: timestamppb.New(day), TemperatureC: pf(21.8), WeatherCode: &code, BoundaryLayerHeightM: pf(280)}},
	}
	w, err := DecodeGridWeather(marshal(t, g))
	if err != nil {
		t.Fatal(err)
	}
	if w.Site.Kind != series.SiteGrid || w.Source != series.SourceOpenMeteo || !w.IssuedAt.Equal(w.FetchedAt) || *w.Steps[0].WeatherCode != 3 ||
		*w.Steps[0].BoundaryLayerM != 280 || w.Steps[0].HumidityPct != nil || *w.Elevation != 661 || w.ArchiveKey != "c/k.json.gz" {
		t.Fatalf("%+v", w)
	}
	g.Site.Cell.Latitude = -7.01
	if w, _ := DecodeGridWeather(marshal(t, g)); w.Validate(now) != nil {
		t.Fatal(w.Validate(now))
	}

	a := &rawv1.AirQualityForecast{
		Meta: meta(), Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: site("grid:-7.00:107.50", "", ""), Model: "cams_global",
		Steps: []*rawv1.AirQualityStep{{ValidTime: timestamppb.New(day), Pm2_5Ugm3: pf(12.2), DustUgm3: pf(0)}},
	}
	aq, err := DecodeAirQuality(marshal(t, a))
	if err != nil || aq.Validate(now) != nil || *aq.Steps[0].PM25 != 12.2 || aq.Dataset != series.AirQuality {
		t.Fatalf("%+v %v", aq, err)
	}

	d := &rawv1.RiverDischargeForecast{
		Meta: meta(), Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: site("river:citarum-dayeuhkolot", "Dayeuhkolot", "Citarum"), Model: "glofas_v4",
		Steps: []*rawv1.DischargeStep{{ValidDate: timestamppb.New(day), DischargeM3S: pf(8.79), EnsembleMinM3S: pf(5.3), EnsembleMaxM3S: pf(17.79)}},
	}
	fl, err := DecodeDischarge(marshal(t, d))
	if err != nil || fl.Validate(now) != nil || fl.Site.Kind != series.SiteRiver || fl.Site.River != "Citarum" || *fl.Steps[0].Max != 17.79 {
		t.Fatalf("%+v %v", fl, err)
	}

	// Sumber selain Open-Meteo dan ID yang tidak dikenal ditolak Validate.
	a.Source = hazardv1.Source_SOURCE_OPENAQ
	if aq, _ := DecodeAirQuality(marshal(t, a)); !errors.Is(aq.Validate(now), series.ErrInvalid) {
		t.Fatal("sumber OpenAQ untuk prakiraan model harus ditolak")
	}
	d.Site.Id = "sensor:1"
	if fl, _ := DecodeDischarge(marshal(t, d)); !errors.Is(fl.Validate(now), series.ErrInvalid) {
		t.Fatal("ID tidak dikenal harus ditolak")
	}
}

func TestDecodeCorrupt(t *testing.T) {
	bad := []byte{0xff, 0xff, 0xff}
	if _, err := DecodeRegionForecast(bad); !errors.Is(err, series.ErrInvalid) {
		t.Error(err)
	}
	if _, err := DecodeGridWeather(bad); !errors.Is(err, series.ErrInvalid) {
		t.Error(err)
	}
	if _, err := DecodeAirQuality(bad); !errors.Is(err, series.ErrInvalid) {
		t.Error(err)
	}
	if _, err := DecodeDischarge(bad); !errors.Is(err, series.ErrInvalid) {
		t.Error(err)
	}
	if _, err := DecodeAirQualityObservation(bad); !errors.Is(err, series.ErrInvalid) {
		t.Error(err)
	}
	if _, err := DecodeFireDetection(bad); !errors.Is(err, series.ErrInvalid) {
		t.Error(err)
	}
	if !ts(nil).IsZero() {
		t.Error("ts(nil)")
	}
}

func TestDecodeAirQualityObservation(t *testing.T) {
	obsAt := fetched.Add(-40 * time.Minute).Truncate(time.Hour)
	m := &rawv1.AirQualityObservation{
		Meta: meta(), Source: hazardv1.Source_SOURCE_OPENAQ,
		Station: &rawv1.AirQualityStation{
			Id: "openaq:2178", Name: " Dago ", Locality: "Bandung", Location: &commonv1.Point{Latitude: -6.88, Longitude: 107.61},
			Provider: "AirGradient", IsMonitor: true, Timezone: "Asia/Jakarta",
		},
		Readings: []*rawv1.SensorReading{
			{SensorId: 1, Parameter: "pm25", Units: "µg/m³", Value: 40.5, ObservedAt: timestamppb.New(obsAt.Add(-time.Hour))},
			{SensorId: 2, Parameter: "o3", Units: "ppb", Value: 30, ObservedAt: timestamppb.New(obsAt)},
		},
	}
	obs, err := DecodeAirQualityObservation(marshal(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if err := obs.Validate(now); err != nil {
		t.Fatal(err)
	}
	if obs.Site.Kind != series.SiteStation || obs.Site.Name != "Dago" || !obs.IssuedAt.Equal(obs.Readings[1].ObservedAt) ||
		!obs.Station.IsMonitor || obs.Readings[0].Value != 40.5 || obs.Cell != obs.Site.Location {
		t.Fatalf("%+v", obs)
	}
	m.Source = hazardv1.Source_SOURCE_OPEN_METEO
	obs, _ = DecodeAirQualityObservation(marshal(t, m))
	if err := obs.Validate(now); !errors.Is(err, series.ErrInvalid) {
		t.Fatalf("sumber salah lolos: %v", err)
	}
}

func TestDecodeFireDetection(t *testing.T) {
	at := time.Date(2026, 9, 24, 6, 12, 0, 0, time.UTC)
	m := &rawv1.FireDetection{
		Meta: meta(), Source: hazardv1.Source_SOURCE_NASA_FIRMS, Id: "MODIS_NRT:20260924T0612:-6.83712:107.44157",
		Product: "MODIS_NRT", Satellite: "Aqua", Instrument: "MODIS",
		Location: &commonv1.Point{Latitude: -6.83712, Longitude: 107.44157}, DetectedAt: timestamppb.New(at),
		Confidence: rawv1.FireConfidence_FIRE_CONFIDENCE_NOMINAL, ConfidencePct: proto.Int32(72),
		BrightnessK: 321.5, FrpMw: pf(12), ScanKm: 1.1, TrackKm: 1, Daytime: true, Version: "6.1NRT",
	}
	d, err := DecodeFireDetection(marshal(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(now); err != nil {
		t.Fatal(err)
	}
	if d.Confidence != hotspot.Nominal || *d.ConfidencePct != 72 || d.BackgroundK != nil || *d.FRPMW != 12 || d.ArchiveKey != "c/k.json.gz" {
		t.Fatalf("%+v", d)
	}
	for c, want := range map[rawv1.FireConfidence]hotspot.Confidence{
		rawv1.FireConfidence_FIRE_CONFIDENCE_LOW: hotspot.Low, rawv1.FireConfidence_FIRE_CONFIDENCE_HIGH: hotspot.High,
		rawv1.FireConfidence_FIRE_CONFIDENCE_UNSPECIFIED: "",
	} {
		m.Confidence = c
		if d, _ := DecodeFireDetection(marshal(t, m)); d.Confidence != want {
			t.Errorf("%v → %q", c, d.Confidence)
		}
	}
	m.Source = hazardv1.Source_SOURCE_USGS
	if _, err := DecodeFireDetection(marshal(t, m)); !errors.Is(err, hotspot.ErrInvalid) {
		t.Fatal(err)
	}
}
