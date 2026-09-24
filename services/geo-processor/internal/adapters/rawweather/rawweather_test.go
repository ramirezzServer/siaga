package rawweather

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

var sent = time.Date(2026, 9, 24, 7, 0, 0, 123_456_789, time.UTC)

func ring(lat, lon float64) *rawv1.Ring {
	pts := [][2]float64{{lat, lon}, {lat, lon + 0.1}, {lat + 0.1, lon + 0.1}, {lat, lon}}
	r := &rawv1.Ring{}
	for _, p := range pts {
		r.Points = append(r.Points, &commonv1.Point{Latitude: p[0], Longitude: p[1]})
	}
	return r
}

func sample() *rawv1.WeatherWarning {
	return &rawv1.WeatherWarning{
		Meta:       &rawv1.FetchMeta{Connector: "bmkg-cap", FetchedAt: timestamppb.New(sent.Add(time.Minute)), ArchiveKey: "bmkg-cap/k.xml.gz"},
		Source:     hazardv1.Source_SOURCE_BMKG,
		Identifier: "2.49.0.1.360.0.2026.09.24.07.32.002",
		Sender:     "cuaca.ekstrem@bmkg.go.id",
		Sent:       timestamppb.New(sent),
		Status:     rawv1.CapStatus_CAP_STATUS_ACTUAL,
		MsgType:    rawv1.CapMsgType_CAP_MSG_TYPE_UPDATE,
		References: []*rawv1.CapReference{
			{Sender: "cuaca.ekstrem@bmkg.go.id", Identifier: "2.49.0.1.360.0.2026.09.24.06.32.001", Sent: timestamppb.New(sent.Add(-time.Hour))},
			{Sender: "cuaca.ekstrem@bmkg.go.id", Identifier: "2.49.0.1.360.0.2026.09.24.05.32.001", Sent: timestamppb.New(sent.Add(-2 * time.Hour))},
		},
		Category: "Met", EventCode: "OET-194",
		Urgency: rawv1.CapUrgency_CAP_URGENCY_IMMEDIATE, Severity: rawv1.CapSeverity_CAP_SEVERITY_SEVERE,
		Certainty: rawv1.CapCertainty_CAP_CERTAINTY_OBSERVED,
		Effective: timestamppb.New(sent), Expires: timestamppb.New(sent.Add(2 * time.Hour)),
		Texts: []*rawv1.CapText{
			{Language: "en", Headline: "Thunderstorm"},
			{Language: "id", Event: "Hujan Lebat dan Petir", Headline: "Hujan Lebat di Jawa Barat", Description: "a\nb"},
		},
		Web: "https://nowcasting.bmkg.go.id/x.jpg", Contact: "196",
		Areas: []*rawv1.CapArea{
			{AreaDesc: "Jawa Barat", Polygons: []*rawv1.Ring{ring(-6.9, 107.6)}},
			{AreaDesc: "Jawa Barat", Polygons: []*rawv1.Ring{ring(-7.0, 107.5)}},
			{AreaDesc: "Banten", Polygons: []*rawv1.Ring{ring(-6.5, 106.2)}},
		},
		SourceUrl: "https://www.bmkg.go.id/alerts/nowcast/id/CJB20260924002_alert.xml",
	}
}

func TestDecode(t *testing.T) {
	b, err := proto.Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	want := sent.Truncate(time.Microsecond)
	if m.Key != (weather.Key{Source: weather.SourceBMKG, Identifier: "2.49.0.1.360.0.2026.09.24.07.32.002"}) ||
		!m.Sent.Equal(want) || m.Sent.Nanosecond()%1000 != 0 || m.MsgType != weather.MsgUpdate ||
		m.Severity != weather.SeveritySevere || m.Urgency != "Immediate" || m.Certainty != "Observed" ||
		m.AreaDesc != "Jawa Barat, Banten" || len(m.Polygons) != 3 || !m.HasArea || m.Polygons[0][1].Lon != ring(-6.9, 107.6).GetPoints()[1].GetLongitude() ||
		m.ArchiveKey != "bmkg-cap/k.xml.gz" || !m.FirstSeenAt.Equal(m.FetchedAt) || !m.Onset.IsZero() {
		t.Fatalf("%s %q %v %v", m.Key, m.AreaDesc, m.Polygons, m.Sent)
	}
	// Rujukan diurutkan menurut kunci supaya lolos invarian domain.
	if len(m.References) != 2 || m.References[0].Identifier != "2.49.0.1.360.0.2026.09.24.05.32.001" {
		t.Fatalf("%+v", m.References)
	}
	if len(m.Texts) != 2 || m.Texts[1].Description != "a\nb" {
		t.Fatalf("%+v", m.Texts)
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := Decode([]byte("bukan protobuf")); !errors.Is(err, weather.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	for name, mutate := range map[string]func(*rawv1.WeatherWarning){
		"sumber lain":          func(w *rawv1.WeatherWarning) { w.Source = hazardv1.Source_SOURCE_USGS },
		"status latihan":       func(w *rawv1.WeatherWarning) { w.Status = rawv1.CapStatus_CAP_STATUS_EXERCISE },
		"msgType Ack":          func(w *rawv1.WeatherWarning) { w.MsgType = rawv1.CapMsgType_CAP_MSG_TYPE_ACK },
		"msgType kosong":       func(w *rawv1.WeatherWarning) { w.MsgType = rawv1.CapMsgType_CAP_MSG_TYPE_UNSPECIFIED },
		"msgType tak dikenal":  func(w *rawv1.WeatherWarning) { w.MsgType = 99 },
		"severity kosong":      func(w *rawv1.WeatherWarning) { w.Severity = rawv1.CapSeverity_CAP_SEVERITY_UNSPECIFIED },
		"severity tak dikenal": func(w *rawv1.WeatherWarning) { w.Severity = 99 },
		"tanpa expires":        func(w *rawv1.WeatherWarning) { w.Expires = nil },
		"tanpa meta":           func(w *rawv1.WeatherWarning) { w.Meta = nil },
	} {
		w := sample()
		mutate(w)
		if _, err := FromProto(w); !errors.Is(err, weather.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestEnumMappings(t *testing.T) {
	for in, want := range map[rawv1.CapSeverity]weather.Severity{
		rawv1.CapSeverity_CAP_SEVERITY_MINOR: weather.SeverityMinor, rawv1.CapSeverity_CAP_SEVERITY_MODERATE: weather.SeverityModerate,
		rawv1.CapSeverity_CAP_SEVERITY_EXTREME: weather.SeverityExtreme, rawv1.CapSeverity_CAP_SEVERITY_UNKNOWN: weather.SeverityUnknown,
	} {
		if severityOf(in) != want {
			t.Errorf("%v", in)
		}
	}
	w := sample()
	w.MsgType = rawv1.CapMsgType_CAP_MSG_TYPE_CANCEL
	w.Areas = nil
	w.Urgency = rawv1.CapUrgency_CAP_URGENCY_UNSPECIFIED
	w.Onset = timestamppb.New(sent.Add(time.Minute))
	m, err := FromProto(w)
	if err != nil || m.MsgType != weather.MsgCancel || m.HasArea || m.Urgency != "" || m.Onset.IsZero() {
		t.Fatalf("%+v %v", m, err)
	}
	w = sample()
	w.MsgType = rawv1.CapMsgType_CAP_MSG_TYPE_ALERT
	w.References = nil
	if m, err := FromProto(w); err != nil || m.MsgType != weather.MsgAlert {
		t.Fatalf("%+v %v", m, err)
	}
	if capWord("CAP_URGENCY_EXPECTED", "CAP_URGENCY_") != "Expected" || capWord("LAIN", "CAP_URGENCY_") != "" {
		t.Fatal("capWord")
	}
}
