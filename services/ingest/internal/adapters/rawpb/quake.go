// Package rawpb menerjemahkan laporan domain ke kontrak Protobuf siaga.raw.v1
// dan membungkusnya sebagai ports.Event.
package rawpb

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// deterministic menjamin byte yang sama untuk isi yang sama, syarat ID pesan stabil.
var deterministic = proto.MarshalOptions{Deterministic: true}

// QuakeEvent adalah satu laporan gempa sebagai ports.Event.
type QuakeEvent struct {
	subject string
	key     string
	msg     *rawv1.QuakeReport // tanpa meta
}

// NewQuakeEvent membangun event dari laporan yang sudah lolos Validate.
func NewQuakeEvent(r quake.Report) (*QuakeEvent, error) {
	src, feed, err := mapSource(r.Feed)
	if err != nil {
		return nil, err
	}
	subject, err := streams.RawSubject(streams.KindQuake, streams.Source(r.Source))
	if err != nil {
		return nil, err
	}
	msg := &rawv1.QuakeReport{
		Source:           src,
		Feed:             feed,
		SourceEventId:    r.EventID,
		OccurredAt:       timestamppb.New(r.OccurredAt),
		Epicenter:        &commonv1.Point{Latitude: r.Latitude, Longitude: r.Longitude},
		Magnitude:        r.Magnitude,
		MagnitudeType:    r.MagnitudeType,
		DepthKm:          r.DepthKm,
		Place:            r.Place,
		FeltDescription:  r.Felt,
		TsunamiPotential: mapTsunami(r.Tsunami),
		PotentialText:    r.PotentialText,
		ShakemapUrl:      r.ShakemapURL,
		ReviewStatus:     r.ReviewStatus,
		AlternateIds:     r.AlternateIDs,
		SourceUrl:        r.SourceURL,
	}
	if !r.SourceUpdatedAt.IsZero() {
		msg.SourceUpdatedAt = timestamppb.New(r.SourceUpdatedAt)
	}
	return &QuakeEvent{subject: subject, key: r.EventID, msg: msg}, nil
}

// Subject mengembalikan subjek raw.quake.<sumber>.
func (e *QuakeEvent) Subject() string { return e.subject }

// Key mengembalikan ID kejadian di sumber.
func (e *QuakeEvent) Key() string { return e.key }

// Content adalah serialisasi deterministik tanpa meta.
func (e *QuakeEvent) Content() ([]byte, error) {
	b, err := deterministic.Marshal(e.msg)
	if err != nil {
		return nil, fmt.Errorf("marshal QuakeReport: %w", err)
	}
	return b, nil
}

// Encode menambahkan meta pengambilan lalu menserialisasi.
func (e *QuakeEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	full := proto.CloneOf(e.msg)
	full.Meta = &rawv1.FetchMeta{
		Connector:     m.Connector,
		FetchedAt:     timestamppb.New(m.FetchedAt),
		ArchiveKey:    m.ArchiveKey,
		PayloadSha256: m.PayloadSHA256,
	}
	b, err := deterministic.Marshal(full)
	if err != nil {
		return nil, fmt.Errorf("marshal QuakeReport: %w", err)
	}
	return b, nil
}

// Report mengembalikan salinan pesan (tanpa meta), untuk test dan rekaman.
func (e *QuakeEvent) Report() *rawv1.QuakeReport { return proto.CloneOf(e.msg) }

func mapSource(f quake.Feed) (hazardv1.Source, rawv1.QuakeFeed, error) {
	switch f {
	case quake.FeedBMKGLatest:
		return hazardv1.Source_SOURCE_BMKG, rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST, nil
	case quake.FeedBMKGRecent:
		return hazardv1.Source_SOURCE_BMKG, rawv1.QuakeFeed_QUAKE_FEED_BMKG_RECENT, nil
	case quake.FeedBMKGFelt:
		return hazardv1.Source_SOURCE_BMKG, rawv1.QuakeFeed_QUAKE_FEED_BMKG_FELT, nil
	case quake.FeedUSGSSummary:
		return hazardv1.Source_SOURCE_USGS, rawv1.QuakeFeed_QUAKE_FEED_USGS_SUMMARY, nil
	default:
		return 0, 0, fmt.Errorf("feed gempa tidak dikenal %q", f)
	}
}

func mapTsunami(t quake.Tsunami) rawv1.TsunamiPotential {
	switch t {
	case quake.TsunamiNone:
		return rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE
	case quake.TsunamiPotential:
		return rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_POTENTIAL
	case quake.TsunamiUnknown:
		return rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED
	default:
		return rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED
	}
}

// Pastikan QuakeEvent memenuhi port.
var _ ports.Event = (*QuakeEvent)(nil)
