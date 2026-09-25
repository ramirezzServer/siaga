package replay

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/eventid"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// memArchive adalah ports.ArchiveStore di memori.
type memArchive struct {
	objs    map[string][]byte
	listErr error
	getErr  error
}

func (m *memArchive) Put(_ context.Context, k string, b []byte) error {
	m.objs[k] = b
	return nil
}

func (m *memArchive) Get(_ context.Context, k string) ([]byte, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	b, ok := m.objs[k]
	if !ok {
		return nil, ports.ErrArchiveNotFound
	}
	return b, nil
}

func (m *memArchive) List(_ context.Context, prefix, after string) iter.Seq2[ports.ArchiveObject, error] {
	return func(yield func(ports.ArchiveObject, error) bool) {
		var keys []string
		for k := range m.objs {
			if strings.HasPrefix(k, prefix) && k > after {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for i, k := range keys {
			if m.listErr != nil && i == 1 {
				yield(ports.ArchiveObject{}, m.listErr)
				return
			}
			if !yield(ports.ArchiveObject{Key: k, Size: int64(len(m.objs[k]))}, nil) {
				return
			}
		}
	}
}

// feed membaca payload "kunci=isi;kunci=isi"; "RUSAK" gagal diparse,
// "tolak" menjadi penolakan.
type feed struct{ name string }

func (f feed) Name() string { return f.name }

func (f feed) Parse(body []byte, at time.Time) ([]ports.Event, []ports.Rejection, error) {
	if string(body) == "RUSAK" {
		return nil, nil, errors.New("format berubah")
	}
	var evs []ports.Event
	var rej []ports.Rejection
	for part := range strings.SplitSeq(string(body), ";") {
		k, v, _ := strings.Cut(part, "=")
		if v == "tolak" {
			rej = append(rej, ports.Rejection{Key: k, Reason: errors.New("tidak valid")})
			continue
		}
		evs = append(evs, event{subject: "raw.uji." + f.name, key: k, val: v, at: at})
	}
	return evs, rej, nil
}

type event struct {
	subject, key, val string
	at                time.Time
}

func (e event) Subject() string { return e.subject }
func (e event) Key() string     { return e.key }
func (e event) Content() ([]byte, error) {
	if e.val == "" {
		return nil, errors.New("isi kosong")
	}
	return []byte(e.val), nil
}

func (e event) Encode(m ports.FetchMeta) ([]byte, error) {
	return fmt.Appendf(nil, "%s|%s|%s|%s|%s", e.val, m.Connector, m.FetchedAt.Format(time.RFC3339), m.ArchiveKey, m.PayloadSHA256), nil
}

// publisher meniru deduplikasi JetStream berdasarkan ID pesan.
type publisher struct {
	msgs []ports.Message
	ids  map[string]bool
	err  error
}

func (p *publisher) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	if p.err != nil {
		return ports.PublishResult{}, p.err
	}
	if p.ids == nil {
		p.ids = map[string]bool{}
	}
	if p.ids[m.ID] {
		return ports.PublishResult{Duplicate: true}, nil
	}
	p.ids[m.ID] = true
	p.msgs = append(p.msgs, m)
	return ports.PublishResult{}, nil
}

type clock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *clock) Now() time.Time { return c.now }
func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func put(t *testing.T, a *memArchive, conn string, at time.Time, body string) string {
	t.Helper()
	key, err := emit.Archive(t.Context(), a, conn, "txt", emit.Sum([]byte(body)), []byte(body), at)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestRunMergesByFetchTimeWithOriginalMeta(t *testing.T) {
	a := &memArchive{objs: map[string][]byte{}}
	k1 := put(t, a, "bmkg", t0, "g1=m5")
	put(t, a, "usgs", t0.Add(30*time.Second), "u1=m5.1")
	put(t, a, "bmkg", t0.Add(time.Minute), "g1=m5;g2=m4")   // g1 sama: tidak terbit ulang
	put(t, a, "usgs", t0.Add(90*time.Second), "u1=m5.2")    // revisi: terbit
	put(t, a, "bmkg", t0.Add(2*time.Minute), "g2=m4;g3=m3") // g1 keluar dari feed
	put(t, a, "bmkg-lain", t0, "x=1")                       // awalan mirip, feed lain
	pub := &publisher{}
	var progress int
	rep, err := New(a, pub, &clock{now: t0}).Run(t.Context(), []Feed{feed{"bmkg"}, feed{"usgs"}},
		Options{Progress: func(FeedReport) { progress++ }})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range pub.msgs {
		got = append(got, strings.SplitN(string(m.Data), "|", 2)[0])
	}
	if want := []string{"m5", "m5.1", "m4", "m5.2", "m3"}; !slices.Equal(got, want) {
		t.Fatalf("urutan %v, ingin %v", got, want)
	}
	first := pub.msgs[0]
	body := "g1=m5"
	if want := fmt.Sprintf("m5|bmkg|%s|%s|%s", t0.Format(time.RFC3339), k1, emit.Sum([]byte(body))); string(first.Data) != want {
		t.Fatalf("meta %q, ingin %q", first.Data, want)
	}
	// ID pesan sama dengan polling langsung, jadi pesan yang sudah ada ditolak broker.
	if first.ID != eventid.MsgID("bmkg", "g1", []byte("m5")) || first.Subject != "raw.uji.bmkg" {
		t.Fatalf("%+v", first)
	}
	b, u := rep.Feeds[0], rep.Feeds[1]
	if b.Connector != "bmkg" || b.Payloads != 3 || b.Events != 5 || b.Published != 3 || b.AlreadySeen != 2 ||
		!b.First.Equal(t0) || !b.Last.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("bmkg %+v", b)
	}
	if u.Payloads != 2 || u.Published != 2 {
		t.Fatalf("usgs %+v", u)
	}
	if tot := rep.Totals(); tot.Payloads != 5 || tot.Published != 5 || progress != 5 {
		t.Fatalf("total %+v, progress %d", tot, progress)
	}

	// Replay kedua ke broker yang sama: semua pesan ditolak sebagai duplikat.
	rep2, err := New(a, pub, &clock{now: t0}).Run(t.Context(), []Feed{feed{"usgs"}, feed{"bmkg"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tot := rep2.Totals(); tot.Published != 0 || tot.Duplicates != 5 || len(pub.msgs) != 5 {
		t.Fatalf("replay ulang %+v", tot)
	}
}

func TestRunRange(t *testing.T) {
	a := &memArchive{objs: map[string][]byte{}}
	for i := range 5 {
		put(t, a, "c", t0.Add(time.Duration(i)*time.Hour), fmt.Sprintf("k=v%d", i))
	}
	pub := &publisher{}
	rep, err := New(a, pub, &clock{now: t0}).Run(t.Context(), []Feed{feed{"c"}},
		Options{From: t0.Add(time.Hour), To: t0.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Feeds[0].Payloads != 2 || !rep.Feeds[0].First.Equal(t0.Add(time.Hour)) || !rep.Feeds[0].Last.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("%+v", rep.Feeds[0])
	}
}

func TestRunSkipsBadPayloads(t *testing.T) {
	a := &memArchive{objs: map[string][]byte{}}
	put(t, a, "c", t0, "a=1;b=tolak")
	put(t, a, "c", t0.Add(time.Second), "RUSAK")
	key := put(t, a, "c", t0.Add(2*time.Second), "a=2")
	a.objs[key] = []byte("bukan gzip")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("isi lain"))
	_ = zw.Close()
	a.objs[archivekey.Build("c", "txt", strings.Repeat("0", 12), t0.Add(3*time.Second))] = buf.Bytes() // SHA-256 tidak cocok
	a.objs["c/catatan.txt"] = []byte("objek asing")
	put(t, a, "c", t0.Add(4*time.Second), "a=;b=3") // a: isi tidak bisa diserialisasi
	pub := &publisher{}
	rep, err := New(a, pub, &clock{now: t0}).Run(t.Context(), []Feed{feed{"c"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := rep.Feeds[0]
	if f.Payloads != 5 || f.Corrupt != 3 || f.ParseErrors != 2 || f.Rejected != 1 || f.Published != 2 || len(f.Samples) != 5 {
		t.Fatalf("%+v", f)
	}
	for _, s := range f.Samples[:MaxSamples/2] {
		if s == "" {
			t.Fatal("contoh kosong")
		}
	}
}

func TestRunSpeed(t *testing.T) {
	a := &memArchive{objs: map[string][]byte{}}
	for i := range 3 {
		put(t, a, "c", t0.Add(time.Duration(i)*time.Minute), fmt.Sprintf("k=v%d", i))
	}
	c := &clock{now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	if _, err := New(a, &publisher{}, c).Run(t.Context(), []Feed{feed{"c"}}, Options{Speed: 60}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.sleeps, []time.Duration{time.Second, time.Second}) {
		t.Fatalf("jeda %v", c.sleeps)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := New(a, &publisher{}, &clock{now: t0}).Run(ctx, []Feed{feed{"c"}}, Options{Speed: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("batal saat menunggu: %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	a := &memArchive{objs: map[string][]byte{}}
	put(t, a, "c", t0, "k=1")
	put(t, a, "c", t0.Add(time.Second), "k=2")
	boom := errors.New("broker mati")
	if _, err := New(a, &publisher{err: boom}, &clock{}).Run(t.Context(), []Feed{feed{"c"}}, Options{}); !errors.Is(err, boom) {
		t.Fatalf("publisher: %v", err)
	}
	a.getErr = errors.New("disk")
	if _, err := New(a, &publisher{}, &clock{}).Run(t.Context(), []Feed{feed{"c"}}, Options{}); err == nil || !strings.Contains(err.Error(), "disk") {
		t.Fatalf("get: %v", err)
	}
	a.getErr, a.listErr = nil, errors.New("jaringan")
	rep, err := New(a, &publisher{}, &clock{}).Run(t.Context(), []Feed{feed{"c"}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "jaringan") || rep.Feeds[0].Payloads != 1 {
		t.Fatalf("list: %v, %+v", err, rep)
	}
	for name, tc := range map[string]struct {
		feeds []Feed
		opts  Options
	}{
		"tanpa feed":    {nil, Options{}},
		"feed ganda":    {[]Feed{feed{"c"}, feed{"c"}}, Options{}},
		"nama bergaris": {[]Feed{feed{"a/b"}}, Options{}},
		"rentang":       {[]Feed{feed{"c"}}, Options{From: t0, To: t0}},
		"speed":         {[]Feed{feed{"c"}}, Options{Speed: -1}},
		"payload":       {[]Feed{feed{"c"}}, Options{MaxPayload: -1}},
	} {
		if _, err := New(a, &publisher{}, &clock{}).Run(t.Context(), tc.feeds, tc.opts); !errors.Is(err, ErrOptions) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := Names([]Feed{feed{"b"}, feed{"a"}}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
}

// FuzzRunOrdered: berapa pun feed dan waktu ambilnya, pesan terbit urut waktu
// ambil, dan replay kedua tidak menerbitkan apa pun.
func FuzzRunOrdered(f *testing.F) {
	f.Add([]byte{0, 5, 1, 3, 2, 3, 0, 0})
	f.Fuzz(func(t *testing.T, spec []byte) {
		a := &memArchive{objs: map[string][]byte{}}
		names := []string{"a", "a-b", "b"}
		for i := 0; i+1 < len(spec) && i < 40; i += 2 {
			conn := names[int(spec[i])%len(names)]
			at := t0.Add(time.Duration(spec[i+1]) * time.Second)
			body := fmt.Sprintf("k%d=%s-%d", i, conn, spec[i+1])
			if _, err := emit.Archive(t.Context(), a, conn, "txt", emit.Sum([]byte(body)), []byte(body), at); err != nil {
				t.Fatal(err)
			}
		}
		feeds := []Feed{feed{"b"}, feed{"a"}, feed{"a-b"}}
		pub := &publisher{}
		if _, err := New(a, pub, &clock{}).Run(t.Context(), feeds, Options{}); err != nil {
			t.Fatal(err)
		}
		times := make([]string, len(pub.msgs))
		for i, m := range pub.msgs {
			times[i] = strings.Split(string(m.Data), "|")[2]
		}
		if !slices.IsSortedFunc(times, cmp.Compare) {
			t.Fatalf("tidak urut waktu: %v", times)
		}
		rep, err := New(a, pub, &clock{}).Run(t.Context(), feeds, Options{})
		if err != nil || rep.Totals().Published != 0 {
			t.Fatalf("replay ulang: %+v, %v", rep.Totals(), err)
		}
	})
}
