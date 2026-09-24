package rawpb

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func sampleWarning() warning.Warning {
	sent := time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)
	return warning.Warning{
		Identifier: "2.49.0.1.360.0.2026.09.24.07.32.002", Sender: "cuaca.ekstrem@bmkg.go.id", Sent: sent,
		Status: warning.StatusActual, MsgType: warning.MsgUpdate, Category: "Met", EventCode: "OET-194",
		References: []warning.Reference{{Sender: "cuaca.ekstrem@bmkg.go.id", Identifier: "2.49.0.1.360.0.2026.09.24.06.32.001", Sent: sent.Add(-time.Hour)}},
		Urgency:    warning.UrgencyImmediate, Severity: warning.SeverityExtreme, Certainty: warning.CertaintyObserved,
		Effective: sent, Expires: sent.Add(2 * time.Hour),
		Texts: []warning.Text{{Language: "en", Headline: "Storm"}, {Language: "id", Headline: "Badai", Description: "a\nb", Instruction: "c", SenderName: "BMKG", Event: "Hujan"}},
		Web:   "https://nowcasting.bmkg.go.id/x.jpg", Contact: "196",
		Areas:     []warning.Area{{Desc: "Jawa Barat", Polygons: []warning.Ring{{{Lat: -6.9, Lon: 107.6}, {Lat: -6.9, Lon: 107.7}, {Lat: -6.8, Lon: 107.7}, {Lat: -6.9, Lon: 107.6}}}}},
		SourceURL: "https://www.bmkg.go.id/alerts/nowcast/id/CJB20260924002_alert.xml",
	}
}

func TestWeatherEvent(t *testing.T) {
	w := sampleWarning()
	ev, err := NewWeatherEvent(w)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Subject() != "raw.weather.bmkg" || ev.Key() != w.Identifier {
		t.Fatal(ev.Subject(), ev.Key())
	}
	c1, _ := ev.Content()
	meta := ports.FetchMeta{Connector: "bmkg-cap", FetchedAt: w.Sent.Add(time.Minute), ArchiveKey: "k", PayloadSHA256: "ab"}
	full, err := ev.Encode(meta)
	if err != nil {
		t.Fatal(err)
	}
	var got rawv1.WeatherWarning
	if err := proto.Unmarshal(full, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetMeta().GetConnector() != "bmkg-cap" || got.GetMeta().GetArchiveKey() != "k" || got.GetOnset() != nil ||
		got.GetSeverity() != rawv1.CapSeverity_CAP_SEVERITY_EXTREME || got.GetMsgType() != rawv1.CapMsgType_CAP_MSG_TYPE_UPDATE ||
		got.GetStatus() != rawv1.CapStatus_CAP_STATUS_ACTUAL || got.GetUrgency() != rawv1.CapUrgency_CAP_URGENCY_IMMEDIATE ||
		got.GetCertainty() != rawv1.CapCertainty_CAP_CERTAINTY_OBSERVED || len(got.GetTexts()) != 2 ||
		got.GetTexts()[1].GetDescription() != "a\nb" || got.GetReferences()[0].GetIdentifier() != w.References[0].Identifier ||
		got.GetAreas()[0].GetPolygons()[0].GetPoints()[1].GetLongitude() != 107.7 {
		t.Fatalf("%v", &got)
	}
	// Isi tidak memuat meta: pengambilan ulang menghasilkan ID pesan yang sama.
	c2, _ := ev.Content()
	if !bytes.Equal(c1, c2) || ev.Warning().GetMeta() != nil {
		t.Fatal("isi tidak stabil atau memuat meta")
	}
	w.Onset = w.Sent.Add(10 * time.Minute)
	ev2, _ := NewWeatherEvent(w)
	if ev2.Warning().GetOnset() == nil {
		t.Fatal("onset hilang")
	}
}

func TestCAPEnumMappings(t *testing.T) {
	for in, want := range map[warning.Status]rawv1.CapStatus{
		warning.StatusActual: rawv1.CapStatus_CAP_STATUS_ACTUAL, warning.StatusExercise: rawv1.CapStatus_CAP_STATUS_EXERCISE,
		warning.StatusSystem: rawv1.CapStatus_CAP_STATUS_SYSTEM, warning.StatusTest: rawv1.CapStatus_CAP_STATUS_TEST,
		warning.StatusDraft: rawv1.CapStatus_CAP_STATUS_DRAFT, warning.StatusUnknown: rawv1.CapStatus_CAP_STATUS_UNSPECIFIED, 99: 0,
	} {
		if capStatus(in) != want {
			t.Errorf("status %v", in)
		}
	}
	for in, want := range map[warning.MsgType]rawv1.CapMsgType{
		warning.MsgAlert: rawv1.CapMsgType_CAP_MSG_TYPE_ALERT, warning.MsgUpdate: rawv1.CapMsgType_CAP_MSG_TYPE_UPDATE,
		warning.MsgCancel: rawv1.CapMsgType_CAP_MSG_TYPE_CANCEL, warning.MsgAck: rawv1.CapMsgType_CAP_MSG_TYPE_ACK,
		warning.MsgError: rawv1.CapMsgType_CAP_MSG_TYPE_ERROR, warning.MsgUnknown: 0, 99: 0,
	} {
		if capMsgType(in) != want {
			t.Errorf("msgType %v", in)
		}
	}
	for in, want := range map[warning.Severity]rawv1.CapSeverity{
		warning.SeverityExtreme: rawv1.CapSeverity_CAP_SEVERITY_EXTREME, warning.SeveritySevere: rawv1.CapSeverity_CAP_SEVERITY_SEVERE,
		warning.SeverityModerate: rawv1.CapSeverity_CAP_SEVERITY_MODERATE, warning.SeverityMinor: rawv1.CapSeverity_CAP_SEVERITY_MINOR,
		warning.SeverityUnknown: rawv1.CapSeverity_CAP_SEVERITY_UNKNOWN, warning.SeverityUnspecified: 0, 99: 0,
	} {
		if capSeverity(in) != want {
			t.Errorf("severity %v", in)
		}
	}
	for in, want := range map[warning.Urgency]rawv1.CapUrgency{
		warning.UrgencyImmediate: rawv1.CapUrgency_CAP_URGENCY_IMMEDIATE, warning.UrgencyExpected: rawv1.CapUrgency_CAP_URGENCY_EXPECTED,
		warning.UrgencyFuture: rawv1.CapUrgency_CAP_URGENCY_FUTURE, warning.UrgencyPast: rawv1.CapUrgency_CAP_URGENCY_PAST,
		warning.UrgencyUnknown: rawv1.CapUrgency_CAP_URGENCY_UNKNOWN, warning.UrgencyUnspecified: 0, 99: 0,
	} {
		if capUrgency(in) != want {
			t.Errorf("urgency %v", in)
		}
	}
	for in, want := range map[warning.Certainty]rawv1.CapCertainty{
		warning.CertaintyObserved: rawv1.CapCertainty_CAP_CERTAINTY_OBSERVED, warning.CertaintyLikely: rawv1.CapCertainty_CAP_CERTAINTY_LIKELY,
		warning.CertaintyPossible: rawv1.CapCertainty_CAP_CERTAINTY_POSSIBLE, warning.CertaintyUnlikely: rawv1.CapCertainty_CAP_CERTAINTY_UNLIKELY,
		warning.CertaintyUnknown: rawv1.CapCertainty_CAP_CERTAINTY_UNKNOWN, warning.CertaintyUnspecified: 0, 99: 0,
	} {
		if capCertainty(in) != want {
			t.Errorf("certainty %v", in)
		}
	}
}

func TestForecastEvent(t *testing.T) {
	vis := 8000.0
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	f := forecast.Forecast{
		RegionCode: "32.73.01.1001", Province: "Jawa Barat", Regency: "Kota Bandung", District: "Sukasari", Village: "Sukarasa",
		Latitude: -6.87, Longitude: 107.58, AnalysisTime: at,
		Steps: []forecast.Step{
			{
				ValidTime: at.Add(7 * time.Hour), TemperatureC: 28, HumidityPct: 52, CloudCoverPct: 64, PrecipitationMM: 0.4, WeatherCode: 61,
				WeatherDesc: "Hujan Ringan", WeatherDescEN: "Light Rain", WindSpeedKmh: 1.4, WindFromDeg: 336, WindFrom: "NW", VisibilityM: &vis, VisibilityText: "< 8 km",
			},
			{ValidTime: at.Add(10 * time.Hour), WeatherCode: 5000},
		},
	}
	ev, err := NewForecastEvent(f)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Subject() != "raw.forecast.bmkg" || ev.Key() != f.RegionCode {
		t.Fatal(ev.Subject(), ev.Key())
	}
	full, err := ev.Encode(ports.FetchMeta{Connector: "bmkg-prakiraan", FetchedAt: at.Add(7 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var got rawv1.RegionForecast
	if err := proto.Unmarshal(full, &got); err != nil {
		t.Fatal(err)
	}
	s0 := got.GetSteps()[0]
	if got.GetMeta().GetConnector() != "bmkg-prakiraan" || got.GetVillage() != "Sukarasa" || got.GetLocation().GetLatitude() != -6.87 ||
		s0.GetWeatherCode() != 61 || s0.GetVisibilityM() != 8000 || s0.GetPrecipitationMm() != 0.4 || s0.GetRelativeHumidityPct() != 52 ||
		s0.GetWindFrom() != "NW" || got.GetSteps()[1].VisibilityM != nil || got.GetSteps()[1].GetWeatherCode() != 999 {
		t.Fatalf("%v", &got)
	}
	if c, err := ev.Content(); err != nil || len(c) == 0 || ev.Forecast().GetMeta() != nil {
		t.Fatal("isi kosong atau memuat meta")
	}
}
