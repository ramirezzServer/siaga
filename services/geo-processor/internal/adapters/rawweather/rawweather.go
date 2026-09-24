// Package rawweather mendekode event raw.weather.* (siaga.raw.v1.WeatherWarning)
// menjadi pesan domain peringatan cuaca geo-processor.
package rawweather

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

// Decode membaca payload Protobuf lalu memvalidasinya. Galat selalu
// membungkus weather.ErrInvalid: payload yang tidak terbaca tidak akan
// terbaca bila dicoba ulang.
//
// Waktu dibulatkan ke mikrodetik, presisi timestamptz PostgreSQL, supaya
// pesan yang dibaca ulang dari database identik dengan pesan aslinya.
func Decode(data []byte) (weather.Message, error) {
	var m rawv1.WeatherWarning
	if err := proto.Unmarshal(data, &m); err != nil {
		return weather.Message{}, fmt.Errorf("%w: payload WeatherWarning rusak: %w", weather.ErrInvalid, err)
	}
	return FromProto(&m)
}

// FromProto menerjemahkan pesan yang sudah didekode.
func FromProto(m *rawv1.WeatherWarning) (weather.Message, error) {
	if m.GetSource() != hazardv1.Source_SOURCE_BMKG {
		return weather.Message{}, fmt.Errorf("%w: sumber %s bukan penerbit CAP", weather.ErrInvalid, m.GetSource())
	}
	if m.GetStatus() != rawv1.CapStatus_CAP_STATUS_ACTUAL {
		return weather.Message{}, fmt.Errorf("%w: pesan %s berstatus %s, bukan Actual", weather.ErrInvalid, m.GetIdentifier(), m.GetStatus())
	}
	msgType, err := msgTypeOf(m.GetMsgType())
	if err != nil {
		return weather.Message{}, err
	}
	fetched := ts(m.GetMeta().GetFetchedAt())
	out := weather.Message{
		Key:         weather.Key{Source: weather.SourceBMKG, Identifier: m.GetIdentifier()},
		Sender:      m.GetSender(),
		Sent:        ts(m.GetSent()),
		MsgType:     msgType,
		Category:    m.GetCategory(),
		EventCode:   m.GetEventCode(),
		Urgency:     capWord(m.GetUrgency().String(), "CAP_URGENCY_"),
		Certainty:   capWord(m.GetCertainty().String(), "CAP_CERTAINTY_"),
		Severity:    severityOf(m.GetSeverity()),
		Effective:   ts(m.GetEffective()),
		Onset:       ts(m.GetOnset()),
		Expires:     ts(m.GetExpires()),
		Web:         m.GetWeb(),
		Contact:     m.GetContact(),
		SourceURL:   m.GetSourceUrl(),
		FetchedAt:   fetched,
		FirstSeenAt: fetched,
		ArchiveKey:  m.GetMeta().GetArchiveKey(),
	}
	for _, r := range m.GetReferences() {
		out.References = append(out.References, weather.Reference{
			Key:    weather.Key{Source: weather.SourceBMKG, Identifier: r.GetIdentifier()},
			Sender: r.GetSender(),
			Sent:   ts(r.GetSent()),
		})
	}
	slices.SortFunc(out.References, func(a, b weather.Reference) int { return a.Compare(b.Key) })
	for _, t := range m.GetTexts() {
		out.Texts = append(out.Texts, weather.Text{
			Language: t.GetLanguage(), Event: t.GetEvent(), Headline: t.GetHeadline(),
			Description: t.GetDescription(), Instruction: t.GetInstruction(), SenderName: t.GetSenderName(),
		})
	}
	var descs []string
	for _, a := range m.GetAreas() {
		if d := strings.TrimSpace(a.GetAreaDesc()); d != "" && !slices.Contains(descs, d) {
			descs = append(descs, d)
		}
		for _, ring := range a.GetPolygons() {
			r := make(weather.Ring, len(ring.GetPoints()))
			for i, p := range ring.GetPoints() {
				r[i] = weather.Point{Lat: p.GetLatitude(), Lon: p.GetLongitude()}
			}
			out.Polygons = append(out.Polygons, r)
		}
	}
	out.AreaDesc = strings.Join(descs, ", ")
	out.HasArea = len(out.Polygons) > 0
	return out, out.Validate()
}

func ts(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime().UTC().Truncate(time.Microsecond)
}

func msgTypeOf(t rawv1.CapMsgType) (weather.MsgType, error) {
	switch t {
	case rawv1.CapMsgType_CAP_MSG_TYPE_ALERT:
		return weather.MsgAlert, nil
	case rawv1.CapMsgType_CAP_MSG_TYPE_UPDATE:
		return weather.MsgUpdate, nil
	case rawv1.CapMsgType_CAP_MSG_TYPE_CANCEL:
		return weather.MsgCancel, nil
	case rawv1.CapMsgType_CAP_MSG_TYPE_UNSPECIFIED, rawv1.CapMsgType_CAP_MSG_TYPE_ACK, rawv1.CapMsgType_CAP_MSG_TYPE_ERROR:
		return "", fmt.Errorf("%w: msgType %s tidak diproses", weather.ErrInvalid, t)
	default:
		return "", fmt.Errorf("%w: msgType tidak dikenal %d", weather.ErrInvalid, t)
	}
}

func severityOf(s rawv1.CapSeverity) weather.Severity {
	switch s {
	case rawv1.CapSeverity_CAP_SEVERITY_MINOR:
		return weather.SeverityMinor
	case rawv1.CapSeverity_CAP_SEVERITY_MODERATE:
		return weather.SeverityModerate
	case rawv1.CapSeverity_CAP_SEVERITY_SEVERE:
		return weather.SeveritySevere
	case rawv1.CapSeverity_CAP_SEVERITY_EXTREME:
		return weather.SeverityExtreme
	case rawv1.CapSeverity_CAP_SEVERITY_UNKNOWN:
		return weather.SeverityUnknown
	case rawv1.CapSeverity_CAP_SEVERITY_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// capWord mengubah nama enum ("CAP_URGENCY_IMMEDIATE") ke kata CAP ("Immediate").
// UNSPECIFIED menjadi kosong.
func capWord(name, prefix string) string {
	w := strings.TrimPrefix(name, prefix)
	if w == "UNSPECIFIED" || w == name {
		return ""
	}
	return w[:1] + strings.ToLower(w[1:])
}
