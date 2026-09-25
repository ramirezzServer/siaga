// Package telemetry adalah metrik OpenTelemetry geo-processor yang dibaca dari
// statistik use case (ADR 0015): pesan per consumer menurut hasilnya,
// perubahan kejadian bahaya, baris deret waktu, dan outbox. Span dan
// histogram per pesan ada di adapter natsjs, span query di adapter postgres.
//
// Tanpa SDK terpasang (otelx tanpa endpoint), semua instrumen adalah noop.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/relay"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Scope adalah nama instrumentasi geo-processor.
const Scope = "github.com/ramirezzServer/siaga/services/geo-processor"

var waitBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// Backlog adalah isi outbox yang belum terbit.
type Backlog struct {
	Pending int64
	// Oldest adalah waktu tulis pesan tertua; nol bila outbox kosong.
	Oldest time.Time
}

// Sources adalah sumber statistik untuk metrik; semua boleh nil.
type Sources struct {
	// Consumers memetakan nama durable consumer ke snapshot statistiknya.
	Consumers map[string]func() consume.Stats
	Relay     func() relay.Stats
	// Backlog membaca isi outbox dari database saat metrik dikumpulkan.
	Backlog func(context.Context) (Backlog, error)
}

// Observe mendaftarkan metrik yang dibaca dari snapshot saat metrik
// dikumpulkan, jadi tidak ada pencatatan tambahan di jalur pemrosesan.
func Observe(m metric.Meter, src Sources) (metric.Registration, error) {
	var errs [7]error
	var (
		messages, changes, rows, published metric.Int64ObservableCounter
		lastSuccess, oldest                metric.Float64ObservableGauge
		pending                            metric.Int64ObservableGauge
	)
	messages, errs[0] = m.Int64ObservableCounter("siaga.geo.messages",
		metric.WithUnit("{message}"), metric.WithDescription("Pesan per consumer: diterima, diterapkan, ulangan tanpa perubahan, dicoba ulang, dipindah ke DLQ."))
	changes, errs[1] = m.Int64ObservableCounter("siaga.geo.hazard.changes",
		metric.WithUnit("{event}"), metric.WithDescription("Kejadian bahaya yang dibuat, diperbarui, atau diakhiri."))
	rows, errs[2] = m.Int64ObservableCounter("siaga.geo.series.rows",
		metric.WithUnit("{row}"), metric.WithDescription("Baris deret waktu yang ditambah atau diubah."))
	lastSuccess, errs[3] = m.Float64ObservableGauge("siaga.geo.consumer.last_success",
		metric.WithUnit("s"), metric.WithDescription("Waktu Unix pesan terakhir yang berhasil diproses consumer."))
	published, errs[4] = m.Int64ObservableCounter("siaga.geo.outbox.published",
		metric.WithUnit("{message}"), metric.WithDescription("Pesan hazard.* yang diterbitkan relay outbox."))
	pending, errs[5] = m.Int64ObservableGauge("siaga.geo.outbox.pending",
		metric.WithUnit("{message}"), metric.WithDescription("Pesan outbox yang belum terbit."))
	oldest, errs[6] = m.Float64ObservableGauge("siaga.geo.outbox.oldest_age",
		metric.WithUnit("s"), metric.WithDescription("Umur pesan outbox tertua yang belum terbit (0 bila kosong)."))
	if err := errors.Join(errs[:]...); err != nil {
		return nil, fmt.Errorf("telemetry: instrumen: %w", err)
	}
	return m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		for name, snap := range src.Consumers {
			s := snap()
			c := attribute.String("consumer", name)
			for result, n := range map[string]int64{
				"received": s.Received, "applied": s.Applied, "unchanged": s.Unchanged,
				"retried": s.Retried, "dead_lettered": s.DeadLettered,
			} {
				o.ObserveInt64(messages, n, metric.WithAttributes(c, attribute.String("result", result)))
			}
			for change, n := range map[string]int64{"created": s.Created, "updated": s.Updated, "ended": s.Ended} {
				o.ObserveInt64(changes, n, metric.WithAttributes(c, attribute.String("change", change)))
			}
			o.ObserveInt64(rows, s.Rows, metric.WithAttributes(c))
			if !s.LastSuccess.IsZero() {
				o.ObserveFloat64(lastSuccess, float64(s.LastSuccess.UnixNano())/1e9, metric.WithAttributes(c))
			}
		}
		if src.Relay != nil {
			o.ObserveInt64(published, src.Relay().Published)
		}
		if src.Backlog != nil {
			qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if b, err := src.Backlog(qctx); err == nil {
				o.ObserveInt64(pending, b.Pending)
				age := 0.0
				if !b.Oldest.IsZero() {
					age = max(time.Since(b.Oldest).Seconds(), 0)
				}
				o.ObserveFloat64(oldest, age)
			}
		}
		return nil
	}, messages, changes, rows, lastSuccess, published, pending, oldest)
}

// OutboxObserver mengembalikan relay.Observer yang mencatat lama pesan
// menunggu di outbox sampai terbit (atau gagal terbit), per subjek.
func OutboxObserver(m metric.Meter) (relay.Observer, error) {
	wait, err := m.Float64Histogram("siaga.geo.outbox.wait",
		metric.WithUnit("s"), metric.WithDescription("Dari pesan ditulis ke outbox sampai relay mencoba menerbitkannya."),
		metric.WithExplicitBucketBoundaries(waitBuckets...))
	if err != nil {
		return nil, fmt.Errorf("telemetry: instrumen outbox: %w", err)
	}
	return func(ctx context.Context, msg ports.OutboxMessage, err error) {
		if msg.CreatedAt.IsZero() {
			return
		}
		result := "ok"
		if err != nil {
			result = "error"
		}
		wait.Record(context.WithoutCancel(ctx), max(time.Since(msg.CreatedAt).Seconds(), 0),
			metric.WithAttributes(attribute.String("subject", msg.Subject), attribute.String("result", result)))
	}, nil
}
