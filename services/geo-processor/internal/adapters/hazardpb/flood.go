package hazardpb

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// FloodEncoder adalah ports.FloodEncoder: pesan hazard.flood.*.
type FloodEncoder struct{}

var _ ports.FloodEncoder = FloodEncoder{}

// Created membentuk hazard.flood.created.
func (FloodEncoder) Created(a flood.Assessment, revision int) (ports.OutboxMessage, error) {
	return encode(streams.KindFlood, streams.Created, a.ID, revision, &hazardv1.HazardCreated{Hazard: FloodHazard(a, revision)})
}

// Updated membentuk hazard.flood.updated.
func (FloodEncoder) Updated(a flood.Assessment, revision int, previous hazard.Level) (ports.OutboxMessage, error) {
	return encode(streams.KindFlood, streams.Updated, a.ID, revision, &hazardv1.HazardUpdated{
		Hazard:        FloodHazard(a, revision),
		PreviousLevel: Level(previous),
	})
}

// Expired membentuk hazard.flood.expired.
func (FloodEncoder) Expired(id hazard.EventID, at time.Time, reason ports.ExpiryReason, mergedInto hazard.EventID, revision int) (ports.OutboxMessage, error) {
	return expired(streams.KindFlood, hazardv1.HazardKind_HAZARD_KIND_FLOOD, id, at, reason, mergedInto, revision)
}

// FloodHazard membentuk pesan Hazard dari kejadian banjir. Kejadian banjir
// belum punya area dan daftar wilayah terdampak: pencocokan lokasi pengguna
// dengan sungai (≤ 2 km, PRD) dilakukan alert-engine.
func FloodHazard(a flood.Assessment, revision int) *hazardv1.Hazard {
	d := &hazardv1.FloodDetail{
		Indicator:      floodIndicator(a.Indicator),
		SiteId:         a.SiteID,
		SiteName:       a.SiteName,
		River:          a.River,
		Model:          a.Model,
		IssuedAt:       timestamppb.New(a.FetchedAt),
		Possible:       a.Possible,
		LastExceededAt: timestamppb.New(a.LastExceededAt),
		MaxLevel:       Level(a.MaxLevel),
	}
	if !a.Peak.IsZero() {
		d.PeakDate = timestamppb.New(a.Peak)
	}
	if a.Indicator == flood.Discharge {
		d.DischargeThresholdsM3S = append([]float64(nil), a.DischargeThresholds[:]...)
	} else {
		for w, hours := range flood.Windows {
			d.RainfallThresholds = append(d.RainfallThresholds, &hazardv1.RainfallThreshold{
				WindowHours: u32(hours), ThresholdsMm: append([]float64(nil), a.RainfallThresholds[w][:]...),
			})
		}
	}
	for _, day := range a.Days {
		d.Days = append(d.Days, &hazardv1.FloodDay{
			Date: timestamppb.New(day.Date), Level: Level(day.Level), Possible: day.Possible,
			DischargeM3S: day.Discharge, DischargeP75M3S: day.P75, DischargeMaxM3S: day.Max,
			Rain_3HMm: day.Rain[0], Rain_6HMm: day.Rain[1], Rain_24HMm: day.Rain[2],
			WindowHours: u32(day.Window), FromEnsemble: day.FromEnsemble,
		})
	}
	return &hazardv1.Hazard{
		Id:            a.ID.String(),
		Kind:          hazardv1.HazardKind_HAZARD_KIND_FLOOD,
		Level:         Level(a.Level),
		PrimarySource: hazardv1.Source_SOURCE_OPEN_METEO,
		SourceEventId: a.SiteID,
		OccurredAt:    timestamppb.New(a.OpenedAt),
		DetectedAt:    timestamppb.New(a.OpenedAt),
		ExpiresAt:     timestamppb.New(a.ExpiresAt),
		Location:      &commonv1.Point{Latitude: a.Location.Lat, Longitude: a.Location.Lon},
		Title:         a.Title,
		Summary:       a.Summary,
		Revision:      u32(revision),
		Detail:        &hazardv1.Hazard_Flood{Flood: d},
	}
}

func floodIndicator(i flood.Indicator) hazardv1.FloodIndicator {
	switch i {
	case flood.Discharge:
		return hazardv1.FloodIndicator_FLOOD_INDICATOR_DISCHARGE
	case flood.Rainfall:
		return hazardv1.FloodIndicator_FLOOD_INDICATOR_RAINFALL_INDEX
	default:
		return hazardv1.FloodIndicator_FLOOD_INDICATOR_UNSPECIFIED
	}
}
