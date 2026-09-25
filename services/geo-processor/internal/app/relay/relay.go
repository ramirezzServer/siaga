// Package relay menerbitkan pesan outbox hazard.* ke broker. Pesan ditulis ke
// outbox dalam transaksi yang sama dengan perubahan kejadian, jadi tidak ada
// event yang hilang walau broker mati atau layanan crash di tengah jalan.
package relay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// ContentType untuk semua event biner SIAGA (docs/events.md).
const ContentType = "application/protobuf"

// HeaderTraceParent adalah header konteks trace W3C (docs/events.md).
// Relay meneruskan konteks yang tersimpan di outbox lewat header ini;
// publisher membuat span penerbitan sebagai anaknya.
const HeaderTraceParent = "traceparent"

// Observer dipanggil setelah setiap percobaan menerbitkan satu pesan outbox
// (err nil berarti berhasil), misal untuk metrik lama tunggu di outbox.
type Observer func(ctx context.Context, m ports.OutboxMessage, err error)

// Relay mengosongkan outbox ke publisher.
type Relay struct {
	outbox ports.Outbox
	pub    ports.Publisher
	log    *slog.Logger
	now    func() time.Time
	batch  int
	wake   chan struct{}
	obs    Observer

	mu    sync.Mutex
	stats Stats
}

// Stats adalah statistik relay untuk endpoint /status.
type Stats struct {
	Published     int64     `json:"published"`
	LastPublished time.Time `json:"last_published,omitzero"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at,omitzero"`
}

// New membuat Relay dengan ukuran batch per transaksi.
func New(outbox ports.Outbox, pub ports.Publisher, log *slog.Logger, now func() time.Time, batch int) *Relay {
	return &Relay{outbox: outbox, pub: pub, log: log, now: now, batch: max(batch, 1), wake: make(chan struct{}, 1)}
}

// Observe memasang pengamat penerbitan; nil mematikannya. Panggil sebelum Run.
func (r *Relay) Observe(obs Observer) *Relay {
	r.obs = obs
	return r
}

// Wake meminta relay memeriksa outbox sekarang, tanpa menunggu interval.
// Tidak pernah memblokir.
func (r *Relay) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Flush menerbitkan semua pesan yang menunggu sampai outbox kosong.
func (r *Relay) Flush(ctx context.Context) (int, error) {
	total := 0
	publish := func(ctx context.Context, m ports.OutboxMessage) error {
		headers := map[string]string{"Content-Type": ContentType}
		if m.TraceParent != "" {
			headers[HeaderTraceParent] = m.TraceParent
		}
		err := r.pub.Publish(ctx, m.Subject, m.MsgID, m.Payload, headers)
		if r.obs != nil {
			r.obs(ctx, m, err)
		}
		return err
	}
	for {
		n, err := r.outbox.Drain(ctx, r.batch, publish)
		total += n
		if n > 0 {
			r.record(func(s *Stats) {
				s.Published += int64(n)
				s.LastPublished = r.now()
			})
		}
		if err != nil {
			r.record(func(s *Stats) {
				s.LastError = err.Error()
				s.LastErrorAt = r.now()
			})
			return total, err
		}
		if n < r.batch {
			return total, nil
		}
	}
}

// Run memeriksa outbox saat dibangunkan dan setiap interval sampai ctx selesai.
func (r *Relay) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := r.Flush(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.log.Warn("gagal menerbitkan outbox; dicoba lagi", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

func (r *Relay) record(fn func(*Stats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.stats)
}

// Snapshot mengembalikan salinan statistik.
func (r *Relay) Snapshot() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}
