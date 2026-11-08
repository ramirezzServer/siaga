package hazardpb

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// FireEncoder adalah ports.FireEncoder: pesan hazard.fire.*.
type FireEncoder struct{}

var _ ports.FireEncoder = FireEncoder{}

// Created membentuk hazard.fire.created.
func (FireEncoder) Created(a fire.Assessment, revision int) (ports.OutboxMessage, error) {
	return encode(streams.KindFire, streams.Created, a.ID, revision, &hazardv1.HazardCreated{Hazard: FireHazard(a, revision)})
}

// Updated membentuk hazard.fire.updated.
func (FireEncoder) Updated(a fire.Assessment, revision int, previous hazard.Level) (ports.OutboxMessage, error) {
	return encode(streams.KindFire, streams.Updated, a.ID, revision, &hazardv1.HazardUpdated{
		Hazard:        FireHazard(a, revision),
		PreviousLevel: Level(previous),
	})
}

// Expired membentuk hazard.fire.expired (berakhir atau digabung).
func (FireEncoder) Expired(id hazard.EventID, at time.Time, reason ports.ExpiryReason, mergedInto hazard.EventID, revision int) (ports.OutboxMessage, error) {
	return expired(streams.KindFire, hazardv1.HazardKind_HAZARD_KIND_WILDFIRE, id, at, reason, mergedInto, revision)
}

// FireHazard membentuk pesan Hazard dari kejadian titik api. Kejadian titik
// api belum punya area dan daftar wilayah terdampak: pencocokan lokasi
// pengguna (≤ 10 km dari deteksi, PRD) dilakukan alert-engine dari daftar
// deteksi.
func FireHazard(a fire.Assessment, revision int) *hazardv1.Hazard {
	d := &hazardv1.FireDetail{
		DetectionCount:      u32(a.Detections),
		OverpassCount:       u32(a.Overpasses),
		HighConfidenceCount: u32(a.HighConfidence),
		PeakDetections_24H:  u32(a.PeakDetections),
		PeakOverpasses_24H:  u32(a.PeakOverpasses),
		FirstDetectedAt:     timestamppb.New(a.First),
		LastDetectedAt:      timestamppb.New(a.Last),
		LevelReachedAt:      timestamppb.New(a.ReachedAt),
		LastFetchedAt:       timestamppb.New(a.LastFetchedAt),
		Products:            append([]string(nil), a.Products...),
		MaxFrpMw:            a.MaxFRPMW,
		SiagaDetections:     u32(a.SiagaCount),
		WaspadaOverpasses:   u32(a.WaspadaPasses),
		OpenedBy:            a.OpenedBy,
	}
	for _, m := range a.Members {
		d.Detections = append(d.Detections, &hazardv1.FireMember{
			Id: m.ID, Location: &commonv1.Point{Latitude: m.Lat, Longitude: m.Lon}, DetectedAt: timestamppb.New(m.DetectedAt),
			Product: m.Product, Confidence: string(m.Confidence), FrpMw: m.FRPMW,
		})
	}
	return &hazardv1.Hazard{
		Id:            a.ID.String(),
		Kind:          hazardv1.HazardKind_HAZARD_KIND_WILDFIRE,
		Level:         Level(a.Level),
		PrimarySource: hazardv1.Source_SOURCE_NASA_FIRMS,
		SourceEventId: a.OpenedBy,
		OccurredAt:    timestamppb.New(a.First),
		DetectedAt:    timestamppb.New(a.FirstFetchedAt),
		ExpiresAt:     timestamppb.New(a.ExpiresAt),
		Location:      &commonv1.Point{Latitude: a.Lat, Longitude: a.Lon},
		Title:         a.Title,
		Summary:       a.Summary,
		Revision:      u32(revision),
		Detail:        &hazardv1.Hazard_Fire{Fire: d},
	}
}
