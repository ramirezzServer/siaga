// Package replay memutar ulang payload mentah dari arsip menjadi event raw.*,
// seolah-olah payload itu baru diambil dari sumbernya. Dipakai untuk uji
// replay (kejadian nyata masa lalu melewati pipa yang sama), untuk mengisi
// lingkungan baru, dan untuk memulihkan stream RAW yang hilang.
//
// Payload dari semua feed digabung dan diputar menurut waktu pengambilan,
// jadi urutan relatif antar-sumber (misal laporan BMKG sebelum USGS) sama
// dengan aslinya. Event diterbitkan dengan FetchMeta asli (waktu ambil, kunci
// arsip, SHA-256 payload), sehingga ID pesannya sama dengan saat polling
// langsung dan JetStream menolak pesan yang sudah pernah diterima.
package replay

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Feed adalah konektor yang satu payload-nya cukup untuk menghasilkan event,
// tanpa konteks request (dipenuhi semua ports.Connector). Payload dibaca dari
// awalan arsip bernama sama dengan feed.
type Feed interface {
	Name() string
	Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error)
}

// MultiFeed adalah feed yang payload-nya tersimpan di beberapa awalan arsip,
// misal daftar stasiun OpenAQ (openaq-stasiun) dan nilai terbaru per stasiun
// (openaq-stasiun-latest). Payload semua awalan diputar urut waktu ambil
// lewat ParseFrom, jadi feed bisa menyimpan konteks dari payload sebelumnya
// (daftar stasiun terakhir). Name tetap dipakai di FetchMeta dan ID pesan,
// sama dengan saat polling langsung.
type MultiFeed interface {
	Feed
	// Archives adalah nama konektor di kunci arsip, minimal satu.
	Archives() []string
	// ParseFrom membaca payload dari awalan archive.
	ParseFrom(archive string, body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error)
}

// PartialFeed dipenuhi feed yang satu payload-nya hanya memuat sebagian
// record, misal satu desa per payload sapuan prakiraan atau satu stasiun per
// payload nilai terbaru. Record yang tidak ada di payload tidak dilupakan,
// sehingga isi yang sama tidak terbit ulang (sama dengan use case sweep dan
// stations yang mengingat isi terakhir per kode).
type PartialFeed interface {
	Feed
	Partial() bool
}

// archives mengembalikan awalan arsip f.
func archives(f Feed) []string {
	if m, ok := f.(MultiFeed); ok {
		return m.Archives()
	}
	return []string{f.Name()}
}

func partial(f Feed) bool {
	p, ok := f.(PartialFeed)
	return ok && p.Partial()
}

func parse(f Feed, archive string, body []byte, at time.Time) ([]ports.Event, []ports.Rejection, error) {
	if m, ok := f.(MultiFeed); ok {
		return m.ParseFrom(archive, body, at)
	}
	return f.Parse(body, at)
}

// Options mengatur Run.
type Options struct {
	// From dan To membatasi waktu pengambilan: From <= t < To, presisi detik.
	// Nol berarti tanpa batas.
	From, To time.Time
	// Speed 0 memutar secepatnya; 1 mengikuti jarak waktu asli antar-payload;
	// 60 berarti satu jam asli diputar dalam satu menit.
	Speed float64
	// MaxPayload membatasi payload hasil dekompresi; nol = emit.DefaultMaxPayload.
	MaxPayload int64
	// Progress, bila diisi, dipanggil setelah setiap payload.
	Progress func(FeedReport)
}

// MaxSamples membatasi contoh galat per feed di laporan.
const MaxSamples = 10

// FeedReport adalah hitungan satu feed.
type FeedReport struct {
	Connector string `json:"connector"`
	Payloads  int    `json:"payloads"`
	Events    int    `json:"events"`
	Published int    `json:"published"`
	// Duplicates ditolak broker karena ID pesan sudah ada.
	Duplicates int `json:"duplicates"`
	// AlreadySeen: isi record sama dengan payload sebelumnya, tidak diterbitkan
	// (sama dengan perilaku poll).
	AlreadySeen int `json:"already_seen"`
	Rejected    int `json:"rejected"`
	// Corrupt: kunci tidak baku, gzip rusak, atau SHA-256 tidak cocok.
	Corrupt int `json:"corrupt"`
	// ParseErrors: payload utuh tetapi formatnya tidak terbaca konektor.
	ParseErrors int       `json:"parse_errors"`
	First       time.Time `json:"first,omitzero"`
	Last        time.Time `json:"last,omitzero"`
	Samples     []string  `json:"samples,omitempty"`
}

func (r *FeedReport) sample(err error) {
	if len(r.Samples) < MaxSamples {
		r.Samples = append(r.Samples, err.Error())
	}
}

// Report merangkum satu replay.
type Report struct {
	Feeds []FeedReport `json:"feeds"`
}

// Totals menjumlahkan semua feed.
func (r Report) Totals() FeedReport {
	t := FeedReport{Connector: "total"}
	for _, f := range r.Feeds {
		t.Payloads += f.Payloads
		t.Events += f.Events
		t.Published += f.Published
		t.Duplicates += f.Duplicates
		t.AlreadySeen += f.AlreadySeen
		t.Rejected += f.Rejected
		t.Corrupt += f.Corrupt
		t.ParseErrors += f.ParseErrors
	}
	return t
}

// Replayer memutar ulang arsip ke publisher.
type Replayer struct {
	archive ports.ArchiveReader
	pub     ports.Publisher
	clock   ports.Clock
}

// New membuat Replayer.
func New(archive ports.ArchiveReader, pub ports.Publisher, clock ports.Clock) *Replayer {
	return &Replayer{archive: archive, pub: pub, clock: clock}
}

// ErrOptions menandai opsi yang tidak masuk akal.
var ErrOptions = errors.New("opsi replay tidak valid")

// stream adalah satu awalan arsip milik satu feed.
type stream struct {
	feed    int
	archive string
	next    func() (ports.ArchiveObject, error, bool)
}

// head adalah objek berikutnya dari satu stream.
type head struct {
	feed    int
	archive string
	key     archivekey.Key
	obj     ports.ArchiveObject
}

// Run memutar semua payload feeds di rentang waktu opts. Payload yang rusak
// atau tidak terbaca dilewati dan dicatat; galat arsip atau publisher
// menghentikan replay (laporan sampai titik itu tetap dikembalikan).
func (r *Replayer) Run(ctx context.Context, feeds []Feed, opts Options) (Report, error) {
	rep := Report{Feeds: make([]FeedReport, len(feeds))}
	if err := validate(feeds, opts); err != nil {
		return rep, err
	}
	if opts.MaxPayload == 0 {
		opts.MaxPayload = emit.DefaultMaxPayload
	}
	var streams []stream
	seen := make([]map[string]string, len(feeds))
	for i, f := range feeds {
		rep.Feeds[i].Connector = f.Name()
		seen[i] = map[string]string{}
		for _, a := range archives(f) {
			startAfter := ""
			if !opts.From.IsZero() {
				startAfter = archivekey.Lower(a, opts.From)
			}
			next, stop := iter.Pull2(r.archive.List(ctx, a+"/", startAfter))
			defer stop()
			streams = append(streams, stream{feed: i, archive: a, next: next})
		}
	}
	heads := make([]*head, len(streams))
	advance := func(i int) error {
		heads[i] = nil
		s := streams[i]
		for {
			obj, err, ok := s.next()
			switch {
			case !ok:
				return nil
			case err != nil:
				return fmt.Errorf("mendaftar arsip %s: %w", s.archive, err)
			}
			if !opts.To.IsZero() && obj.Key >= archivekey.Lower(s.archive, opts.To) {
				return nil
			}
			k, err := archivekey.Parse(obj.Key)
			if err != nil || k.Connector != s.archive {
				// Objek asing di bawah awalan konektor (bukan hasil emit.Archive).
				rep.Feeds[s.feed].Corrupt++
				rep.Feeds[s.feed].sample(fmt.Errorf("%w: kunci %s", emit.ErrCorrupt, obj.Key))
				continue
			}
			heads[i] = &head{feed: s.feed, archive: s.archive, key: k, obj: obj}
			return nil
		}
	}
	for i := range streams {
		if err := advance(i); err != nil {
			return rep, err
		}
	}

	var t0, w0 time.Time
	for {
		i := earliest(heads)
		if i < 0 {
			return rep, nil
		}
		h := heads[i]
		if opts.Speed > 0 {
			if t0.IsZero() {
				t0, w0 = h.key.FetchedAt, r.clock.Now()
			}
			due := w0.Add(time.Duration(float64(h.key.FetchedAt.Sub(t0)) / opts.Speed))
			if wait := due.Sub(r.clock.Now()); wait > 0 {
				if err := r.clock.Sleep(ctx, wait); err != nil {
					return rep, err
				}
			}
		}
		if err := r.play(ctx, feeds[h.feed], h, seen[h.feed], &rep.Feeds[h.feed], opts.MaxPayload); err != nil {
			return rep, err
		}
		if opts.Progress != nil {
			opts.Progress(rep.Feeds[h.feed])
		}
		if err := advance(i); err != nil {
			return rep, err
		}
	}
}

func validate(feeds []Feed, opts Options) error {
	var errs []error
	if len(feeds) == 0 {
		errs = append(errs, errors.New("tidak ada feed"))
	}
	names, owners := map[string]bool{}, map[string]string{}
	for _, f := range feeds {
		if names[f.Name()] {
			errs = append(errs, fmt.Errorf("feed %s ganda", f.Name()))
		}
		names[f.Name()] = true
		as := archives(f)
		if len(as) == 0 {
			errs = append(errs, fmt.Errorf("feed %s tanpa awalan arsip", f.Name()))
		}
		for _, a := range as {
			if a == "" || strings.Contains(a, "/") {
				errs = append(errs, fmt.Errorf("awalan arsip %q feed %s kosong atau memuat /", a, f.Name()))
			}
			if o, dup := owners[a]; dup && o != f.Name() {
				errs = append(errs, fmt.Errorf("awalan arsip %s dipakai feed %s dan %s", a, o, f.Name()))
			} else if dup {
				errs = append(errs, fmt.Errorf("awalan arsip %s ganda di feed %s", a, f.Name()))
			}
			owners[a] = f.Name()
		}
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && !opts.From.Before(opts.To) {
		errs = append(errs, fmt.Errorf("from %s harus sebelum to %s", opts.From, opts.To))
	}
	if opts.Speed < 0 {
		errs = append(errs, fmt.Errorf("speed %v negatif", opts.Speed))
	}
	if opts.MaxPayload < 0 {
		errs = append(errs, errors.New("MaxPayload negatif"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w: %w", ErrOptions, err)
	}
	return nil
}

// earliest memilih indeks objek dengan waktu ambil paling awal (-1 bila
// semua habis); seri diputus nama konektor arsip lalu kunci supaya urutan
// replay deterministik.
func earliest(heads []*head) int {
	best := -1
	for i, h := range heads {
		if h == nil {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		b := heads[best]
		if h.key.FetchedAt.Before(b.key.FetchedAt) ||
			h.key.FetchedAt.Equal(b.key.FetchedAt) && (h.key.Connector < b.key.Connector ||
				h.key.Connector == b.key.Connector && h.obj.Key < b.obj.Key) {
			best = i
		}
	}
	return best
}

// play memutar satu payload dengan aturan yang sama seperti poll: hanya
// record yang isinya berubah sejak payload sebelumnya yang diterbitkan. Untuk
// PartialFeed, pembanding adalah isi terakhir per record dari semua payload
// sebelumnya.
func (r *Replayer) play(ctx context.Context, f Feed, h *head, seen map[string]string, rep *FeedReport, maxPayload int64) error {
	rep.Payloads++
	if rep.First.IsZero() {
		rep.First = h.key.FetchedAt
	}
	rep.Last = h.key.FetchedAt
	data, err := r.archive.Get(ctx, h.obj.Key)
	if err != nil {
		return fmt.Errorf("membaca arsip %s: %w", h.obj.Key, err)
	}
	k, body, sum, err := emit.Unarchive(h.obj.Key, data, maxPayload)
	if err != nil {
		rep.Corrupt++
		rep.sample(err)
		return nil
	}
	events, rejections, err := parse(f, h.archive, body, k.FetchedAt)
	if err != nil {
		rep.ParseErrors++
		rep.sample(fmt.Errorf("%s: %w", h.obj.Key, err))
		return nil
	}
	rep.Events += len(events)
	rep.Rejected += len(rejections)
	meta := ports.FetchMeta{Connector: f.Name(), FetchedAt: k.FetchedAt, ArchiveKey: h.obj.Key, PayloadSHA256: sum}
	current := make(map[string]string, len(events))
	for _, ev := range events {
		content, contentSum, err := emit.Content(ev)
		if err != nil {
			rep.ParseErrors++
			rep.sample(fmt.Errorf("%s: %w", h.obj.Key, err))
			continue
		}
		current[ev.Key()] = contentSum
		if seen[ev.Key()] == contentSum {
			rep.AlreadySeen++
			continue
		}
		ack, err := emit.Publish(ctx, r.pub, ev, content, meta)
		if err != nil {
			return err
		}
		if ack.Duplicate {
			rep.Duplicates++
		} else {
			rep.Published++
		}
	}
	if !partial(f) {
		clear(seen)
	}
	for k, v := range current {
		seen[k] = v
	}
	return nil
}

// Names mengembalikan nama feeds, untuk pesan galat dan bantuan CLI.
func Names(feeds []Feed) []string {
	out := make([]string, len(feeds))
	for i, f := range feeds {
		out[i] = f.Name()
	}
	slices.Sort(out)
	return out
}
