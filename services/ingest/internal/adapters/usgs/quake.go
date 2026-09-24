// Package usgs membaca feed ringkasan gempa USGS (GeoJSON) sebagai sumber
// cadangan dan pembanding BMKG. Hanya gempa di kotak Indonesia yang diteruskan.
package usgs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultSummaryURL adalah feed M2.5+ 24 jam terakhir. Feed harian dipilih
// (bukan per jam) supaya ingest yang mati beberapa jam tidak kehilangan gempa.
const DefaultSummaryURL = "https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/2.5_day.geojson"

// ErrStructure menandai payload yang bukan FeatureCollection GeoJSON.
var ErrStructure = errors.New("struktur payload USGS berubah")

// Feed harian biasanya < 200 KB; batas ini menampung hari dengan banyak gempa.
const maxPayload = 8 << 20

// SummaryConnector adalah feed ringkasan USGS sebagai ports.Connector.
type SummaryConnector struct {
	url string
}

// NewSummaryConnector membuat konektor untuk URL feed ringkasan.
func NewSummaryConnector(url string) *SummaryConnector { return &SummaryConnector{url: url} }

// Name mengembalikan nama konektor.
func (c *SummaryConnector) Name() string { return "usgs-2.5-day" }

// ArchiveExt mengembalikan "geojson".
func (c *SummaryConnector) ArchiveExt() string { return "geojson" }

// Request mengembalikan permintaan ke feed.
func (c *SummaryConnector) Request() ports.Request {
	return ports.Request{URL: c.url, Accept: "application/geo+json, application/json", MaxBytes: maxPayload}
}

type feature struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Properties struct {
		Mag     *float64 `json:"mag"`
		Place   string   `json:"place"`
		Time    *int64   `json:"time"`
		Updated *int64   `json:"updated"`
		URL     string   `json:"url"`
		Status  string   `json:"status"`
		Type    string   `json:"type"`
		MagType string   `json:"magType"`
		IDs     string   `json:"ids"`
	} `json:"properties"`
	Geometry *struct {
		Type        string    `json:"type"`
		Coordinates []float64 `json:"coordinates"`
	} `json:"geometry"`
}

// Parse membaca FeatureCollection. Fitur selain gempa (misal ledakan tambang)
// dan gempa di luar kotak Indonesia dilewati tanpa dianggap penolakan.
func (c *SummaryConnector) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	var doc struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrStructure, err)
	}
	if doc.Type != "FeatureCollection" {
		return nil, nil, fmt.Errorf("%w: type %q bukan FeatureCollection", ErrStructure, doc.Type)
	}

	var events []ports.Event
	var rejected []ports.Rejection
	seen := map[string]bool{}
	for i, raw := range doc.Features {
		var f feature
		if err := json.Unmarshal(raw, &f); err != nil {
			rejected = append(rejected, ports.Rejection{Key: fmt.Sprintf("#%d", i), Reason: err})
			continue
		}
		key := fmt.Sprintf("#%d %s", i, f.ID)
		rep, keep, err := f.toReport()
		if err == nil && !keep {
			continue
		}
		if err == nil {
			err = rep.Validate(fetchedAt)
		}
		if err == nil && seen[rep.EventID] {
			err = fmt.Errorf("ID %s muncul dua kali dalam satu payload", rep.EventID)
		}
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		seen[rep.EventID] = true
		ev, err := rawpb.NewQuakeEvent(rep)
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		events = append(events, ev)
	}
	return events, rejected, nil
}

// toReport mengembalikan keep=false untuk fitur yang sengaja dilewati.
func (f feature) toReport() (quake.Report, bool, error) {
	if f.Properties.Type != "earthquake" {
		return quake.Report{}, false, nil
	}
	if f.Geometry == nil || f.Geometry.Type != "Point" || len(f.Geometry.Coordinates) < 3 {
		return quake.Report{}, false, errors.New("geometri bukan Point [lon, lat, kedalaman]")
	}
	lon, lat, depth := f.Geometry.Coordinates[0], f.Geometry.Coordinates[1], f.Geometry.Coordinates[2]
	if !quake.InIndonesiaBox(lat, lon) {
		return quake.Report{}, false, nil
	}
	if f.Properties.Mag == nil {
		return quake.Report{}, false, errors.New("magnitudo kosong")
	}
	if f.Properties.Time == nil {
		return quake.Report{}, false, errors.New("waktu kosong")
	}
	rep := quake.Report{
		Source:        quake.SourceUSGS,
		Feed:          quake.FeedUSGSSummary,
		EventID:       f.ID,
		OccurredAt:    fromMillis(*f.Properties.Time),
		Latitude:      lat,
		Longitude:     lon,
		Magnitude:     round(*f.Properties.Mag, 2),
		MagnitudeType: f.Properties.MagType,
		DepthKm:       depth,
		Place:         strings.TrimSpace(f.Properties.Place),
		ReviewStatus:  f.Properties.Status,
		SourceURL:     f.Properties.URL,
		// Flag "tsunami" USGS hanya menandai gempa besar di laut, bukan peringatan;
		// PRD memakai pernyataan BMKG untuk tsunami.
		Tsunami: quake.TsunamiUnknown,
	}
	if f.Properties.Updated != nil {
		rep.SourceUpdatedAt = fromMillis(*f.Properties.Updated)
	}
	for id := range strings.SplitSeq(f.Properties.IDs, ",") {
		if id = strings.TrimSpace(id); id != "" && id != f.ID {
			rep.AlternateIDs = append(rep.AlternateIDs, id)
		}
	}
	return rep, true, nil
}

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}
