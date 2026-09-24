// Package natsjs menghubungkan geo-processor dengan NATS JetStream:
// durable pull consumer untuk raw.quake.*, raw.weather.*, dan deret waktu
// (raw.forecast.*, raw.aq.openmeteo, raw.flood.openmeteo), DLQ, dan
// publisher hazard.*.
package natsjs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Header DLQ (docs/events.md).
const (
	HeaderDLQSubject   = "Siaga-Dlq-Subject"
	HeaderDLQStream    = "Siaga-Dlq-Stream"
	HeaderDLQSequence  = "Siaga-Dlq-Sequence"
	HeaderDLQConsumer  = "Siaga-Dlq-Consumer"
	HeaderDLQDelivered = "Siaga-Dlq-Delivered"
	HeaderDLQError     = "Siaga-Dlq-Error"
	maxHeaderError     = 512
)

// Publisher adalah ports.Publisher di atas JetStream yang memastikan pesan
// masuk ke stream yang diharapkan.
type Publisher struct {
	js     jetstream.JetStream
	stream string
}

var _ ports.Publisher = (*Publisher)(nil)

// NewPublisher membuat publisher untuk satu stream.
func NewPublisher(js jetstream.JetStream, stream string) *Publisher {
	return &Publisher{js: js, stream: stream}
}

// Publish menerbitkan pesan dengan Nats-Msg-Id dan menunggu konfirmasi simpan.
func (p *Publisher) Publish(ctx context.Context, subject, msgID string, data []byte, headers map[string]string) error {
	m := nats.NewMsg(subject)
	m.Data = data
	for k, v := range headers {
		m.Header.Set(k, v)
	}
	m.Header.Set(jetstream.MsgIDHeader, msgID)
	if _, err := p.js.PublishMsg(ctx, m, jetstream.WithExpectStream(p.stream)); err != nil {
		return fmt.Errorf("jetstream publish %s: %w", subject, err)
	}
	return nil
}

// ConsumerSpec adalah identitas satu durable consumer di stream RAW.
type ConsumerSpec struct {
	Durable     string
	Description string
	Filter      string
}

// Consumer geo-processor (docs/events.md, bagian Consumer).
var (
	QuakeConsumer = ConsumerSpec{
		Durable: "geo-processor-quake", Filter: "raw.quake.>",
		Description: "geo-processor: normalisasi dan deduplikasi gempa",
	}
	WeatherConsumer = ConsumerSpec{
		Durable: "geo-processor-weather", Filter: "raw.weather.>",
		Description: "geo-processor: peringatan dini cuaca CAP",
	}
	// Deret waktu: satu consumer per subjek karena tiap subjek membawa jenis
	// pesan Protobuf yang berbeda (ADR 0012).
	ForecastBMKGConsumer = ConsumerSpec{
		Durable: "geo-processor-forecast-bmkg", Filter: "raw.forecast.bmkg",
		Description: "geo-processor: prakiraan cuaca BMKG per kelurahan/desa ke ts.weather_forecast",
	}
	ForecastOpenMeteoConsumer = ConsumerSpec{
		Durable: "geo-processor-forecast-openmeteo", Filter: "raw.forecast.openmeteo",
		Description: "geo-processor: prakiraan cuaca grid Open-Meteo ke ts.weather_forecast",
	}
	AirQualityOpenMeteoConsumer = ConsumerSpec{
		Durable: "geo-processor-aq-openmeteo", Filter: "raw.aq.openmeteo",
		Description: "geo-processor: prakiraan kualitas udara CAMS ke ts.aq_forecast",
	}
	FloodOpenMeteoConsumer = ConsumerSpec{
		Durable: "geo-processor-flood-openmeteo", Filter: "raw.flood.openmeteo",
		Description: "geo-processor: debit sungai GloFAS ke ts.river_discharge",
	}
	AirQualityOpenAQConsumer = ConsumerSpec{
		Durable: "geo-processor-aq-openaq", Filter: "raw.aq.openaq",
		Description: "geo-processor: nilai sensor stasiun OpenAQ ke ts.aq_observation",
	}
	FireFIRMSConsumer = ConsumerSpec{
		Durable: "geo-processor-fire-firms", Filter: "raw.fire.firms",
		Description: "geo-processor: titik panas NASA FIRMS ke ts.hotspot",
	}
)

// ConsumerConfig adalah konfigurasi durable consumer.
func ConsumerConfig(spec ConsumerSpec) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       spec.Durable,
		Description:   spec.Description,
		FilterSubject: spec.Filter,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		// Batas percobaan diatur Handler (pesan dipindah ke DLQ), bukan server,
		// supaya pesan tidak pernah hilang diam-diam setelah MaxDeliver.
		MaxDeliver:    -1,
		MaxAckPending: 64,
	}
}

// Handler memutuskan nasib satu pesan (dipenuhi *consume.Handler).
type Handler interface {
	Handle(ctx context.Context, data []byte, delivered int) consume.Decision
}

// Consumer menarik pesan satu durable consumer dan menjalankan keputusan Handler.
type Consumer struct {
	cons    jetstream.Consumer
	handler Handler
	dlq     *Publisher
	dlqSubj string
	name    string
	log     *slog.Logger
	after   func()
}

// NewConsumer menyiapkan durable consumer di stream RAW. Stream RAW milik
// ingest; bila belum ada, galatnya dikembalikan supaya pemanggil mencoba lagi.
func NewConsumer(ctx context.Context, js jetstream.JetStream, spec ConsumerSpec, service string, h Handler, log *slog.Logger, after func()) (*Consumer, error) {
	cons, err := js.CreateOrUpdateConsumer(ctx, streams.Raw.Name, ConsumerConfig(spec))
	if err != nil {
		return nil, fmt.Errorf("menyiapkan consumer %s: %w", spec.Durable, err)
	}
	dlqSubj, err := streams.DLQSubject(service)
	if err != nil {
		return nil, err
	}
	return &Consumer{
		cons: cons, handler: h, dlq: NewPublisher(js, streams.DLQ.Name), dlqSubj: dlqSubj,
		name: spec.Durable, log: log, after: after,
	}, nil
}

// Run memproses pesan satu per satu sampai ctx selesai.
func (c *Consumer) Run(ctx context.Context) error {
	it, err := c.cons.Messages(jetstream.PullMaxMessages(16))
	if err != nil {
		return fmt.Errorf("membuka aliran pesan %s: %w", c.name, err)
	}
	stop := context.AfterFunc(ctx, it.Stop)
	defer stop()
	for {
		msg, err := it.Next()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return nil
			}
			return fmt.Errorf("menarik pesan %s: %w", c.name, err)
		}
		c.handle(ctx, msg)
	}
}

func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) {
	meta, err := msg.Metadata()
	if err != nil {
		c.log.Error("pesan tanpa metadata JetStream", slog.String("subject", msg.Subject()), slog.Any("error", err))
		_ = msg.Term()
		return
	}
	delivered := int(min(meta.NumDelivered, uint64(1<<30)))
	d := c.handler.Handle(ctx, msg.Data(), delivered)
	attrs := []any{
		slog.String("subject", msg.Subject()), slog.Uint64("stream_seq", meta.Sequence.Stream),
		slog.Int("delivered", delivered),
	}
	switch d.Action {
	case consume.Ack:
		if err := msg.DoubleAck(ctx); err != nil {
			c.log.Warn("ack gagal; pesan akan dikirim ulang dan diproses idempotent", append(attrs, slog.Any("error", err))...)
		}
		if c.after != nil {
			c.after()
		}
	case consume.Retry:
		c.log.Warn("galat sementara; dicoba ulang", append(attrs, slog.Duration("delay", d.Delay), slog.Any("error", d.Err))...)
		_ = msg.NakWithDelay(d.Delay)
	case consume.DeadLetter:
		if err := c.deadLetter(ctx, msg, meta, delivered, d.Err); err != nil {
			c.log.Error("gagal memindah pesan ke DLQ; dicoba ulang", append(attrs, slog.Any("error", err))...)
			_ = msg.NakWithDelay(5 * time.Second)
			return
		}
		c.log.Error("pesan dipindah ke DLQ", append(attrs, slog.String("dlq", c.dlqSubj), slog.Any("error", d.Err))...)
		_ = msg.Term()
	}
}

// deadLetter menyalin pesan ke DLQ. Nats-Msg-Id = stream:sequence asal, jadi
// bila Term gagal dan pesan dipindah lagi, DLQ tetap berisi satu salinan.
func (c *Consumer) deadLetter(ctx context.Context, msg jetstream.Msg, meta *jetstream.MsgMetadata, delivered int, reason error) error {
	headers := map[string]string{
		HeaderDLQSubject:   msg.Subject(),
		HeaderDLQStream:    meta.Stream,
		HeaderDLQSequence:  strconv.FormatUint(meta.Sequence.Stream, 10),
		HeaderDLQConsumer:  c.name,
		HeaderDLQDelivered: strconv.Itoa(delivered),
		HeaderDLQError:     headerValue(fmt.Sprint(reason), maxHeaderError),
	}
	if ct := msg.Headers().Get("Content-Type"); ct != "" {
		headers["Content-Type"] = ct
	}
	id := meta.Stream + ":" + strconv.FormatUint(meta.Sequence.Stream, 10)
	return c.dlq.Publish(ctx, c.dlqSubj, id, msg.Data(), headers)
}

// headerValue membuat teks aman untuk header NATS: satu baris, paling banyak n byte.
func headerValue(s string, n int) string {
	s = strings.NewReplacer("\r", " ", "\n", " | ").Replace(s)
	if len(s) <= n {
		return s
	}
	// Potong di batas rune supaya header tetap UTF-8 valid.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
