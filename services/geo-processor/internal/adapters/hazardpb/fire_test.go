package hazardpb

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func fireAssessment(t *testing.T) fire.Assessment {
	t.Helper()
	at := time.Date(2023, 8, 25, 6, 23, 0, 0, time.UTC)
	ds := []fire.Detection{
		{ID: "VIIRS_NOAA20_NRT:20230825T0623:-6.86000:108.42500", Product: "VIIRS_NOAA20_NRT", Lat: -6.86, Lon: 108.425, DetectedAt: at, Confidence: hotspot.Nominal, FRPMW: pf(12.5), FetchedAt: at.Add(3 * time.Hour)},
		{ID: "MODIS_NRT:20230825T0640:-6.86500:108.42500", Product: "MODIS_NRT", Lat: -6.865, Lon: 108.425, DetectedAt: at.Add(17 * time.Minute), Confidence: hotspot.High, FetchedAt: at.Add(4 * time.Hour)},
	}
	a, err := fire.DefaultPolicy().Assess(fire.EventIDFor(ds[0].ID), ds[0].ID, ds)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFireEncoder(t *testing.T) {
	a := fireAssessment(t)
	msg, err := FireEncoder{}.Created(a, 1)
	if err != nil || msg.Subject != "hazard.fire.created" || msg.MsgID != a.ID.String()+":r1" {
		t.Fatalf("%+v %v", msg, err)
	}
	var created hazardv1.HazardCreated
	if err := proto.Unmarshal(msg.Payload, &created); err != nil {
		t.Fatal(err)
	}
	h := created.GetHazard()
	d := h.GetFire()
	if h.GetKind() != hazardv1.HazardKind_HAZARD_KIND_WILDFIRE || h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_WASPADA ||
		h.GetPrimarySource() != hazardv1.Source_SOURCE_NASA_FIRMS || h.GetSourceEventId() != a.OpenedBy ||
		h.GetLocation().GetLatitude() != -6.8625 || !h.GetOccurredAt().AsTime().Equal(a.First) || !h.GetDetectedAt().AsTime().Equal(a.FirstFetchedAt) ||
		!h.GetExpiresAt().AsTime().Equal(a.LastFetchedAt.Add(24*time.Hour)) || h.GetTitle() != "Titik api: 2 deteksi satelit" || h.GetRevision() != 1 {
		t.Fatalf("%v", h)
	}
	if d.GetDetectionCount() != 2 || d.GetOverpassCount() != 2 || d.GetHighConfidenceCount() != 1 || d.GetPeakDetections_24H() != 2 ||
		d.GetPeakOverpasses_24H() != 2 || d.GetMaxFrpMw() != 12.5 || len(d.GetProducts()) != 2 || d.GetSiagaDetections() != 5 ||
		d.GetWaspadaOverpasses() != 2 || d.GetOpenedBy() != a.OpenedBy || !d.GetLevelReachedAt().AsTime().Equal(a.Last) {
		t.Fatalf("%v", d)
	}
	det := d.GetDetections()
	if len(det) != 2 || det[0].GetConfidence() != "nominal" || det[1].FrpMw != nil || det[1].GetProduct() != "MODIS_NRT" || det[0].GetLocation().GetLongitude() != 108.425 {
		t.Fatalf("%v", det)
	}
	msg, err = FireEncoder{}.Updated(a, 2, hazard.LevelInfo)
	var updated hazardv1.HazardUpdated
	if err != nil || proto.Unmarshal(msg.Payload, &updated) != nil || msg.Subject != "hazard.fire.updated" ||
		updated.GetPreviousLevel() != hazardv1.AlertLevel_ALERT_LEVEL_INFO || updated.GetHazard().GetRevision() != 2 {
		t.Fatalf("%v %v", &updated, err)
	}
	into := fire.EventIDFor("lain")
	msg, err = FireEncoder{}.Expired(a.ID, a.ExpiresAt, ports.ExpiryMerged, into, 3)
	var expiredMsg hazardv1.HazardExpired
	if err != nil || proto.Unmarshal(msg.Payload, &expiredMsg) != nil || msg.Subject != "hazard.fire.expired" ||
		expiredMsg.GetKind() != hazardv1.HazardKind_HAZARD_KIND_WILDFIRE || expiredMsg.GetReason() != hazardv1.ExpiryReason_EXPIRY_REASON_MERGED ||
		expiredMsg.GetMergedIntoHazardId() != into.String() || expiredMsg.GetRevision() != 3 {
		t.Fatalf("%v %v", &expiredMsg, err)
	}
}
