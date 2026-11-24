// Package statuspb mendekode hasil polling konektor ingest
// (siaga.status.v1.ConnectorPoll, subjek status.connector.<konektor>)
// menjadi nilai domain sourcestatus. Validasi dilakukan use case; payload
// yang rusak membungkus sourcestatus.ErrInvalid.
package statuspb

import (
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	statusv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/status/v1"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/sourcestatus"
)

// Decode membaca satu ConnectorPoll.
func Decode(data []byte) (sourcestatus.Poll, error) {
	var m statusv1.ConnectorPoll
	if err := proto.Unmarshal(data, &m); err != nil {
		return sourcestatus.Poll{}, fmt.Errorf("%w: protobuf: %w", sourcestatus.ErrInvalid, err)
	}
	if err := m.GetInterval().CheckValid(); err != nil {
		return sourcestatus.Poll{}, fmt.Errorf("%w: interval: %w", sourcestatus.ErrInvalid, err)
	}
	var ok bool
	switch m.GetOutcome() {
	case statusv1.PollOutcome_POLL_OUTCOME_OK:
		ok = true
	case statusv1.PollOutcome_POLL_OUTCOME_FAILED:
	case statusv1.PollOutcome_POLL_OUTCOME_UNSPECIFIED:
		return sourcestatus.Poll{}, fmt.Errorf("%w: %s: hasil polling kosong", sourcestatus.ErrInvalid, m.GetConnector())
	default:
		return sourcestatus.Poll{}, fmt.Errorf("%w: %s: hasil %s", sourcestatus.ErrInvalid, m.GetConnector(), m.GetOutcome())
	}
	return sourcestatus.Poll{
		Connector:  m.GetConnector(),
		Subject:    m.GetSubject(),
		Interval:   m.GetInterval().AsDuration(),
		StartedAt:  ts(m.GetStartedAt()),
		FinishedAt: ts(m.GetFinishedAt()),
		OK:         ok,
		Published:  int(min(m.GetPublished(), math.MaxInt32)),
	}, nil
}

// ts mengubah timestamp menjadi waktu UTC; kosong atau tidak valid menjadi
// waktu nol (ditolak Validate).
func ts(t *timestamppb.Timestamp) time.Time {
	if t == nil || t.CheckValid() != nil {
		return time.Time{}
	}
	return t.AsTime().UTC()
}
