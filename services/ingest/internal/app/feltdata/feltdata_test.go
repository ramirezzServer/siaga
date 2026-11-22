package feltdata

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const listJSON = `{"type":"FeatureCollection","features":[
{"type":"Feature","properties":{"mag":5.6,"place":"11 km NE of Sukabumi, Indonesia","time":1669011667066,"felt":169,"magType":"mww"},
 "geometry":{"type":"Point","coordinates":[106.9968,-6.836,10]},"id":"us7000ir9t"},
{"type":"Feature","properties":{"mag":4.6,"place":"Banda Sea","time":1100000000000,"felt":2,"magType":"mb"},
 "geometry":{"type":"Point","coordinates":[124.868,-8.152,10]},"id":"usp000d85g"},
{"type":"Feature","properties":{"mag":4.5,"place":"Laut","time":1100000001000,"felt":1,"magType":"mb"},
 "geometry":{"type":"Point","coordinates":[124,-8,10]},"id":"usp000aaaa"},
{"type":"Feature","properties":{"mag":4.4,"place":"Laut","time":1100000002000,"felt":1,"magType":"mb"},
 "geometry":{"type":"Point","coordinates":[124,-8,10]},"id":"usp000bbbb"}]}`

const cdiURL = "https://earthquake.usgs.gov/product/dyfi/us7000ir9t/us/1/cdi_geo.txt"

func detail(code, url string) string {
	contents := `{}`
	if url != "" {
		contents = `{"cdi_geo.txt":{"url":"` + url + `"},"lain.txt":{"url":"x"}}`
	}
	return `{"type":"Feature","properties":{"products":{"dyfi":[{"type":"dyfi","code":"` + code +
		`","source":"us","updateTime":1,"properties":{"num-responses":"169"},"contents":` + contents + `}]}}}`
}

const cdiText = `# Columns: Geocoded box,CDI,No. of responses,Hypocentral distance,Latitude,Longitude,Suspect?,Standard deviation,City,State
"UTM:(48M 072 923 10000)",6.5,1,14,-6.9165,107.0363,0,0.330,"Sukabumi",""
"UTM:(48M 070 930 10000)",1.0,2,63,-6.2843,106.8530,1,0.293,"Depok, Jawa Barat",""
`

type fake struct {
	calls  []string
	routes map[string][]reply
}

type reply struct {
	body string
	err  error
}

func (f *fake) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.calls = append(f.calls, r.URL)
	for k, rs := range f.routes {
		if strings.Contains(r.URL, k) && len(rs) > 0 {
			x := rs[0]
			if len(rs) > 1 {
				f.routes[k] = rs[1:]
			}
			return ports.Response{Body: []byte(x.body)}, x.err
		}
	}
	return ports.Response{}, &httpfetch.StatusError{Status: 500}
}

type clock struct{ slept []time.Duration }

func (c *clock) Now() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	return ctx.Err()
}

func routes() map[string][]reply {
	return map[string][]reply{
		"producttype=dyfi":    {{body: listJSON}},
		"eventid=us7000ir9t":  {{err: &httpfetch.StatusError{Status: 429}}, {body: detail("us7000ir9t", cdiURL)}},
		"eventid=usp000d85g":  {{body: detail("usp000d85g", "")}},
		"eventid=usp000aaaa":  {{body: `{"type":"Feature","properties":{"products":{"origin":[]}}}`}},
		"eventid=usp000bbbb":  {{body: detail("usp000bbbb", "https://x/hilang/cdi_geo.txt")}},
		"us7000ir9t/us/1/cdi": {{body: cdiText}},
		"hilang/cdi_geo.txt":  {{err: &httpfetch.StatusError{Status: 404}}},
	}
}

func loader(t *testing.T, f ports.Fetcher, c *clock, dir string) *DYFILoader {
	t.Helper()
	cache, err := fsarchive.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &DYFILoader{
		Fetch: f, Clock: c, Cache: cache, Box: area.Indonesia, MinFelt: 1,
		From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
	}
}

func TestDYFILoad(t *testing.T) {
	dir := t.TempDir()
	f, c := &fake{routes: routes()}, &clock{}
	l := loader(t, f, c, dir)
	var notices []string
	l.Notice = func(s string) { notices = append(notices, s) }
	progress := 0
	l.Progress = func(done, total, _ int) { progress = done }
	got, st, err := l.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Events != 4 || st.NoProduct != 1 || st.NoCDI != 2 || st.Cells != 2 || st.Retries != 1 || st.Fetched != 7 || progress != 4 {
		t.Fatalf("%+v progres %d", st, progress)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "HTTP 429") {
		t.Fatalf("pesan %v", notices)
	}
	if !got[0].HasProduct || !got[0].HasCDI || len(got[0].Cells) != 2 || got[0].Product.Responses() != 169 || got[2].HasProduct || got[3].HasCDI {
		t.Fatalf("%+v", got)
	}
	if len(c.slept) != 7 || c.slept[0] != DefaultPause {
		t.Fatalf("jeda %v", c.slept)
	}
	if !strings.Contains(f.calls[0], "endtime=2026-11-01") || !strings.Contains(f.calls[0], "minfelt=1") {
		t.Fatalf("URL daftar %s", f.calls[0])
	}

	// Jalan ulang -offline: semua dari cache, hasil sama, tanpa request.
	off := loader(t, nil, nil, dir)
	off.Offline = true
	again, st2, err := off.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st2.Fetched != 0 || st2.Cells != 2 || len(again) != 4 || again[0].Cells[1] != got[0].Cells[1] {
		t.Fatalf("%+v", st2)
	}
	var a, b bytes.Buffer
	m := DYFIMeta{From: l.From, To: l.To, Box: l.Box, MinFelt: 1}
	if err := WriteDYFICSV(&a, m, got); err != nil {
		t.Fatal(err)
	}
	if err := WriteDYFICSV(&b, m, again); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("CSV dari cache berbeda")
	}
	for _, want := range []string{
		"usgs_id,waktu_utc", "us7000ir9t,2022-11-21T06:21:07.066Z,-6.8360,106.9968,10.0,5.6,mww,169,UTM:(48M 072 923 10000),6.5,1,14,hiposenter",
		`1.0,2,63,hiposenter,-6.2843,106.8530,1,"Depok, Jawa Barat"`,
	} {
		if !strings.Contains(a.String(), want) {
			t.Errorf("CSV tanpa %q:\n%s", want, a.String())
		}
	}
}

func TestDYFILoadErrors(t *testing.T) {
	t.Run("offline tanpa cache", func(t *testing.T) {
		l := loader(t, nil, nil, t.TempDir())
		l.Offline = true
		if _, _, err := l.Load(t.Context()); !errors.Is(err, ErrNotCached) {
			t.Fatal(err)
		}
	})
	t.Run("batas request", func(t *testing.T) {
		l := loader(t, &fake{routes: routes()}, &clock{}, t.TempDir())
		l.MaxRequests = 2
		_, _, err := l.Load(t.Context())
		var be *BudgetError
		if !errors.As(err, &be) || be.Remaining != 4 || !strings.Contains(be.Error(), "4 kejadian") {
			t.Fatal(err)
		}
	})
	t.Run("galat klien", func(t *testing.T) {
		r := routes()
		r["eventid=us7000ir9t"] = []reply{{err: &httpfetch.StatusError{Status: 400}}}
		if _, _, err := loader(t, &fake{routes: r}, &clock{}, t.TempDir()).Load(t.Context()); err == nil || !strings.Contains(err.Error(), "us7000ir9t") {
			t.Fatal(err)
		}
	})
	t.Run("gagal terus", func(t *testing.T) {
		r := routes()
		r["producttype=dyfi"] = []reply{{err: &ports.RetryAfterError{Status: 503, After: time.Hour}}}
		c := &clock{}
		l := loader(t, &fake{routes: r}, c, t.TempDir())
		l.MaxRetries = 2
		if _, _, err := l.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "gagal 2 kali") {
			t.Fatal(err)
		}
		if c.slept[0] != 30*time.Minute {
			t.Fatalf("Retry-After tidak dihormati: %v", c.slept)
		}
	})
	t.Run("ID aneh", func(t *testing.T) {
		r := routes()
		r["producttype=dyfi"] = []reply{{body: strings.Replace(listJSON, `"id":"us7000ir9t"`, `"id":"../x"`, 1)}}
		if _, _, err := loader(t, &fake{routes: r}, &clock{}, t.TempDir()).Load(t.Context()); err == nil || !strings.Contains(err.Error(), "garis bawah") {
			t.Fatal(err)
		}
	})
	t.Run("daftar rusak", func(t *testing.T) {
		r := routes()
		r["producttype=dyfi"] = []reply{{body: "<html>"}}
		if _, _, err := loader(t, &fake{routes: r}, &clock{}, t.TempDir()).Load(t.Context()); err == nil {
			t.Fatal("harus galat")
		}
	})
	t.Run("sel rusak", func(t *testing.T) {
		r := routes()
		r["us7000ir9t/us/1/cdi"] = []reply{{body: "tanpa header"}}
		if _, _, err := loader(t, &fake{routes: r}, &clock{}, t.TempDir()).Load(t.Context()); err == nil || !strings.Contains(err.Error(), "cdi_geo") {
			t.Fatal(err)
		}
	})
	t.Run("konfigurasi", func(t *testing.T) {
		l := loader(t, nil, nil, t.TempDir())
		if _, _, err := l.Load(t.Context()); err == nil {
			t.Fatal("Fetch kosong harus galat")
		}
		l = loader(t, &fake{}, &clock{}, t.TempDir())
		l.MinFelt = 0
		if _, _, err := l.Load(t.Context()); err == nil {
			t.Fatal("MinFelt 0 harus galat")
		}
		l.MinFelt, l.Box = 1, area.Box{West: 10, East: 5}
		if _, _, err := l.Load(t.Context()); err == nil {
			t.Fatal("kotak terbalik harus galat")
		}
	})
	t.Run("batal", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := loader(t, &fake{routes: routes()}, &clock{}, t.TempDir()).Load(ctx); err == nil {
			t.Fatal("konteks batal harus galat")
		}
	})
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	b, err := gzipBytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func feltPayload(felt string) string {
	return `{"Infogempa":{"gempa":[{"DateTime":"2026-09-25T08:04:42+00:00","Coordinates":"-6.90,107.11","Magnitude":"2.4","Kedalaman":"7 km",
"Wilayah":"Pusat gempa di darat","Potensi":"Gempa tidak berpotensi tsunami","Dirasakan":"` + felt + `"},
{"DateTime":"2026-09-20T17:51:33+00:00","Coordinates":"-9.44,115.80","Magnitude":"4.5","Kedalaman":"22 km","Wilayah":"Laut","Dirasakan":"II Mataram"},
{"DateTime":"rusak","Coordinates":"x","Magnitude":"x","Kedalaman":"x"}]}}`
}

func TestCollectBMKG(t *testing.T) {
	ar, err := fsarchive.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(at time.Time, body []byte) {
		if err := ar.Put(t.Context(), archivekey.Build(FeltConnector, "json", "abcdef0123456789", at), body); err != nil {
			t.Fatal(err)
		}
	}
	t1 := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	put(t1, gz(t, feltPayload("II - III Kota Cianjur")))
	put(t1.Add(time.Hour), gz(t, feltPayload("II - III Kota Cianjur, II  Cibeber")))
	put(t1.Add(2*time.Hour), []byte("bukan gzip"))
	put(t1.Add(3*time.Hour), gz(t, `{"bukan":"bmkg"}`))
	put(t1.Add(48*time.Hour), gz(t, feltPayload("III Cibeber")))
	if err := ar.Put(t.Context(), FeltConnector+"/kunci-aneh.json.gz", gz(t, "{}")); err != nil {
		t.Fatal(err)
	}
	parse := func(b []byte, at time.Time) ([]quake.Report, []ports.Rejection, error) {
		return bmkg.ParseQuakeReports(b, quake.FeedBMKGFelt, at)
	}
	got, st, err := CollectBMKG(t.Context(), ar, parse, t1.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if st.Payloads != 3 || st.Unreadable != 2 || st.Rejected != 2 || st.Events != 2 || st.Revised != 1 {
		t.Fatalf("%+v", st)
	}
	if got[0].EventID != "20260920175133" || got[1].Felt != "II - III Kota Cianjur, II Cibeber" || got[1].Versions != 2 ||
		!got[1].FirstSeen.Equal(t1) || !got[1].LastSeen.Equal(t1.Add(time.Hour)) {
		t.Fatalf("%+v", got)
	}
	all, ast, err := CollectBMKG(t.Context(), ar, parse, time.Time{})
	if err != nil || all[1].Felt != "III Cibeber" || all[1].Versions != 3 || ast.Unreadable != 3 {
		t.Fatalf("tanpa batas: %+v %v", all, err)
	}
	var buf bytes.Buffer
	if err := WriteBMKGCSV(&buf, st, got); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"3 payload (2 tidak terbaca, 2 gempa ditolak validasi) diambil 2026-09-25T09:00:00Z..2026-09-25T10:00:00Z; 2 gempa, 1 dengan isi Dirasakan berubah",
		`20260925080442,2026-09-25T08:04:42Z,-6.90,107.11,7,2.4,Pusat gempa di darat,"II - III Kota Cianjur, II Cibeber",2026-09-25T09:00:00Z,2026-09-25T10:00:00Z,2`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("CSV tanpa %q:\n%s", want, buf.String())
		}
	}
	if err := WriteBMKGCSV(&buf, BMKGStats{}, nil); err != nil || fmtTime(time.Time{}) != "" {
		t.Fatal(err)
	}
}

type failingArchive struct{ ports.ArchiveReader }

func (failingArchive) Get(context.Context, string) ([]byte, error) { return nil, errors.New("rusak") }

func TestCollectBMKGArchiveError(t *testing.T) {
	ar, err := fsarchive.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ar.Put(t.Context(), archivekey.Build(FeltConnector, "json", "abcdef0123456789", time.Now()), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CollectBMKG(t.Context(), failingArchive{ar}, nil, time.Time{}); err == nil {
		t.Fatal("galat baca arsip harus diteruskan")
	}
}
