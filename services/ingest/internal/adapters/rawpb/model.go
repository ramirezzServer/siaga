package rawpb

import (
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ModelEvent adalah deret satu titik keluaran model grid sebagai ports.Event
// (raw.forecast.openmeteo, raw.aq.openmeteo, raw.flood.openmeteo).
type ModelEvent struct {
	subject string
	key     string
	msg     proto.Message // tanpa meta
	setMeta func(proto.Message, *rawv1.FetchMeta)
}

var _ ports.Event = (*ModelEvent)(nil)

// Subject mengembalikan subjek raw.<jenis>.openmeteo.
func (e *ModelEvent) Subject() string { return e.subject }

// Key mengembalikan ID titik.
func (e *ModelEvent) Key() string { return e.key }

// Content adalah serialisasi deterministik tanpa meta.
func (e *ModelEvent) Content() ([]byte, error) { return marshal(e.msg) }

// Encode menambahkan meta pengambilan lalu menserialisasi.
func (e *ModelEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	full := proto.Clone(e.msg)
	e.setMeta(full, fetchMeta(m))
	return marshal(full)
}

// Message mengembalikan salinan pesan (tanpa meta), untuk test dan rekaman.
func (e *ModelEvent) Message() proto.Message { return proto.Clone(e.msg) }

// NewGridWeatherEvent membangun raw.forecast.openmeteo dari deret yang sudah
// lolos Validate dengan series.WeatherSpec.
func NewGridWeatherEvent(s series.Series) (ports.Event, error) {
	col, err := columns(s, series.WeatherSpec)
	if err != nil {
		return nil, err
	}
	msg := &rawv1.GridWeatherForecast{
		Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: site(s), Model: s.Model,
		Steps: make([]*rawv1.GridWeatherStep, len(s.Times)),
	}
	for i, t := range s.Times {
		msg.Steps[i] = &rawv1.GridWeatherStep{
			ValidTime:            timestamppb.New(t),
			TemperatureC:         col("temperature_2m", i),
			RelativeHumidityPct:  col("relative_humidity_2m", i),
			PrecipitationMm:      col("precipitation", i),
			WeatherCode:          code(col("weather_code", i)),
			CloudCoverPct:        col("cloud_cover", i),
			WindSpeedKmh:         col("wind_speed_10m", i),
			WindFromDeg:          col("wind_direction_10m", i),
			WindGustKmh:          col("wind_gusts_10m", i),
			SurfacePressureHpa:   col("surface_pressure", i),
			BoundaryLayerHeightM: col("boundary_layer_height", i),
		}
	}
	return newModelEvent(streams.KindForecast, s.Site.ID, msg, func(m proto.Message, meta *rawv1.FetchMeta) {
		m.(*rawv1.GridWeatherForecast).Meta = meta
	})
}

// NewAirQualityEvent membangun raw.aq.openmeteo dari deret yang sudah lolos
// Validate dengan series.AirQualitySpec.
func NewAirQualityEvent(s series.Series) (ports.Event, error) {
	col, err := columns(s, series.AirQualitySpec)
	if err != nil {
		return nil, err
	}
	msg := &rawv1.AirQualityForecast{
		Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: site(s), Model: s.Model,
		Steps: make([]*rawv1.AirQualityStep, len(s.Times)),
	}
	for i, t := range s.Times {
		msg.Steps[i] = &rawv1.AirQualityStep{
			ValidTime:           timestamppb.New(t),
			Pm2_5Ugm3:           col("pm2_5", i),
			Pm10Ugm3:            col("pm10", i),
			CarbonMonoxideUgm3:  col("carbon_monoxide", i),
			NitrogenDioxideUgm3: col("nitrogen_dioxide", i),
			SulphurDioxideUgm3:  col("sulphur_dioxide", i),
			OzoneUgm3:           col("ozone", i),
			AerosolOpticalDepth: col("aerosol_optical_depth", i),
			DustUgm3:            col("dust", i),
		}
	}
	return newModelEvent(streams.KindAQ, s.Site.ID, msg, func(m proto.Message, meta *rawv1.FetchMeta) {
		m.(*rawv1.AirQualityForecast).Meta = meta
	})
}

// NewDischargeEvent membangun raw.flood.openmeteo dari deret yang sudah lolos
// Validate dengan series.DischargeSpec.
func NewDischargeEvent(s series.Series) (ports.Event, error) {
	col, err := columns(s, series.DischargeSpec)
	if err != nil {
		return nil, err
	}
	msg := &rawv1.RiverDischargeForecast{
		Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: site(s), Model: s.Model,
		Steps: make([]*rawv1.DischargeStep, len(s.Times)),
	}
	for i, t := range s.Times {
		msg.Steps[i] = &rawv1.DischargeStep{
			ValidDate:         timestamppb.New(t),
			DischargeM3S:      col("river_discharge", i),
			EnsembleMeanM3S:   col("river_discharge_mean", i),
			EnsembleMedianM3S: col("river_discharge_median", i),
			EnsembleMaxM3S:    col("river_discharge_max", i),
			EnsembleMinM3S:    col("river_discharge_min", i),
			EnsembleP25M3S:    col("river_discharge_p25", i),
			EnsembleP75M3S:    col("river_discharge_p75", i),
		}
	}
	return newModelEvent(streams.KindFlood, s.Site.ID, msg, func(m proto.Message, meta *rawv1.FetchMeta) {
		m.(*rawv1.RiverDischargeForecast).Meta = meta
	})
}

func newModelEvent(kind streams.Kind, key string, msg proto.Message, setMeta func(proto.Message, *rawv1.FetchMeta)) (ports.Event, error) {
	subject, err := streams.RawSubject(kind, streams.SourceOpenMeteo)
	if err != nil {
		return nil, err
	}
	return &ModelEvent{subject: subject, key: key, msg: msg, setMeta: setMeta}, nil
}

// columns mengembalikan pengambil nilai per nama variabel. Deret harus
// punya kolom sebanyak variabel spec, masing-masing sepanjang Times.
func columns(s series.Series, spec series.Spec) (func(name string, i int) *float64, error) {
	if len(s.Values) != len(spec.Vars) {
		return nil, fmt.Errorf("deret %s berisi %d variabel, spesifikasi %s punya %d", s.Site.ID, len(s.Values), spec.Dataset, len(spec.Vars))
	}
	for v, col := range s.Values {
		if len(col) != len(s.Times) {
			return nil, fmt.Errorf("deret %s: %s berisi %d nilai untuk %d langkah", s.Site.ID, spec.Vars[v].Name, len(col), len(s.Times))
		}
	}
	return func(name string, i int) *float64 {
		v := spec.Index(name)
		if v < 0 || s.Values[v][i] == nil {
			return nil
		}
		x := *s.Values[v][i]
		return &x
	}, nil
}

func site(s series.Series) *rawv1.ModelSite {
	var elev *float64
	if s.Elevation != nil {
		e := *s.Elevation
		elev = &e
	}
	return &rawv1.ModelSite{
		Id: s.Site.ID, Name: s.Site.Name, River: s.Site.River,
		Requested:  &commonv1.Point{Latitude: s.Site.Requested.Lat, Longitude: s.Site.Requested.Lon},
		Cell:       &commonv1.Point{Latitude: s.Cell.Lat, Longitude: s.Cell.Lon},
		ElevationM: elev,
	}
}

// code mengubah kode cuaca (sudah divalidasi bilangan bulat 0..99) ke int32.
func code(v *float64) *int32 {
	if v == nil || math.IsNaN(*v) {
		return nil
	}
	c := int32(min(max(*v, 0), 999))
	return &c
}
