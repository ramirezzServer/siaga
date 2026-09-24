package hazardpb

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// WeatherEncoder adalah ports.WeatherEncoder: pesan hazard.weather.*.
type WeatherEncoder struct{}

var _ ports.WeatherEncoder = WeatherEncoder{}

// Created membentuk hazard.weather.created.
func (WeatherEncoder) Created(a weather.Assessment, revision int) (ports.OutboxMessage, error) {
	return encode(streams.KindWeather, streams.Created, a.ID, revision, &hazardv1.HazardCreated{Hazard: WeatherHazard(a, revision)})
}

// Updated membentuk hazard.weather.updated.
func (WeatherEncoder) Updated(a weather.Assessment, revision int, previous hazard.Level) (ports.OutboxMessage, error) {
	return encode(streams.KindWeather, streams.Updated, a.ID, revision, &hazardv1.HazardUpdated{
		Hazard:        WeatherHazard(a, revision),
		PreviousLevel: Level(previous),
	})
}

// Expired membentuk hazard.weather.expired.
func (WeatherEncoder) Expired(id hazard.EventID, at time.Time, reason ports.ExpiryReason, mergedInto hazard.EventID, revision int) (ports.OutboxMessage, error) {
	return expired(streams.KindWeather, hazardv1.HazardKind_HAZARD_KIND_WEATHER, id, at, reason, mergedInto, revision)
}

// WeatherHazard membentuk pesan Hazard dari penilaian peringatan cuaca.
// Wilayah terdampak diurutkan dari cakupan terbesar.
func WeatherHazard(a weather.Assessment, revision int) *hazardv1.Hazard {
	c := a.Content
	id, _ := c.Text(weather.LanguagePrimary)
	en, _ := c.Text("en")
	h := &hazardv1.Hazard{
		Id:                  a.ID.String(),
		Kind:                hazardv1.HazardKind_HAZARD_KIND_WEATHER,
		Level:               Level(a.Level),
		PrimarySource:       hazardv1.Source_SOURCE_BMKG,
		SourceEventId:       a.Root.Identifier,
		OccurredAt:          timestamppb.New(a.OccurredAt),
		DetectedAt:          timestamppb.New(a.DetectedAt),
		ExpiresAt:           timestamppb.New(a.ExpiresAt),
		Location:            &commonv1.Point{Latitude: a.Latitude, Longitude: a.Longitude},
		AreaGeojson:         a.AreaGeoJSON,
		Title:               a.Title,
		Summary:             a.Summary,
		Revision:            u32(revision),
		ImpactedRegionCount: u32(len(a.Impacts)),
		Detail: &hazardv1.Hazard_Weather{Weather: &hazardv1.WeatherWarningDetail{
			CapSeverity:   c.Severity.CAP(),
			CapEvent:      id.Event,
			Headline:      id.Headline,
			Description:   id.Description,
			Instruction:   id.Instruction,
			CapIdentifier: a.Current.Identifier,
			CapMsgType:    capMsgType(a.Current.MsgType),
			CapUrgency:    c.Urgency,
			CapCertainty:  c.Certainty,
			CapEventCode:  c.EventCode,
			CapEventEn:    en.Event,
			HeadlineEn:    en.Headline,
			DescriptionEn: en.Description,
			InstructionEn: en.Instruction,
			AreaDesc:      c.AreaDesc,
			WebUrl:        c.Web,
			SourceUrl:     c.SourceURL,
			AreaKm2:       a.AreaKm2,
			MessageCount:  u32(a.Messages),
		}},
	}
	for i, im := range a.Impacts {
		if i == MaxImpactedRegions {
			break
		}
		h.ImpactedRegions = append(h.ImpactedRegions, &commonv1.RegionRef{Code: im.Code, Name: im.Name})
	}
	return h
}

func capMsgType(t weather.MsgType) string {
	switch t {
	case weather.MsgAlert:
		return "Alert"
	case weather.MsgUpdate:
		return "Update"
	case weather.MsgCancel:
		return "Cancel"
	default:
		return string(t)
	}
}
