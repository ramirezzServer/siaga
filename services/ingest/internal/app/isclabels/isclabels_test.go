package isclabels

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/quakeml"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quakelabel"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPlan(t *testing.T) {
	weeks := Plan(Range{From: day("2024-10-17"), To: day("2024-10-31")})
	if len(weeks) != 3 {
		t.Fatalf("%d minggu", len(weeks))
	}
	if weeks[0].Key() != "2024/2024-10-17.d7.xml.gz" || weeks[2].Days != 1 || !weeks[2].End().Equal(day("2024-11-01")) {
		t.Fatalf("%+v %s", weeks, weeks[0].Key())
	}
	full := Plan(Range{From: day("2021-01-01"), To: day("2024-10-31")})
	if len(full) != 200 {
		t.Fatalf("periode bawaan %d minggu", len(full))
	}
	total := 0
	for _, w := range full {
		total += w.Days
	}
	if total != int(day("2024-11-01").Sub(day("2021-01-01"))/(24*time.Hour)) {
		t.Fatalf("hari tidak tertutup penuh: %d", total)
	}
	if Plan(Range{From: day("2024-01-02"), To: day("2024-01-01")}) != nil || Plan(Range{}) != nil {
		t.Fatal("rentang tidak valid harus kosong")
	}
}

func TestURL(t *testing.T) {
	l := &Loader{Box: area.Indonesia}
	u, err := url.Parse(l.URL(Week{Start: day("2023-01-15"), Days: 7}))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "www.isc.ac.uk" || q.Get("starttime") != "2023-01-15T00:00:00" || q.Get("endtime") != "2023-01-22T00:00:00" ||
		q.Get("minlatitude") != "-12" || q.Get("maxlongitude") != "142" || q.Get("includeallorigins") != "true" {
		t.Fatalf("%s", u)
	}
}

type reply struct {
	body string
	err  error
}

type fake struct {
	replies []reply // dipakai berurutan; sesudah habis, sample
	urls    []string
	sample  string
}

func (f *fake) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.urls = append(f.urls, r.URL)
	if len(f.replies) > 0 {
		x := f.replies[0]
		f.replies = f.replies[1:]
		return ports.Response{Body: []byte(x.body)}, x.err
	}
	return ports.Response{Body: []byte(f.sample)}, nil
}

type clock struct{ slept []time.Duration }

func (c *clock) Now() time.Time { return day("2026-11-09") }

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	return ctx.Err()
}

func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../adapters/quakeml/testdata/isc-sampel.xml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newLoader(t *testing.T, f *fake, c *clock) *Loader {
	t.Helper()
	cache, err := fsarchive.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Loader{Fetch: f, Clock: c, Cache: cache, Box: area.Indonesia}
}

func TestLoad(t *testing.T) {
	f, c := &fake{sample: sample(t), replies: []reply{{err: &httpfetch.StatusError{Status: 204}}}}, &clock{}
	l := newLoader(t, f, c)
	weeks := Plan(Range{From: day("2023-01-01"), To: day("2023-01-21")})
	var progress []bool
	l.Progress = func(_, _ int, _ Week, cached bool) { progress = append(progress, cached) }
	count := 0
	st, err := l.Load(t.Context(), weeks, func(_ Week, evs []quakelabel.Event) error {
		count += len(evs)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Fetched != 3 || st.Cached != 0 || st.Empty != 1 || count != 12 || len(f.urls) != 3 {
		t.Fatalf("%+v, %d kejadian, %d request", st, count, len(f.urls))
	}
	if len(c.slept) != 2 || c.slept[0] != DefaultPause {
		t.Fatalf("jeda %v", c.slept)
	}
	// Jalan ulang: semua dari cache, tanpa request; minggu kosong tetap kosong.
	count = 0
	st, err = l.Load(t.Context(), weeks, func(_ Week, evs []quakelabel.Event) error {
		count += len(evs)
		return nil
	})
	if err != nil || st.Cached != 3 || st.Fetched != 0 || count != 12 || len(f.urls) != 3 {
		t.Fatalf("jalan ulang %+v %v %d", st, err, count)
	}
	if len(progress) != 6 || progress[0] || !progress[5] {
		t.Fatalf("progres %v", progress)
	}
	// Offline dengan cache lengkap jalan; dengan minggu baru ditolak.
	l.Offline, l.Fetch, l.Clock = true, nil, nil
	if _, err := l.Load(t.Context(), weeks, func(Week, []quakelabel.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	more := Plan(Range{From: day("2023-01-22"), To: day("2023-01-22")})
	if _, err := l.Load(t.Context(), more, func(Week, []quakelabel.Event) error { return nil }); !errors.Is(err, ErrNotCached) {
		t.Fatalf("offline: %v", err)
	}
	if _, err := l.Load(t.Context(), weeks, func(Week, []quakelabel.Event) error { return errors.New("berhenti") }); err == nil {
		t.Fatal("galat visit harus diteruskan")
	}
}

func TestLoadBudgetAndValidation(t *testing.T) {
	f, c := &fake{sample: sample(t)}, &clock{}
	l := newLoader(t, f, c)
	l.MaxRequests = 1
	weeks := Plan(Range{From: day("2023-01-01"), To: day("2023-01-14")})
	_, err := l.Load(t.Context(), weeks, func(Week, []quakelabel.Event) error { return nil })
	var be *BudgetError
	if !errors.As(err, &be) || be.Remaining != 1 || !strings.Contains(be.Error(), "1 minggu") {
		t.Fatalf("%v", err)
	}
	if _, err := (&Loader{}).Load(t.Context(), weeks, nil); err == nil {
		t.Fatal("Loader kosong harus galat")
	}
	l.Box = area.Box{West: 0, South: 0, East: 1, North: 1}
	if _, err := l.Load(t.Context(), weeks, nil); err == nil {
		t.Fatal("kotak di luar Indonesia harus galat")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	l.Box = area.Indonesia
	if _, err := l.Load(ctx, weeks, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("batal: %v", err)
	}
}

func TestLoadRetries(t *testing.T) {
	week := Plan(Range{From: day("2023-01-15"), To: day("2023-01-21")})
	t.Run("sementara lalu pulih", func(t *testing.T) {
		f := &fake{sample: sample(t), replies: []reply{
			{err: &ports.RetryAfterError{Status: 429, After: 5 * time.Minute}},
			{err: &httpfetch.StatusError{Status: 502}},
			{err: errors.New("dial tcp: i/o timeout")},
			{body: "<html>Service Temporarily Unavailable</html>"},
		}}
		c := &clock{}
		l := newLoader(t, f, c)
		var notes []string
		l.Notice = func(s string) { notes = append(notes, s) }
		st, err := l.Load(t.Context(), week, func(Week, []quakelabel.Event) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if st.Retries != 4 || len(notes) != 4 || st.Fetched != 1 {
			t.Fatalf("%+v %v", st, notes)
		}
		// 429 dengan Retry-After 5 menit dihormati; sesudahnya jeda bertingkat 60 s, 120 s, 240 s.
		want := []time.Duration{5 * time.Minute, 60 * time.Second, 120 * time.Second, 240 * time.Second}
		for i, d := range want {
			if c.slept[i] != d {
				t.Fatalf("jeda %v, ingin %v", c.slept, want)
			}
		}
	})
	t.Run("galat klien langsung berhenti", func(t *testing.T) {
		f := &fake{sample: sample(t), replies: []reply{{err: &httpfetch.StatusError{Status: 400, Body: "Bad request"}}}}
		l := newLoader(t, f, &clock{})
		if _, err := l.Load(t.Context(), week, func(Week, []quakelabel.Event) error { return nil }); err == nil || len(f.urls) != 1 {
			t.Fatalf("%v, %d request", err, len(f.urls))
		}
	})
	t.Run("menyerah", func(t *testing.T) {
		var rs []reply
		for range DefaultMaxRetries {
			rs = append(rs, reply{err: &httpfetch.StatusError{Status: 503}})
		}
		f := &fake{sample: sample(t), replies: rs}
		l := newLoader(t, f, &clock{})
		_, err := l.Load(t.Context(), week, func(Week, []quakelabel.Event) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "gagal 5 kali") {
			t.Fatalf("%v", err)
		}
		if _, gerr := l.Cache.Get(t.Context(), week[0].Key()); !errors.Is(gerr, ports.ErrArchiveNotFound) {
			t.Fatal("jawaban gagal tidak boleh masuk cache")
		}
	})
	t.Run("batal saat menunggu", func(t *testing.T) {
		f := &fake{sample: sample(t), replies: []reply{{err: &httpfetch.StatusError{Status: 503}}}}
		ctx, cancel := context.WithCancel(t.Context())
		l := newLoader(t, f, &clock{})
		l.Clock = sleeper(cancel)
		if _, err := l.Load(ctx, week, func(Week, []quakelabel.Event) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	})
}

type sleeper func()

func (sleeper) Now() time.Time { return time.Time{} }

func (s sleeper) Sleep(ctx context.Context, _ time.Duration) error {
	s()
	return ctx.Err()
}

func TestLoadCorruptCache(t *testing.T) {
	week := Plan(Range{From: day("2023-01-15"), To: day("2023-01-21")})
	for name, data := range map[string][]byte{"bukan gzip": []byte("x")} {
		t.Run(name, func(t *testing.T) {
			l := newLoader(t, &fake{}, &clock{})
			if err := l.Cache.Put(t.Context(), week[0].Key(), data); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Load(t.Context(), week, nil); err == nil || !strings.Contains(err.Error(), "rusak") {
				t.Fatalf("%v", err)
			}
		})
	}
	t.Run("gzip bukan QuakeML", func(t *testing.T) {
		l := newLoader(t, &fake{}, &clock{})
		gz, err := gzipBytes([]byte("<html/>"))
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Cache.Put(t.Context(), week[0].Key(), gz); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Load(t.Context(), week, nil); err == nil || !strings.Contains(err.Error(), "rusak") {
			t.Fatalf("%v", err)
		}
	})
}

func TestWriteCSV(t *testing.T) {
	events, err := quakeml.Parse([]byte(sample(t)))
	if err != nil {
		t.Fatal(err)
	}
	rows, st, err := quakelabel.Build(events, quakelabel.DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].DepthKm = math.NaN()
	rows[0].Note = `koma, "kutip"`
	var b bytes.Buffer
	m := Meta{Range: Range{From: day("2023-01-08"), To: day("2023-01-18")}, Box: area.Indonesia, Opt: quakelabel.DefaultOptions, Stats: st}
	if err := WriteCSV(&b, m, rows); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if !strings.HasPrefix(lines[0], "# Set berlabel") || !strings.Contains(lines[1], "10.31905/D808B830") ||
		!strings.Contains(lines[2], "2023-01-08..2023-01-18") || !strings.Contains(lines[3], "6 kejadian ISC, 4 origin USGS (4 berpasangan BMKG), 4 origin BMKG, 2 pasangan negatif") {
		t.Fatalf("komentar:\n%s", strings.Join(lines[:4], "\n"))
	}
	if lines[4] != strings.Join(Header, ",") || len(lines) != 5+8 {
		t.Fatalf("%d baris:\n%s", len(lines), b.String())
	}
	if lines[5] != `isc,625597758,usgs,619770658,2023-01-08T20:02:04.79Z,1.5274,128.2405,,4.30,mb,,"koma, ""kutip"""` {
		t.Fatalf("baris pertama %s", lines[5])
	}
	if lines[6] != "isc,625597758,bmkg,631703347,2023-01-08T20:02:05.10Z,1.6000,128.2900,63.0,4.38,MLv,Mjma 4.31," {
		t.Fatalf("baris kedua %s", lines[6])
	}
	if !strings.Contains(b.String(), "isc,625554142,usgs,620100004,2023-01-15T22:29:58.90Z,2.0045,98.0143,10.0,6.10,Mww,,") ||
		!strings.Contains(b.String(), "isc,625554142,bmkg,631703500,2023-01-15T22:30:03.00Z,1.9700,97.9100,10.0,6.16,Mw,MLv 6.51; Mwp 6.1; mB 6.38; mb 6.08,") {
		t.Fatalf("Sumatra:\n%s", b.String())
	}
}

func TestGzipRoundTrip(t *testing.T) {
	gz, err := gzipBytes([]byte(quakeml.Empty))
	if err != nil {
		t.Fatal(err)
	}
	b, err := gunzip(gz)
	if err != nil || string(b) != quakeml.Empty {
		t.Fatalf("%q %v", b, err)
	}
}
