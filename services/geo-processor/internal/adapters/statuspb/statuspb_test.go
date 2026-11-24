package statuspb

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	statusv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/status/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
)

func encode(t *testing.T, m *statusv1.ConnectorPoll) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecode(t *testing.T) {
	start := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	m := &statusv1.ConnectorPoll{
		Connector: "openmeteo-sungai", Subject: "raw.flood.openmeteo", Interval: durationpb.New(6 * time.Hour),
		StartedAt: timestamppb.New(start), FinishedAt: timestamppb.New(start.Add(3 * time.Second)),
		Outcome: statusv1.PollOutcome_POLL_OUTCOME_OK, Published: 38,
	}
	p, err := Decode(encode(t, m))
	if err != nil {
		t.Fatal(err)
	}
	want := sourcestatus.Poll{
		Connector: "openmeteo-sungai", Subject: "raw.flood.openmeteo", Interval: 6 * time.Hour,
		StartedAt: start, FinishedAt: start.Add(3 * time.Second), OK: true, Published: 38,
	}
	if p != want {
		t.Fatalf("dapat %+v\ningin %+v", p, want)
	}
	if err := p.Validate(start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	m.Outcome, m.Published = statusv1.PollOutcome_POLL_OUTCOME_FAILED, 0
	if p, err := Decode(encode(t, m)); err != nil || p.OK {
		t.Fatalf("gagal: %+v %v", p, err)
	}
	// Tanpa waktu: waktu nol, ditolak Validate.
	m.StartedAt = nil
	if p, err := Decode(encode(t, m)); err != nil || !p.StartedAt.IsZero() || p.Validate(start) == nil {
		t.Fatalf("tanpa waktu mulai: %+v %v", p, err)
	}
}

func TestDecodeRejects(t *testing.T) {
	for name, data := range map[string][]byte{
		"bukan protobuf": {0xff, 0xff, 0xff},
		"hasil kosong":   encode(t, &statusv1.ConnectorPoll{Connector: "x", Interval: durationpb.New(time.Minute)}),
		"interval rusak": encode(t, &statusv1.ConnectorPoll{Connector: "x", Interval: &durationpb.Duration{Seconds: 1, Nanos: -1}}),
		"hasil asing":    encode(t, &statusv1.ConnectorPoll{Connector: "x", Interval: durationpb.New(time.Minute), Outcome: statusv1.PollOutcome(9)}),
	} {
		if _, err := Decode(data); !errors.Is(err, sourcestatus.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
