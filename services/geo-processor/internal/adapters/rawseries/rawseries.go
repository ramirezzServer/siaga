// Package rawseries mendekode event raw deret waktu menjadi nilai domain
// geo-processor: raw.forecast.bmkg (RegionForecast), raw.forecast.openmeteo
// (GridWeatherForecast), raw.aq.openmeteo (AirQualityForecast),
// raw.flood.openmeteo (RiverDischargeForecast), raw.aq.openaq
// (AirQualityObservation), dan raw.fire.firms (FireDetection). Validasi
// dilakukan use case; di sini hanya penerjemahan. Payload yang rusak
// membungkus series.ErrInvalid.
package rawseries

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

// DecodeRegionForecast membaca prakiraan BMKG per kelurahan/desa. Waktu
// terbit adalah waktu analisis BMKG.
func DecodeRegionForecast(data []byte) (series.WeatherRun, error) {
	var m rawv1.RegionForecast
	if err := unmarshal(data, &m); err != nil {
		return series.WeatherRun{}, err
	}
	if m.GetSource() != hazardv1.Source_SOURCE_BMKG {
		return series.WeatherRun{}, fmt.Errorf("%w: sumber prakiraan wilayah %s bukan BMKG", series.ErrInvalid, m.GetSource())
	}
	loc := point(m.GetLocation())
	run := series.WeatherRun{Run: series.Run{
		Site: series.Site{
			ID: series.RegionSiteID(m.GetRegionCode()), Kind: series.SiteRegion,
			Name: strings.TrimSpace(m.GetVillage()), Location: loc, RegionCode: m.GetRegionCode(),
		},
		Dataset: series.Weather, Source: series.SourceBMKG, Model: series.ModelBMKG,
		Cell: loc, IssuedAt: ts(m.GetAnalysisTime()),
		FetchedAt: ts(m.GetMeta().GetFetchedAt()), ArchiveKey: m.GetMeta().GetArchiveKey(),
	}}
	for _, s := range m.GetSteps() {
		code := int(s.GetWeatherCode())
		run.Steps = append(run.Steps, series.WeatherStep{
			ValidTime:       ts(s.GetValidTime()),
			TemperatureC:    val(s.GetTemperatureC()),
			HumidityPct:     val(s.GetRelativeHumidityPct()),
			PrecipitationMM: val(s.GetPrecipitationMm()),
			WeatherCode:     &code,
			CloudCoverPct:   val(s.GetCloudCoverPct()),
			WindSpeedKmh:    val(s.GetWindSpeedKmh()),
			WindFromDeg:     val(s.GetWindFromDeg()),
			VisibilityM:     s.VisibilityM,
		})
	}
	return run, nil
}

// DecodeGridWeather membaca prakiraan cuaca Open-Meteo per simpul grid.
func DecodeGridWeather(data []byte) (series.WeatherRun, error) {
	var m rawv1.GridWeatherForecast
	if err := unmarshal(data, &m); err != nil {
		return series.WeatherRun{}, err
	}
	run := series.WeatherRun{Run: modelRun(m.GetSource(), m.GetSite(), m.GetModel(), m.GetMeta(), series.Weather)}
	for _, s := range m.GetSteps() {
		var code *int
		if s.WeatherCode != nil {
			c := int(s.GetWeatherCode())
			code = &c
		}
		run.Steps = append(run.Steps, series.WeatherStep{
			ValidTime:       ts(s.GetValidTime()),
			TemperatureC:    s.TemperatureC,
			HumidityPct:     s.RelativeHumidityPct,
			PrecipitationMM: s.PrecipitationMm,
			WeatherCode:     code,
			CloudCoverPct:   s.CloudCoverPct,
			WindSpeedKmh:    s.WindSpeedKmh,
			WindFromDeg:     s.WindFromDeg,
			WindGustKmh:     s.WindGustKmh,
			PressureHPa:     s.SurfacePressureHpa,
			BoundaryLayerM:  s.BoundaryLayerHeightM,
		})
	}
	return run, nil
}

// DecodeAirQuality membaca prakiraan kualitas udara model per simpul grid.
func DecodeAirQuality(data []byte) (series.AirQualityRun, error) {
	var m rawv1.AirQualityForecast
	if err := unmarshal(data, &m); err != nil {
		return series.AirQualityRun{}, err
	}
	run := series.AirQualityRun{Run: modelRun(m.GetSource(), m.GetSite(), m.GetModel(), m.GetMeta(), series.AirQuality)}
	for _, s := range m.GetSteps() {
		run.Steps = append(run.Steps, series.AirQualityStep{
			ValidTime:           ts(s.GetValidTime()),
			PM25:                s.Pm2_5Ugm3,
			PM10:                s.Pm10Ugm3,
			CO:                  s.CarbonMonoxideUgm3,
			NO2:                 s.NitrogenDioxideUgm3,
			SO2:                 s.SulphurDioxideUgm3,
			O3:                  s.OzoneUgm3,
			AerosolOpticalDepth: s.AerosolOpticalDepth,
			Dust:                s.DustUgm3,
		})
	}
	return run, nil
}

// DecodeDischarge membaca prakiraan debit harian di titik pantau sungai.
func DecodeDischarge(data []byte) (series.DischargeRun, error) {
	var m rawv1.RiverDischargeForecast
	if err := unmarshal(data, &m); err != nil {
		return series.DischargeRun{}, err
	}
	run := series.DischargeRun{Run: modelRun(m.GetSource(), m.GetSite(), m.GetModel(), m.GetMeta(), series.Discharge)}
	for _, s := range m.GetSteps() {
		run.Steps = append(run.Steps, series.DischargeStep{
			ValidDate: ts(s.GetValidDate()),
			Discharge: s.DischargeM3S,
			Mean:      s.EnsembleMeanM3S,
			Median:    s.EnsembleMedianM3S,
			Max:       s.EnsembleMaxM3S,
			Min:       s.EnsembleMinM3S,
			P25:       s.EnsembleP25M3S,
			P75:       s.EnsembleP75M3S,
		})
	}
	return run, nil
}

// DecodeAirQualityObservation membaca nilai terbaru satu stasiun OpenAQ.
// Waktu terbit deret adalah waktu ukur terbaru.
func DecodeAirQualityObservation(data []byte) (series.StationObservation, error) {
	var m rawv1.AirQualityObservation
	if err := unmarshal(data, &m); err != nil {
		return series.StationObservation{}, err
	}
	st := m.GetStation()
	loc := point(st.GetLocation())
	kind, _ := series.KindOf(st.GetId())
	source := series.Source("")
	if m.GetSource() == hazardv1.Source_SOURCE_OPENAQ {
		source = series.SourceOpenAQ
	}
	fetched := ts(m.GetMeta().GetFetchedAt())
	obs := series.StationObservation{
		Run: series.Run{
			Site:    series.Site{ID: st.GetId(), Kind: kind, Name: strings.TrimSpace(st.GetName()), Location: loc},
			Dataset: series.AirQualityObs, Source: source, Model: series.ModelSensor, Cell: loc,
			FetchedAt: fetched, ArchiveKey: m.GetMeta().GetArchiveKey(),
		},
		Station: series.Station{
			Provider: st.GetProvider(), Owner: st.GetOwner(), Locality: st.GetLocality(),
			IsMonitor: st.GetIsMonitor(), Timezone: st.GetTimezone(),
		},
	}
	for _, r := range m.GetReadings() {
		at := ts(r.GetObservedAt())
		obs.Readings = append(obs.Readings, series.Reading{
			SensorID: r.GetSensorId(), Parameter: r.GetParameter(), Unit: r.GetUnits(), Value: r.GetValue(), ObservedAt: at,
		})
		if at.After(obs.IssuedAt) {
			obs.IssuedAt = at
		}
	}
	return obs, nil
}

// DecodeFireDetection membaca satu deteksi titik panas FIRMS.
func DecodeFireDetection(data []byte) (hotspot.Detection, error) {
	var m rawv1.FireDetection
	if err := unmarshal(data, &m); err != nil {
		return hotspot.Detection{}, err
	}
	if m.GetSource() != hazardv1.Source_SOURCE_NASA_FIRMS {
		return hotspot.Detection{}, fmt.Errorf("%w: sumber titik panas %s bukan NASA FIRMS", hotspot.ErrInvalid, m.GetSource())
	}
	d := hotspot.Detection{
		ID: m.GetId(), Product: m.GetProduct(), Satellite: m.GetSatellite(), Instrument: m.GetInstrument(),
		Lat: m.GetLocation().GetLatitude(), Lon: m.GetLocation().GetLongitude(), DetectedAt: ts(m.GetDetectedAt()),
		BrightnessK: m.GetBrightnessK(), BackgroundK: m.BackgroundBrightnessK, FRPMW: m.FrpMw,
		ScanKm: m.GetScanKm(), TrackKm: m.GetTrackKm(), Daytime: m.GetDaytime(), Version: m.GetVersion(),
		FetchedAt: ts(m.GetMeta().GetFetchedAt()), ArchiveKey: m.GetMeta().GetArchiveKey(),
	}
	switch m.GetConfidence() {
	case rawv1.FireConfidence_FIRE_CONFIDENCE_LOW:
		d.Confidence = hotspot.Low
	case rawv1.FireConfidence_FIRE_CONFIDENCE_NOMINAL:
		d.Confidence = hotspot.Nominal
	case rawv1.FireConfidence_FIRE_CONFIDENCE_HIGH:
		d.Confidence = hotspot.High
	case rawv1.FireConfidence_FIRE_CONFIDENCE_UNSPECIFIED:
		// Dibiarkan kosong; Validate menolaknya.
	}
	if m.ConfidencePct != nil {
		pct := int(m.GetConfidencePct())
		d.ConfidencePct = &pct
	}
	return d, nil
}

// modelRun membentuk kepala deret Open-Meteo. Waktu terbit adalah waktu
// ingest pertama kali menerima isi ini: Open-Meteo tidak menyebut waktu
// jalan modelnya, dan ingest hanya menerbitkan isi yang berubah (ADR 0012).
func modelRun(src hazardv1.Source, site *rawv1.ModelSite, model string, meta *rawv1.FetchMeta, ds series.Dataset) series.Run {
	kind, _ := series.KindOf(site.GetId())
	source := series.Source("")
	if src == hazardv1.Source_SOURCE_OPEN_METEO {
		source = series.SourceOpenMeteo
	}
	fetched := ts(meta.GetFetchedAt())
	return series.Run{
		Site: series.Site{
			ID: site.GetId(), Kind: kind, Name: strings.TrimSpace(site.GetName()), River: strings.TrimSpace(site.GetRiver()),
			Location: point(site.GetRequested()),
		},
		Dataset: ds, Source: source, Model: model,
		Cell: point(site.GetCell()), Elevation: site.ElevationM,
		IssuedAt: fetched, FetchedAt: fetched, ArchiveKey: meta.GetArchiveKey(),
	}
}

func unmarshal(data []byte, m proto.Message) error {
	if err := proto.Unmarshal(data, m); err != nil {
		return fmt.Errorf("%w: payload %s rusak: %w", series.ErrInvalid, m.ProtoReflect().Descriptor().Name(), err)
	}
	return nil
}

func point(p *commonv1.Point) series.Point {
	return series.Point{Lat: p.GetLatitude(), Lon: p.GetLongitude()}
}

func val(v float64) *float64 { return &v }

// ts membulatkan ke mikrodetik, presisi timestamptz PostgreSQL, supaya
// nilai yang dibaca ulang dari database identik.
func ts(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime().UTC().Truncate(time.Microsecond)
}
