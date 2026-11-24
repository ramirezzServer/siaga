// Package pollstatus menerbitkan hasil setiap percobaan konektor sebagai
// siaga.status.v1.ConnectorPoll ke stream STATUS (subjek
// status.connector.<konektor>), bahan status sumber yang tahan restart
// (ADR 0030). geo-processor menyimpan keadaan terakhirnya di ts.source_status.
package pollstatus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	statusv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/status/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Timeout membatasi satu penerbitan, supaya NATS yang lambat tidak menahan
// polling berikutnya lebih lama dari itu.
const Timeout = 5 * time.Second

// Reporter adalah ports.PollReporter di atas ports.Publisher.
type Reporter struct {
	pub ports.Publisher
}

var _ ports.PollReporter = (*Reporter)(nil)

// New membuat Reporter. pub harus menerbitkan ke stream STATUS.
func New(pub ports.Publisher) *Reporter { return &Reporter{pub: pub} }

// Report menerbitkan satu hasil percobaan. ID pesan = konektor + waktu mulai,
// jadi percobaan yang sama tidak tersimpan dua kali.
func (r *Reporter) Report(ctx context.Context, rep ports.PollReport) error {
	msg, err := Message(rep)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	_, err = r.pub.Publish(ctx, msg)
	return err
}

// Message menyusun pesan NATS untuk satu hasil percobaan.
func Message(rep ports.PollReport) (ports.Message, error) {
	subject, err := streams.StatusSubject(rep.Connector)
	if err != nil {
		return ports.Message{}, err
	}
	data, err := Encode(rep)
	if err != nil {
		return ports.Message{}, err
	}
	return ports.Message{
		Subject: subject,
		ID:      rep.Connector + ":" + rep.StartedAt.UTC().Format(time.RFC3339Nano),
		Data:    data,
	}, nil
}

// Encode menyusun ConnectorPoll. Hasil tanpa subjek raw, interval, atau
// waktu ditolak: tanpa itu status tidak bisa dihitung.
func Encode(rep ports.PollReport) ([]byte, error) {
	switch {
	case rep.Subject == "":
		return nil, fmt.Errorf("konektor %s tanpa subjek raw", rep.Connector)
	case rep.Interval <= 0:
		return nil, fmt.Errorf("konektor %s tanpa interval", rep.Connector)
	case rep.StartedAt.IsZero() || rep.FinishedAt.Before(rep.StartedAt):
		return nil, errors.New("waktu percobaan tidak valid")
	}
	outcome := statusv1.PollOutcome_POLL_OUTCOME_FAILED
	if rep.OK {
		outcome = statusv1.PollOutcome_POLL_OUTCOME_OK
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(&statusv1.ConnectorPoll{
		Connector:  rep.Connector,
		Subject:    rep.Subject,
		Interval:   durationpb.New(rep.Interval),
		StartedAt:  timestamppb.New(rep.StartedAt),
		FinishedAt: timestamppb.New(rep.FinishedAt),
		Outcome:    outcome,
		Published:  uint32(min(max(rep.Published, 0), math.MaxUint32)),
	})
}
