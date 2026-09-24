package poll

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Payload uji: baris "kunci=isi"; baris "!" membuat parse gagal total,
// baris "?kunci" ditolak sebagai record rusak.
type fakeConn struct{}

func (fakeConn) Name() string       { return "uji" }
func (fakeConn) ArchiveExt() string { return "txt" }
func (fakeConn) Request() ports.Request {
	return ports.Request{URL: "https://sumber.test/feed", MaxBytes: 1 << 20}
}

func (fakeConn) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	var evs []ports.Event
	var rej []ports.Rejection
	for line := range strings.Lines(string(body)) {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case line == "!":
			return nil, nil, errors.New("format berubah")
		case strings.HasPrefix(line, "?"):
			rej = append(rej, ports.Rejection{Key: line[1:], Reason: errors.New("rusak")})
		default:
			k, v, _ := strings.Cut(line, "=")
			evs = append(evs, fakeEvent{key: k, val: v, at: fetchedAt})
		}
	}
	return evs, rej, nil
}

type fakeEvent struct {
	key, val string
	at       time.Time
}

func (e fakeEvent) Subject() string { return "raw.uji.test" }
func (e fakeEvent) Key() string     { return e.key }
func (e fakeEvent) Content() ([]byte, error) {
	if e.val == "tidak-bisa-diserialisasi" {
		return nil, errors.New("serialisasi gagal")
	}
	return []byte(e.key + "=" + e.val), nil
}

func (e fakeEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	if e.val == "tidak-bisa-diencode" {
		return nil, errors.New("encode gagal")
	}
	return fmt.Appendf(nil, "%s=%s|%s|%s|%s", e.key, e.val, m.Connector, m.ArchiveKey, m.FetchedAt.Format(time.RFC3339)), nil
}

type fakeFetcher struct {
	responses []ports.Response
	errs      []error
	requests  []ports.Request
}

func (f *fakeFetcher) Fetch(_ context.Context, req ports.Request) (ports.Response, error) {
	f.requests = append(f.requests, req)
	i := len(f.requests) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return ports.Response{}, f.errs[i]
	}
	return f.responses[i], nil
}

type fakeArchive struct {
	objects map[string][]byte
	fail    bool
}

func (a *fakeArchive) Put(_ context.Context, key string, data []byte) error {
	if a.fail {
		return errors.New("disk penuh")
	}
	a.objects[key] = data
	return nil
}

type fakePub struct {
	msgs    []ports.Message
	ids     map[string]bool
	failOn  string // kunci record yang gagal diterbitkan
	failCnt int
}

func (p *fakePub) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	if p.failCnt > 0 && strings.HasPrefix(string(m.Data), p.failOn+"=") {
		p.failCnt--
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

type rig struct {
	p   *Poller
	f   *fakeFetcher
	a   *fakeArchive
	pub *fakePub
	clk *fakeClock
}

func newRig(bodies ...string) *rig {
	f := &fakeFetcher{}
	for i, b := range bodies {
		f.responses = append(f.responses, ports.Response{Body: []byte(b), ETag: fmt.Sprintf(`"v%d"`, i)})
	}
	r := &rig{
		f: f, a: &fakeArchive{objects: map[string][]byte{}}, pub: &fakePub{ids: map[string]bool{}},
		clk: &fakeClock{now: time.Date(2026, 9, 24, 3, 4, 5, 0, time.FixedZone("WIB", 7*3600))},
	}
	r.p = New(fakeConn{}, r.f, r.a, r.pub, r.clk)
	return r
}

func TestPollPublishesNewAndRevisedOnly(t *testing.T) {
	r := newRig("a=1\nb=1\n", "a=1\nb=2\nc=1\n", "a=1\nb=2\nc=1\n")
	ctx := context.Background()

	res, err := r.p.Poll(ctx)
	if err != nil || res.Published != 2 || res.Events != 2 || r.p.Name() != "uji" {
		t.Fatalf("polling pertama: %+v, %v", res, err)
	}
	if want := "uji/2026/09/23/200405Z-"; !strings.HasPrefix(res.ArchiveKey, want) || !strings.HasSuffix(res.ArchiveKey, ".txt.gz") {
		t.Fatalf("kunci arsip %q harus UTC dengan prefix %q", res.ArchiveKey, want)
	}
	if got := gunzip(t, r.a.objects[res.ArchiveKey]); got != "a=1\nb=1\n" {
		t.Fatalf("isi arsip %q", got)
	}
	if !strings.HasSuffix(string(r.pub.msgs[0].Data), "2026-09-23T20:04:05Z") {
		t.Fatalf("waktu ambil harus UTC: %s", r.pub.msgs[0].Data)
	}

	res, err = r.p.Poll(ctx)
	if err != nil || res.Published != 2 || res.AlreadySeen != 1 {
		t.Fatalf("polling kedua harus menerbitkan b revisi dan c baru saja: %+v, %v", res, err)
	}
	if r.f.requests[1].ETag != `"v0"` {
		t.Fatalf("conditional request harus membawa ETag sebelumnya, dapat %q", r.f.requests[1].ETag)
	}

	res, err = r.p.Poll(ctx)
	if err != nil || !res.Unchanged || res.Published != 0 {
		t.Fatalf("payload identik harus dilewati: %+v, %v", res, err)
	}
	if len(r.a.objects) != 2 {
		t.Fatalf("payload identik tidak boleh diarsipkan ulang, arsip berisi %d", len(r.a.objects))
	}
}

func TestPollNotModified(t *testing.T) {
	r := newRig()
	r.f.responses = []ports.Response{{NotModified: true}}
	res, err := r.p.Poll(context.Background())
	if err != nil || !res.NotModified || len(r.pub.msgs) != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestPollRetriesAfterPublishFailureWithoutReArchiving(t *testing.T) {
	r := newRig("a=1\nb=1\n", "a=1\nb=1\n")
	r.pub.failOn, r.pub.failCnt = "b", 1
	ctx := context.Background()

	if _, err := r.p.Poll(ctx); err == nil {
		t.Fatal("galat publish harus dikembalikan")
	}
	res, err := r.p.Poll(ctx)
	if err != nil || res.Published != 1 || res.AlreadySeen != 1 {
		t.Fatalf("retry harus menerbitkan b saja: %+v, %v", res, err)
	}
	if r.f.requests[1].ETag != "" {
		t.Fatal("ETag tidak boleh dipakai sebelum semua record terbit, nanti sumber menjawab 304")
	}
	if len(r.a.objects) != 1 || res.ArchiveKey == "" {
		t.Fatalf("payload sama tidak boleh diarsipkan dua kali: %d objek, kunci %q", len(r.a.objects), res.ArchiveKey)
	}
}

func TestPollForgetsRecordsThatLeftTheFeed(t *testing.T) {
	r := newRig("a=1\n", "b=1\n", "a=1\n")
	ctx := context.Background()
	for range 3 {
		if _, err := r.p.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	res := r.pub.msgs
	if len(res) != 3 || r.pub.ids[res[2].ID] != true || res[0].ID != res[2].ID {
		t.Fatal("record yang muncul lagi diterbitkan ulang dengan ID sama agar ditolak broker")
	}
}

func TestPollBrokerDuplicateCounted(t *testing.T) {
	r := newRig("a=1\n")
	r.pub.ids[mustID(t, "a", "a=1")] = true
	res, err := r.p.Poll(context.Background())
	if err != nil || res.Duplicates != 1 || res.Published != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestPollArchiveFailureDoesNotBlockPublishing(t *testing.T) {
	r := newRig("a=1\n")
	r.a.fail = true
	res, err := r.p.Poll(context.Background())
	if err != nil || res.ArchiveErr == nil || res.Published != 1 || res.ArchiveKey != "" {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestPollWithoutArchive(t *testing.T) {
	r := newRig("a=1\n")
	r.p = New(fakeConn{}, r.f, nil, r.pub, r.clk)
	res, err := r.p.Poll(context.Background())
	if err != nil || res.Published != 1 || res.ArchiveKey != "" {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestPollErrors(t *testing.T) {
	ctx := context.Background()

	r := newRig()
	r.f.errs = []error{errors.New("timeout")}
	if _, err := r.p.Poll(ctx); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("galat fetch: %v", err)
	}

	r = newRig("a=1\n!\n")
	_, err := r.p.Poll(ctx)
	if !errors.Is(err, ErrParse) {
		t.Fatalf("galat parse harus ErrParse: %v", err)
	}
	if len(r.a.objects) != 1 {
		t.Fatal("payload yang gagal di-parse tetap diarsipkan untuk diselidiki")
	}

	for _, bad := range []string{"a=tidak-bisa-diserialisasi\n", "a=tidak-bisa-diencode\n"} {
		r = newRig(bad)
		if _, err := r.p.Poll(ctx); err == nil {
			t.Fatalf("%q harus gagal", bad)
		}
	}
}

func TestPollRejectionsKeptBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("a=1\n")
	for i := range 25 {
		fmt.Fprintf(&b, "?r%d\n", i)
	}
	r := newRig(b.String())
	res, err := r.p.Poll(context.Background())
	if err != nil || res.Rejected != 25 || len(res.Rejections) != maxRejectionsKept || res.Published != 1 {
		t.Fatalf("%+v, %v", res, err)
	}
}

func mustID(t *testing.T, key, content string) string {
	t.Helper()
	r := newRig(content + "\n")
	if _, err := r.p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, m := range r.pub.msgs {
		if strings.HasPrefix(string(m.Data), key+"=") {
			return m.ID
		}
	}
	t.Fatal("tidak ada pesan")
	return ""
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
