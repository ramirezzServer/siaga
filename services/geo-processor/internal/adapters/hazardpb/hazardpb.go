// Package hazardpb menerjemahkan keadaan domain (gempa, cuaca) ke kontrak
// Protobuf siaga.hazard.v1 dan membentuk pesan outbox hazard.<jenis>.*.
package hazardpb

import (
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// MaxImpactedRegions membatasi daftar wilayah di pesan; daftar lengkap ada di
// hazard.impact_region dan jumlahnya di impacted_region_count.
const MaxImpactedRegions = 100

var deterministic = proto.MarshalOptions{Deterministic: true}

// Encoder adalah ports.HazardEncoder untuk gempa.
type Encoder struct{}

var _ ports.HazardEncoder = Encoder{}

// MsgID membentuk Nats-Msg-Id: satu ID per revisi kejadian, jadi pesan yang
// diterbitkan ulang dari outbox setelah crash ditolak JetStream.
func MsgID(id hazard.EventID, revision int) string {
	return fmt.Sprintf("%s:r%d", id, revision)
}

// Created membentuk hazard.quake.created.
func (Encoder) Created(a quake.Assessment, revision int) (ports.OutboxMessage, error) {
	return encode(streams.KindQuake, streams.Created, a.ID, revision, &hazardv1.HazardCreated{Hazard: Hazard(a, revision)})
}

// Updated membentuk hazard.quake.updated.
func (Encoder) Updated(a quake.Assessment, revision int, previous quake.Level) (ports.OutboxMessage, error) {
	return encode(streams.KindQuake, streams.Updated, a.ID, revision, &hazardv1.HazardUpdated{
		Hazard:        Hazard(a, revision),
		PreviousLevel: Level(previous),
	})
}

// Expired membentuk hazard.quake.expired.
func (Encoder) Expired(id quake.EventID, at time.Time, reason ports.ExpiryReason, mergedInto quake.EventID, revision int) (ports.OutboxMessage, error) {
	return expired(streams.KindQuake, hazardv1.HazardKind_HAZARD_KIND_EARTHQUAKE, id, at, reason, mergedInto, revision)
}

func expired(kind streams.Kind, hk hazardv1.HazardKind, id hazard.EventID, at time.Time, reason ports.ExpiryReason, mergedInto hazard.EventID, revision int) (ports.OutboxMessage, error) {
	msg := &hazardv1.HazardExpired{
		HazardId:  id.String(),
		Kind:      hk,
		ExpiredAt: timestamppb.New(at),
		Reason:    expiryReason(reason),
		Revision:  u32(revision),
	}
	if !mergedInto.IsZero() {
		msg.MergedIntoHazardId = mergedInto.String()
	}
	return encode(kind, streams.Expired, id, revision, msg)
}

func encode(kind streams.Kind, t streams.Transition, id hazard.EventID, revision int, m proto.Message) (ports.OutboxMessage, error) {
	subject, err := streams.HazardSubject(kind, t)
	if err != nil {
		return ports.OutboxMessage{}, err
	}
	b, err := deterministic.Marshal(m)
	if err != nil {
		return ports.OutboxMessage{}, fmt.Errorf("marshal %s: %w", subject, err)
	}
	return ports.OutboxMessage{Subject: subject, MsgID: MsgID(id, revision), Payload: b}, nil
}

// Hazard membentuk pesan Hazard dari penilaian gempa.
func Hazard(a quake.Assessment, revision int) *hazardv1.Hazard {
	p := a.Primary
	h := &hazardv1.Hazard{
		Id:                  a.ID.String(),
		Kind:                hazardv1.HazardKind_HAZARD_KIND_EARTHQUAKE,
		Level:               Level(a.Level),
		PrimarySource:       Source(p.Key.Source),
		SourceEventId:       p.Key.EventID,
		OccurredAt:          timestamppb.New(p.OccurredAt),
		DetectedAt:          timestamppb.New(a.DetectedAt),
		ExpiresAt:           timestamppb.New(a.ExpiresAt),
		Location:            &commonv1.Point{Latitude: p.Latitude, Longitude: p.Longitude},
		Title:               a.Title,
		Summary:             a.Summary,
		Revision:            u32(revision),
		ImpactedRegionCount: u32(len(a.Impacts)),
		Detail: &hazardv1.Hazard_Earthquake{Earthquake: &hazardv1.EarthquakeDetail{
			Magnitude:        p.Magnitude,
			DepthKm:          p.DepthKm,
			FeltDescription:  p.Felt,
			TsunamiPotential: p.Tsunami == quake.TsunamiPotential,
			ShakemapUrl:      p.ShakemapURL,
			FeltRadiusKm:     a.FeltRadiusKm,
			MagnitudeType:    p.MagnitudeType,
		}},
	}
	for i, im := range a.Impacts {
		if i == MaxImpactedRegions {
			break
		}
		h.ImpactedRegions = append(h.ImpactedRegions, &commonv1.RegionRef{Code: im.Code, Name: im.Name})
	}
	eq := h.GetEarthquake()
	for _, c := range a.Corroborating {
		eq.CorroboratingReports = append(eq.CorroboratingReports, &hazardv1.SourceReport{
			Source:        Source(c.Key.Source),
			SourceEventId: c.Key.EventID,
			Magnitude:     c.Magnitude,
			Location:      &commonv1.Point{Latitude: c.Latitude, Longitude: c.Longitude},
			OccurredAt:    timestamppb.New(c.OccurredAt),
			DepthKm:       c.DepthKm,
			SourceUrl:     c.SourceURL,
		})
	}
	return h
}

// Level memetakan tingkat domain ke enum kontrak.
func Level(l hazard.Level) hazardv1.AlertLevel {
	switch l {
	case hazard.LevelInfo:
		return hazardv1.AlertLevel_ALERT_LEVEL_INFO
	case hazard.LevelWaspada:
		return hazardv1.AlertLevel_ALERT_LEVEL_WASPADA
	case hazard.LevelSiaga:
		return hazardv1.AlertLevel_ALERT_LEVEL_SIAGA
	case hazard.LevelBahaya:
		return hazardv1.AlertLevel_ALERT_LEVEL_BAHAYA
	default:
		return hazardv1.AlertLevel_ALERT_LEVEL_UNSPECIFIED
	}
}

// Source memetakan sumber domain ke enum kontrak.
func Source(s quake.Source) hazardv1.Source {
	switch s {
	case quake.SourceBMKG:
		return hazardv1.Source_SOURCE_BMKG
	case quake.SourceUSGS:
		return hazardv1.Source_SOURCE_USGS
	default:
		return hazardv1.Source_SOURCE_UNSPECIFIED
	}
}

func expiryReason(r ports.ExpiryReason) hazardv1.ExpiryReason {
	switch r {
	case ports.ExpiryElapsed:
		return hazardv1.ExpiryReason_EXPIRY_REASON_ELAPSED
	case ports.ExpiryMerged:
		return hazardv1.ExpiryReason_EXPIRY_REASON_MERGED
	case ports.ExpiryRetracted:
		return hazardv1.ExpiryReason_EXPIRY_REASON_RETRACTED
	default:
		return hazardv1.ExpiryReason_EXPIRY_REASON_UNSPECIFIED
	}
}

// u32 mengubah bilangan bulat ke uint32 dengan batas bawah 0 dan batas atas MaxUint32.
func u32(n int) uint32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxUint32:
		return math.MaxUint32
	default:
		return uint32(n)
	}
}
