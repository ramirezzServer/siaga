package firearchive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const key = "KUNCIUJIKUNCIUJIKUNCIUJIKUNCIUJI"

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseAvailability(t *testing.T) {
	body := "\ufeffdata_id,min_date,max_date\nMODIS_NRT,2025-07-01,2026-10-19\nMODIS_SP,2000-11-01,2025-06-30\n" +
		"VIIRS_SNPP_SP,2012-01-20,2025-06-30\nLANDSAT_NRT,2022-06-20,2026-10-19\nVIIRS_NOAA21_NRT,2024-01-17,2026-10-19\n"
	av, err := ParseAvailability([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(av) != 4 || av[fire.MODISSP].From != day("2000-11-01") || av[fire.VIIRSNOAA21].To != day("2026-10-19") {
		t.Fatalf("%+v", av)
	}
	for name, bad := range map[string]string{
		"kosong":       "",
		"tanpa kolom":  "data_id,min_date\nMODIS_SP,2000-11-01\n",
		"tanggal":      "data_id,min_date,max_date\nMODIS_SP,kemarin,2025-06-30\n",
		"tanpa produk": "data_id,min_date,max_date\nLANDSAT_NRT,2022-06-20,2026-10-19\n",
		"key salah":    "Invalid MAP_KEY.",
		"csv rusak":    "data_id,min_date,max_date\n\"MODIS_SP,2000-11-01,2025-06-30\n",
	} {
		if _, err := ParseAvailability([]byte(bad)); err == nil {
			t.Errorf("%s diterima", name)
		}
	}
}

func TestPlan(t *testing.T) {
	av := Availability{
		fire.VIIRSSNPPSP: {From: day("2012-01-20"), To: day("2025-06-30")},
		fire.VIIRSSNPP:   {From: day("2025-05-01"), To: day("2026-10-19")},
		fire.VIIRSNOAA21: {From: day("2024-01-17"), To: day("2026-10-19")},
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	chunks, spans := Plan(fire.Satellites, av, Range{From: day("2025-06-20"), To: day("2030-01-01")}, now)
	if len(spans) != 3 || spans[0].Product != fire.VIIRSSNPPSP || spans[1].From != day("2025-07-01") || spans[1].To != day("2026-10-19") ||
		spans[2].Satellite != "NOAA-21" || spans[2].From != day("2025-06-20") {
		t.Fatalf("%+v", spans)
	}
	// Potongan mengikuti grid Epoch: 2025-06-20 .. 2025-06-22 (grid 2025-06-18 .. 2025-06-22).
	first := chunks[0]
	if first.Product != fire.VIIRSSNPPSP || first.Start != day("2025-06-20") || first.Days != 3 || !first.Final ||
		first.Key() != "VIIRS_SNPP_SP/2025/2025-06-20.d3.csv" || first.End() != day("2025-06-22") {
		t.Fatalf("%+v %s", first, first.Key())
	}
	covered := map[fire.Product]int{}
	for i, c := range chunks {
		covered[c.Product] += c.Days
		if c.Days < 1 || c.Days > ChunkDays {
			t.Fatalf("potongan %+v", c)
		}
		if k := int(c.Start.Sub(Epoch).Hours()/24) / ChunkDays; int(c.End().Sub(Epoch).Hours()/24)/ChunkDays != k {
			t.Fatalf("potongan melewati grid %+v", c)
		}
		if i > 0 && chunks[i-1].Product == c.Product && chunks[i-1].End().AddDate(0, 0, 1) != c.Start {
			t.Fatalf("celah sebelum %+v", c)
		}
	}
	for _, s := range spans {
		if covered[s.Product] != s.Days() {
			t.Fatalf("%s: %d hari dari %d", s.Product, covered[s.Product], s.Days())
		}
	}
	last := chunks[len(chunks)-1]
	if last.Product != fire.VIIRSNOAA21 || last.End() != day("2026-10-19") || last.Final {
		t.Fatalf("potongan terakhir %+v", last)
	}
	// NRT yang sudah lewat seminggu final.
	for _, c := range chunks {
		if c.Product == fire.VIIRSNOAA21 && c.End().Before(day("2026-10-13")) && !c.Final {
			t.Fatalf("%+v belum final", c)
		}
	}
	if chunks, _ := Plan(fire.Satellites, av, Range{From: day("2030-01-01"), To: day("2030-02-01")}, now); len(chunks) != 0 {
		t.Fatal("rentang masa depan menghasilkan potongan")
	}
	if (Range{}).Days() != 0 || (Range{From: day("2026-01-02"), To: day("2026-01-01")}).Valid() {
		t.Fatal("rentang kosong")
	}
}

// fake adalah FIRMS tiruan.
type fake struct {
	mu       sync.Mutex
	area     []string
	status   []string
	statusN  int
	failArea map[string][]error
	body     func(url string) string
	statusFn func(n int) (string, error)
}

func (f *fake) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.Contains(r.URL, "/mapserver/mapkey_status/"):
		f.statusN++
		f.status = append(f.status, r.URL)
		if f.statusFn != nil {
			b, err := f.statusFn(f.statusN)
			return ports.Response{Body: []byte(b)}, err
		}
		return ports.Response{Body: []byte(`{"transaction_limit":5000,"current_transactions":10,"transaction_interval":"10 minutes"}`)}, nil
	case strings.Contains(r.URL, "/api/data_availability/"):
		return ports.Response{Body: []byte("data_id,min_date,max_date\nVIIRS_SNPP_SP,2012-01-20,2025-06-30\n")}, nil
	}
	f.area = append(f.area, r.URL)
	for frag, errs := range f.failArea {
		if strings.Contains(r.URL, frag) && len(errs) > 0 {
			err := errs[0]
			f.failArea[frag] = errs[1:]
			if err != nil {
				return ports.Response{}, err
			}
			return ports.Response{Body: []byte("Exceeding allowed transaction limit. Try again later.")}, nil
		}
	}
	return ports.Response{Body: []byte(f.body(r.URL))}, nil
}

type clock struct {
	mu     sync.Mutex
	slept  []time.Duration
	cancel bool
}

func (c *clock) Now() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	if c.cancel {
		return context.Canceled
	}
	return ctx.Err()
}

const header = "latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_ti5,frp,daynight,type\n"

func csvFor(url string) string {
	// Satu baris per potongan supaya isi berbeda.
	parts := strings.Split(url, "/")
	date := parts[len(parts)-1]
	return header + fmt.Sprintf("-6.9,107.6,330,0.4,0.4,%s,600,N,VIIRS,n,2,290,3,D,0\n", date)
}

func check(p fire.Product, b []byte) error {
	if !strings.HasPrefix(string(b), "latitude,") {
		return fmt.Errorf("%s: bukan CSV", p)
	}
	return nil
}

func loader(t *testing.T, f *fake, c *clock) *Loader {
	t.Helper()
	cache, err := fsarchive.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Loader{Fetch: f, Clock: c, Cache: cache, Key: key, Box: area.JawaBarat, Check: check, BaseURL: "http://firms.test/"}
}

func TestLoad(t *testing.T) {
	f := &fake{body: csvFor}
	c := &clock{}
	l := loader(t, f, c)
	l.StatusEvery = 2
	av, err := l.Availability(t.Context())
	if err != nil || av[fire.VIIRSSNPPSP].To != day("2025-06-30") {
		t.Fatal(av, err)
	}
	chunks := []Chunk{
		{Product: fire.VIIRSSNPPSP, Start: day("2019-08-04"), Days: 5, Final: true},
		{Product: fire.VIIRSSNPPSP, Start: day("2019-08-09"), Days: 5, Final: true},
		{Product: fire.VIIRSSNPP, Start: day("2026-10-15"), Days: 5, Final: false},
	}
	var seen []string
	var progress []bool
	l.Progress = func(done, total int, c Chunk, cached bool) { progress = append(progress, cached) }
	st, err := l.Load(t.Context(), chunks, func(c Chunk, b []byte) error {
		seen = append(seen, c.Key())
		return nil
	})
	if err != nil || st.Fetched != 3 || st.Cached != 0 || len(seen) != 3 || st.LastStatus.Limit != 5000 {
		t.Fatalf("%+v %v", st, err)
	}
	if want := "http://firms.test/api/area/csv/" + key + "/VIIRS_SNPP_SP/106.3,-7.9,108.9,-5.7/5/2019-08-04"; f.area[0] != want {
		t.Fatalf("URL %s", f.area[0])
	}
	// Status diperiksa sebelum request pertama dan setiap 2 request.
	if len(f.status) != 2 {
		t.Fatalf("status %d kali", len(f.status))
	}
	// Jalan kedua: potongan final dari cache, NRT terbaru diambil lagi.
	f.area = nil
	st, err = l.Load(t.Context(), chunks, func(Chunk, []byte) error { return nil })
	if err != nil || st.Cached != 2 || st.Fetched != 1 || len(f.area) != 1 || !strings.Contains(f.area[0], "VIIRS_SNPP_NRT") {
		t.Fatalf("%+v %v %v", st, err, f.area)
	}
	if len(progress) != 6 || progress[3] != true {
		t.Fatalf("progres %v", progress)
	}
	// Batas request.
	l.MaxRequests = 1
	f.area = nil
	more := append(chunks, Chunk{Product: fire.VIIRSSNPPSP, Start: day("2019-08-14"), Days: 5, Final: true})
	_, err = l.Load(t.Context(), more, func(Chunk, []byte) error { return nil })
	var be *BudgetError
	if !errors.As(err, &be) || be.Remaining != 1 || !strings.Contains(be.Error(), "1 potongan") {
		t.Fatalf("%v", err)
	}
	// visit gagal menghentikan Load.
	l.MaxRequests = 0
	if _, err := l.Load(t.Context(), chunks, func(Chunk, []byte) error { return errors.New("berhenti") }); err == nil {
		t.Fatal("galat visit hilang")
	}
	if len(c.slept) != 0 {
		t.Fatalf("tidur %v", c.slept)
	}
}

func TestLoadRetries(t *testing.T) {
	chunk := []Chunk{{Product: fire.VIIRSSNPPSP, Start: day("2019-08-04"), Days: 5, Final: true}}
	t.Run("batas lalu pulih", func(t *testing.T) {
		f := &fake{body: csvFor, failArea: map[string][]error{"2019-08-04": {
			nil, // teks batas transaksi
			&httpfetch.StatusError{Status: 503, Body: "sibuk"},
			&httpfetch.StatusError{Status: 403, Body: "MAP_KEY is invalid or your have exceeded"},
			&ports.RetryAfterError{Status: 429},
		}}}
		c := &clock{}
		var notes []string
		l := loader(t, f, c)
		l.Notice = func(s string) { notes = append(notes, s) }
		st, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil })
		if err != nil || st.Fetched != 1 || len(c.slept) != 4 || c.slept[1] != 2*time.Minute || len(notes) != 4 {
			t.Fatalf("%+v %v %v %v", st, err, c.slept, notes)
		}
		for _, n := range notes {
			if strings.Contains(n, key) {
				t.Fatalf("key bocor: %s", n)
			}
		}
	})
	t.Run("key salah", func(t *testing.T) {
		f := &fake{body: csvFor, failArea: map[string][]error{"2019": {&httpfetch.StatusError{Status: 400, Body: "Invalid MAP_KEY."}}}}
		l := loader(t, f, &clock{})
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); !errors.Is(err, ErrKey) {
			t.Fatal(err)
		}
	})
	t.Run("galat lain langsung", func(t *testing.T) {
		f := &fake{body: csvFor, failArea: map[string][]error{"2019": {&httpfetch.StatusError{Status: 404, Body: "tidak ada"}}}}
		l := loader(t, f, &clock{})
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); err == nil || errors.Is(err, ErrKey) {
			t.Fatal(err)
		}
	})
	t.Run("bukan CSV", func(t *testing.T) {
		f := &fake{body: func(string) string { return "<html>pemeliharaan</html>" }}
		l := loader(t, f, &clock{})
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); err == nil || !strings.Contains(err.Error(), "bukan CSV") {
			t.Fatal(err)
		}
	})
	t.Run("habis percobaan", func(t *testing.T) {
		errs := make([]error, 10)
		for i := range errs {
			errs[i] = &httpfetch.StatusError{Status: 502}
		}
		f := &fake{body: csvFor, failArea: map[string][]error{"2019": errs}}
		c := &clock{}
		l := loader(t, f, c)
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); err == nil || !strings.Contains(err.Error(), "gagal 6 kali") {
			t.Fatal(err)
		}
		if c.slept[len(c.slept)-1] != 10*time.Minute {
			t.Fatalf("jeda maks %v", c.slept)
		}
	})
	t.Run("batal saat menunggu", func(t *testing.T) {
		f := &fake{body: csvFor, failArea: map[string][]error{"2019": {&httpfetch.StatusError{Status: 502}}}}
		l := loader(t, f, &clock{cancel: true})
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestStatusWaits(t *testing.T) {
	chunk := []Chunk{{Product: fire.VIIRSSNPPSP, Start: day("2019-08-04"), Days: 5, Final: true}}
	t.Run("kuota hampir habis lalu pulih", func(t *testing.T) {
		f := &fake{body: csvFor, statusFn: func(n int) (string, error) {
			switch n {
			case 1:
				return `{"transaction_limit":5000,"current_transactions":4900}`, nil
			case 2:
				return "", &httpfetch.StatusError{Status: 403, Body: "exceeded"}
			}
			return `{"transaction_limit":"5000","current_transactions":"12"}`, nil
		}}
		c := &clock{}
		l := loader(t, f, c)
		st, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil })
		if err != nil || st.Waits != 2 || len(c.slept) != 2 || st.LastStatus.Used != 12 {
			t.Fatalf("%+v %v", st, err)
		}
	})
	t.Run("status tidak dikenal", func(t *testing.T) {
		for _, fn := range []func(int) (string, error){
			func(int) (string, error) { return "<html>", nil },
			func(int) (string, error) { return `{"x":1}`, nil },
			func(int) (string, error) { return "", errors.New("jaringan") },
		} {
			f := &fake{body: csvFor, statusFn: fn}
			c := &clock{}
			l := loader(t, f, c)
			st, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil })
			if err != nil || st.Fetched != 1 || len(c.slept) != 0 || st.LastStatus.Limit != 0 {
				t.Fatalf("%+v %v", st, err)
			}
		}
	})
	t.Run("tidak pernah pulih", func(t *testing.T) {
		f := &fake{body: csvFor, statusFn: func(int) (string, error) { return `{"transaction_limit":5000,"current_transactions":5000}`, nil }}
		c := &clock{}
		l := loader(t, f, c)
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); err == nil || len(c.slept) != 30 {
			t.Fatal(err, len(c.slept))
		}
	})
	t.Run("batal saat menunggu kuota", func(t *testing.T) {
		f := &fake{body: csvFor, statusFn: func(int) (string, error) { return `{"transaction_limit":5000,"current_transactions":5000}`, nil }}
		l := loader(t, f, &clock{cancel: true})
		if _, err := l.Load(t.Context(), chunk, func(Chunk, []byte) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestLoaderConfig(t *testing.T) {
	l := &Loader{}
	if _, err := l.Load(t.Context(), nil, nil); err == nil {
		t.Fatal("loader kosong diterima")
	}
	l = loader(t, &fake{body: csvFor}, &clock{})
	l.Box = area.Box{}
	if _, err := l.Load(t.Context(), nil, nil); err == nil {
		t.Fatal("kotak kosong diterima")
	}
	l = loader(t, &fake{body: csvFor}, &clock{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := l.Load(ctx, []Chunk{{Product: fire.MODISSP, Start: day("2019-01-01"), Days: 1, Final: true}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
