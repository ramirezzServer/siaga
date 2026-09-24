package hazardpb

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func weatherAssessment(t *testing.T, impacts int) weather.Assessment {
	t.Helper()
	sent := time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)
	m := weather.Message{
		Key: weather.Key{Source: weather.SourceBMKG, Identifier: "2.49.0.1.360.0.2026.09.24.07.32.001"}, Sender: "bmkg",
		Sent: sent, MsgType: weather.MsgAlert, Severity: weather.SeveritySevere, Urgency: "Immediate", Certainty: "Likely",
		EventCode: "OET-194", Effective: sent, Expires: sent.Add(2 * time.Hour),
		Texts: []weather.Text{
			{Language: "en", Event: "Thunderstorm", Headline: "Storm", Description: "d-en", Instruction: "i-en"},
			{Language: "id", Event: "Hujan Lebat", Headline: "Hujan Lebat di Jawa Barat", Description: "d", Instruction: "i"},
		},
		AreaDesc: "Jawa Barat", Web: "https://nowcasting.bmkg.go.id/x.jpg", SourceURL: "https://www.bmkg.go.id/alerts/nowcast/id/CJB.xml",
		HasArea: true, FetchedAt: sent, FirstSeenAt: sent,
	}
	v, err := weather.Derive([]weather.Message{m})
	if err != nil {
		t.Fatal(err)
	}
	fp := weather.Footprint{AreaGeoJSON: `{"type":"MultiPolygon","coordinates":[]}`, Latitude: -6.9, Longitude: 107.6, AreaKm2: 42.5}
	for i := range impacts {
		fp.Regions = append(fp.Regions, weather.RegionCoverage{Code: fmt.Sprintf("32.73.01.%04d", 1000+i), Name: "x", Coverage: 1 - float64(i)/1000})
	}
	return weather.DefaultPolicy().Assess(v, fp)
}

func TestWeatherEncoder(t *testing.T) {
	a := weatherAssessment(t, 150)
	enc := WeatherEncoder{}
	msg, err := enc.Created(a, 1)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "hazard.weather.created" || msg.MsgID != a.ID.String()+":r1" {
		t.Fatalf("%+v", msg)
	}
	var created hazardv1.HazardCreated
	if err := proto.Unmarshal(msg.Payload, &created); err != nil {
		t.Fatal(err)
	}
	h := created.GetHazard()
	w := h.GetWeather()
	if h.GetKind() != hazardv1.HazardKind_HAZARD_KIND_WEATHER || h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA ||
		h.GetSourceEventId() != a.Root.Identifier || h.GetAreaGeojson() == "" || h.GetLocation().GetLatitude() != -6.9 ||
		len(h.GetImpactedRegions()) != MaxImpactedRegions || h.GetImpactedRegionCount() != 150 || h.GetRevision() != 1 {
		t.Fatalf("%v", h)
	}
	if w.GetCapSeverity() != "Severe" || w.GetCapMsgType() != "Alert" || w.GetHeadline() != "Hujan Lebat di Jawa Barat" ||
		w.GetHeadlineEn() != "Storm" || w.GetDescriptionEn() != "d-en" || w.GetInstructionEn() != "i-en" || w.GetCapEventEn() != "Thunderstorm" ||
		w.GetCapUrgency() != "Immediate" || w.GetCapCertainty() != "Likely" || w.GetCapEventCode() != "OET-194" ||
		w.GetAreaKm2() != 42.5 || w.GetMessageCount() != 1 || w.GetWebUrl() == "" || w.GetSourceUrl() == "" || w.GetCapIdentifier() != a.Current.Identifier {
		t.Fatalf("%v", w)
	}
	up, err := enc.Updated(a, 2, hazard.LevelWaspada)
	if err != nil {
		t.Fatal(err)
	}
	var updated hazardv1.HazardUpdated
	if err := proto.Unmarshal(up.Payload, &updated); err != nil || updated.GetPreviousLevel() != hazardv1.AlertLevel_ALERT_LEVEL_WASPADA ||
		up.Subject != "hazard.weather.updated" {
		t.Fatalf("%+v %v", up, err)
	}
	into := weather.EventIDFor(weather.Key{Source: weather.SourceBMKG, Identifier: "lain"})
	ex, err := enc.Expired(a.ID, a.ExpiresAt, ports.ExpiryMerged, into, 3)
	if err != nil {
		t.Fatal(err)
	}
	var expired hazardv1.HazardExpired
	if err := proto.Unmarshal(ex.Payload, &expired); err != nil || ex.Subject != "hazard.weather.expired" ||
		expired.GetKind() != hazardv1.HazardKind_HAZARD_KIND_WEATHER || expired.GetReason() != hazardv1.ExpiryReason_EXPIRY_REASON_MERGED ||
		expired.GetMergedIntoHazardId() != into.String() || expired.GetRevision() != 3 {
		t.Fatalf("%v %v", &expired, err)
	}
	for mt, want := range map[weather.MsgType]string{weather.MsgUpdate: "Update", weather.MsgCancel: "Cancel", "lain": "lain"} {
		if capMsgType(mt) != want {
			t.Errorf("%s", mt)
		}
	}
}
