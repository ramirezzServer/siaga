package hazardpb

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func pf(v float64) *float64 { return &v }

func floodAssessment(t *testing.T) flood.Assessment {
	t.Helper()
	p := flood.DefaultPolicy()
	th, err := p.Discharge(flood.DischargeRow{
		SiteID: "river:citarum-nanjung", River: "Citarum", Name: "Nanjung", Cell: series.Point{Lat: -6.975, Lon: 107.525},
		P50: 95.83, Climatology: flood.Set{215.77, 271.90, 378.73, 479.70}, SeamlessRatio: 0.87,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC)
	today := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	run := series.DischargeRun{Run: series.Run{
		Site: series.Site{ID: th.SiteID}, Model: "glofas_v4", Cell: th.Cell, FetchedAt: at, IssuedAt: at,
	}, Steps: []series.DischargeStep{
		{ValidDate: today, Median: pf(240), P75: pf(340), Max: pf(600)},
		{ValidDate: today.AddDate(0, 0, 1), Discharge: pf(100)},
	}}
	o, err := p.AssessDischarge(run, th)
	if err != nil {
		t.Fatal(err)
	}
	return p.Assess(flood.EventIDFor(th.SiteID, at), o, at, at)
}

func TestFloodEncoder(t *testing.T) {
	a := floodAssessment(t)
	msg, err := FloodEncoder{}.Created(a, 1)
	if err != nil || msg.Subject != "hazard.flood.created" || msg.MsgID != a.ID.String()+":r1" {
		t.Fatalf("%+v %v", msg, err)
	}
	var created hazardv1.HazardCreated
	if err := proto.Unmarshal(msg.Payload, &created); err != nil {
		t.Fatal(err)
	}
	h := created.GetHazard()
	d := h.GetFlood()
	if h.GetKind() != hazardv1.HazardKind_HAZARD_KIND_FLOOD || h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA ||
		h.GetPrimarySource() != hazardv1.Source_SOURCE_OPEN_METEO || h.GetSourceEventId() != "river:citarum-nanjung" ||
		h.GetLocation().GetLatitude() != -6.975 || h.GetAreaGeojson() != "" || !h.GetExpiresAt().AsTime().Equal(a.ExpiresAt) ||
		h.GetTitle() != "Potensi banjir Citarum di Nanjung" || h.GetRevision() != 1 {
		t.Fatalf("%v", h)
	}
	if d.GetIndicator() != hazardv1.FloodIndicator_FLOOD_INDICATOR_DISCHARGE || !d.GetPossible() || d.GetRiver() != "Citarum" ||
		len(d.GetDischargeThresholdsM3S()) != 4 || len(d.GetRainfallThresholds()) != 0 || d.GetMaxLevel() != hazardv1.AlertLevel_ALERT_LEVEL_BAHAYA ||
		len(d.GetDays()) != 4 || !d.GetPeakDate().AsTime().Equal(a.Peak) || d.GetModel() != "glofas_v4" {
		t.Fatalf("%v", d)
	}
	day0, day1 := d.GetDays()[0], d.GetDays()[1]
	if day0.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA || !day0.GetPossible() || day0.GetDischargeMaxM3S() != 600 ||
		!day0.GetFromEnsemble() || day0.Rain_3HMm != nil || day1.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_UNSPECIFIED || day1.GetFromEnsemble() {
		t.Fatalf("%v %v", day0, day1)
	}

	msg, err = FloodEncoder{}.Updated(a, 2, hazard.LevelWaspada)
	var updated hazardv1.HazardUpdated
	if err != nil || proto.Unmarshal(msg.Payload, &updated) != nil || msg.Subject != "hazard.flood.updated" ||
		updated.GetPreviousLevel() != hazardv1.AlertLevel_ALERT_LEVEL_WASPADA || updated.GetHazard().GetRevision() != 2 {
		t.Fatalf("%v %v", &updated, err)
	}
	msg, err = FloodEncoder{}.Expired(a.ID, a.ExpiresAt, ports.ExpiryElapsed, hazard.EventID{}, 3)
	var expiredMsg hazardv1.HazardExpired
	if err != nil || proto.Unmarshal(msg.Payload, &expiredMsg) != nil || msg.Subject != "hazard.flood.expired" ||
		expiredMsg.GetKind() != hazardv1.HazardKind_HAZARD_KIND_FLOOD || expiredMsg.GetReason() != hazardv1.ExpiryReason_EXPIRY_REASON_ELAPSED {
		t.Fatalf("%v %v", &expiredMsg, err)
	}
}

func TestFloodHazardRainfallAndBelow(t *testing.T) {
	p := flood.DefaultPolicy()
	var rows []flood.RainfallRow
	for _, h := range flood.Windows {
		rows = append(rows, flood.RainfallRow{SiteID: "catchment:cikapundung", Name: "Cikapundung", Hours: h, P50: 1, Levels: flood.Set{5, 10, 20, 30}})
	}
	rain, err := p.Rainfall(rows)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC)
	run := series.WeatherRun{Run: series.Run{Site: series.Site{ID: "catchment:cikapundung"}, Model: "ecmwf_ifs", FetchedAt: at}}
	o, err := p.AssessRainfall(run, rain["catchment:cikapundung"])
	if err != nil {
		t.Fatal(err)
	}
	h := FloodHazard(p.Assess(flood.EventIDFor(o.SiteID, at), o, at, at), 1)
	d := h.GetFlood()
	if d.GetIndicator() != hazardv1.FloodIndicator_FLOOD_INDICATOR_RAINFALL_INDEX || d.GetPeakDate() != nil || len(d.GetRainfallThresholds()) != 3 ||
		d.GetRainfallThresholds()[2].GetWindowHours() != 24 || len(d.GetDischargeThresholdsM3S()) != 0 || d.GetMaxLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA ||
		h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_INFO {
		t.Fatalf("%v", h)
	}
	if floodIndicator("lain") != hazardv1.FloodIndicator_FLOOD_INDICATOR_UNSPECIFIED {
		t.Fatal("indikator tidak dikenal")
	}
}
