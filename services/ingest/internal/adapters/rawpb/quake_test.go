package rawpb

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func usgsReport() quake.Report {
	return quake.Report{
		Source: quake.SourceUSGS, Feed: quake.FeedUSGSSummary, EventID: "us6000tx31",
		OccurredAt: time.Date(2026, 9, 23, 12, 23, 20, 274e6, time.UTC),
		Latitude:   1.217, Longitude: 126.5263, Magnitude: 4.6, MagnitudeType: "mb", DepthKm: 48.462,
		Place: "106 km WNW of Ternate, Indonesia", Tsunami: quake.TsunamiNone,
		SourceUpdatedAt: time.Date(2026, 9, 23, 13, 15, 1, 40e6, time.UTC), ReviewStatus: "reviewed",
		AlternateIDs: []string{"at00abc"}, SourceURL: "https://earthquake.usgs.gov/earthquakes/eventpage/us6000tx31",
	}
}

func TestQuakeEventContentExcludesMeta(t *testing.T) {
	ev, err := NewQuakeEvent(usgsReport())
	if err != nil {
		t.Fatal(err)
	}
	if ev.Subject() != "raw.quake.usgs" || ev.Key() != "us6000tx31" {
		t.Fatalf("subjek/kunci %s %s", ev.Subject(), ev.Key())
	}
	c1, err := ev.Content()
	if err != nil {
		t.Fatal(err)
	}
	meta := ports.FetchMeta{Connector: "usgs-2.5-day", FetchedAt: time.Date(2026, 9, 23, 13, 50, 0, 0, time.UTC), ArchiveKey: "k", PayloadSHA256: "abc"}
	data, err := ev.Encode(meta)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := ev.Content()
	if !bytes.Equal(c1, c2) {
		t.Fatal("Encode tidak boleh mengubah isi dasar event")
	}

	var got rawv1.QuakeReport
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetMeta().GetConnector() != "usgs-2.5-day" || got.GetMeta().GetArchiveKey() != "k" ||
		!got.GetMeta().GetFetchedAt().AsTime().Equal(meta.FetchedAt) || got.GetMeta().GetPayloadSha256() != "abc" {
		t.Fatalf("meta: %v", got.GetMeta())
	}
	if got.GetTsunamiPotential() != rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE || got.GetMagnitudeType() != "mb" ||
		!got.GetSourceUpdatedAt().AsTime().Equal(usgsReport().SourceUpdatedAt) || got.GetAlternateIds()[0] != "at00abc" {
		t.Fatalf("isi: %v", &got)
	}
	got.Meta = nil
	if !proto.Equal(&got, ev.Report()) {
		t.Fatal("pesan tanpa meta harus sama dengan Report()")
	}
}

func TestMappings(t *testing.T) {
	for feed, want := range map[quake.Feed]rawv1.QuakeFeed{
		quake.FeedBMKGLatest: rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST, quake.FeedBMKGRecent: rawv1.QuakeFeed_QUAKE_FEED_BMKG_RECENT,
		quake.FeedBMKGFelt: rawv1.QuakeFeed_QUAKE_FEED_BMKG_FELT, quake.FeedUSGSSummary: rawv1.QuakeFeed_QUAKE_FEED_USGS_SUMMARY,
	} {
		if _, got, err := mapSource(feed); err != nil || got != want {
			t.Errorf("mapSource(%s) = %v, %v", feed, got, err)
		}
	}
	for ts, want := range map[quake.Tsunami]rawv1.TsunamiPotential{
		quake.TsunamiUnknown: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED, quake.TsunamiNone: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE,
		quake.TsunamiPotential: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_POTENTIAL, 9: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED,
	} {
		if got := mapTsunami(ts); got != want {
			t.Errorf("mapTsunami(%d) = %v", ts, got)
		}
	}
	bad := usgsReport()
	bad.Feed = "lain"
	if _, err := NewQuakeEvent(bad); err == nil {
		t.Fatal("feed tidak dikenal harus gagal")
	}
	bad = usgsReport()
	bad.Source = "Bukan Token"
	if _, err := NewQuakeEvent(bad); err == nil {
		t.Fatal("sumber yang bukan token subjek harus gagal")
	}
}

func TestBMKGWithoutUpdatedAt(t *testing.T) {
	r := usgsReport()
	r.Source, r.Feed, r.SourceUpdatedAt = quake.SourceBMKG, quake.FeedBMKGFelt, time.Time{}
	ev, err := NewQuakeEvent(r)
	if err != nil || ev.Report().GetSourceUpdatedAt() != nil || ev.Subject() != "raw.quake.bmkg" {
		t.Fatalf("%v, %v", ev.Report(), err)
	}
}
