// Package consume memutuskan nasib setiap pesan raw yang diterima consumer:
// diakui, dicoba ulang dengan jeda, atau dipindah ke DLQ. Keputusannya murni
// supaya bisa diuji tanpa broker; adapter JetStream yang menjalankannya.
package consume

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/quakes"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

// Action adalah tindakan atas satu pesan.
type Action int

// Tindakan yang mungkin.
const (
	// Ack: pesan selesai diproses (termasuk ulangan yang tidak mengubah apa pun).
	Ack Action = iota + 1
	// Retry: galat sementara (database atau jaringan); kirim ulang setelah Delay.
	Retry
	// DeadLetter: pesan tidak bisa diproses (rusak, melanggar invarian) atau
	// sudah gagal MaxDeliveries kali; pindahkan ke DLQ lalu hentikan pengiriman.
	DeadLetter
)

func (a Action) String() string {
	switch a {
	case Ack:
		return "ack"
	case Retry:
		return "retry"
	case DeadLetter:
		return "dead-letter"
	default:
		return "unknown"
	}
}

// Decision adalah hasil Handle.
type Decision struct {
	Action Action
	Delay  time.Duration
	// Err adalah alasan untuk Retry dan DeadLetter (dicatat di log dan header DLQ).
	Err error
}

// Options mengatur percobaan ulang.
type Options struct {
	// MaxDeliveries adalah jumlah pengiriman sebelum pesan dipindah ke DLQ
	// (docs/events.md: 5).
	MaxDeliveries int
	// Backoff adalah jeda sebelum pengiriman ke-2, ke-3, ...; elemen terakhir
	// dipakai untuk pengiriman selanjutnya.
	Backoff []time.Duration
}

// DefaultOptions sesuai dokumen arsitektur: 5 kali percobaan.
func DefaultOptions() Options {
	return Options{MaxDeliveries: 5, Backoff: []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}}
}

// Decoder mengubah payload menjadi laporan domain.
type Decoder func([]byte) (quake.Report, error)

// Processor memproses laporan domain.
type Processor func(context.Context, quake.Report) (quakes.Result, error)

// Handler memproses pesan raw.quake.*.
type Handler struct {
	decode  Decoder
	process Processor
	opts    Options
	now     func() time.Time
	stats   Stats
	mu      sync.Mutex
}

// Stats adalah statistik consumer untuk endpoint /status.
type Stats struct {
	Received     int64     `json:"received"`
	Applied      int64     `json:"applied"`
	Unchanged    int64     `json:"unchanged"`
	Retried      int64     `json:"retried"`
	DeadLettered int64     `json:"dead_lettered"`
	Created      int64     `json:"created"`
	Updated      int64     `json:"updated"`
	Ended        int64     `json:"ended"`
	LastSuccess  time.Time `json:"last_success,omitzero"`
	LastError    string    `json:"last_error,omitempty"`
	LastErrorAt  time.Time `json:"last_error_at,omitzero"`
}

// New membuat Handler.
func New(decode Decoder, process Processor, opts Options, now func() time.Time) (*Handler, error) {
	if opts.MaxDeliveries < 1 || len(opts.Backoff) == 0 {
		return nil, errors.New("consume: MaxDeliveries >= 1 dan Backoff wajib diisi")
	}
	return &Handler{decode: decode, process: process, opts: opts, now: now}, nil
}

// Handle memproses satu pesan. delivered adalah nomor pengiriman pesan ini
// (1 untuk pengiriman pertama).
func (h *Handler) Handle(ctx context.Context, data []byte, delivered int) Decision {
	h.count(func(s *Stats) { s.Received++ })
	r, err := h.decode(data)
	if err != nil {
		return h.fail(Decision{Action: DeadLetter, Err: err})
	}
	res, err := h.process(ctx, r)
	switch {
	case err == nil:
		h.count(func(s *Stats) {
			if res.Changed {
				s.Applied++
			} else {
				s.Unchanged++
			}
			s.Created += int64(res.Created)
			s.Updated += int64(res.Updated)
			s.Ended += int64(res.Ended)
			s.LastSuccess = h.now()
		})
		return Decision{Action: Ack}
	case errors.Is(err, quake.ErrInvalid):
		return h.fail(Decision{Action: DeadLetter, Err: err})
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		// Layanan sedang berhenti: kirim ulang secepatnya ke instance lain.
		return Decision{Action: Retry, Err: err}
	case delivered >= h.opts.MaxDeliveries:
		return h.fail(Decision{Action: DeadLetter, Err: err})
	default:
		return h.fail(Decision{Action: Retry, Delay: h.backoff(delivered), Err: err})
	}
}

func (h *Handler) backoff(delivered int) time.Duration {
	i := min(max(delivered-1, 0), len(h.opts.Backoff)-1)
	return h.opts.Backoff[i]
}

func (h *Handler) fail(d Decision) Decision {
	h.count(func(s *Stats) {
		switch d.Action {
		case DeadLetter:
			s.DeadLettered++
		case Retry:
			s.Retried++
		case Ack:
		}
		s.LastError = d.Err.Error()
		s.LastErrorAt = h.now()
	})
	return d
}

func (h *Handler) count(fn func(*Stats)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(&h.stats)
}

// Snapshot mengembalikan salinan statistik.
func (h *Handler) Snapshot() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats
}
