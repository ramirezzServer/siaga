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
// tanpa konteks request (dipenuhi semua ports.Connector). Use case dengan
// request per item (CAP, sapuan prakiraan, stasiun OpenAQ) belum bisa
// diputar ulang karena konteks itu tidak ada di kunci arsip (ADR 0014).
type Feed interface {
	Name() string
	Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error)
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

// head adalah objek berikutnya dari satu feed.
type head struct {
	feed int
	key  archivekey.Key
	obj  ports.ArchiveObject
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
	nexts := make([]func() (ports.ArchiveObject, error, bool), len(feeds))
	seen := make([]map[string]string, len(feeds))
	for i, f := range feeds {
		rep.Feeds[i].Connector = f.Name()
		seen[i] = map[string]string{}
		startAfter := ""
		if !opts.From.IsZero() {
			startAfter = archivekey.Lower(f.Name(), opts.From)
		}
		next, stop := iter.Pull2(r.archive.List(ctx, f.Name()+"/", startAfter))
		defer stop()
		nexts[i] = next
	}
	heads := make([]*head, len(feeds))
	advance := func(i int) error {
		heads[i] = nil
		for {
			obj, err, ok := nexts[i]()
			switch {
			case !ok:
				return nil
			case err != nil:
				return fmt.Errorf("mendaftar arsip %s: %w", feeds[i].Name(), err)
			}
			if !opts.To.IsZero() && obj.Key >= archivekey.Lower(feeds[i].Name(), opts.To) {
				return nil
			}
			k, err := archivekey.Parse(obj.Key)
			if err != nil || k.Connector != feeds[i].Name() {
				// Objek asing di bawah awalan konektor (bukan hasil emit.Archive).
				rep.Feeds[i].Corrupt++
				rep.Feeds[i].sample(fmt.Errorf("%w: kunci %s", emit.ErrCorrupt, obj.Key))
				continue
			}
			heads[i] = &head{feed: i, key: k, obj: obj}
			return nil
		}
	}
	for i := range feeds {
		if err := advance(i); err != nil {
			return rep, err
		}
	}

	var t0, w0 time.Time
	for {
		h := earliest(heads)
		if h == nil {
			return rep, nil
		}
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
		if err := advance(h.feed); err != nil {
			return rep, err
		}
	}
}

func validate(feeds []Feed, opts Options) error {
	var errs []error
	if len(feeds) == 0 {
		errs = append(errs, errors.New("tidak ada feed"))
	}
	names := map[string]bool{}
	for _, f := range feeds {
		if names[f.Name()] {
			errs = append(errs, fmt.Errorf("feed %s ganda", f.Name()))
		}
		names[f.Name()] = true
		if strings.Contains(f.Name(), "/") {
			errs = append(errs, fmt.Errorf("nama feed %q memuat /", f.Name()))
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

// earliest memilih objek dengan waktu ambil paling awal; seri diputus nama
// konektor lalu kunci supaya urutan replay deterministik.
func earliest(heads []*head) *head {
	var best *head
	for _, h := range heads {
		if h == nil {
			continue
		}
		if best == nil || h.key.FetchedAt.Before(best.key.FetchedAt) ||
			h.key.FetchedAt.Equal(best.key.FetchedAt) && (h.key.Connector < best.key.Connector ||
				h.key.Connector == best.key.Connector && h.obj.Key < best.obj.Key) {
			best = h
		}
	}
	return best
}

// play memutar satu payload dengan aturan yang sama seperti poll: hanya
// record yang isinya berubah sejak payload sebelumnya yang diterbitkan.
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
	events, rejections, err := f.Parse(body, k.FetchedAt)
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
	clear(seen)
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
