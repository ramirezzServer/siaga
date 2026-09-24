// Package consume memutuskan nasib setiap pesan raw yang diterima consumer:
// diakui, dicoba ulang dengan jeda, atau dipindah ke DLQ. Keputusannya murni
// supaya bisa diuji tanpa broker; adapter JetStream yang menjalankannya.
package consume

import (
	"context"
	"errors"
	"sync"
	"time"
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

// Outcome adalah dampak satu pesan yang berhasil diproses, untuk statistik.
type Outcome struct {
	// Changed false berarti pesan ulangan yang tidak mengubah apa pun.
	Changed bool
	Created int
	Updated int
	Ended   int
	// Rows adalah baris deret waktu yang ditambah atau diubah (consumer ts.*).
	Rows int
}

// Decoder mengubah payload menjadi nilai domain R.
type Decoder[R any] func([]byte) (R, error)

// Processor memproses nilai domain.
type Processor[R any] func(context.Context, R) (Outcome, error)

// Handler memproses pesan satu consumer (misal raw.quake.* atau raw.weather.*).
type Handler[R any] struct {
	decode    Decoder[R]
	process   Processor[R]
	permanent []error
	opts      Options
	now       func() time.Time
	stats     Stats
	mu        sync.Mutex
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
	Rows         int64     `json:"rows"`
	LastSuccess  time.Time `json:"last_success,omitzero"`
	LastError    string    `json:"last_error,omitempty"`
	LastErrorAt  time.Time `json:"last_error_at,omitzero"`
}

// New membuat Handler. permanent adalah galat domain yang tidak akan berhasil
// bila diulang (misal quake.ErrInvalid); pesan dengan galat itu langsung ke DLQ.
func New[R any](decode Decoder[R], process Processor[R], permanent []error, opts Options, now func() time.Time) (*Handler[R], error) {
	if opts.MaxDeliveries < 1 || len(opts.Backoff) == 0 {
		return nil, errors.New("consume: MaxDeliveries >= 1 dan Backoff wajib diisi")
	}
	return &Handler[R]{decode: decode, process: process, permanent: permanent, opts: opts, now: now}, nil
}

func (h *Handler[R]) isPermanent(err error) bool {
	for _, p := range h.permanent {
		if errors.Is(err, p) {
			return true
		}
	}
	return false
}

// Handle memproses satu pesan. delivered adalah nomor pengiriman pesan ini
// (1 untuk pengiriman pertama).
func (h *Handler[R]) Handle(ctx context.Context, data []byte, delivered int) Decision {
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
			s.Rows += int64(res.Rows)
			s.LastSuccess = h.now()
		})
		return Decision{Action: Ack}
	case h.isPermanent(err):
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

func (h *Handler[R]) backoff(delivered int) time.Duration {
	i := min(max(delivered-1, 0), len(h.opts.Backoff)-1)
	return h.opts.Backoff[i]
}

func (h *Handler[R]) fail(d Decision) Decision {
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

func (h *Handler[R]) count(fn func(*Stats)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(&h.stats)
}

// Snapshot mengembalikan salinan statistik.
func (h *Handler[R]) Snapshot() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats
}
