// Package rawquake mendekode event raw.quake.* (siaga.raw.v1.QuakeReport)
// menjadi laporan domain geo-processor.
package rawquake

import (
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

// Decode membaca payload Protobuf. Galat selalu membungkus quake.ErrInvalid:
// payload yang tidak terbaca tidak akan terbaca bila dicoba ulang.
//
// Waktu dibulatkan ke mikrodetik, presisi timestamptz PostgreSQL, supaya
// laporan yang dibaca ulang dari database identik dengan laporan aslinya.
func Decode(data []byte) (quake.Report, error) {
	var m rawv1.QuakeReport
	if err := proto.Unmarshal(data, &m); err != nil {
		return quake.Report{}, fmt.Errorf("%w: payload QuakeReport rusak: %w", quake.ErrInvalid, err)
	}
	return FromProto(&m)
}

// FromProto menerjemahkan pesan yang sudah didekode.
func FromProto(m *rawv1.QuakeReport) (quake.Report, error) {
	feed, err := feedOf(m.GetFeed())
	if err != nil {
		return quake.Report{}, err
	}
	src, err := sourceOf(m.GetSource())
	if err != nil {
		return quake.Report{}, err
	}
	if m.GetEpicenter() == nil {
		return quake.Report{}, fmt.Errorf("%w: laporan %s tanpa episenter", quake.ErrInvalid, m.GetSourceEventId())
	}
	r := quake.Report{
		Source:          src,
		Feed:            feed,
		EventID:         m.GetSourceEventId(),
		OccurredAt:      ts(m.GetOccurredAt()),
		Latitude:        m.GetEpicenter().GetLatitude(),
		Longitude:       m.GetEpicenter().GetLongitude(),
		Magnitude:       m.GetMagnitude(),
		MagnitudeType:   m.GetMagnitudeType(),
		DepthKm:         m.GetDepthKm(),
		Place:           m.GetPlace(),
		Felt:            m.GetFeltDescription(),
		Tsunami:         tsunamiOf(m.GetTsunamiPotential()),
		PotentialText:   m.GetPotentialText(),
		ShakemapURL:     m.GetShakemapUrl(),
		SourceUpdatedAt: ts(m.GetSourceUpdatedAt()),
		ReviewStatus:    m.GetReviewStatus(),
		AlternateIDs:    slices.Clone(m.GetAlternateIds()),
		SourceURL:       m.GetSourceUrl(),
		FetchedAt:       ts(m.GetMeta().GetFetchedAt()),
		ArchiveKey:      m.GetMeta().GetArchiveKey(),
	}
	if len(r.AlternateIDs) == 0 {
		r.AlternateIDs = nil
	}
	return r, r.Validate()
}

func ts(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime().UTC().Truncate(time.Microsecond)
}

func feedOf(f rawv1.QuakeFeed) (quake.Feed, error) {
	switch f {
	case rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST:
		return quake.FeedBMKGLatest, nil
	case rawv1.QuakeFeed_QUAKE_FEED_BMKG_RECENT:
		return quake.FeedBMKGRecent, nil
	case rawv1.QuakeFeed_QUAKE_FEED_BMKG_FELT:
		return quake.FeedBMKGFelt, nil
	case rawv1.QuakeFeed_QUAKE_FEED_USGS_SUMMARY:
		return quake.FeedUSGSSummary, nil
	case rawv1.QuakeFeed_QUAKE_FEED_UNSPECIFIED:
		return "", fmt.Errorf("%w: feed gempa kosong", quake.ErrInvalid)
	default:
		return "", fmt.Errorf("%w: feed gempa tidak dikenal %d", quake.ErrInvalid, f)
	}
}

func sourceOf(s hazardv1.Source) (quake.Source, error) {
	switch s {
	case hazardv1.Source_SOURCE_BMKG:
		return quake.SourceBMKG, nil
	case hazardv1.Source_SOURCE_USGS:
		return quake.SourceUSGS, nil
	case hazardv1.Source_SOURCE_UNSPECIFIED, hazardv1.Source_SOURCE_OPEN_METEO, hazardv1.Source_SOURCE_NASA_FIRMS,
		hazardv1.Source_SOURCE_OPENAQ, hazardv1.Source_SOURCE_DRILL:
		return "", fmt.Errorf("%w: sumber %s bukan sumber gempa", quake.ErrInvalid, s)
	default:
		return "", fmt.Errorf("%w: sumber tidak dikenal %d", quake.ErrInvalid, s)
	}
}

func tsunamiOf(t rawv1.TsunamiPotential) quake.Tsunami {
	switch t {
	case rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE:
		return quake.TsunamiNone
	case rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_POTENTIAL:
		return quake.TsunamiPotential
	case rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED:
		return quake.TsunamiUnknown
	default:
		return quake.TsunamiUnknown
	}
}
