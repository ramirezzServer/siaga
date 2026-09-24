package usgs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
)

var fetchedAt = time.Date(2026, 9, 23, 13, 50, 0, 0, time.UTC)

func TestSummaryFixture(t *testing.T) {
	c := NewSummaryConnector(DefaultSummaryURL)
	if c.Name() != "usgs-2.5-day" || c.ArchiveExt() != "geojson" || c.Request().URL != DefaultSummaryURL {
		t.Fatal("identitas konektor")
	}
	body, err := os.ReadFile(filepath.Join("testdata", "2.5_day.geojson"))
	if err != nil {
		t.Fatal(err)
	}
	evs, rej, err := c.Parse(body, fetchedAt)
	if err != nil || len(rej) != 0 || len(evs) != 1 {
		t.Fatalf("hanya gempa Ternate yang masuk: %d event, %v, %v", len(evs), rej, err)
	}
	r := evs[0].(*rawpb.QuakeEvent).Report()
	if evs[0].Subject() != "raw.quake.usgs" || r.GetSourceEventId() != "us6000tx31" || r.GetMagnitude() != 4.6 ||
		r.GetMagnitudeType() != "mb" || r.GetDepthKm() != 48.462 || r.GetEpicenter().GetLatitude() != 1.217 ||
		r.GetReviewStatus() != "reviewed" || len(r.GetAlternateIds()) != 0 ||
		r.GetFeed() != rawv1.QuakeFeed_QUAKE_FEED_USGS_SUMMARY ||
		r.GetSourceUrl() != "https://earthquake.usgs.gov/earthquakes/eventpage/us6000tx31" {
		t.Fatalf("laporan: %v", r)
	}
	if !r.GetOccurredAt().AsTime().Equal(time.Date(2026, 9, 23, 12, 23, 20, 274e6, time.UTC)) ||
		!r.GetSourceUpdatedAt().AsTime().Equal(time.UnixMilli(1790169301040)) {
		t.Fatalf("waktu: %v %v", r.GetOccurredAt().AsTime(), r.GetSourceUpdatedAt().AsTime())
	}
}

func TestFeatureHandling(t *testing.T) {
	feat := func(props, geom string) string {
		return `{"type":"Feature","id":"us1","properties":{` + props + `},"geometry":` + geom + `}`
	}
	pt := `{"type":"Point","coordinates":[107.6,-6.9,10]}`
	body := `{"type":"FeatureCollection","features":[` + strings.Join([]string{
		feat(`"type":"earthquake","mag":5.12345,"time":1790166200274,"ids":",us1,at9,,","status":"automatic"`, pt),  // masuk
		feat(`"type":"quarry blast","mag":2.6,"time":1790166200274`, pt),                                            // dilewati
		feat(`"type":"earthquake","mag":3,"time":1790166200274`, `{"type":"Point","coordinates":[-155.5,57.2,51]}`), // dilewati
		feat(`"type":"earthquake","mag":null,"time":1790166200274`, pt),                                             // tolak
		feat(`"type":"earthquake","mag":3`, pt),                                                                     // tolak
		feat(`"type":"earthquake","mag":3,"time":1790166200274`, `null`),                                            // tolak
		feat(`"type":"earthquake","mag":3,"time":1790166200274`, `{"type":"Point","coordinates":[107.6,-6.9]}`),     // tolak
		feat(`"type":"earthquake","mag":11,"time":1790166200274`, pt),                                               // tolak validasi
		feat(`"type":"earthquake","mag":5,"time":1790166200274`, pt),                                                // tolak ID ganda
		`{"type":"Feature","properties":"rusak"}`,                                                                   // tolak
	}, ",") + `]}`
	evs, rej, err := NewSummaryConnector("x").Parse([]byte(body), fetchedAt)
	if err != nil || len(evs) != 1 || len(rej) != 7 {
		t.Fatalf("%d event, %d tolak, %v: %v", len(evs), len(rej), err, rej)
	}
	r := evs[0].(*rawpb.QuakeEvent).Report()
	if r.GetMagnitude() != 5.12 || len(r.GetAlternateIds()) != 1 || r.GetAlternateIds()[0] != "at9" || r.GetSourceUpdatedAt() != nil {
		t.Fatalf("laporan: %v", r)
	}
}

func TestStructureErrors(t *testing.T) {
	for _, body := range []string{``, `[]`, `{"type":"Feature"}`, `{"type":"FeatureCollection","features":{}}`} {
		if _, _, err := NewSummaryConnector("x").Parse([]byte(body), fetchedAt); !errors.Is(err, ErrStructure) {
			t.Errorf("Parse(%q) = %v", body, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	body, err := os.ReadFile(filepath.Join("testdata", "2.5_day.geojson"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	c := NewSummaryConnector(DefaultSummaryURL)
	f.Fuzz(func(t *testing.T, body []byte) {
		evs, _, err := c.Parse(body, fetchedAt)
		if err != nil && len(evs) > 0 {
			t.Fatal("galat struktur tidak boleh disertai event")
		}
		for _, e := range evs {
			if e.Key() == "" {
				t.Fatal("kunci kosong")
			}
			if _, err := e.Content(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
