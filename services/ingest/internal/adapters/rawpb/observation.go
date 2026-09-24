package rawpb

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// NewAirQualityObservationEvent membangun raw.aq.openaq dari pengukuran
// yang sudah lolos Validate. Kunci event adalah ID stasiun.
func NewAirQualityObservationEvent(o airquality.Observation) (ports.Event, error) {
	st := o.Station
	msg := &rawv1.AirQualityObservation{
		Source: hazardv1.Source_SOURCE_OPENAQ,
		Station: &rawv1.AirQualityStation{
			Id: st.ID, Name: st.Name, Locality: st.Locality,
			Location: &commonv1.Point{Latitude: st.Lat, Longitude: st.Lon},
			Provider: st.Provider, Owner: st.Owner, IsMonitor: st.IsMonitor, Timezone: st.Timezone,
		},
		Readings: make([]*rawv1.SensorReading, len(o.Readings)),
	}
	for i, r := range o.Readings {
		msg.Readings[i] = &rawv1.SensorReading{
			SensorId: r.SensorID, Parameter: string(r.Parameter), Units: r.Unit,
			Value: r.Value, ObservedAt: timestamppb.New(r.ObservedAt),
		}
	}
	return newEvent(streams.KindAQ, streams.SourceOpenAQ, st.ID, msg, func(m proto.Message, meta *rawv1.FetchMeta) {
		m.(*rawv1.AirQualityObservation).Meta = meta
	})
}

// NewFireEvent membangun raw.fire.firms dari deteksi yang sudah lolos
// Validate. Kunci event adalah ID deteksi.
func NewFireEvent(d fire.Detection) (ports.Event, error) {
	msg := &rawv1.FireDetection{
		Source: hazardv1.Source_SOURCE_NASA_FIRMS, Id: d.ID(), Product: string(d.Product),
		Satellite: d.Satellite, Instrument: string(d.Instrument),
		Location:   &commonv1.Point{Latitude: d.Lat, Longitude: d.Lon},
		DetectedAt: timestamppb.New(d.DetectedAt), Confidence: fireConfidence(d.Confidence),
		BrightnessK: d.BrightnessK, BackgroundBrightnessK: d.BackgroundK, FrpMw: d.FRPMW,
		ScanKm: d.ScanKm, TrackKm: d.TrackKm, Daytime: d.Daytime, Version: d.Version,
	}
	if d.ConfidencePct != nil {
		pct := int32(min(max(*d.ConfidencePct, 0), 100))
		msg.ConfidencePct = &pct
	}
	if msg.GetConfidence() == rawv1.FireConfidence_FIRE_CONFIDENCE_UNSPECIFIED {
		return nil, fmt.Errorf("deteksi %s: kelas keyakinan %d tidak dikenal", msg.GetId(), d.Confidence)
	}
	return newEvent(streams.KindFire, streams.SourceFIRMS, msg.GetId(), msg, func(m proto.Message, meta *rawv1.FetchMeta) {
		m.(*rawv1.FireDetection).Meta = meta
	})
}

func fireConfidence(c fire.Confidence) rawv1.FireConfidence {
	switch c {
	case fire.Low:
		return rawv1.FireConfidence_FIRE_CONFIDENCE_LOW
	case fire.Nominal:
		return rawv1.FireConfidence_FIRE_CONFIDENCE_NOMINAL
	case fire.High:
		return rawv1.FireConfidence_FIRE_CONFIDENCE_HIGH
	}
	return rawv1.FireConfidence_FIRE_CONFIDENCE_UNSPECIFIED
}

// newEvent membungkus pesan tanpa meta sebagai ports.Event untuk subjek
// raw.<kind>.<src>.
func newEvent(kind streams.Kind, src streams.Source, key string, msg proto.Message, setMeta func(proto.Message, *rawv1.FetchMeta)) (ports.Event, error) {
	subject, err := streams.RawSubject(kind, src)
	if err != nil {
		return nil, err
	}
	return &ModelEvent{subject: subject, key: key, msg: msg, setMeta: setMeta}, nil
}
