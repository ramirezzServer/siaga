package natsx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// infoTimeout membatasi setiap permintaan info stream/consumer saat metrik
// dikumpulkan, supaya NATS yang lambat tidak menahan ekspor metrik lain.
const infoTimeout = 2 * time.Second

// ObserveStreams mendaftarkan gauge isi stream JetStream (jumlah pesan dan
// byte), dibaca saat metrik dikumpulkan. Stream yang belum ada atau gagal
// dibaca dilewati.
func ObserveStreams(m metric.Meter, js jetstream.JetStream, names ...string) (metric.Registration, error) {
	msgs, err1 := m.Int64ObservableGauge("siaga.nats.stream.messages",
		metric.WithUnit("{message}"), metric.WithDescription("Jumlah pesan tersimpan di stream JetStream."))
	size, err2 := m.Int64ObservableGauge("siaga.nats.stream.size",
		metric.WithUnit("By"), metric.WithDescription("Ukuran isi stream JetStream."))
	if err := errors.Join(err1, err2); err != nil {
		return nil, fmt.Errorf("natsx: instrumen stream: %w", err)
	}
	return m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		for _, name := range names {
			info, err := streamInfo(ctx, js, name)
			if err != nil {
				continue
			}
			attrs := metric.WithAttributes(attribute.String("stream", name))
			o.ObserveInt64(msgs, int64(min(info.State.Msgs, 1<<62)), attrs)
			o.ObserveInt64(size, int64(min(info.State.Bytes, 1<<62)), attrs)
		}
		return nil
	}, msgs, size)
}

func streamInfo(ctx context.Context, js jetstream.JetStream, name string) (*jetstream.StreamInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	s, err := js.Stream(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.Info(ctx)
}

// ObserveConsumers mendaftarkan gauge antrean durable consumer: pesan yang
// belum dikirim (pending), sudah dikirim tetapi belum di-ack (ack_pending),
// dan yang sedang dikirim ulang (redelivered).
func ObserveConsumers(m metric.Meter, js jetstream.JetStream, stream string, durables ...string) (metric.Registration, error) {
	pending, err1 := m.Int64ObservableGauge("siaga.nats.consumer.pending",
		metric.WithUnit("{message}"), metric.WithDescription("Pesan di stream yang belum dikirim ke consumer."))
	ackPending, err2 := m.Int64ObservableGauge("siaga.nats.consumer.ack_pending",
		metric.WithUnit("{message}"), metric.WithDescription("Pesan yang sudah dikirim tetapi belum di-ack."))
	redelivered, err3 := m.Int64ObservableGauge("siaga.nats.consumer.redelivered",
		metric.WithUnit("{message}"), metric.WithDescription("Pesan yang sedang dikirim ulang."))
	if err := errors.Join(err1, err2, err3); err != nil {
		return nil, fmt.Errorf("natsx: instrumen consumer: %w", err)
	}
	return m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		for _, d := range durables {
			info, err := consumerInfo(ctx, js, stream, d)
			if err != nil {
				continue
			}
			attrs := metric.WithAttributes(attribute.String("stream", stream), attribute.String("consumer", d))
			o.ObserveInt64(pending, int64(min(info.NumPending, 1<<62)), attrs)
			o.ObserveInt64(ackPending, int64(info.NumAckPending), attrs)
			o.ObserveInt64(redelivered, int64(info.NumRedelivered), attrs)
		}
		return nil
	}, pending, ackPending, redelivered)
}

func consumerInfo(ctx context.Context, js jetstream.JetStream, stream, durable string) (*jetstream.ConsumerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	c, err := js.Consumer(ctx, stream, durable)
	if err != nil {
		return nil, err
	}
	return c.Info(ctx)
}
