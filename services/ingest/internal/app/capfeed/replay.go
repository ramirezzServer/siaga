package capfeed

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/replay"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ReplaySource adalah Source yang juga bisa mengenali payload arsipnya
// (dipenuhi adapter bmkg.CAPFeed).
type ReplaySource interface {
	Source
	Inspect(body []byte) (ports.FeedPayload, error)
}

// TranslationWindow adalah batas tunggu terjemahan saat replay. Poller
// mengambil dokumen bahasa tambahan langsung setelah dokumen utama
// (fetcher 20 dtk, throttle anggaran BMKG), dan percobaan ulangnya baru di
// polling berikutnya (2 menit). Terjemahan dalam jendela ini dianggap satu
// percobaan dengan dokumen utama; yang datang sesudahnya adalah revisi.
const TranslationWindow = time.Minute

// ErrNoContext menandai dokumen CAP yang tidak bisa dipasangkan: entri RSS
// atau dokumen utamanya tidak ada di payload sebelumnya, misal replay
// dengan -from setelah RSS terakhir diarsipkan.
var ErrNoContext = errors.New("dokumen CAP tanpa konteks")

// ReplayOptions mengatur Replay.
type ReplayOptions struct {
	// Languages adalah bahasa tambahan yang diambil ingest saat arsip
	// direkam (INGEST_CAP_LANGUAGES). Dokumen berbahasa lain dianggap dokumen
	// bahasa utama, sama dengan permintaan "id" saat polling.
	Languages []string
	// Window menimpa TranslationWindow; nol = bawaan.
	Window time.Duration
}

// Replay memutar ulang arsip feed CAP (replay.DeferredFeed). RSS, dokumen
// bahasa Indonesia, dan terjemahan tersimpan sebagai payload terpisah di
// awalan arsip yang sama; Replay memasangkannya lagi dan menerbitkan event
// yang sama dengan Poller: sekali dengan semua terjemahan yang berhasil
// diambil bersama dokumen utama, FetchMeta dokumen utama, lalu revisi bila
// terjemahan baru datang belakangan (ADR 0027).
//
// Kunci arsip berpresisi detik, jadi urutan payload dalam satu detik tidak
// diketahui. Payload satu detik dikumpulkan dulu, lalu diolah dengan urutan
// polling: RSS, dokumen utama, terjemahan.
type Replay struct {
	src    ReplaySource
	langs  []string
	window time.Duration

	items  map[string]ports.FeedItem // entri RSS terakhir per guid
	known  map[string]bool           // semua guid yang pernah ada di RSS
	states map[string]*replayState   // per identifier
	group  []pending                 // payload detik yang belum diolah
	out    replay.Batch
}

type pending struct {
	p    replay.Payload
	info ports.FeedPayload
	// RSS yang sudah dibaca saat Add.
	items      []ports.FeedItem
	rejections []ports.Rejection
}

// replayState sama dengan itemState Poller: dibuat ulang setiap dokumen
// utama baru (Poller hanya mengambil ulang dokumen utama bila entri RSS
// berubah atau ingest dijalankan ulang, dua-duanya itemState baru).
type replayState struct {
	item    ports.FeedItem
	primary *doc
	extra   map[string]*doc
	// ignored: dokumen utama ditolak atau tidak relevan; Poller tidak
	// mengambil terjemahannya.
	ignored bool
	// tried: bahasa tambahan yang dokumennya sudah datang (berhasil atau tidak).
	tried map[string]bool
	// waiting: belum terbit; jatuh tempo pada due bila terjemahan belum lengkap.
	waiting   bool
	due       time.Time
	published string
}

// NewReplay membuat Replay.
func NewReplay(src ReplaySource, opts ReplayOptions) *Replay {
	r := &Replay{src: src, window: opts.Window, items: map[string]ports.FeedItem{}, known: map[string]bool{}, states: map[string]*replayState{}}
	if r.window <= 0 {
		r.window = TranslationWindow
	}
	for _, l := range opts.Languages {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" && l != warning.LanguagePrimary && !slices.Contains(r.langs, l) {
			r.langs = append(r.langs, l)
		}
	}
	slices.Sort(r.langs)
	return r
}

// Name sama dengan konektor polling, jadi FetchMeta dan ID pesan sama.
func (r *Replay) Name() string { return r.src.Name() }

// Parse ada untuk memenuhi replay.Feed; Replayer memakai Add dan Due.
func (r *Replay) Parse([]byte, time.Time) ([]ports.Event, []ports.Rejection, error) {
	return nil, nil, errors.New("feed CAP diputar lewat Add dan Due")
}

// Add mengenali payload lalu menyimpannya sampai detiknya lewat. RSS dibaca
// di sini supaya RSS yang rusak dihitung gagal parse, sama dengan polling.
func (r *Replay) Add(p replay.Payload) error {
	info, err := r.src.Inspect(p.Body)
	if err != nil {
		return err
	}
	pd := pending{p: p, info: info}
	if info.List {
		if pd.items, pd.rejections, err = r.src.ParseFeed(p.Body); err != nil {
			return err
		}
	}
	if len(r.group) > 0 && !r.group[0].p.FetchedAt.Equal(p.FetchedAt) {
		r.flushGroup()
	}
	r.group = append(r.group, pd)
	return nil
}

// Due mengolah payload dari detik sebelum t dan menerbitkan peringatan yang
// sudah lewat batas tunggu terjemahan; t nol berarti semua sisa.
func (r *Replay) Due(t time.Time) replay.Batch {
	if len(r.group) > 0 && (t.IsZero() || r.group[0].p.FetchedAt.Before(t)) {
		r.flushGroup()
	}
	r.expire(t)
	out := r.out
	r.out = replay.Batch{}
	return out
}

// expire menerbitkan peringatan yang batas tunggunya sebelum t (nol = semua),
// urut batas tunggu. Saat polling, peringatan itu sudah terbit tanpa
// terjemahan yang gagal diambil.
func (r *Replay) expire(t time.Time) {
	var ids []string
	for id, st := range r.states {
		if st.waiting && (t.IsZero() || st.due.Before(t)) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Or(r.states[a].due.Compare(r.states[b].due), cmp.Compare(a, b))
	})
	for _, id := range ids {
		r.publish(r.states[id])
	}
}

// kind mengurutkan payload satu detik seperti satu putaran polling.
func (r *Replay) kind(pd pending) int {
	switch {
	case pd.info.List:
		return 0
	case r.extraLang(pd.info.Language) == "":
		return 1
	default:
		return 2
	}
}

// extraLang mengembalikan bahasa tambahan untuk bahasa info dokumen (kode
// dasar, misal "en-US" → "en"), atau "" untuk dokumen bahasa utama.
func (r *Replay) extraLang(lang string) string {
	base, _, _ := strings.Cut(lang, "-")
	if slices.Contains(r.langs, base) {
		return base
	}
	return ""
}

// flushGroup mengolah payload satu detik. Peringatan yang batas tunggunya
// sudah lewat sebelum detik itu terbit lebih dulu, karena saat polling
// peringatan itu terbit sebelum payload detik ini diambil.
func (r *Replay) flushGroup() {
	g := r.group
	r.group = nil
	r.expire(g[0].p.FetchedAt)
	slices.SortStableFunc(g, func(a, b pending) int {
		return cmp.Or(cmp.Compare(r.kind(a), r.kind(b)), cmp.Compare(a.p.Key, b.p.Key))
	})
	for _, pd := range g {
		switch r.kind(pd) {
		case 0:
			r.list(pd)
		case 1:
			r.primary(pd)
		default:
			r.translation(pd, r.extraLang(pd.info.Language))
		}
	}
}

// list mengganti entri RSS. Peringatan yang keluar dari RSS dilupakan, sama
// dengan Poller: dokumen yang masih memuat identifier-nya setelah itu adalah
// dokumen basi untuk entri lain (ADR 0028), bukan terjemahan susulan.
func (r *Replay) list(pd pending) {
	r.reject(pd.rejections...)
	clear(r.items)
	for _, it := range pd.items {
		r.items[it.Key] = it
		r.known[it.Key] = true
	}
	for _, id := range slices.Sorted(maps.Keys(r.states)) {
		if _, ok := r.items[id]; ok {
			continue
		}
		// Poller sudah menerbitkannya di polling dokumen utamanya.
		if st := r.states[id]; st.waiting {
			r.publish(st)
		}
		delete(r.states, id)
	}
}

// missing adalah alasan penolakan dokumen yang identifier-nya tidak ada di
// RSS terakhir: basi bila guid itu pernah ada di RSS (file sudah ditautkan
// ke pesan lain), tanpa konteks bila belum pernah.
func (r *Replay) missing(id, key string) error {
	if r.known[id] {
		return fmt.Errorf("%w: guid %s sudah keluar dari RSS (%s)", ErrStale, id, key)
	}
	return fmt.Errorf("%w: guid %s tidak ada di RSS terakhir (%s)", ErrNoContext, id, key)
}

func (r *Replay) primary(pd pending) {
	id := pd.info.Identifier
	it, ok := r.items[id]
	if !ok {
		delete(r.states, id)
		r.reject(ports.Rejection{Key: id, Reason: r.missing(id, pd.p.Key)})
		return
	}
	// Dokumen utama baru menggantikan state lama yang masih menunggu
	// terjemahan; Poller sudah menerbitkannya sebelum mengambil ulang.
	if old := r.states[id]; old != nil && old.waiting {
		r.publish(old)
	}
	st := &replayState{item: it, extra: map[string]*doc{}, tried: map[string]bool{}}
	r.states[id] = st
	w, err := r.src.ParseDetail(it, warning.LanguagePrimary, pd.p.Body)
	if err != nil {
		st.ignored = true
		r.reject(ports.Rejection{Key: id, Reason: err})
		return
	}
	st.primary = &doc{w: w, fetchedAt: pd.p.FetchedAt, archiveKey: pd.p.Key, sum: pd.p.SHA256}
	relevant, err := acceptPrimary(st.primary)
	switch {
	case err != nil:
		st.ignored = true
		r.reject(ports.Rejection{Key: id, Reason: err})
		return
	case !relevant:
		st.ignored = true
		return
	}
	st.waiting, st.due = true, pd.p.FetchedAt.Add(r.window)
	if len(r.langs) == 0 {
		r.publish(st)
	}
}

func (r *Replay) translation(pd pending, lang string) {
	id := pd.info.Identifier
	st := r.states[id]
	key := id + " [" + lang + "]"
	switch {
	case st == nil:
		if _, listed := r.items[id]; listed {
			r.reject(ports.Rejection{Key: key, Reason: fmt.Errorf("%w: dokumen bahasa utama %s belum ada (%s)", ErrNoContext, id, pd.p.Key)})
		} else {
			r.reject(ports.Rejection{Key: key, Reason: r.missing(id, pd.p.Key)})
		}
		return
	case st.ignored:
		return
	}
	w, err := r.src.ParseDetail(st.item, lang, pd.p.Body)
	if err == nil {
		d := &doc{w: w, fetchedAt: pd.p.FetchedAt, archiveKey: pd.p.Key, sum: pd.p.SHA256}
		if err = checkTranslation(st.primary, d); err == nil {
			st.extra[lang] = d
		}
	}
	if err != nil {
		// Sama dengan polling: terjemahan rusak tidak menahan peringatan.
		r.reject(ports.Rejection{Key: key, Reason: err})
	}
	st.tried[lang] = true
	// Masih menunggu: terbit setelah semua bahasa dicoba, seperti satu
	// processItem. Sudah terbit: terjemahan susulan adalah revisi.
	if !st.waiting || len(st.tried) == len(r.langs) {
		r.publish(st)
	}
}

// publish membentuk event dari state seperti Poller.processItem.
func (r *Replay) publish(st *replayState) {
	st.waiting = false
	ev, err := build(r.src, st.primary, st.extra)
	if err != nil {
		st.ignored = true
		r.reject(ports.Rejection{Key: st.primary.w.Identifier, Reason: err})
		return
	}
	_, sum, err := emit.Content(ev)
	if err != nil {
		r.reject(ports.Rejection{Key: st.primary.w.Identifier, Reason: err})
		return
	}
	r.out.Emissions = append(r.out.Emissions, replay.Emission{
		Event: ev,
		Meta:  ports.FetchMeta{FetchedAt: st.primary.fetchedAt, ArchiveKey: st.primary.archiveKey, PayloadSHA256: st.primary.sum},
		Seen:  sum == st.published,
	})
	st.published = sum
}

func (r *Replay) reject(rs ...ports.Rejection) {
	r.out.Rejections = append(r.out.Rejections, rs...)
}
