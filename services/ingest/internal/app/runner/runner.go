// Package runner menjadwalkan polling semua konektor: satu goroutine per
// konektor, anggaran request bersama per sumber, backoff saat gagal, dan
// status kesehatan tiap konektor untuk endpoint /status.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/ratelimit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/schedule"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Limiter adalah anggaran request bersama, misal satu untuk semua endpoint
// BMKG karena batasnya per IP. Aman dipakai banyak goroutine.
type Limiter struct {
	name string
	mu   sync.Mutex
	b    *ratelimit.Bucket
}

// NewLimiter membuat anggaran perMinute request per menit dengan lonjakan burst.
func NewLimiter(name string, perMinute, burst int) (*Limiter, error) {
	b, err := ratelimit.New(perMinute, burst)
	if err != nil {
		return nil, fmt.Errorf("anggaran %s: %w", name, err)
	}
	return &Limiter{name: name, b: b}, nil
}

// Name mengembalikan nama anggaran.
func (l *Limiter) Name() string { return l.name }

func (l *Limiter) reserve(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Reserve(now)
}

// Poller adalah satu konektor yang bisa dipolling (dipenuhi *poll.Poller).
type Poller interface {
	Name() string
	Poll(ctx context.Context) (poll.Result, error)
}

// Job adalah satu konektor beserta jadwal dan anggarannya.
type Job struct {
	Poller   Poller
	Interval time.Duration
	// Semua anggaran harus mengizinkan sebelum request dikirim.
	Limiters []*Limiter
}

// Status adalah kesehatan satu konektor.
type Status struct {
	Connector           string        `json:"connector"`
	Interval            time.Duration `json:"interval_ns"`
	LastAttempt         time.Time     `json:"last_attempt,omitzero"`
	LastSuccess         time.Time     `json:"last_success,omitzero"`
	LastChange          time.Time     `json:"last_change,omitzero"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
	LastError           string        `json:"last_error,omitempty"`
	Published           int           `json:"published_total"`
	Rejected            int           `json:"rejected_total"`
}

// Options mengatur Runner.
type Options struct {
	// MaxBackoff adalah jeda terlama antar-polling saat konektor terus gagal.
	MaxBackoff time.Duration
	// JitterFrac menggeser tiap jeda acak ±JitterFrac (0..0.5).
	JitterFrac float64
	// Rand menghasilkan bilangan acak di [0,1); nil memakai math/rand/v2.
	Rand func() float64
}

// Runner menjalankan Job.
type Runner struct {
	clock ports.Clock
	log   *slog.Logger
	opt   Options

	mu     sync.Mutex
	status map[string]*Status
}

// New membuat Runner.
func New(clock ports.Clock, log *slog.Logger, opt Options) *Runner {
	if opt.Rand == nil {
		opt.Rand = rand.Float64
	}
	if opt.MaxBackoff <= 0 {
		opt.MaxBackoff = 10 * time.Minute
	}
	return &Runner{clock: clock, log: log, opt: opt, status: map[string]*Status{}}
}

// Run menjalankan semua Job sampai ctx selesai.
func (r *Runner) Run(ctx context.Context, jobs []Job) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		r.register(j)
		wg.Go(func() { r.loop(ctx, j) })
	}
	wg.Wait()
}

// RunOnce menjalankan setiap Job tepat sekali (tetap menghormati anggaran)
// dan mengembalikan gabungan galatnya. Dipakai untuk merekam payload.
func (r *Runner) RunOnce(ctx context.Context, jobs []Job) error {
	var errs []error
	for _, j := range jobs {
		r.register(j)
		if err := r.acquire(ctx, j.Limiters); err != nil {
			return err
		}
		if _, err := r.pollOnce(ctx, j); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Snapshot mengembalikan salinan status semua konektor, urut nama.
func (r *Runner) Snapshot() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.status))
	for _, s := range r.status {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Connector < out[j].Connector })
	return out
}

func (r *Runner) register(j Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.status[j.Poller.Name()]; !ok {
		r.status[j.Poller.Name()] = &Status{Connector: j.Poller.Name(), Interval: j.Interval}
	}
}

func (r *Runner) loop(ctx context.Context, j Job) {
	for {
		if err := r.acquire(ctx, j.Limiters); err != nil {
			return
		}
		st, err := r.pollOnce(ctx, j)
		if ctx.Err() != nil {
			return
		}
		var retryAfter time.Duration
		if ra, ok := errors.AsType[*ports.RetryAfterError](err); ok {
			retryAfter = ra.After
		}
		next := schedule.Next(j.Interval, st.ConsecutiveFailures, r.opt.MaxBackoff, retryAfter)
		next = schedule.Jitter(next, r.opt.JitterFrac, r.opt.Rand())
		if err := r.clock.Sleep(ctx, next); err != nil {
			return
		}
	}
}

func (r *Runner) acquire(ctx context.Context, limiters []*Limiter) error {
	now := r.clock.Now()
	var wait time.Duration
	for _, l := range limiters {
		wait = max(wait, l.reserve(now))
	}
	if wait == 0 {
		return nil
	}
	return r.clock.Sleep(ctx, wait)
}

// pollOnce menjalankan satu polling, memperbarui status, dan menulis log.
// Mengembalikan salinan status setelah polling.
func (r *Runner) pollOnce(ctx context.Context, j Job) (Status, error) {
	name := j.Poller.Name()
	start := r.clock.Now()
	res, err := j.Poller.Poll(ctx)
	elapsed := r.clock.Now().Sub(start)

	r.mu.Lock()
	st := r.status[name]
	st.LastAttempt = start.UTC()
	st.Published += res.Published
	st.Rejected += res.Rejected
	if err != nil {
		st.ConsecutiveFailures++
		st.LastError = err.Error()
	} else {
		st.ConsecutiveFailures = 0
		st.LastError = ""
		st.LastSuccess = start.UTC()
		if !res.NotModified && !res.Unchanged {
			st.LastChange = start.UTC()
		}
	}
	snap := *st
	r.mu.Unlock()

	attrs := []any{
		slog.String("connector", name), slog.Duration("elapsed", elapsed),
		slog.Int("events", res.Events), slog.Int("published", res.Published),
		slog.Int("duplicates", res.Duplicates), slog.Int("already_seen", res.AlreadySeen),
		slog.Int("rejected", res.Rejected), slog.Bool("not_modified", res.NotModified),
		slog.Bool("unchanged", res.Unchanged), slog.String("archive_key", res.ArchiveKey),
	}
	switch {
	case err != nil && ctx.Err() != nil:
		// Dimatikan; bukan kegagalan sumber.
	case err != nil:
		level := slog.LevelWarn
		if snap.ConsecutiveFailures >= 3 {
			level = slog.LevelError
		}
		r.log.Log(ctx, level, "polling gagal", append(attrs,
			slog.Int("consecutive_failures", snap.ConsecutiveFailures), slog.Any("error", err))...)
	case res.Published > 0 || res.Rejected > 0:
		r.log.Info("polling selesai", attrs...)
	default:
		r.log.Debug("polling selesai", attrs...)
	}
	for _, rej := range res.Rejections {
		r.log.Warn("record ditolak", slog.String("connector", name), slog.String("key", rej.Key), slog.Any("reason", rej.Reason))
	}
	if res.ArchiveErr != nil {
		r.log.Warn("arsip gagal, event tetap diterbitkan", slog.String("connector", name), slog.Any("error", res.ArchiveErr))
	}
	return snap, err
}
