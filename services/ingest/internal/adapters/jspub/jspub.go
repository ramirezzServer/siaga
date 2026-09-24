// Package jspub menerbitkan pesan ke NATS JetStream dan menunggu konfirmasi
// simpan (PubAck), dengan header yang disepakati di docs/events.md.
package jspub

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ContentType untuk semua event biner SIAGA.
const ContentType = "application/protobuf"

// Publisher adalah ports.Publisher di atas JetStream.
type Publisher struct {
	js jetstream.JetStream
	// stream yang diharapkan menampung pesan; PubAck dari stream lain berarti
	// konfigurasi subjek salah dan dianggap galat.
	stream string
}

// New membuat Publisher untuk stream tertentu.
func New(js jetstream.JetStream, stream string) *Publisher {
	return &Publisher{js: js, stream: stream}
}

// Publish menerbitkan msg dengan Nats-Msg-Id untuk deduplikasi di server.
func (p *Publisher) Publish(ctx context.Context, msg ports.Message) (ports.PublishResult, error) {
	m := nats.NewMsg(msg.Subject)
	m.Data = msg.Data
	m.Header.Set(jetstream.MsgIDHeader, msg.ID)
	m.Header.Set("Content-Type", ContentType)
	ack, err := p.js.PublishMsg(ctx, m, jetstream.WithExpectStream(p.stream))
	if err != nil {
		return ports.PublishResult{}, fmt.Errorf("jetstream publish %s: %w", msg.Subject, err)
	}
	return ports.PublishResult{Duplicate: ack.Duplicate}, nil
}
