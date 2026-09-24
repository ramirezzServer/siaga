package hazardpb

import (
	"fmt"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

var at = time.Date(2026, 9, 23, 12, 16, 56, 0, time.UTC)

func assessment(regions int) quake.Assessment {
	b := quake.Solution{
		Key: quake.IdentityKey{Source: quake.SourceBMKG, EventID: "20260923121656"}, OccurredAt: at,
		Latitude: -6.85, Longitude: 107.03, Magnitude: 5.6, DepthKm: 11, Felt: "V Cianjur",
		Tsunami: quake.TsunamiPotential, ShakemapURL: "https://data.bmkg.go.id/x.mmi.jpg", FirstSeenAt: at.Add(time.Minute),
	}
	u := quake.Solution{
		Key: quake.IdentityKey{Source: quake.SourceUSGS, EventID: "us7000ir9t"}, OccurredAt: at.Add(-3 * time.Second),
		Latitude: -6.836, Longitude: 106.997, Magnitude: 5.6, MagnitudeType: "mww", DepthKm: 10,
		SourceURL: "https://earthquake.usgs.gov/x", FirstSeenAt: at.Add(2 * time.Minute),
	}
	p := quake.DefaultPolicy()
	v, err := p.Derive(quake.NewEventID(b.Key, 0), []quake.Solution{b, u})
	if err != nil {
		panic(err)
	}
	a := quake.Assessment{View: v, Level: quake.LevelBahaya}
	for i := range regions {
		a.Impacts = append(a.Impacts, quake.Impact{
			RegionDistance: quake.RegionDistance{Code: fmt.Sprintf("32.03.01.%04d", 2001+i), Name: "Desa", DistanceKm: float64(i)},
			Level:          quake.LevelSiaga,
		})
	}
	return a
}

func TestCreatedCarriesFullHazard(t *testing.T) {
	a := assessment(150)
	msg, err := Encoder{}.Created(a, 1)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "hazard.quake.created" || msg.MsgID != a.ID.String()+":r1" {
		t.Fatalf("subjek/ID: %+v", msg)
	}
	var ev hazardv1.HazardCreated
	if err := proto.Unmarshal(msg.Payload, &ev); err != nil {
		t.Fatal(err)
	}
	h := ev.GetHazard()
	eq := h.GetEarthquake()
	if h.GetId() != a.ID.String() || h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_BAHAYA ||
		h.GetPrimarySource() != hazardv1.Source_SOURCE_BMKG || h.GetRevision() != 1 ||
		len(h.GetImpactedRegions()) != MaxImpactedRegions || h.GetImpactedRegionCount() != 150 ||
		h.GetImpactedRegions()[0].GetCode() != "32.03.01.2001" || !h.GetExpiresAt().AsTime().Equal(at.Add(6*time.Hour)) {
		t.Fatalf("hazard %v", h)
	}
	if !eq.GetTsunamiPotential() || eq.GetFeltRadiusKm() <= 0 || len(eq.GetCorroboratingReports()) != 1 ||
		eq.GetCorroboratingReports()[0].GetSource() != hazardv1.Source_SOURCE_USGS || eq.GetCorroboratingReports()[0].GetDepthKm() != 10 {
		t.Fatalf("detail gempa %v", eq)
	}
	again, _ := Encoder{}.Created(a, 1)
	if string(again.Payload) != string(msg.Payload) {
		t.Error("serialisasi harus deterministik")
	}
}

func TestUpdatedAndExpired(t *testing.T) {
	a := assessment(0)
	msg, err := Encoder{}.Updated(a, 3, quake.LevelSiaga)
	if err != nil {
		t.Fatal(err)
	}
	var up hazardv1.HazardUpdated
	if err := proto.Unmarshal(msg.Payload, &up); err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "hazard.quake.updated" || up.GetPreviousLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA || up.GetHazard().GetRevision() != 3 {
		t.Fatalf("updated %v", &up)
	}

	into := quake.NewEventID(quake.IdentityKey{Source: quake.SourceUSGS, EventID: "x"}, 0)
	msg, err = Encoder{}.Expired(a.ID, at, ports.ExpiryMerged, into, 4)
	if err != nil {
		t.Fatal(err)
	}
	var ex hazardv1.HazardExpired
	if err := proto.Unmarshal(msg.Payload, &ex); err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "hazard.quake.expired" || ex.GetReason() != hazardv1.ExpiryReason_EXPIRY_REASON_MERGED ||
		ex.GetMergedIntoHazardId() != into.String() || ex.GetRevision() != 4 || ex.GetKind() != hazardv1.HazardKind_HAZARD_KIND_EARTHQUAKE {
		t.Fatalf("expired %v", &ex)
	}
	msg, _ = Encoder{}.Expired(a.ID, at, ports.ExpiryElapsed, quake.EventID{}, 2)
	_ = proto.Unmarshal(msg.Payload, &ex)
	if ex.GetMergedIntoHazardId() != "" {
		t.Error("tanpa tujuan gabung, field harus kosong")
	}
}

func TestEnumMappings(t *testing.T) {
	for l, want := range map[quake.Level]hazardv1.AlertLevel{
		quake.LevelInfo: hazardv1.AlertLevel_ALERT_LEVEL_INFO, quake.LevelWaspada: hazardv1.AlertLevel_ALERT_LEVEL_WASPADA,
		quake.LevelSiaga: hazardv1.AlertLevel_ALERT_LEVEL_SIAGA, quake.LevelBahaya: hazardv1.AlertLevel_ALERT_LEVEL_BAHAYA,
		0: hazardv1.AlertLevel_ALERT_LEVEL_UNSPECIFIED,
	} {
		if Level(l) != want {
			t.Errorf("Level(%v)", l)
		}
	}
	if Source("x") != hazardv1.Source_SOURCE_UNSPECIFIED || Source(quake.SourceUSGS) != hazardv1.Source_SOURCE_USGS {
		t.Error("Source")
	}
	for r, want := range map[ports.ExpiryReason]hazardv1.ExpiryReason{
		ports.ExpiryElapsed: hazardv1.ExpiryReason_EXPIRY_REASON_ELAPSED, ports.ExpiryRetracted: hazardv1.ExpiryReason_EXPIRY_REASON_RETRACTED,
		0: hazardv1.ExpiryReason_EXPIRY_REASON_UNSPECIFIED,
	} {
		if expiryReason(r) != want {
			t.Errorf("expiryReason(%v)", r)
		}
	}
	if u32(-1) != 0 || u32(7) != 7 || u32(math.MaxInt) != math.MaxUint32 {
		t.Error("u32")
	}
}
