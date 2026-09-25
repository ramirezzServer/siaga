// Package jspub menerbitkan pesan ke NATS JetStream dan menunggu konfirmasi
// simpan (PubAck), dengan header yang disepakati di docs/events.md: ID pesan,
// jenis isi, waktu ambil sumber, dan konteks trace W3C (traceparent).
package jspub

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
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
// Setiap penerbitan adalah satu span producer; konteksnya ikut di header
// supaya consumer melanjutkan trace yang sama.
func (p *Publisher) Publish(ctx context.Context, msg ports.Message) (ports.PublishResult, error) {
	ctx, span := natsx.StartPublish(ctx, msg.Subject, msg.ID)
	defer span.End()

	m := nats.NewMsg(msg.Subject)
	m.Data = msg.Data
	m.Header.Set(jetstream.MsgIDHeader, msg.ID)
	m.Header.Set("Content-Type", ContentType)
	if !msg.FetchedAt.IsZero() {
		m.Header.Set(natsx.HeaderFetchedAt, msg.FetchedAt.UTC().Format(time.RFC3339Nano))
	}
	natsx.Inject(ctx, m.Header)
	ack, err := p.js.PublishMsg(ctx, m, jetstream.WithExpectStream(p.stream))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish gagal")
		return ports.PublishResult{}, fmt.Errorf("jetstream publish %s: %w", msg.Subject, err)
	}
	span.SetAttributes(attribute.Bool("siaga.duplicate", ack.Duplicate), attribute.Int64("siaga.stream_seq", int64(min(ack.Sequence, 1<<62))))
	return ports.PublishResult{Duplicate: ack.Duplicate}, nil
}
