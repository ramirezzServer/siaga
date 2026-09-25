// Package sweep adalah use case sapuan berkala satu sumber per kode wilayah,
// misal prakiraan cuaca BMKG untuk ±5.900 kelurahan/desa Jawa Barat: satu
// request per kode, dibatasi anggaran sendiri dan hanya memakai sisa
// anggaran bersama, dengan conditional request dan penerbitan hanya untuk
// prakiraan yang berubah.
package sweep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/schedule"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Source adalah sumber yang disapu per kode (dipenuhi bmkg.ForecastSource).
type Source interface {
	Name() string
	ArchiveExt() string
	Request(code string) ports.Request
	// Parse membaca dan memvalidasi payload satu kode. Galat berarti payload
	// ditolak dan tidak dicoba ulang di sapuan yang sama.
	Parse(code string, body []byte, fetchedAt time.Time) (ports.Event, error)
}

// Options mengatur Sweeper.
type Options struct {
	// Interval adalah jarak antar-awal sapuan. Sapuan yang lebih lama dari
	// Interval langsung disusul sapuan berikutnya.
	Interval time.Duration
	// Retries adalah jumlah putaran ulang untuk kode yang gagal, di akhir sapuan.
	Retries int
	// PauseAfter kegagalan beruntun membuat sapuan dijeda (sumber kemungkinan
	// sedang mati); jedanya naik eksponensial dari PauseBase sampai PauseMax.
	PauseAfter          int
	PauseBase, PauseMax time.Duration
	// Limit membatasi jumlah kode per sapuan (0 = semua), untuk rekaman dan uji.
	Limit int
	// MaxSamples membatasi contoh kode di Status.
	MaxSamples int
	// Observe boleh nil. Dipanggil untuk setiap kode setelah izin anggaran
	// didapat; lihat ItemObserver.
	Observe ItemObserver
}

// ItemObserver dipanggil di awal pengambilan satu kode. Context yang
// dikembalikan dipakai pengambilan itu (misal membawa span trace), dan fungsi
// yang dikembalikan dipanggil sekali di akhir dengan hasilnya (lihat
// Outcome*) dan galatnya. Dipakai adapter telemetri.
type ItemObserver func(ctx context.Context, code string) (context.Context, func(outcome string, err error))

// DefaultOptions sesuai dokumen arsitektur: sapuan tiap 6 jam.
func DefaultOptions() Options {
	return Options{
		Interval: 6 * time.Hour, Retries: 1, PauseAfter: 10,
		PauseBase: time.Minute, PauseMax: 15 * time.Minute, MaxSamples: 20,
	}
}

// Progress adalah hitungan satu sapuan.
type Progress struct {
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
	Total       int       `json:"total"`
	Done        int       `json:"done"`
	Published   int       `json:"published"`
	Duplicates  int       `json:"duplicates"`
	Unchanged   int       `json:"unchanged"`
	NotModified int       `json:"not_modified"`
	NotFound    int       `json:"not_found"`
	Rejected    int       `json:"rejected"`
	Failed      int       `json:"failed"`
	Pauses      int       `json:"pauses"`
	// Contoh kode yang tidak dikenal sumber (404), ditolak, atau gagal.
	NotFoundSample []string `json:"not_found_sample,omitempty"`
	RejectedSample []string `json:"rejected_sample,omitempty"`
	FailedSample   []string `json:"failed_sample,omitempty"`
}

// Status adalah keadaan Sweeper untuk endpoint /status.
type Status struct {
	Connector           string        `json:"connector"`
	Codes               int           `json:"codes"`
	Interval            time.Duration `json:"interval_ns"`
	Completed           int           `json:"sweeps_completed"`
	Current             *Progress     `json:"current,omitempty"`
	Last                *Progress     `json:"last,omitempty"`
	NextAt              time.Time     `json:"next_at,omitzero"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
	PausedUntil         time.Time     `json:"paused_until,omitzero"`
	LastError           string        `json:"last_error,omitempty"`
}

// Sweeper menjalankan sapuan. Run dan Sweep tidak boleh dipanggil bersamaan;
// Snapshot aman dipanggil dari goroutine lain.
type Sweeper struct {
	src      Source
	codes    []string
	fetch    ports.Fetcher
	archive  ports.Archive // nil berarti arsip dimatikan
	pub      ports.Publisher
	clock    ports.Clock
	throttle ports.Throttle
	log      *slog.Logger
	opts     Options

	etag map[string]string // validator terakhir per kode
	seen map[string]string // hash isi terakhir yang terbit per kode

	mu     sync.Mutex
	status Status
}

// New membuat Sweeper untuk codes (urutan dipertahankan).
func New(src Source, codes []string, fetch ports.Fetcher, archive ports.Archive, pub ports.Publisher,
	clock ports.Clock, throttle ports.Throttle, log *slog.Logger, opts Options,
) (*Sweeper, error) {
	if len(codes) == 0 {
		return nil, errors.New("sapuan tanpa kode wilayah")
	}
	if opts.Interval <= 0 || opts.PauseAfter < 1 || opts.PauseBase <= 0 || opts.PauseMax < opts.PauseBase || opts.Retries < 0 || opts.Limit < 0 {
		return nil, fmt.Errorf("opsi sapuan tidak valid: %+v", opts)
	}
	n := len(codes)
	if opts.Limit > 0 {
		n = min(n, opts.Limit)
	}
	return &Sweeper{
		src: src, codes: codes[:n], fetch: fetch, archive: archive, pub: pub, clock: clock,
		throttle: throttle, log: log.With(slog.String("connector", src.Name())), opts: opts,
		etag: map[string]string{}, seen: map[string]string{},
		status: Status{Connector: src.Name(), Codes: n, Interval: opts.Interval},
	}, nil
}

// Name mengembalikan nama konektor.
func (s *Sweeper) Name() string { return s.src.Name() }

// Snapshot mengembalikan salinan status.
func (s *Sweeper) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	if st.Current != nil {
		c := *st.Current
		st.Current = &c
	}
	if st.Last != nil {
		l := *st.Last
		st.Last = &l
	}
	return st
}

// Run menjalankan sapuan setiap Interval sampai ctx selesai.
func (s *Sweeper) Run(ctx context.Context) {
	for {
		start := s.clock.Now()
		if _, err := s.Sweep(ctx); err != nil {
			return
		}
		next := start.Add(s.opts.Interval)
		s.update(func(st *Status) { st.NextAt = next.UTC() })
		if wait := next.Sub(s.clock.Now()); wait > 0 {
			if err := s.clock.Sleep(ctx, wait); err != nil {
				return
			}
		}
	}
}

type outcome int

const (
	published outcome = iota + 1
	duplicate
	unchanged
	notModified
	notFound
	rejected
	failed
)

// Nama hasil pengambilan satu kode untuk ItemObserver dan metrik.
const (
	OutcomePublished   = "published"
	OutcomeDuplicate   = "duplicate"
	OutcomeUnchanged   = "unchanged"
	OutcomeNotModified = "not_modified"
	OutcomeNotFound    = "not_found"
	OutcomeRejected    = "rejected"
	OutcomeFailed      = "failed"
)

func (o outcome) String() string {
	switch o {
	case published:
		return OutcomePublished
	case duplicate:
		return OutcomeDuplicate
	case unchanged:
		return OutcomeUnchanged
	case notModified:
		return OutcomeNotModified
	case notFound:
		return OutcomeNotFound
	case rejected:
		return OutcomeRejected
	case failed:
		return OutcomeFailed
	default:
		return "unknown"
	}
}

// Sweep menjalankan satu sapuan penuh. Galat hanya bila ctx dibatalkan.
func (s *Sweeper) Sweep(ctx context.Context) (Progress, error) {
	p := &Progress{StartedAt: s.clock.Now().UTC(), Total: len(s.codes)}
	s.update(func(st *Status) { st.Current = p; st.NextAt = time.Time{} })
	s.log.Info("sapuan dimulai", slog.Int("codes", p.Total))

	queue := s.codes
	for round := 0; round <= s.opts.Retries && len(queue) > 0; round++ {
		var retry []string
		for _, code := range queue {
			out, err := s.item(ctx, code)
			if ctx.Err() != nil {
				s.finish(p, true)
				return *p, ctx.Err()
			}
			last := round == s.opts.Retries
			s.count(p, code, out, err, last)
			if out == failed {
				retry = append(retry, code)
				if err := s.maybePause(ctx, p, err); err != nil {
					s.finish(p, true)
					return *p, err
				}
			}
		}
		queue = retry
	}
	s.finish(p, false)
	return *p, nil
}

// item menunggu izin anggaran lalu mengambil satu kode.
func (s *Sweeper) item(ctx context.Context, code string) (outcome, error) {
	if err := s.throttle.Wait(ctx); err != nil {
		return failed, err
	}
	if s.opts.Observe == nil {
		return s.one(ctx, code)
	}
	ictx, done := s.opts.Observe(ctx, code)
	out, err := s.one(ictx, code)
	done(out.String(), err)
	return out, err
}

// one mengambil satu kode.
func (s *Sweeper) one(ctx context.Context, code string) (outcome, error) {
	req := s.src.Request(code)
	req.ETag = s.etag[code]
	resp, err := s.fetch.Fetch(ctx, req)
	if err != nil {
		if sc, ok := errors.AsType[ports.StatusCoder](err); ok && sc.HTTPStatus() == http.StatusNotFound {
			delete(s.etag, code)
			return notFound, err
		}
		return failed, err
	}
	if resp.NotModified {
		return notModified, nil
	}
	fetchedAt := s.clock.Now().UTC()
	ev, err := s.src.Parse(code, resp.Body, fetchedAt)
	if err != nil {
		return rejected, err
	}
	content, sum, err := emit.Content(ev)
	if err != nil {
		return rejected, err
	}
	if s.seen[code] == sum {
		s.etag[code] = resp.ETag
		return unchanged, nil
	}
	bodySum := emit.Sum(resp.Body)
	meta := ports.FetchMeta{Connector: s.src.Name(), FetchedAt: fetchedAt, PayloadSHA256: bodySum}
	if s.archive != nil {
		if key, err := emit.Archive(ctx, s.archive, s.src.Name(), s.src.ArchiveExt(), bodySum, resp.Body, fetchedAt); err != nil {
			s.log.WarnContext(ctx, "arsip gagal, event tetap diterbitkan", slog.String("code", code), slog.Any("error", err))
		} else {
			meta.ArchiveKey = key
		}
	}
	ack, err := emit.Publish(ctx, s.pub, ev, content, meta)
	if err != nil {
		return failed, err
	}
	s.seen[code] = sum
	// Validator baru disimpan setelah terbit; kalau lebih awal, sumber akan
	// menjawab 304 dan prakiraan yang gagal terbit tidak pernah dicoba lagi.
	s.etag[code] = resp.ETag
	if ack.Duplicate {
		return duplicate, nil
	}
	return published, nil
}

func (s *Sweeper) count(p *Progress, code string, out outcome, err error, lastRound bool) {
	sample := func(list *[]string) {
		if len(*list) < s.opts.MaxSamples {
			*list = append(*list, code)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &s.status
	if out != failed || lastRound {
		p.Done++
	}
	switch out {
	case published:
		p.Published++
	case duplicate:
		p.Duplicates++
	case unchanged:
		p.Unchanged++
	case notModified:
		p.NotModified++
	case notFound:
		p.NotFound++
		sample(&p.NotFoundSample)
	case rejected:
		p.Rejected++
		sample(&p.RejectedSample)
		s.log.Warn("prakiraan ditolak", slog.String("code", code), slog.Any("reason", err))
	case failed:
		st.ConsecutiveFailures++
		st.LastError = fmt.Sprintf("%s: %v", code, err)
		if lastRound {
			p.Failed++
			sample(&p.FailedSample)
		}
		return
	}
	st.ConsecutiveFailures = 0
}

// maybePause menjeda sapuan bila sumber meminta melambat atau kegagalan
// beruntun mencapai PauseAfter, lalu melanjutkan dari kode berikutnya.
func (s *Sweeper) maybePause(ctx context.Context, p *Progress, err error) error {
	var retryAfter time.Duration
	ra, isRetryAfter := errors.AsType[*ports.RetryAfterError](err)
	if isRetryAfter {
		retryAfter = ra.After
	}
	s.mu.Lock()
	consecutive := s.status.ConsecutiveFailures
	s.mu.Unlock()
	if !isRetryAfter && consecutive < s.opts.PauseAfter {
		return nil
	}
	s.mu.Lock()
	p.Pauses++
	pauses := p.Pauses
	s.mu.Unlock()
	d := schedule.Next(s.opts.PauseBase, pauses-1, s.opts.PauseMax, retryAfter)
	until := s.clock.Now().Add(d)
	s.update(func(st *Status) { st.PausedUntil = until.UTC() })
	s.log.Warn("sapuan dijeda", slog.Duration("pause", d), slog.Int("consecutive_failures", consecutive), slog.Any("error", err))
	if err := s.clock.Sleep(ctx, d); err != nil {
		return err
	}
	s.update(func(st *Status) { st.PausedUntil = time.Time{} })
	return nil
}

func (s *Sweeper) finish(p *Progress, cancelled bool) {
	now := s.clock.Now().UTC()
	s.update(func(st *Status) {
		p.FinishedAt = now
		st.Current = nil
		if !cancelled {
			st.Completed++
			done := *p
			st.Last = &done
		}
	})
	if cancelled {
		return
	}
	s.log.Info("sapuan selesai",
		slog.Duration("elapsed", p.FinishedAt.Sub(p.StartedAt)), slog.Int("total", p.Total),
		slog.Int("published", p.Published), slog.Int("duplicates", p.Duplicates),
		slog.Int("unchanged", p.Unchanged), slog.Int("not_modified", p.NotModified),
		slog.Int("not_found", p.NotFound), slog.Int("rejected", p.Rejected),
		slog.Int("failed", p.Failed), slog.Int("pauses", p.Pauses), slog.Any("not_found_sample", p.NotFoundSample))
}

func (s *Sweeper) update(fn func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.status)
}
