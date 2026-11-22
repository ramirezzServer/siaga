// Package jsread membaca identitas pesan yang tersimpan di stream JetStream
// (subjek, Nats-Msg-Id, Siaga-Fetched-At) lewat consumer berurutan sementara
// yang hanya mengambil header. Tidak mengubah stream maupun consumer lain.
package jsread

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Reader adalah ports.StreamReader untuk satu stream.
type Reader struct {
	js     jetstream.JetStream
	stream string
	// batch dan wait mengatur satu Fetch; batch habis tanpa pesan berarti
	// tidak ada pesan lagi.
	batch int
	wait  time.Duration
}

var _ ports.StreamReader = (*Reader)(nil)

// New membuat Reader untuk stream.
func New(js jetstream.JetStream, stream string) *Reader {
	return &Reader{js: js, stream: stream, batch: 500, wait: 2 * time.Second}
}

// Messages membaca pesan bersubjek subjects sejak since sampai pesan
// terakhir saat consumer dibuat.
func (r *Reader) Messages(ctx context.Context, subjects []string, since time.Time) iter.Seq2[ports.StreamMessage, error] {
	return func(yield func(ports.StreamMessage, error) bool) {
		if len(subjects) == 0 {
			return
		}
		cfg := jetstream.OrderedConsumerConfig{FilterSubjects: subjects, HeadersOnly: true, DeliverPolicy: jetstream.DeliverAllPolicy}
		if !since.IsZero() {
			cfg.DeliverPolicy, cfg.OptStartTime = jetstream.DeliverByStartTimePolicy, &since
		}
		cons, err := r.js.OrderedConsumer(ctx, r.stream, cfg)
		if err != nil {
			yield(ports.StreamMessage{}, fmt.Errorf("consumer %s: %w", r.stream, err))
			return
		}
		for {
			batch, err := cons.Fetch(r.batch, jetstream.FetchMaxWait(r.wait))
			if err != nil {
				yield(ports.StreamMessage{}, fmt.Errorf("fetch %s: %w", r.stream, err))
				return
			}
			n, last := 0, false
			for msg := range batch.Messages() {
				n++
				m, pending, err := identity(msg)
				if !yield(m, err) || err != nil {
					return
				}
				last = pending == 0
			}
			if err := batch.Error(); err != nil && !errors.Is(err, jetstream.ErrNoMessages) && !errors.Is(err, context.DeadlineExceeded) {
				yield(ports.StreamMessage{}, fmt.Errorf("fetch %s: %w", r.stream, err))
				return
			}
			if n == 0 || last {
				return
			}
		}
	}
}

func identity(msg jetstream.Msg) (ports.StreamMessage, uint64, error) {
	md, err := msg.Metadata()
	if err != nil {
		return ports.StreamMessage{}, 0, fmt.Errorf("metadata pesan: %w", err)
	}
	m := ports.StreamMessage{Subject: msg.Subject(), ID: msg.Headers().Get(jetstream.MsgIDHeader)}
	if v := msg.Headers().Get(natsx.HeaderFetchedAt); v != "" {
		if m.FetchedAt, err = time.Parse(time.RFC3339Nano, v); err != nil {
			return ports.StreamMessage{}, 0, fmt.Errorf("pesan %s #%d: header %s: %w", m.Subject, md.Sequence.Stream, natsx.HeaderFetchedAt, err)
		}
	}
	return m, md.NumPending, nil
}
