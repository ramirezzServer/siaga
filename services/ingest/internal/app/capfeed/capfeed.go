// Package capfeed adalah use case polling peringatan dini cuaca berformat
// CAP: ambil daftar peringatan (RSS), saring per provinsi, ambil dokumen CAP
// tiap peringatan baru dalam bahasa utama dan bahasa tambahan, lalu terbitkan
// satu event per pesan CAP.
//
// Peringatan lebih penting daripada terjemahan: bila dokumen bahasa tambahan
// gagal diambil, pesan tetap terbit dengan teks bahasa Indonesia, lalu terbit
// ulang sebagai revisi setelah terjemahannya berhasil diambil.
//
// BMKG menimpa file CAP yang sama untuk pesan baru, dan file itu bisa masih
// berisi pesan lama saat entri barunya sudah ada di RSS. Dokumen yang
// identifier-nya bukan guid entrinya adalah dokumen basi: tidak diterbitkan,
// dan diambil lagi di polling berikutnya (ADR 0028).
package capfeed

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Source adalah sumber peringatan CAP (dipenuhi adapter bmkg.CAPFeed).
type Source interface {
	Name() string
	ArchiveExt() string
	FeedRequest() ports.Request
	ParseFeed(body []byte) ([]ports.FeedItem, []ports.Rejection, error)
	DetailRequest(it ports.FeedItem, lang string) ports.Request
	// ParseDetail membaca dokumen CAP tanpa memvalidasi.
	ParseDetail(it ports.FeedItem, lang string, body []byte) (warning.Warning, error)
	// Event membungkus peringatan yang sudah valid.
	Event(w warning.Warning) (ports.Event, error)
}

// Options mengatur Poller.
type Options struct {
	// Provinces membatasi peringatan ke kode provinsi ini (misal "32" untuk
	// Jawa Barat). Kosong berarti semua. Peringatan yang provinsinya tidak
	// bisa dibaca dari ID-nya tetap diambil, supaya tidak hilang diam-diam.
	Provinces []string
	// Languages adalah bahasa tambahan selain bahasa Indonesia, misal ["en"].
	Languages []string
	// MaxAttempts adalah batas gagal ambil per dokumen sebelum dokumen itu
	// dilewati sampai entrinya di daftar berubah.
	MaxAttempts int
	// MaxStaleAttempts adalah batas dokumen basi per bahasa (file masih
	// berisi pesan lain) sebelum peringatan atau terjemahannya dilewati.
	// Bawaan DefaultMaxStaleAttempts.
	MaxStaleAttempts int
}

// DefaultMaxStaleAttempts adalah bawaan MaxStaleAttempts: ±1 jam pada
// interval RSS 2 menit. Satu request per peringatan per polling, jadi
// menunggu lama tidak mahal, sedangkan melewatkan pesan baru berarti
// peringatan tidak pernah terbit.
const DefaultMaxStaleAttempts = 30

// ErrStale menandai dokumen CAP basi: file yang ditautkan entri RSS masih
// berisi pesan lain (identifier ≠ guid), biasanya pesan lama yang belum
// ditimpa BMKG. Poller mengambilnya lagi; replay menolaknya (ADR 0028).
var ErrStale = errors.New("dokumen CAP basi")

// Poller menyimpan status feed antar-polling. Tidak aman dipakai bersamaan;
// runner menjalankan satu goroutine per konektor.
type Poller struct {
	src      Source
	fetch    ports.Fetcher
	archive  ports.Archive // nil berarti arsip dimatikan
	pub      ports.Publisher
	clock    ports.Clock
	throttle ports.Throttle
	opts     Options

	etag, lastModified string
	doneSum            string
	archivedSum        string
	archivedKey        string
	items              map[string]*itemState
}

// doc adalah satu dokumen CAP yang sudah diambil dan dibaca.
type doc struct {
	w          warning.Warning
	fetchedAt  time.Time
	archiveKey string
	sum        string
}

type itemState struct {
	item ports.FeedItem
	// final: peringatan sudah selesai (terbit lengkap, ditolak, dilewati, atau
	// menyerah); tidak ada request lagi sampai entrinya berubah.
	final    bool
	primary  *doc
	extra    map[string]*doc // terjemahan yang berhasil
	settled  map[string]bool // bahasa tambahan yang tidak perlu dicoba lagi
	attempts map[string]int
	stale    map[string]int
	// published adalah hash isi event terakhir yang terbit.
	published string
	// reported adalah jumlah percobaan ulang yang sudah dilaporkan lewat
	// Result.Recovered; revisi tanpa percobaan ulang baru tidak dilaporkan lagi.
	reported int
}

// retries menjumlahkan percobaan ulang semua bahasa: gagal ambil dan
// dokumen basi.
func (st *itemState) retries() int {
	n := 0
	for _, v := range st.attempts {
		n += v
	}
	for _, v := range st.stale {
		n += v
	}
	return n
}

// New membuat Poller. archive boleh nil.
func New(src Source, fetch ports.Fetcher, archive ports.Archive, pub ports.Publisher, clock ports.Clock, throttle ports.Throttle, opts Options) *Poller {
	if opts.MaxAttempts < 1 {
		opts.MaxAttempts = 5
	}
	if opts.MaxStaleAttempts < 1 {
		opts.MaxStaleAttempts = DefaultMaxStaleAttempts
	}
	opts.Languages = slices.DeleteFunc(slices.Clone(opts.Languages), func(l string) bool { return l == warning.LanguagePrimary })
	return &Poller{
		src: src, fetch: fetch, archive: archive, pub: pub, clock: clock, throttle: throttle,
		opts: opts, items: map[string]*itemState{},
	}
}

// Name mengembalikan nama konektor.
func (p *Poller) Name() string { return p.src.Name() }

// Pending mengembalikan jumlah peringatan yang masih menunggu dokumen.
func (p *Poller) Pending() int {
	n := 0
	for _, st := range p.items {
		if !st.final {
			n++
		}
	}
	return n
}

// Poll menjalankan satu polling. Galat hanya untuk kegagalan daftar
// peringatan atau penerbitan; kegagalan dokumen per peringatan dicatat di
// Result.Failures dan dicoba lagi di polling berikutnya.
func (p *Poller) Poll(ctx context.Context) (poll.Result, error) {
	var res poll.Result
	req := p.src.FeedRequest()
	req.ETag, req.LastModified = p.etag, p.lastModified
	resp, err := p.fetch.Fetch(ctx, req)
	if err != nil {
		return res, fmt.Errorf("%s: mengambil %s: %w", p.src.Name(), req.Brief(), err)
	}
	fetchedAt := p.clock.Now().UTC()
	if resp.NotModified {
		res.NotModified = true
		return res, p.process(ctx, p.pendingItems(), &res)
	}
	sum := emit.Sum(resp.Body)
	if sum == p.doneSum {
		res.Unchanged = true
		p.etag, p.lastModified = resp.ETag, resp.LastModified
		return res, p.process(ctx, p.pendingItems(), &res)
	}
	if p.archive != nil {
		if sum == p.archivedSum {
			res.ArchiveKey = p.archivedKey
		} else if key, err := emit.Archive(ctx, p.archive, p.src.Name(), p.src.ArchiveExt(), sum, resp.Body, fetchedAt); err != nil {
			res.ArchiveErr = err
		} else {
			res.ArchiveKey = key
			p.archivedSum, p.archivedKey = sum, key
		}
	}
	items, rejections, err := p.src.ParseFeed(resp.Body)
	if err != nil {
		return res, fmt.Errorf("%s: %w: %w", p.src.Name(), poll.ErrParse, err)
	}
	for _, r := range rejections {
		p.reject(&res, r.Key, r.Reason)
	}

	current := make(map[string]*itemState, len(items))
	var selected []ports.FeedItem
	for _, it := range items {
		st := p.items[it.Key]
		if st == nil || st.item.Digest != it.Digest {
			st = &itemState{item: it, extra: map[string]*doc{}, settled: map[string]bool{}, attempts: map[string]int{}, stale: map[string]int{}}
			if !p.wanted(it) {
				st.final = true
			}
		}
		current[it.Key] = st
		if !st.final {
			selected = append(selected, it)
		}
	}
	// Peringatan yang sudah keluar dari daftar dilupakan; bila muncul lagi,
	// JetStream menolak ID gandanya selama jendela duplikat.
	p.items = current
	if err := p.process(ctx, selected, &res); err != nil {
		return res, err
	}
	p.doneSum = sum
	p.etag, p.lastModified = resp.ETag, resp.LastModified
	return res, nil
}

// wanted menyaring peringatan per provinsi dari ID-nya.
func (p *Poller) wanted(it ports.FeedItem) bool {
	if len(p.opts.Provinces) == 0 {
		return true
	}
	prov, ok := warning.BMKGProvince(it.Key)
	return !ok || slices.Contains(p.opts.Provinces, prov)
}

func (p *Poller) pendingItems() []ports.FeedItem {
	var out []ports.FeedItem
	for _, st := range p.items {
		if !st.final {
			out = append(out, st.item)
		}
	}
	slices.SortFunc(out, func(a, b ports.FeedItem) int { return cmp.Compare(a.Key, b.Key) })
	return out
}

func (p *Poller) process(ctx context.Context, items []ports.FeedItem, res *poll.Result) error {
	for _, it := range items {
		if err := p.processItem(ctx, p.items[it.Key], res); err != nil {
			return err
		}
	}
	return nil
}

// processItem mengambil dokumen yang belum ada lalu menerbitkan pesan. Hanya
// galat penerbitan (atau ctx batal) yang dikembalikan.
func (p *Poller) processItem(ctx context.Context, st *itemState, res *poll.Result) error {
	it := st.item
	// Hanya percobaan ulang dari polling sebelumnya yang membuat terbitan
	// ini sebuah pemulihan; kegagalan di polling ini sendiri belum.
	retried := st.retries()
	if st.primary == nil {
		d, err := p.document(ctx, st, warning.LanguagePrimary)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, errFetch):
			p.fail(res, st, warning.LanguagePrimary, err)
			return nil
		case err != nil:
			st.final = true
			p.reject(res, it.Key, err)
			return nil
		}
		if d.w.Identifier != it.Key {
			p.staleDoc(res, st, warning.LanguagePrimary, d.w.Identifier)
			return nil
		}
		relevant, err := acceptPrimary(d)
		switch {
		case err != nil:
			st.final = true
			p.reject(res, it.Key, err)
			return nil
		case !relevant:
			st.final = true
			return nil
		}
		st.primary = d
	}
	for _, lang := range p.opts.Languages {
		if st.settled[lang] {
			continue
		}
		d, err := p.document(ctx, st, lang)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, errFetch):
			p.fail(res, st, lang, err)
			continue
		case err != nil:
			// Terjemahan rusak tidak menahan peringatan.
			st.settled[lang] = true
			p.failure(res, it.Key+" ["+lang+"]", err)
			continue
		}
		if d.w.Identifier != st.primary.w.Identifier {
			p.staleDoc(res, st, lang, d.w.Identifier)
			continue
		}
		if err := checkTranslation(st.primary, d); err != nil {
			st.settled[lang] = true
			p.failure(res, it.Key+" ["+lang+"]", err)
			continue
		}
		st.extra[lang] = d
		st.settled[lang] = true
	}

	ev, err := build(p.src, st.primary, st.extra)
	if err != nil {
		st.final = true
		p.reject(res, it.Key, err)
		return nil
	}
	res.Events++
	content, sum, err := emit.Content(ev)
	if err != nil {
		return fmt.Errorf("%s: %w", p.src.Name(), err)
	}
	st.final = len(st.settled) == len(p.opts.Languages)
	if sum == st.published {
		res.AlreadySeen++
		return nil
	}
	meta := ports.FetchMeta{
		Connector: p.src.Name(), FetchedAt: st.primary.fetchedAt,
		ArchiveKey: st.primary.archiveKey, PayloadSHA256: st.primary.sum,
	}
	ack, err := emit.Publish(ctx, p.pub, ev, content, meta)
	if err != nil {
		st.final = false
		return fmt.Errorf("%s: %w", p.src.Name(), err)
	}
	if ack.Duplicate {
		res.Duplicates++
	} else {
		res.Published++
		if retried > st.reported {
			st.reported = retried
			if len(res.Recovered) < poll.MaxRejectionsKept {
				res.Recovered = append(res.Recovered, poll.Recovery{Key: it.Key, Retries: retried})
			}
		}
	}
	st.published = sum
	return nil
}

// acceptPrimary memeriksa dokumen bahasa utama: galat berarti peringatan
// ditolak, relevant false berarti diabaikan (latihan, uji, draft, Ack, dan
// Error tidak diteruskan). Dipakai polling dan replay.
func acceptPrimary(d *doc) (relevant bool, err error) {
	if err := d.w.Validate(d.fetchedAt); err != nil {
		return false, err
	}
	return d.w.Relevant(), nil
}

// checkTranslation memastikan dokumen bahasa tambahan adalah terjemahan
// pesan yang sama; galat berarti terjemahan itu dilewati.
func checkTranslation(primary, d *doc) error {
	_, err := primary.w.WithTranslation(d.w)
	return err
}

// build menggabungkan terjemahan ke dokumen utama urut kode bahasa,
// memvalidasi hasilnya pada waktu ambil dokumen utama, lalu membungkusnya
// sebagai event. Galat berarti peringatan ditolak. Dipakai polling dan replay.
func build(src Source, primary *doc, extra map[string]*doc) (ports.Event, error) {
	w := primary.w
	for _, lang := range slices.Sorted(maps.Keys(extra)) {
		merged, err := w.WithTranslation(extra[lang].w)
		if err != nil {
			return nil, fmt.Errorf("menggabungkan terjemahan %s: %w", lang, err)
		}
		w = merged
	}
	if err := w.Validate(primary.fetchedAt); err != nil {
		return nil, err
	}
	return src.Event(w)
}

// errFetch menandai kegagalan mengambil dokumen (sementara, dicoba lagi).
var errFetch = errors.New("gagal mengambil dokumen")

// document mengambil, mengarsipkan, dan membaca satu dokumen CAP.
func (p *Poller) document(ctx context.Context, st *itemState, lang string) (*doc, error) {
	if err := p.throttle.Wait(ctx); err != nil {
		return nil, err
	}
	req := p.src.DetailRequest(st.item, lang)
	resp, err := p.fetch.Fetch(ctx, req)
	if err == nil && resp.NotModified {
		err = errors.New("sumber menjawab 304 untuk request tanpa validator")
	}
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", errFetch, req.Brief(), err)
	}
	d := &doc{fetchedAt: p.clock.Now().UTC(), sum: emit.Sum(resp.Body)}
	if p.archive != nil {
		// Kegagalan arsip tidak menahan peringatan; kuncinya saja yang kosong.
		if key, err := emit.Archive(ctx, p.archive, p.src.Name(), p.src.ArchiveExt(), d.sum, resp.Body, d.fetchedAt); err == nil {
			d.archiveKey = key
		}
	}
	w, err := p.src.ParseDetail(st.item, lang, resp.Body)
	if err != nil {
		return nil, err
	}
	d.w = w
	return d, nil
}

func (p *Poller) fail(res *poll.Result, st *itemState, lang string, err error) {
	st.attempts[lang]++
	if st.attempts[lang] >= p.opts.MaxAttempts {
		if lang == warning.LanguagePrimary {
			st.final = true
		} else {
			st.settled[lang] = true
		}
		err = fmt.Errorf("menyerah setelah %d percobaan: %w", st.attempts[lang], err)
	}
	key := st.item.Key
	if lang != warning.LanguagePrimary {
		key += " [" + lang + "]"
	}
	p.failure(res, key, err)
}

// staleDoc mencatat dokumen basi: dicoba lagi di polling berikutnya sampai
// MaxStaleAttempts, lalu peringatan (bahasa utama) atau terjemahannya
// dilewati sampai entrinya berubah.
func (p *Poller) staleDoc(res *poll.Result, st *itemState, lang, got string) {
	st.stale[lang]++
	err := fmt.Errorf("%w: %s/%s berisi pesan %s", ErrStale, lang, st.item.File, got)
	if st.stale[lang] >= p.opts.MaxStaleAttempts {
		if lang == warning.LanguagePrimary {
			st.final = true
		} else {
			st.settled[lang] = true
		}
		err = fmt.Errorf("menyerah setelah %d dokumen basi: %w", st.stale[lang], err)
	}
	key := st.item.Key
	if lang != warning.LanguagePrimary {
		key += " [" + lang + "]"
	}
	p.failure(res, key, err)
}

func (p *Poller) failure(res *poll.Result, key string, err error) {
	res.Failed++
	if len(res.Failures) < poll.MaxRejectionsKept {
		res.Failures = append(res.Failures, ports.Rejection{Key: key, Reason: err})
	}
}

func (p *Poller) reject(res *poll.Result, key string, err error) {
	res.Rejected++
	if len(res.Rejections) < poll.MaxRejectionsKept {
		res.Rejections = append(res.Rejections, ports.Rejection{Key: key, Reason: err})
	}
}
