package rawquake

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

var at = time.Date(2026, 9, 23, 12, 16, 56, 0, time.UTC)

func usgsMsg() *rawv1.QuakeReport {
	return &rawv1.QuakeReport{
		Meta: &rawv1.FetchMeta{
			Connector: "usgs-2.5_day", FetchedAt: timestamppb.New(at.Add(90*time.Second + 123456789)),
			ArchiveKey: "usgs-2.5_day/2026/09/23/121826Z-0123456789ab.geojson.gz",
		},
		Source:           hazardv1.Source_SOURCE_USGS,
		Feed:             rawv1.QuakeFeed_QUAKE_FEED_USGS_SUMMARY,
		SourceEventId:    "us7000ir9t",
		OccurredAt:       timestamppb.New(at.Add(-2800 * time.Millisecond)),
		Epicenter:        &commonv1.Point{Latitude: -6.836, Longitude: 106.9968},
		Magnitude:        5.6,
		MagnitudeType:    "mww",
		DepthKm:          10,
		Place:            "11 km NE of Sukabumi, Indonesia",
		TsunamiPotential: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE,
		SourceUpdatedAt:  timestamppb.New(at.Add(time.Minute)),
		ReviewStatus:     "reviewed",
		AlternateIds:     []string{"at00rlqzvj"},
		SourceUrl:        "https://earthquake.usgs.gov/earthquakes/eventpage/us7000ir9t",
	}
}

func TestDecodeMapsAllFields(t *testing.T) {
	b, err := proto.Marshal(usgsMsg())
	if err != nil {
		t.Fatal(err)
	}
	r, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != quake.SourceUSGS || r.Feed != quake.FeedUSGSSummary || r.EventID != "us7000ir9t" ||
		r.Magnitude != 5.6 || r.Latitude != -6.836 || r.Tsunami != quake.TsunamiNone || r.AlternateIDs[0] != "at00rlqzvj" ||
		r.ArchiveKey == "" || r.SourceURL == "" || r.ReviewStatus != "reviewed" {
		t.Fatalf("Decode = %+v", r)
	}
	if r.FetchedAt.Nanosecond()%1000 != 0 || r.FetchedAt.Location() != time.UTC {
		t.Errorf("waktu harus UTC dan dibulatkan ke mikrodetik: %v", r.FetchedAt)
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := Decode([]byte{0xff, 0xff}); !errors.Is(err, quake.ErrInvalid) {
		t.Errorf("payload rusak: %v", err)
	}
	cases := map[string]func(*rawv1.QuakeReport){
		"feed kosong":   func(m *rawv1.QuakeReport) { m.Feed = rawv1.QuakeFeed_QUAKE_FEED_UNSPECIFIED },
		"feed asing":    func(m *rawv1.QuakeReport) { m.Feed = 99 },
		"sumber cuaca":  func(m *rawv1.QuakeReport) { m.Source = hazardv1.Source_SOURCE_OPEN_METEO },
		"sumber asing":  func(m *rawv1.QuakeReport) { m.Source = 99 },
		"feed ≠ sumber": func(m *rawv1.QuakeReport) { m.Source = hazardv1.Source_SOURCE_BMKG },
		"tanpa titik":   func(m *rawv1.QuakeReport) { m.Epicenter = nil },
		"tanpa meta":    func(m *rawv1.QuakeReport) { m.Meta = nil },
		"tanpa waktu":   func(m *rawv1.QuakeReport) { m.OccurredAt = nil },
	}
	for name, mutate := range cases {
		m := usgsMsg()
		mutate(m)
		if _, err := FromProto(m); !errors.Is(err, quake.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTsunamiAndBMKGFeeds(t *testing.T) {
	for f, want := range map[rawv1.QuakeFeed]quake.Feed{
		rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST: quake.FeedBMKGLatest,
		rawv1.QuakeFeed_QUAKE_FEED_BMKG_RECENT: quake.FeedBMKGRecent,
		rawv1.QuakeFeed_QUAKE_FEED_BMKG_FELT:   quake.FeedBMKGFelt,
	} {
		m := usgsMsg()
		m.Source, m.Feed, m.SourceEventId, m.AlternateIds = hazardv1.Source_SOURCE_BMKG, f, "20260923121656", nil
		m.TsunamiPotential = rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_POTENTIAL
		r, err := FromProto(m)
		if err != nil || r.Feed != want || r.Tsunami != quake.TsunamiPotential || r.AlternateIDs != nil {
			t.Errorf("%v: %+v %v", f, r, err)
		}
	}
	for v, want := range map[rawv1.TsunamiPotential]quake.Tsunami{
		rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED: quake.TsunamiUnknown,
		99: quake.TsunamiUnknown,
	} {
		if got := tsunamiOf(v); got != want {
			t.Errorf("tsunamiOf(%v) = %v", v, got)
		}
	}
}
