package rawpb

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// WeatherEvent adalah satu peringatan CAP sebagai ports.Event (raw.weather.bmkg).
type WeatherEvent struct {
	subject string
	msg     *rawv1.WeatherWarning // tanpa meta
}

// NewWeatherEvent membangun event dari peringatan yang sudah lolos Validate.
func NewWeatherEvent(w warning.Warning) (*WeatherEvent, error) {
	subject, err := streams.RawSubject(streams.KindWeather, streams.SourceBMKG)
	if err != nil {
		return nil, err
	}
	msg := &rawv1.WeatherWarning{
		Source:     hazardv1.Source_SOURCE_BMKG,
		Identifier: w.Identifier,
		Sender:     w.Sender,
		Sent:       timestamppb.New(w.Sent),
		Status:     capStatus(w.Status),
		MsgType:    capMsgType(w.MsgType),
		Category:   w.Category,
		EventCode:  w.EventCode,
		Urgency:    capUrgency(w.Urgency),
		Severity:   capSeverity(w.Severity),
		Certainty:  capCertainty(w.Certainty),
		Effective:  timestamppb.New(w.Effective),
		Onset:      optionalTime(w.Onset),
		Expires:    timestamppb.New(w.Expires),
		Web:        w.Web,
		Contact:    w.Contact,
		SourceUrl:  w.SourceURL,
	}
	for _, r := range w.References {
		msg.References = append(msg.References, &rawv1.CapReference{
			Sender: r.Sender, Identifier: r.Identifier, Sent: timestamppb.New(r.Sent),
		})
	}
	for _, t := range w.Texts {
		msg.Texts = append(msg.Texts, &rawv1.CapText{
			Language: t.Language, Event: t.Event, Headline: t.Headline,
			Description: t.Description, Instruction: t.Instruction, SenderName: t.SenderName,
		})
	}
	for _, a := range w.Areas {
		area := &rawv1.CapArea{AreaDesc: a.Desc}
		for _, ring := range a.Polygons {
			r := &rawv1.Ring{Points: make([]*commonv1.Point, len(ring))}
			for i, p := range ring {
				r.Points[i] = &commonv1.Point{Latitude: p.Lat, Longitude: p.Lon}
			}
			area.Polygons = append(area.Polygons, r)
		}
		msg.Areas = append(msg.Areas, area)
	}
	return &WeatherEvent{subject: subject, msg: msg}, nil
}

// Subject mengembalikan raw.weather.bmkg.
func (e *WeatherEvent) Subject() string { return e.subject }

// Key mengembalikan identifier CAP.
func (e *WeatherEvent) Key() string { return e.msg.GetIdentifier() }

// Content adalah serialisasi deterministik tanpa meta.
func (e *WeatherEvent) Content() ([]byte, error) { return marshal(e.msg) }

// Encode menambahkan meta pengambilan lalu menserialisasi.
func (e *WeatherEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	full := proto.CloneOf(e.msg)
	full.Meta = fetchMeta(m)
	return marshal(full)
}

// Warning mengembalikan salinan pesan (tanpa meta), untuk test dan rekaman.
func (e *WeatherEvent) Warning() *rawv1.WeatherWarning { return proto.CloneOf(e.msg) }

// ForecastEvent adalah prakiraan satu kelurahan/desa sebagai ports.Event (raw.forecast.bmkg).
type ForecastEvent struct {
	subject string
	msg     *rawv1.RegionForecast // tanpa meta
}

// NewForecastEvent membangun event dari prakiraan yang sudah lolos Validate.
func NewForecastEvent(f forecast.Forecast) (*ForecastEvent, error) {
	subject, err := streams.RawSubject(streams.KindForecast, streams.SourceBMKG)
	if err != nil {
		return nil, err
	}
	msg := &rawv1.RegionForecast{
		Source:       hazardv1.Source_SOURCE_BMKG,
		RegionCode:   f.RegionCode,
		Province:     f.Province,
		Regency:      f.Regency,
		District:     f.District,
		Village:      f.Village,
		Location:     &commonv1.Point{Latitude: f.Latitude, Longitude: f.Longitude},
		AnalysisTime: timestamppb.New(f.AnalysisTime),
		Steps:        make([]*rawv1.ForecastStep, len(f.Steps)),
	}
	for i, s := range f.Steps {
		msg.Steps[i] = &rawv1.ForecastStep{
			ValidTime:           timestamppb.New(s.ValidTime),
			TemperatureC:        s.TemperatureC,
			RelativeHumidityPct: s.HumidityPct,
			CloudCoverPct:       s.CloudCoverPct,
			PrecipitationMm:     s.PrecipitationMM,
			WeatherCode:         int32(min(max(s.WeatherCode, 0), 999)),
			WeatherDesc:         s.WeatherDesc,
			WeatherDescEn:       s.WeatherDescEN,
			WindSpeedKmh:        s.WindSpeedKmh,
			WindFromDeg:         s.WindFromDeg,
			WindFrom:            s.WindFrom,
			VisibilityM:         s.VisibilityM,
			VisibilityText:      s.VisibilityText,
		}
	}
	return &ForecastEvent{subject: subject, msg: msg}, nil
}

// Subject mengembalikan raw.forecast.bmkg.
func (e *ForecastEvent) Subject() string { return e.subject }

// Key mengembalikan kode adm4.
func (e *ForecastEvent) Key() string { return e.msg.GetRegionCode() }

// Content adalah serialisasi deterministik tanpa meta.
func (e *ForecastEvent) Content() ([]byte, error) { return marshal(e.msg) }

// Encode menambahkan meta pengambilan lalu menserialisasi.
func (e *ForecastEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	full := proto.CloneOf(e.msg)
	full.Meta = fetchMeta(m)
	return marshal(full)
}

// Forecast mengembalikan salinan pesan (tanpa meta), untuk test dan rekaman.
func (e *ForecastEvent) Forecast() *rawv1.RegionForecast { return proto.CloneOf(e.msg) }

var (
	_ ports.Event = (*WeatherEvent)(nil)
	_ ports.Event = (*ForecastEvent)(nil)
)

func marshal(m proto.Message) ([]byte, error) {
	b, err := deterministic.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", m.ProtoReflect().Descriptor().Name(), err)
	}
	return b, nil
}

func fetchMeta(m ports.FetchMeta) *rawv1.FetchMeta {
	return &rawv1.FetchMeta{
		Connector:     m.Connector,
		FetchedAt:     timestamppb.New(m.FetchedAt),
		ArchiveKey:    m.ArchiveKey,
		PayloadSha256: m.PayloadSHA256,
	}
}

func optionalTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func capStatus(s warning.Status) rawv1.CapStatus {
	switch s {
	case warning.StatusActual:
		return rawv1.CapStatus_CAP_STATUS_ACTUAL
	case warning.StatusExercise:
		return rawv1.CapStatus_CAP_STATUS_EXERCISE
	case warning.StatusSystem:
		return rawv1.CapStatus_CAP_STATUS_SYSTEM
	case warning.StatusTest:
		return rawv1.CapStatus_CAP_STATUS_TEST
	case warning.StatusDraft:
		return rawv1.CapStatus_CAP_STATUS_DRAFT
	case warning.StatusUnknown:
		return rawv1.CapStatus_CAP_STATUS_UNSPECIFIED
	default:
		return rawv1.CapStatus_CAP_STATUS_UNSPECIFIED
	}
}

func capMsgType(t warning.MsgType) rawv1.CapMsgType {
	switch t {
	case warning.MsgAlert:
		return rawv1.CapMsgType_CAP_MSG_TYPE_ALERT
	case warning.MsgUpdate:
		return rawv1.CapMsgType_CAP_MSG_TYPE_UPDATE
	case warning.MsgCancel:
		return rawv1.CapMsgType_CAP_MSG_TYPE_CANCEL
	case warning.MsgAck:
		return rawv1.CapMsgType_CAP_MSG_TYPE_ACK
	case warning.MsgError:
		return rawv1.CapMsgType_CAP_MSG_TYPE_ERROR
	case warning.MsgUnknown:
		return rawv1.CapMsgType_CAP_MSG_TYPE_UNSPECIFIED
	default:
		return rawv1.CapMsgType_CAP_MSG_TYPE_UNSPECIFIED
	}
}

func capSeverity(s warning.Severity) rawv1.CapSeverity {
	switch s {
	case warning.SeverityExtreme:
		return rawv1.CapSeverity_CAP_SEVERITY_EXTREME
	case warning.SeveritySevere:
		return rawv1.CapSeverity_CAP_SEVERITY_SEVERE
	case warning.SeverityModerate:
		return rawv1.CapSeverity_CAP_SEVERITY_MODERATE
	case warning.SeverityMinor:
		return rawv1.CapSeverity_CAP_SEVERITY_MINOR
	case warning.SeverityUnknown:
		return rawv1.CapSeverity_CAP_SEVERITY_UNKNOWN
	case warning.SeverityUnspecified:
		return rawv1.CapSeverity_CAP_SEVERITY_UNSPECIFIED
	default:
		return rawv1.CapSeverity_CAP_SEVERITY_UNSPECIFIED
	}
}

func capUrgency(u warning.Urgency) rawv1.CapUrgency {
	switch u {
	case warning.UrgencyImmediate:
		return rawv1.CapUrgency_CAP_URGENCY_IMMEDIATE
	case warning.UrgencyExpected:
		return rawv1.CapUrgency_CAP_URGENCY_EXPECTED
	case warning.UrgencyFuture:
		return rawv1.CapUrgency_CAP_URGENCY_FUTURE
	case warning.UrgencyPast:
		return rawv1.CapUrgency_CAP_URGENCY_PAST
	case warning.UrgencyUnknown:
		return rawv1.CapUrgency_CAP_URGENCY_UNKNOWN
	case warning.UrgencyUnspecified:
		return rawv1.CapUrgency_CAP_URGENCY_UNSPECIFIED
	default:
		return rawv1.CapUrgency_CAP_URGENCY_UNSPECIFIED
	}
}

func capCertainty(c warning.Certainty) rawv1.CapCertainty {
	switch c {
	case warning.CertaintyObserved:
		return rawv1.CapCertainty_CAP_CERTAINTY_OBSERVED
	case warning.CertaintyLikely:
		return rawv1.CapCertainty_CAP_CERTAINTY_LIKELY
	case warning.CertaintyPossible:
		return rawv1.CapCertainty_CAP_CERTAINTY_POSSIBLE
	case warning.CertaintyUnlikely:
		return rawv1.CapCertainty_CAP_CERTAINTY_UNLIKELY
	case warning.CertaintyUnknown:
		return rawv1.CapCertainty_CAP_CERTAINTY_UNKNOWN
	case warning.CertaintyUnspecified:
		return rawv1.CapCertainty_CAP_CERTAINTY_UNSPECIFIED
	default:
		return rawv1.CapCertainty_CAP_CERTAINTY_UNSPECIFIED
	}
}
