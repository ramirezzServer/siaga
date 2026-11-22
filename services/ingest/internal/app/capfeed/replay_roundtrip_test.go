package capfeed_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/capfeed"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/replay"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Uji bolak-balik: Poller asli (adapter bmkg.CAPFeed, event rawpb) menulis
// arsip dan menerbitkan pesan; Replay atas arsip itu harus menerbitkan pesan
// yang sama persis (subjek, Nats-Msg-Id, isi termasuk FetchMeta, waktu ambil),
// termasuk terjemahan yang gagal lalu menyusul, entri yang berubah, dokumen
// rusak, ingest yang dijalankan ulang, dan file yang ditimpa pesan baru
// tetapi masih berisi pesan lama (ADR 0028).

var base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const nowcast = bmkg.DefaultCAPBaseURL

// memArchive adalah ports.ArchiveStore di memori.
type memArchive struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (m *memArchive) Put(_ context.Context, k string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[k] = bytes.Clone(b)
	return nil
}

func (m *memArchive) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objs[k]
	if !ok {
		return nil, ports.ErrArchiveNotFound
	}
	return b, nil
}

func (m *memArchive) List(_ context.Context, prefix, after string) iter.Seq2[ports.ArchiveObject, error] {
	m.mu.Lock()
	var keys []string
	for k := range m.objs {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	m.mu.Unlock()
	slices.Sort(keys)
	return func(yield func(ports.ArchiveObject, error) bool) {
		for _, k := range keys {
			if !yield(ports.ArchiveObject{Key: k}, nil) {
				return
			}
		}
	}
}

// recPub mencatat pesan dan menolak ID ganda seperti JetStream.
type recPub struct {
	msgs []ports.Message
	ids  map[string]bool
}

func (p *recPub) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	if p.ids == nil {
		p.ids = map[string]bool{}
	}
	dup := p.ids[m.ID]
	p.ids[m.ID] = true
	p.msgs = append(p.msgs, m)
	return ports.PublishResult{Duplicate: dup}, nil
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }
func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

type noThrottle struct{}

func (noThrottle) Wait(context.Context) error { return nil }

// alert adalah satu peringatan sintetis. rev berubah bila BMKG memperbarui
// entri (judul dan headline berubah, jadi digest RSS berubah).
type alert struct {
	id, file string
	listed   bool
	rev      int
	// enBad: 1 terjemahan tidak cocok (severity lain), 2 dokumen Inggris
	// tidak terbaca (sent rusak). idBad: 1 dokumen utama tidak terbaca,
	// 2 latihan (Exercise), 3 kedaluwarsa sebelum berlaku.
	enBad, idBad int
	// prev adalah pesan lama di file yang sama: BMKG menimpa file untuk
	// pesan baru (ADR 0027), dan sampai lag[bahasa] request berikutnya file
	// itu masih berisi pesan lama (ADR 0028).
	prev   *alert
	lag    map[string]int
	prov   string
	n, gen int
}

// overwrite: BMKG menerbitkan pesan baru (identifier baru) di file yang sama;
// entri lama diganti entri baru di RSS, dan file masih berisi pesan lama
// selama lag request per bahasa.
func (a *alert) overwrite(lag int) {
	old := *a
	old.prev, old.lag = nil, nil
	a.prev = &old
	a.lag = map[string]int{"id": lag, "en": lag}
	a.gen++
	a.id = fmt.Sprintf("2.49.0.1.360.0.2026.10.01.%02d.%s.%03d", 11+a.gen, a.prov, a.n)
	a.listed = true
	a.rev++
}

func (a *alert) doc(lang string) string {
	sev, sent, status := "Severe", "2026-10-01T18:30:00+07:00", "Actual"
	expires := "2026-10-02T00:00:00+07:00"
	switch {
	case lang == "en" && a.enBad == 1:
		sev = "Moderate"
	case lang == "en" && a.enBad == 2, lang == "id" && a.idBad == 1:
		sent = "kemarin"
	case lang == "id" && a.idBad == 2:
		status = "Exercise"
	case lang == "id" && a.idBad == 3:
		expires = "2026-10-01T18:00:00+07:00"
	}
	headline := fmt.Sprintf("Hujan lebat %s revisi %d (%s)", a.file, a.rev, lang)
	return `<?xml version="1.0" ?>
<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2">
  <identifier>` + a.id + `</identifier>
  <sender>cuaca.ekstrem@bmkg.go.id</sender>
  <sent>` + sent + `</sent>
  <status>` + status + `</status>
  <msgType>Alert</msgType>
  <scope>Public</scope>
  <info>
    <language>` + lang + `</language>
    <category>Met</category>
    <event>Hujan Lebat dan Petir</event>
    <urgency>Immediate</urgency>
    <severity>` + sev + `</severity>
    <certainty>Observed</certainty>
    <effective>2026-10-01T18:45:00+07:00</effective>
    <expires>` + expires + `</expires>
    <senderName>BMKG</senderName>
    <headline>` + headline + `</headline>
    <description>Waspada.</description>
    <area>
      <areaDesc>Jawa Barat</areaDesc>
      <polygon>-6.90,107.60 -6.90,107.65 -6.85,107.65 -6.90,107.60</polygon>
    </area>
  </info>
</alert>`
}

type world struct {
	alerts []*alert
	clock  *clock
	// bytes mengatur kegagalan dan jeda tiap request.
	bytes []byte
}

func (w *world) next() byte {
	if len(w.bytes) == 0 {
		return 0x08 // berhasil, tanpa jeda
	}
	b := w.bytes[0]
	w.bytes = w.bytes[1:]
	return b
}

func (w *world) rss() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Peringatan Dini Cuaca BMKG</title>`)
	for _, a := range w.alerts {
		if a.listed {
			fmt.Fprintf(&b, `<item><title>Hujan Lebat %s rev %d</title><link>%sid/%s</link><guid isPermaLink="false">%s</guid></item>`,
				a.file, a.rev, nowcast, a.file, a.id)
		}
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// Fetch melayani RSS dan dokumen; satu byte per request: bit 0–2 nol = gagal
// (bukan untuk RSS), bit 3–4 = jeda 0–3 detik sebelum jawaban.
func (w *world) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	b := w.next()
	w.clock.now = w.clock.now.Add(time.Duration(b>>3&3) * time.Second)
	if r.URL == nowcast+"id/rss.xml" {
		return ports.Response{Body: []byte(w.rss())}, nil
	}
	if b&7 == 0 {
		return ports.Response{}, errors.New("timeout")
	}
	lang, file, _ := strings.Cut(strings.TrimPrefix(r.URL, nowcast), "/")
	for _, a := range w.alerts {
		if a.file == file {
			if a.lag[lang] > 0 {
				a.lag[lang]--
				return ports.Response{Body: []byte(a.prev.doc(lang))}, nil
			}
			return ports.Response{Body: []byte(a.doc(lang))}, nil
		}
	}
	return ports.Response{}, errors.New("404")
}

// scenario menjalankan polling langsung dan mengembalikan arsip serta pesannya.
func scenario(t *testing.T, ops []byte) (*memArchive, *recPub, *clock) {
	t.Helper()
	ck := &clock{now: base}
	w := &world{clock: ck}
	for i, prov := range []string{"32", "32", "32", "33"} {
		w.alerts = append(w.alerts, &alert{
			id:   fmt.Sprintf("2.49.0.1.360.0.2026.10.01.11.%s.%03d", prov, i+1),
			file: fmt.Sprintf("C%d20261001%03d_alert.xml", i, i+1),
			prov: prov, n: i + 1,
		})
	}
	// Byte pertama per peringatan: varian dokumen.
	for _, a := range w.alerts {
		if len(ops) == 0 {
			break
		}
		v := ops[0]
		ops = ops[1:]
		a.listed, a.enBad, a.idBad = v&1 == 1, int(v>>1&3)%3, int(v>>3&7)%5%4
	}
	archive := &memArchive{objs: map[string][]byte{}}
	pub := &recPub{}
	src := bmkg.NewCAPFeed(nowcast)
	newPoller := func() *capfeed.Poller {
		return capfeed.New(src, w, archive, pub, ck, noThrottle{},
			capfeed.Options{Provinces: []string{"32"}, Languages: []string{"en"}, MaxAttempts: 3, MaxStaleAttempts: 3})
	}
	p := newPoller()
	// Sisanya: pasangan (operasi, byte request).
	for len(ops) >= 2 && ck.now.Before(base.Add(2*time.Hour)) {
		op, fetchBytes := ops[0], ops[1]
		ops = ops[2:]
		a := w.alerts[int(op>>2)%len(w.alerts)]
		switch op & 3 {
		case 1:
			a.listed = true
			a.rev++
		case 2:
			a.listed = !a.listed
		case 3:
			switch {
			case op&0x80 != 0:
				p = newPoller() // ingest dijalankan ulang
			case op&0x40 != 0:
				a.overwrite(int(op >> 4 & 3))
			}
		}
		w.bytes = []byte{fetchBytes, fetchBytes >> 1, fetchBytes >> 2, fetchBytes >> 3, fetchBytes >> 4}
		if _, err := p.Poll(t.Context()); err != nil {
			t.Fatalf("poll: %v", err)
		}
		ck.now = ck.now.Truncate(time.Second).Add(2 * time.Minute)
	}
	return archive, pub, ck
}

func identifier(t *testing.T, m ports.Message) string {
	t.Helper()
	var ww rawv1.WeatherWarning
	if err := proto.Unmarshal(m.Data, &ww); err != nil {
		t.Fatalf("pesan %s: %v", m.ID, err)
	}
	return ww.GetIdentifier()
}

// assertSame: pesan replay sama dengan pesan langsung sebagai himpunan, dan
// urutan pesan per peringatan sama (revisi tidak tertukar). Urutan antar-
// peringatan dalam satu detik tidak diketahui dari kunci arsip.
func assertSame(t *testing.T, live, replayed []ports.Message) {
	t.Helper()
	perAlert := func(ms []ports.Message) map[string][]string {
		out := map[string][]string{}
		for _, m := range ms {
			if m.Subject != "raw.weather.bmkg" {
				t.Fatalf("subjek %s", m.Subject)
			}
			id := identifier(t, m)
			out[id] = append(out[id], fmt.Sprintf("%s %s %x", m.ID, m.FetchedAt.Format(time.RFC3339Nano), m.Data))
		}
		return out
	}
	l, r := perAlert(live), perAlert(replayed)
	for id := range l {
		if !slices.Equal(l[id], r[id]) {
			t.Errorf("%s:\nlangsung %d pesan %q\nreplay   %d pesan %q", id, len(l[id]), short(l[id]), len(r[id]), short(r[id]))
		}
	}
	for id := range r {
		if _, ok := l[id]; !ok {
			t.Errorf("%s: %d pesan hanya ada di replay", id, len(r[id]))
		}
	}
}

func short(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s[:min(len(s), 60)]
	}
	return out
}

func runReplay(t *testing.T, archive *memArchive, ck *clock) (replay.FeedReport, *recPub) {
	t.Helper()
	pub := &recPub{}
	feed := capfeed.NewReplay(bmkg.NewCAPFeed(nowcast), capfeed.ReplayOptions{Languages: []string{"en"}})
	rep, err := replay.New(archive, pub, ck).Run(t.Context(), []replay.Feed{feed}, replay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return rep.Feeds[0], pub
}

func TestReplayRoundTripScenarios(t *testing.T) {
	cases := map[string]struct {
		ops  []byte
		msgs int // pesan polling langsung, supaya skenario tidak kosong diam-diam
	}{
		// Satu peringatan, semua berhasil: satu pesan id + en.
		"lancar": {[]byte{1, 0, 0, 0, 0x03, 0x09}, 1},
		// Terjemahan gagal diambil: pesan id saja, lalu revisi id + en di polling berikutnya.
		"terjemahan menyusul": {[]byte{1, 0, 0, 0, 0x03, 0x02, 0x03, 0x09}, 2},
		// Dokumen utama gagal sekali, entri berubah, lalu keluar dari RSS.
		"entri berubah": {[]byte{1, 1, 0, 0, 0x03, 0b1000, 0x01, 0x09, 0x02, 0x09, 0x05, 0x19}, 5},
		// Terjemahan tidak cocok, terjemahan rusak, latihan, dan kedaluwarsa.
		"dokumen bermasalah": {[]byte{0b11, 0b101, 0b10001, 0b11001, 0x03, 0x09, 0x01, 0x09, 0x05, 0x09, 0x09, 0x09, 0x0d, 0x09}, 4},
		// Ingest dijalankan ulang: semua entri diambil ulang, pesan sama ditolak broker.
		"ingest ulang": {[]byte{1, 1, 1, 1, 0x03, 0x09, 0x83, 0x09, 0x83, 0x11}, 6},
		// Request tanpa jeda: RSS, dokumen, dan terjemahan berbagi detik.
		"satu detik": {[]byte{1, 1, 1, 0, 0x03, 0, 0x01, 0, 0x05, 0, 0x09, 0x18}, 2},
		// File ditimpa pesan baru, tetapi dua request pertama per bahasa masih
		// berisi pesan lama: pesan lama tidak terbit ulang, pesan baru terbit
		// setelah dokumennya benar, terjemahan menyusul sebagai revisi.
		"file ditimpa": {[]byte{1, 0, 0, 0, 0x03, 0x09, 0x63, 0x09, 0x03, 0x09, 0x03, 0x09, 0x03, 0x09, 0x03, 0x09}, 3},
		// Dokumen basi melewati batas percobaan: pesan baru tidak terbit.
		"file ditimpa menyerah": {[]byte{1, 0, 0, 0, 0x03, 0x09, 0x73, 0x09, 0x03, 0x09, 0x03, 0x09, 0x03, 0x09}, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			archive, live, ck := scenario(t, c.ops)
			if len(live.msgs) != c.msgs {
				t.Fatalf("polling langsung menerbitkan %d pesan, skenario mengharapkan %d", len(live.msgs), c.msgs)
			}
			got, replayed := runReplay(t, archive, ck)
			assertSame(t, live.msgs, replayed.msgs)
			if got.Corrupt+got.ParseErrors != 0 {
				t.Errorf("laporan %+v", got)
			}
		})
	}
}

// Properti: untuk sembarang urutan polling, kegagalan request, perubahan
// entri, file yang ditimpa, dan jalan ulang ingest, replay arsip menerbitkan pesan yang sama
// dengan polling langsung.
func FuzzReplayRoundTrip(f *testing.F) {
	f.Add([]byte{1, 0, 0, 0, 0x03, 0b0_1001_0001, 0x03, 0x0f, 0x03, 0x09})
	f.Add([]byte{0b11, 0b101, 0b10001, 0b11001, 0x03, 0x09, 0x01, 0x09, 0x83, 0, 0x05, 0x18})
	f.Add([]byte{1, 1, 1, 1, 0x03, 0, 0x01, 0, 0x05, 0, 0x09, 0x18, 0x0a, 0x09})
	f.Add([]byte{1, 1, 0, 0, 0x03, 0x09, 0x63, 0x09, 0x77, 0, 0x03, 0x09, 0x83, 0x09, 0x03, 0x02, 0x03, 0x09})
	f.Fuzz(func(t *testing.T, ops []byte) {
		archive, live, ck := scenario(t, ops)
		got, replayed := runReplay(t, archive, ck)
		assertSame(t, live.msgs, replayed.msgs)
		if got.Corrupt+got.ParseErrors != 0 {
			t.Fatalf("laporan %+v", got)
		}
	})
}
