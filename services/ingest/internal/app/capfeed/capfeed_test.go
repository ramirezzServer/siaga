package capfeed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var t0 = time.Date(2026, 9, 24, 7, 20, 0, 0, time.UTC)

const (
	idJabar = "2.49.0.1.360.0.2026.09.24.07.32.001"
	idGtlo  = "2.49.0.1.360.0.2026.09.24.07.75.002"
	idAneh  = "urn:bmkg:cap:ANEH-1"
)

// fakeSource: RSS berisi baris "guid|file|digest"; dokumen detail diambil
// dari docs menurut URL "<lang>/<file>".
type fakeSource struct {
	docs map[string]warning.Warning
	bad  map[string]error // galat parse per URL
}

func (fakeSource) Name() string       { return "bmkg-cap" }
func (fakeSource) ArchiveExt() string { return "xml" }
func (fakeSource) FeedRequest() ports.Request {
	return ports.Request{URL: "rss", MaxBytes: 1 << 20}
}

func (fakeSource) ParseFeed(body []byte) ([]ports.FeedItem, []ports.Rejection, error) {
	var items []ports.FeedItem
	var rej []ports.Rejection
	for line := range strings.Lines(string(body)) {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case line == "!":
			return nil, nil, errors.New("RSS rusak")
		case strings.HasPrefix(line, "?"):
			rej = append(rej, ports.Rejection{Key: line, Reason: errors.New("entri rusak")})
		default:
			p := strings.Split(line, "|")
			items = append(items, ports.FeedItem{Key: p[0], File: p[1], Digest: p[2]})
		}
	}
	return items, rej, nil
}

func (fakeSource) DetailRequest(it ports.FeedItem, lang string) ports.Request {
	return ports.Request{URL: lang + "/" + it.File}
}

func (s fakeSource) ParseDetail(it ports.FeedItem, lang string, body []byte) (warning.Warning, error) {
	url := lang + "/" + it.File
	if err := s.bad[url]; err != nil {
		return warning.Warning{}, err
	}
	w, ok := s.docs[url]
	if !ok || string(body) != url {
		return warning.Warning{}, fmt.Errorf("dokumen %s tidak dikenal", url)
	}
	return w, nil
}

func (fakeSource) Event(w warning.Warning) (ports.Event, error) {
	if w.Contact == "tidak-bisa-dibungkus" {
		return nil, errors.New("gagal membungkus")
	}
	return fakeEvent{w: w}, nil
}

type fakeEvent struct{ w warning.Warning }

func (e fakeEvent) Subject() string { return "raw.weather.bmkg" }
func (e fakeEvent) Key() string     { return e.w.Identifier }
func (e fakeEvent) Content() ([]byte, error) {
	var b strings.Builder
	b.WriteString(e.w.Identifier)
	for _, t := range e.w.Texts {
		b.WriteString("|" + t.Language + ":" + t.Headline)
	}
	return []byte(b.String()), nil
}

func (e fakeEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	c, _ := e.Content()
	return fmt.Appendf(c, "|%s|%s", m.ArchiveKey, m.FetchedAt.Format(time.RFC3339)), nil
}

// fakeFetcher menjawab per URL; errs[url] berisi galat untuk n panggilan berikutnya.
type fakeFetcher struct {
	rss      []ports.Response
	errs     map[string][]error
	requests []string
}

func (f *fakeFetcher) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.requests = append(f.requests, r.URL)
	if q := f.errs[r.URL]; len(q) > 0 {
		f.errs[r.URL] = q[1:]
		if q[0] != nil {
			return ports.Response{}, q[0]
		}
	}
	if r.URL == "rss" {
		resp := f.rss[0]
		if len(f.rss) > 1 {
			f.rss = f.rss[1:]
		}
		if resp.ETag != "" && r.ETag == resp.ETag {
			return ports.Response{NotModified: true}, nil
		}
		return resp, nil
	}
	return ports.Response{Body: []byte(r.URL)}, nil
}

func (f *fakeFetcher) count(url string) int {
	n := 0
	for _, r := range f.requests {
		if r == url {
			n++
		}
	}
	return n
}

type fakeArchive struct {
	keys []string
	fail bool
}

func (a *fakeArchive) Put(_ context.Context, key string, _ []byte) error {
	if a.fail {
		return errors.New("disk penuh")
	}
	a.keys = append(a.keys, key)
	return nil
}

type fakePub struct {
	msgs   []ports.Message
	ids    map[string]bool
	failN  int
	failOn string
}

func (p *fakePub) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	if p.failN > 0 && strings.HasPrefix(string(m.Data), p.failOn) {
		p.failN--
		return ports.PublishResult{}, errors.New("NATS mati")
	}
	dup := p.ids[m.ID]
	p.ids[m.ID] = true
	p.msgs = append(p.msgs, m)
	return ports.PublishResult{Duplicate: dup}, nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time                             { return c.now }
func (c *fakeClock) Sleep(context.Context, time.Duration) error { return nil }

type countingThrottle struct {
	n   int
	err error
}

func (c *countingThrottle) Wait(context.Context) error {
	c.n++
	return c.err
}

func warn(id string, lang string, mutate ...func(*warning.Warning)) warning.Warning {
	sent := t0.Add(-20 * time.Minute)
	w := warning.Warning{
		Identifier: id, Sender: "cuaca.ekstrem@bmkg.go.id", Sent: sent,
		Status: warning.StatusActual, MsgType: warning.MsgAlert,
		Urgency: warning.UrgencyImmediate, Severity: warning.SeverityModerate, Certainty: warning.CertaintyObserved,
		Effective: sent, Expires: sent.Add(2 * time.Hour),
		Texts: []warning.Text{{Language: lang, Headline: "Hujan lebat " + lang}},
		Areas: []warning.Area{{Desc: "x", Polygons: []warning.Ring{{{Lat: -6.9, Lon: 107.6}, {Lat: -6.9, Lon: 107.7}, {Lat: -6.8, Lon: 107.7}, {Lat: -6.9, Lon: 107.6}}}}},
	}
	for _, m := range mutate {
		m(&w)
	}
	return w
}

type rig struct {
	p   *Poller
	f   *fakeFetcher
	a   *fakeArchive
	pub *fakePub
	th  *countingThrottle
	src fakeSource
}

func newRig(rss ...string) *rig {
	f := &fakeFetcher{errs: map[string][]error{}}
	for i, body := range rss {
		f.rss = append(f.rss, ports.Response{Body: []byte(body), ETag: fmt.Sprintf(`"v%d"`, i)})
	}
	src := fakeSource{docs: map[string]warning.Warning{}, bad: map[string]error{}}
	for _, id := range []string{idJabar, idGtlo, idAneh} {
		file := strings.ReplaceAll(id, ":", "_") + ".xml"
		src.docs["id/"+file] = warn(id, "id")
		src.docs["en/"+file] = warn(id, "en")
	}
	r := &rig{f: f, a: &fakeArchive{}, pub: &fakePub{ids: map[string]bool{}}, th: &countingThrottle{}, src: src}
	r.p = New(src, f, r.a, r.pub, &fakeClock{now: t0}, r.th, Options{Provinces: []string{"32"}, Languages: []string{"en", "id"}, MaxAttempts: 3})
	return r
}

func line(id, digest string) string {
	return id + "|" + strings.ReplaceAll(id, ":", "_") + ".xml|" + digest + "\n"
}

func file(id string) string { return strings.ReplaceAll(id, ":", "_") + ".xml" }

func mustPoll(t *testing.T, r *rig) poll.Result {
	t.Helper()
	res, err := r.p.Poll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFiltersProvinceAndPublishesWithTranslation(t *testing.T) {
	r := newRig(line(idJabar, "d1") + line(idGtlo, "d1") + line(idAneh, "d1") + "?rusak\n")
	res := mustPoll(t, r)
	// Gorontalo disaring tanpa request; ID yang tak terbaca tetap diambil.
	if r.f.count("id/"+file(idGtlo)) != 0 || r.f.count("id/"+file(idJabar)) != 1 || r.f.count("en/"+file(idAneh)) != 1 {
		t.Fatalf("requests %v", r.f.requests)
	}
	if res.Events != 2 || res.Published != 2 || res.Rejected != 1 || res.Failed != 0 || r.th.n != 4 {
		t.Fatalf("%+v throttle=%d", res, r.th.n)
	}
	if got := string(r.pub.msgs[0].Data); !strings.HasPrefix(got, idJabar+"|en:Hujan lebat en|id:Hujan lebat id|bmkg-cap/2026/09/24/072000Z-") {
		t.Fatalf("pesan %q", got)
	}
	// RSS + 4 dokumen diarsipkan.
	if len(r.a.keys) != 5 {
		t.Fatalf("arsip %v", r.a.keys)
	}
	if r.p.Pending() != 0 {
		t.Fatalf("pending %d", r.p.Pending())
	}
	// Polling berikutnya: RSS 304, tidak ada request dokumen.
	before := len(r.f.requests)
	res = mustPoll(t, r)
	if !res.NotModified || len(r.f.requests) != before+1 || res.Published != 0 {
		t.Fatalf("%+v requests %v", res, r.f.requests[before:])
	}
}

func TestTranslationFailureStillPublishesThenRevises(t *testing.T) {
	r := newRig(line(idJabar, "d1"))
	r.f.errs["en/"+file(idJabar)] = []error{errors.New("timeout")}
	res := mustPoll(t, r)
	if res.Published != 1 || res.Failed != 1 || !strings.Contains(res.Failures[0].Key, "[en]") {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(string(r.pub.msgs[0].Data), "en:") {
		t.Fatal("pesan pertama seharusnya hanya teks id")
	}
	if r.p.Pending() != 1 {
		t.Fatal("peringatan harus menunggu terjemahan")
	}
	// RSS 304: hanya dokumen en yang diambil lagi, lalu terbit revisi.
	res = mustPoll(t, r)
	if !res.NotModified || res.Published != 1 || r.f.count("id/"+file(idJabar)) != 1 || r.f.count("en/"+file(idJabar)) != 2 {
		t.Fatalf("%+v requests %v", res, r.f.requests)
	}
	if !strings.Contains(string(r.pub.msgs[1].Data), "en:Hujan lebat en") || r.p.Pending() != 0 {
		t.Fatalf("revisi %q", r.pub.msgs[1].Data)
	}
}

func TestTranslationGivesUpAfterMaxAttempts(t *testing.T) {
	r := newRig(line(idJabar, "d1"))
	r.f.errs["en/"+file(idJabar)] = []error{errors.New("a"), errors.New("b"), errors.New("c"), errors.New("d")}
	for range 3 {
		mustPoll(t, r)
	}
	if r.p.Pending() != 0 || r.f.count("en/"+file(idJabar)) != 3 || len(r.pub.msgs) != 1 {
		t.Fatalf("pending=%d en=%d msgs=%d", r.p.Pending(), r.f.count("en/"+file(idJabar)), len(r.pub.msgs))
	}
}

func TestPrimaryFailureRetriesThenGivesUp(t *testing.T) {
	r := newRig(line(idJabar, "d1"))
	r.f.errs["id/"+file(idJabar)] = []error{errors.New("503"), errors.New("503"), errors.New("503")}
	res := mustPoll(t, r)
	if res.Failed != 1 || res.Published != 0 || r.p.Pending() != 1 {
		t.Fatalf("%+v", res)
	}
	mustPoll(t, r)
	res = mustPoll(t, r)
	if !strings.Contains(res.Failures[0].Reason.Error(), "menyerah setelah 3 percobaan") || r.p.Pending() != 0 {
		t.Fatalf("%+v", res)
	}
	mustPoll(t, r)
	if r.f.count("id/"+file(idJabar)) != 3 {
		t.Fatalf("dokumen diambil %d kali", r.f.count("id/"+file(idJabar)))
	}
}

func TestRejectsBadDocumentsWithoutRetry(t *testing.T) {
	r := newRig(line(idJabar, "d1") + line(idAneh, "d1"))
	r.src.bad["id/"+file(idJabar)] = errors.New("XML rusak")
	r.src.docs["id/"+file(idAneh)] = warn(idAneh, "id", func(w *warning.Warning) { w.Expires = w.Effective })
	res := mustPoll(t, r)
	if res.Rejected != 2 || res.Published != 0 || r.p.Pending() != 0 {
		t.Fatalf("%+v", res)
	}
	// Entri berubah: dokumen diambil lagi.
	r.f.rss = []ports.Response{{Body: []byte(line(idJabar, "d2") + line(idAneh, "d1")), ETag: `"v9"`}}
	delete(r.src.bad, "id/"+file(idJabar))
	res = mustPoll(t, r)
	if res.Published != 1 || r.f.count("id/"+file(idJabar)) != 2 || r.f.count("id/"+file(idAneh)) != 1 {
		t.Fatalf("%+v %v", res, r.f.requests)
	}
}

func TestIrrelevantAndMismatchedTranslation(t *testing.T) {
	r := newRig(line(idJabar, "d1") + line(idAneh, "d1"))
	r.src.docs["id/"+file(idAneh)] = warn(idAneh, "id", func(w *warning.Warning) { w.Status = warning.StatusExercise })
	r.src.docs["en/"+file(idJabar)] = warn(idJabar, "en", func(w *warning.Warning) { w.Severity = warning.SeveritySevere })
	res := mustPoll(t, r)
	// Latihan tidak terbit dan tidak diterjemahkan; terjemahan yang tidak cocok dilewati.
	if res.Published != 1 || res.Failed != 1 || r.f.count("en/"+file(idAneh)) != 0 || r.p.Pending() != 0 {
		t.Fatalf("%+v %v", res, r.f.requests)
	}
	r2 := newRig(line(idJabar, "d1"))
	r2.src.bad["en/"+file(idJabar)] = errors.New("XML en rusak")
	res = mustPoll(t, r2)
	if res.Published != 1 || res.Failed != 1 || r2.p.Pending() != 0 {
		t.Fatalf("%+v", res)
	}
	r3 := newRig(line(idJabar, "d1"))
	r3.src.docs["en/"+file(idJabar)] = warn(idJabar, "en", func(w *warning.Warning) { w.Texts[0].Headline = "" })
	res = mustPoll(t, r3)
	if res.Rejected != 1 || res.Published != 0 {
		t.Fatalf("terjemahan yang membuat pesan tidak valid: %+v", res)
	}
	r4 := newRig(line(idJabar, "d1"))
	r4.src.docs["id/"+file(idJabar)] = warn(idJabar, "id", func(w *warning.Warning) { w.Contact = "tidak-bisa-dibungkus" })
	r4.src.docs["en/"+file(idJabar)] = warn(idJabar, "en", func(w *warning.Warning) { w.Contact = "tidak-bisa-dibungkus" })
	if res = mustPoll(t, r4); res.Rejected != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestForgetsRemovedItemsAndRepublishesAsDuplicate(t *testing.T) {
	r := newRig(line(idJabar, "d1"), "", line(idJabar, "d1"))
	mustPoll(t, r)
	mustPoll(t, r) // RSS kosong: status dilupakan
	res := mustPoll(t, r)
	if res.Published != 0 || res.Duplicates != 1 || r.f.count("id/"+file(idJabar)) != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestPublishFailureIsRetried(t *testing.T) {
	r := newRig(line(idJabar, "d1"))
	r.pub.failN, r.pub.failOn = 1, idJabar
	if _, err := r.p.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), "NATS mati") {
		t.Fatalf("err = %v", err)
	}
	// RSS dijawab penuh lagi (validator belum disimpan) dan pesan terbit tanpa mengambil ulang dokumen.
	res := mustPoll(t, r)
	if res.Published != 1 || r.f.count("id/"+file(idJabar)) != 1 || r.f.count("rss") != 2 {
		t.Fatalf("%+v %v", res, r.f.requests)
	}
	if r.f.rss[0].ETag == "" {
		t.Fatal("rig rusak")
	}
}

func TestFeedErrors(t *testing.T) {
	r := newRig("!")
	if _, err := r.p.Poll(t.Context()); !errors.Is(err, poll.ErrParse) {
		t.Fatalf("err = %v", err)
	}
	r = newRig(line(idJabar, "d1"))
	r.f.errs["rss"] = []error{errors.New("DNS")}
	if _, err := r.p.Poll(t.Context()); err == nil {
		t.Fatal("galat RSS harus dikembalikan")
	}
	// Payload identik tanpa validator: Unchanged.
	r = newRig(line(idJabar, "d1"))
	r.f.rss[0].ETag = ""
	mustPoll(t, r)
	if res := mustPoll(t, r); !res.Unchanged {
		t.Fatalf("%+v", res)
	}
	// Arsip gagal tidak menahan peringatan.
	r = newRig(line(idJabar, "d1"))
	r.a.fail = true
	res := mustPoll(t, r)
	if res.ArchiveErr == nil || res.Published != 1 || strings.Contains(string(r.pub.msgs[0].Data), "bmkg-cap/") {
		t.Fatalf("%+v %q", res, r.pub.msgs[0].Data)
	}
	// Throttle batal: Poll berhenti dengan galat ctx.
	r = newRig(line(idJabar, "d1"))
	r.th.err = context.Canceled
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.p.Poll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestNotModifiedDetailIsFailure(t *testing.T) {
	r := newRig(line(idJabar, "d1"))
	r.p.fetch = notModifiedDetails{r.f}
	res := mustPoll(t, r)
	if res.Failed != 1 || res.Published != 0 || r.p.Pending() != 1 {
		t.Fatalf("%+v", res)
	}
}

type notModifiedDetails struct{ f *fakeFetcher }

func (n notModifiedDetails) Fetch(ctx context.Context, r ports.Request) (ports.Response, error) {
	if r.URL == "rss" {
		return n.f.Fetch(ctx, r)
	}
	return ports.Response{NotModified: true}, nil
}

func TestNoProvinceFilterAndDefaults(t *testing.T) {
	r := newRig(line(idGtlo, "d1"))
	r.p = New(r.src, r.f, nil, r.pub, &fakeClock{now: t0}, r.th, Options{})
	res := mustPoll(t, r)
	if res.Published != 1 || r.p.opts.MaxAttempts != 5 || r.p.Name() != "bmkg-cap" {
		t.Fatalf("%+v %+v", res, r.p.opts)
	}
}
