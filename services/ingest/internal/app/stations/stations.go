// Package stations adalah use case polling stasiun pengukur kualitas udara:
// ambil daftar stasiun dalam wilayah (satu request), lalu ambil nilai
// terbaru hanya untuk stasiun aktif yang melapor data baru sejak polling
// sebelumnya, dan terbitkan satu event per stasiun.
//
// Sumber menyebut waktu laporan terakhir tiap stasiun di daftar, jadi stasiun
// yang tidak berubah tidak menghabiskan request. Stasiun yang lama diam
// (banyak lokasi lama di OpenAQ) dilewati tanpa request.
package stations

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Station adalah satu entri daftar stasiun.
type Station struct {
	airquality.Station
	// LastReport adalah waktu laporan terakhir menurut daftar; nol bila
	// sumber tidak menyebutnya.
	LastReport time.Time
}

// Source adalah sumber stasiun (dipenuhi adapter openaq.Source).
type Source interface {
	Name() string
	ListRequest() ports.Request
	// ParseList membaca daftar stasiun. Hanya stasiun yang punya sensor
	// parameter SIAGA; stasiun yang rusak dilaporkan lewat Rejection.
	ParseList(body []byte) ([]Station, []ports.Rejection, error)
	DetailRequest(st Station) ports.Request
	// ParseDetail membaca nilai terbaru satu stasiun tanpa membersihkan.
	ParseDetail(st Station, body []byte) ([]airquality.Reading, error)
	// Event membungkus pengukuran yang sudah valid.
	Event(o airquality.Observation) (ports.Event, error)
}

// Options mengatur Poller.
type Options struct {
	// MaxSilence: stasiun yang laporan terakhirnya lebih tua dari ini dilewati.
	MaxSilence time.Duration
	// MaxAge: nilai sensor yang lebih tua dari ini dibuang.
	MaxAge time.Duration
	// MaxAttempts adalah batas gagal ambil per stasiun sebelum stasiun itu
	// dilewati sampai waktu laporan terakhirnya berubah.
	MaxAttempts int
}

// DefaultOptions mengembalikan bawaan: stasiun aktif bila melapor dalam 2
// hari terakhir; nilai dipakai bila umurnya paling lama 1 hari.
func DefaultOptions() Options {
	return Options{MaxSilence: 48 * time.Hour, MaxAge: 24 * time.Hour, MaxAttempts: 3}
}

type state struct {
	// done: laporan dengan waktu ini sudah selesai diproses (terbit, ditolak,
	// atau menyerah).
	done     time.Time
	attempts int
	// published adalah hash isi event terakhir yang terbit.
	published string
}

// Poller menyimpan status antar-polling. Tidak aman dipakai bersamaan;
// runner menjalankan satu goroutine per konektor.
type Poller struct {
	src      Source
	fetch    ports.Fetcher
	archive  ports.Archive // nil berarti arsip dimatikan
	pub      ports.Publisher
	clock    ports.Clock
	throttle ports.Throttle
	opts     Options

	archivedSum, archivedKey string
	states                   map[string]*state
}

// New membuat Poller. archive boleh nil.
func New(src Source, fetch ports.Fetcher, archive ports.Archive, pub ports.Publisher, clock ports.Clock, throttle ports.Throttle, opts Options) (*Poller, error) {
	d := DefaultOptions()
	if opts.MaxSilence <= 0 {
		opts.MaxSilence = d.MaxSilence
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = d.MaxAge
	}
	if opts.MaxAttempts < 1 {
		opts.MaxAttempts = d.MaxAttempts
	}
	if opts.MaxAge > opts.MaxSilence {
		return nil, fmt.Errorf("umur nilai maksimum %v lebih panjang dari batas diam stasiun %v", opts.MaxAge, opts.MaxSilence)
	}
	return &Poller{
		src: src, fetch: fetch, archive: archive, pub: pub, clock: clock, throttle: throttle,
		opts: opts, states: map[string]*state{},
	}, nil
}

// Name mengembalikan nama konektor.
func (p *Poller) Name() string { return p.src.Name() }

// Poll menjalankan satu polling. Galat hanya untuk kegagalan daftar stasiun
// atau penerbitan; kegagalan per stasiun dicatat di Result.Failures dan
// dicoba lagi di polling berikutnya.
//
// Result.Events adalah jumlah stasiun aktif; stasiun yang lama diam tidak
// dihitung.
func (p *Poller) Poll(ctx context.Context) (poll.Result, error) {
	var res poll.Result
	req := p.src.ListRequest()
	resp, err := p.fetch.Fetch(ctx, req)
	if err == nil && resp.NotModified {
		err = errors.New("sumber menjawab 304 untuk request tanpa validator")
	}
	if err != nil {
		return res, fmt.Errorf("%s: mengambil %s: %w", p.src.Name(), req.Redacted(), err)
	}
	fetchedAt := p.clock.Now().UTC()
	sum := emit.Sum(resp.Body)
	if p.archive != nil {
		if sum == p.archivedSum {
			res.ArchiveKey = p.archivedKey
		} else if key, err := emit.Archive(ctx, p.archive, p.src.Name(), "json", sum, resp.Body, fetchedAt); err != nil {
			res.ArchiveErr = err
		} else {
			res.ArchiveKey, p.archivedSum, p.archivedKey = key, sum, key
		}
	}
	list, rejected, err := p.src.ParseList(resp.Body)
	if err != nil {
		return res, fmt.Errorf("%s: %w: %w", p.src.Name(), poll.ErrParse, err)
	}
	slices.SortFunc(list, func(a, b Station) int { return cmp.Compare(a.ID, b.ID) })
	for _, r := range rejected {
		p.reject(&res, r)
	}

	present := make(map[string]bool, len(list))
	for _, st := range list {
		if present[st.ID] {
			p.reject(&res, ports.Rejection{Key: st.ID, Reason: errors.New("stasiun ganda di daftar")})
			continue
		}
		present[st.ID] = true
		if st.LastReport.IsZero() || st.LastReport.Before(fetchedAt.Add(-p.opts.MaxSilence)) {
			continue
		}
		res.Events++
		s := p.states[st.ID]
		if s == nil {
			s = &state{}
			p.states[st.ID] = s
		}
		if s.done.Equal(st.LastReport) {
			res.AlreadySeen++
			continue
		}
		if err := p.station(ctx, st, s, &res); err != nil {
			return res, err
		}
	}
	for id := range p.states {
		if !present[id] {
			delete(p.states, id)
		}
	}
	return res, nil
}

// station mengambil, membersihkan, dan menerbitkan nilai satu stasiun.
// Galat hanya untuk kegagalan penerbitan atau pembatalan.
func (p *Poller) station(ctx context.Context, st Station, s *state, res *poll.Result) error {
	if err := p.throttle.Wait(ctx); err != nil {
		return err
	}
	req := p.src.DetailRequest(st)
	resp, err := p.fetch.Fetch(ctx, req)
	if err == nil && resp.NotModified {
		err = errors.New("sumber menjawab 304 untuk request tanpa validator")
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.attempts++
		if s.attempts >= p.opts.MaxAttempts {
			s.done, s.attempts = st.LastReport, 0
			err = fmt.Errorf("menyerah setelah %d percobaan: %w", p.opts.MaxAttempts, err)
		}
		p.fail(res, ports.Rejection{Key: st.ID, Reason: fmt.Errorf("mengambil %s: %w", req.Redacted(), err)})
		return nil
	}
	s.attempts = 0
	fetchedAt := p.clock.Now().UTC()
	sum := emit.Sum(resp.Body)
	meta := ports.FetchMeta{Connector: p.src.Name(), FetchedAt: fetchedAt, PayloadSHA256: sum}
	if p.archive != nil {
		if key, err := emit.Archive(ctx, p.archive, p.src.Name()+"-latest", "json", sum, resp.Body, fetchedAt); err != nil {
			res.ArchiveErr = err
		} else {
			meta.ArchiveKey = key
		}
	}

	// Setelah ini laporan dianggap selesai apa pun hasilnya: payload yang
	// rusak tidak akan membaik dengan diulang sebelum sumber melapor lagi.
	s.done = st.LastReport
	raw, err := p.src.ParseDetail(st, resp.Body)
	if err != nil {
		p.reject(res, ports.Rejection{Key: st.ID, Reason: err})
		return nil
	}
	readings, dropped := airquality.Clean(raw, fetchedAt, p.opts.MaxAge)
	for _, d := range dropped {
		if !errors.Is(d, airquality.ErrStale) {
			p.reject(res, ports.Rejection{Key: st.ID, Reason: d})
		}
	}
	if len(readings) == 0 {
		return nil
	}
	obs := airquality.Observation{Station: st.Station, Readings: readings}
	if err := obs.Validate(fetchedAt, p.opts.MaxAge); err != nil {
		p.reject(res, ports.Rejection{Key: st.ID, Reason: err})
		return nil
	}
	ev, err := p.src.Event(obs)
	if err != nil {
		p.reject(res, ports.Rejection{Key: st.ID, Reason: err})
		return nil
	}
	content, contentSum, err := emit.Content(ev)
	if err != nil {
		return fmt.Errorf("%s: %w", p.src.Name(), err)
	}
	if contentSum == s.published {
		res.AlreadySeen++
		return nil
	}
	ack, err := emit.Publish(ctx, p.pub, ev, content, meta)
	if err != nil {
		// Belum terbit: ulangi di polling berikutnya.
		s.done = time.Time{}
		return fmt.Errorf("%s: %w", p.src.Name(), err)
	}
	if ack.Duplicate {
		res.Duplicates++
	} else {
		res.Published++
	}
	s.published = contentSum
	return nil
}

func (p *Poller) reject(res *poll.Result, r ports.Rejection) {
	res.Rejected++
	if len(res.Rejections) < poll.MaxRejectionsKept {
		res.Rejections = append(res.Rejections, r)
	}
}

func (p *Poller) fail(res *poll.Result, r ports.Rejection) {
	res.Failed++
	if len(res.Failures) < poll.MaxRejectionsKept {
		res.Failures = append(res.Failures, r)
	}
}
