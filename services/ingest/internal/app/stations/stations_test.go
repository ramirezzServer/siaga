package stations

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var t0 = time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)

// fakeSource: daftar berisi baris "id|menit sejak laporan terakhir"; baris
// "!" membuat daftar rusak, baris "?x" ditolak. Nilai terbaru per stasiun
// diambil dari readings.
type fakeSource struct {
	readings map[string][]airquality.Reading
	bad      map[string]error // galat ParseDetail per ID
	noEvent  map[string]bool
}

func (fakeSource) Name() string { return "openaq-stasiun" }
func (fakeSource) ListRequest() ports.Request {
	return ports.Request{URL: "daftar?key=RAHASIA", Secrets: []string{"RAHASIA"}}
}

func (fakeSource) ParseList(body []byte) ([]Station, []ports.Rejection, error) {
	var out []Station
	var rej []ports.Rejection
	for line := range strings.Lines(string(body)) {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case line == "!":
			return nil, nil, errors.New("daftar rusak")
		case strings.HasPrefix(line, "?"):
			rej = append(rej, ports.Rejection{Key: line, Reason: errors.New("lokasi rusak")})
		default:
			id, ago, _ := strings.Cut(line, "|")
			st := Station{Station: airquality.Station{ID: id, Name: "Stasiun " + id, Provider: "Uji", Lat: -6.9, Lon: 107.6}}
			if ago != "" {
				m, _ := strconv.Atoi(ago)
				st.LastReport = t0.Add(-time.Duration(m) * time.Minute)
			}
			out = append(out, st)
		}
	}
	return out, rej, nil
}

func (fakeSource) DetailRequest(st Station) ports.Request {
	return ports.Request{URL: "latest/" + st.ID}
}

func (s fakeSource) ParseDetail(st Station, _ []byte) ([]airquality.Reading, error) {
	if err := s.bad[st.ID]; err != nil {
		return nil, err
	}
	return s.readings[st.ID], nil
}

func (s fakeSource) Event(o airquality.Observation) (ports.Event, error) {
	if s.noEvent[o.Station.ID] {
		return nil, errors.New("gagal membungkus")
	}
	return fakeEvent{o: o}, nil
}

type fakeEvent struct{ o airquality.Observation }

func (e fakeEvent) Subject() string { return "raw.aq.openaq" }
func (e fakeEvent) Key() string     { return e.o.Station.ID }
func (e fakeEvent) Content() ([]byte, error) {
	var b strings.Builder
	b.WriteString(e.o.Station.ID)
	for _, r := range e.o.Readings {
		fmt.Fprintf(&b, "|%d:%v@%s", r.SensorID, r.Value, r.ObservedAt.Format(time.RFC3339))
	}
	return []byte(b.String()), nil
}

func (e fakeEvent) Encode(m ports.FetchMeta) ([]byte, error) {
	c, _ := e.Content()
	return fmt.Appendf(c, "#%s", m.ArchiveKey), nil
}

type fakeFetcher struct {
	list     []string
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
	if strings.HasPrefix(r.URL, "daftar") {
		body := f.list[0]
		if len(f.list) > 1 {
			f.list = f.list[1:]
		}
		if body == "304" {
			return ports.Response{NotModified: true}, nil
		}
		return ports.Response{Body: []byte(body)}, nil
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
	msgs  []ports.Message
	ids   map[string]bool
	failN int
}

func (p *fakePub) Publish(_ context.Context, m ports.Message) (ports.PublishResult, error) {
	if p.failN > 0 {
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

type throttle struct {
	calls int
	err   error
}

func (t *throttle) Wait(context.Context) error {
	t.calls++
	return t.err
}

func pm25(id int64, v float64, ago time.Duration) airquality.Reading {
	return airquality.Reading{SensorID: id, Parameter: airquality.PM25, Unit: airquality.UnitMicrogram, Value: v, ObservedAt: t0.Add(-ago)}
}

type rig struct {
	src   fakeSource
	fetch *fakeFetcher
	arch  *fakeArchive
	pub   *fakePub
	clock *fakeClock
	thr   *throttle
	p     *Poller
}

func newRig(t *testing.T, opts Options, lists ...string) *rig {
	t.Helper()
	r := &rig{
		src: fakeSource{
			readings: map[string][]airquality.Reading{"openaq:1": {pm25(11, 40, 10*time.Minute)}},
			bad:      map[string]error{}, noEvent: map[string]bool{},
		},
		fetch: &fakeFetcher{list: lists, errs: map[string][]error{}},
		arch:  &fakeArchive{}, pub: &fakePub{ids: map[string]bool{}}, clock: &fakeClock{now: t0}, thr: &throttle{},
	}
	p, err := New(r.src, r.fetch, r.arch, r.pub, r.clock, r.thr, opts)
	if err != nil {
		t.Fatal(err)
	}
	r.p = p
	return r
}

func (r *rig) poll(t *testing.T) poll.Result {
	t.Helper()
	res, err := r.p.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestActiveStationsOnly(t *testing.T) {
	r := newRig(t, Options{MaxAttempts: 2}, "openaq:1|10\nopenaq:2|4320\nopenaq:3|30\nopenaq:4\n?rusak")
	r.fetch.errs["latest/openaq:3"] = []error{errors.New("timeout"), errors.New("timeout")}
	res := r.poll(t)
	if res.Events != 2 || res.Published != 1 || res.Failed != 1 || res.Rejected != 1 ||
		r.fetch.count("latest/openaq:2") != 0 || r.fetch.count("latest/openaq:4") != 0 || r.thr.calls != 2 {
		t.Fatalf("%+v, request %v", res, r.fetch.requests)
	}
	if r.p.Name() != "openaq-stasiun" || len(r.arch.keys) != 2 || !strings.HasPrefix(r.arch.keys[1], "openaq-stasiun-latest/") ||
		!strings.HasSuffix(string(r.pub.msgs[0].Data), "#"+r.arch.keys[1]) {
		t.Fatalf("arsip %v, pesan %s", r.arch.keys, r.pub.msgs[0].Data)
	}

	// Polling kedua: stasiun 1 tidak berubah (tanpa request), stasiun 3 gagal
	// lagi lalu dilewati sampai laporannya berubah.
	res = r.poll(t)
	if res.AlreadySeen != 1 || res.Failed != 1 || !strings.Contains(res.Failures[0].Reason.Error(), "menyerah") ||
		r.fetch.count("latest/openaq:1") != 1 || len(r.arch.keys) != 2 {
		t.Fatalf("%+v", res)
	}
	res = r.poll(t)
	if res.AlreadySeen != 2 || r.fetch.count("latest/openaq:3") != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestNewReportSameContent(t *testing.T) {
	r := newRig(t, Options{}, "openaq:1|10", "openaq:1|5", "openaq:1|1")
	r.poll(t)
	// Laporan baru tetapi nilai terbaru sama: tidak terbit ulang.
	res := r.poll(t)
	if res.Published != 0 || res.AlreadySeen != 1 || r.fetch.count("latest/openaq:1") != 2 || len(r.pub.msgs) != 1 {
		t.Fatalf("%+v", res)
	}
	r.src.readings["openaq:1"] = []airquality.Reading{pm25(11, 44, time.Minute)}
	res = r.poll(t)
	if res.Published != 1 || len(r.pub.msgs) != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestPublishFailureRetries(t *testing.T) {
	r := newRig(t, Options{}, "openaq:1|10")
	r.pub.failN = 1
	if _, err := r.p.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "NATS mati") {
		t.Fatalf("galat %v", err)
	}
	res := r.poll(t)
	if res.Published != 1 || r.fetch.count("latest/openaq:1") != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestReadingsCleaned(t *testing.T) {
	r := newRig(t, Options{}, "openaq:1|10\nopenaq:5|10\nopenaq:6|10\nopenaq:7|10\nopenaq:8|10\nopenaq:1|10")
	r.src.readings["openaq:1"] = []airquality.Reading{
		pm25(11, 40, 10*time.Minute),
		{SensorID: 12, Parameter: airquality.PM10, Unit: "ppm", Value: 1, ObservedAt: t0},
		pm25(13, 20, 3*24*time.Hour), // basi, dibuang diam-diam
	}
	r.src.readings["openaq:5"] = []airquality.Reading{pm25(51, 1, 3*24*time.Hour)}
	r.src.bad["openaq:6"] = errors.New("format berubah")
	r.src.readings["openaq:7"] = []airquality.Reading{pm25(71, 5, time.Minute)}
	r.src.noEvent["openaq:7"] = true
	r.src.readings["openaq:8"] = []airquality.Reading{pm25(81, 5, time.Minute)}
	res := r.poll(t)
	// Terbit: stasiun 1 (tanpa nilai rusak dan basi) dan 8. Ditolak: satuan
	// salah, detail rusak, gagal bungkus, stasiun ganda. Stasiun 5 hanya
	// punya nilai basi: tidak terbit dan tidak dihitung ditolak.
	if res.Published != 2 || res.Rejected != 4 {
		t.Fatalf("%+v", res)
	}
	if got := string(r.pub.msgs[0].Data); !strings.HasPrefix(got, "openaq:1|11:40@") || strings.Contains(got, "|12:") || strings.Contains(got, "|13:") {
		t.Fatalf("pesan %s", got)
	}
}

func TestInvalidObservationRejected(t *testing.T) {
	r := newRig(t, Options{}, "openaq:0|10")
	r.src.readings["openaq:0"] = []airquality.Reading{pm25(1, 5, time.Minute)}
	res := r.poll(t)
	if res.Rejected != 1 || res.Published != 0 || !errors.Is(res.Rejections[0].Reason, airquality.ErrInvalid) {
		t.Fatalf("%+v", res)
	}
}

func TestListFailures(t *testing.T) {
	r := newRig(t, Options{}, "!", "304", "openaq:1|10")
	if _, err := r.p.Poll(context.Background()); !errors.Is(err, poll.ErrParse) {
		t.Fatalf("daftar rusak: %v", err)
	}
	if _, err := r.p.Poll(context.Background()); err == nil {
		t.Fatal("304 tanpa validator harus galat")
	}
	r.fetch.errs["daftar?key=RAHASIA"] = []error{errors.New("dial tcp")}
	_, err := r.p.Poll(context.Background())
	if err == nil || strings.Contains(err.Error(), "RAHASIA") || !strings.Contains(err.Error(), "daftar?key=***") {
		t.Fatalf("galat %v", err)
	}
}

func TestArchiveFailureAndRemoval(t *testing.T) {
	r := newRig(t, Options{}, "openaq:1|10", "openaq:9|10")
	r.arch.fail = true
	res := r.poll(t)
	if res.ArchiveErr == nil || res.Published != 1 || !strings.HasSuffix(string(r.pub.msgs[0].Data), "#") {
		t.Fatalf("%+v", res)
	}
	r.poll(t)
	if _, ok := r.p.states["openaq:1"]; ok || len(r.p.states) != 1 {
		t.Fatalf("status stasiun yang hilang dari daftar tidak dibuang: %v", r.p.states)
	}
}

func TestCancelAndOptions(t *testing.T) {
	r := newRig(t, Options{}, "openaq:1|10")
	r.thr.err = context.Canceled
	if _, err := r.p.Poll(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	r = newRig(t, Options{}, "openaq:1|10")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.fetch.errs["latest/openaq:1"] = []error{context.Canceled}
	if _, err := r.p.Poll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := New(fakeSource{}, nil, nil, nil, nil, nil, Options{MaxAge: 72 * time.Hour}); err == nil {
		t.Fatal("MaxAge > MaxSilence diterima")
	}
	d := DefaultOptions()
	if d.MaxSilence != 48*time.Hour || d.MaxAge != 24*time.Hour || d.MaxAttempts != 3 {
		t.Fatalf("%+v", d)
	}
}
