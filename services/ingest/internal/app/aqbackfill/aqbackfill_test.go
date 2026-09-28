package aqbackfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var now = time.Date(2026, 9, 28, 13, 30, 0, 0, time.UTC)

func station(id int, last time.Time) stations.Station {
	return stations.Station{
		Station:    airquality.Station{ID: fmt.Sprintf("openaq:%d", id), Name: fmt.Sprintf("Stasiun %d", id), Lat: -6.4, Lon: 106.8, Provider: "AirGradient"},
		LastReport: last,
	}
}

// fakeSource: daftar = stasiun tetap; halaman nilai = JSON []Measurement,
// URL "m/<sensor>/<halaman>".
type fakeSource struct {
	list    []stations.Station
	sensors map[string][]Sensor
	listErr error
}

func (f *fakeSource) ListRequest() ports.Request { return ports.Request{URL: "daftar"} }
func (f *fakeSource) ParseList(b []byte) ([]stations.Station, []ports.Rejection, error) {
	if string(b) != "DAFTAR" {
		return nil, nil, errors.New("daftar rusak")
	}
	return append([]stations.Station(nil), f.list...), []ports.Rejection{{Key: "openaq:9", Reason: errors.New("koordinat kosong")}}, f.listErr
}
func (f *fakeSource) Sensors(st stations.Station) []Sensor { return f.sensors[st.ID] }
func (f *fakeSource) MeasurementsRequest(id int64, from, to time.Time, page int) ports.Request {
	return ports.Request{URL: fmt.Sprintf("m/%d/%d", id, page)}
}

func (f *fakeSource) ParseMeasurements(b []byte) ([]Measurement, error) {
	var out []Measurement
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *fakeSource) Event(o airquality.Observation) (ports.Event, error) {
	if o.Readings[0].Value == 13 {
		return nil, errors.New("encode gagal")
	}
	return event{o}, nil
}

type event struct{ o airquality.Observation }

func (e event) Subject() string { return "raw.aq.openaq" }
func (e event) Key() string     { return e.o.Station.ID }
func (e event) Content() ([]byte, error) {
	if e.o.Readings[0].Value == 14 {
		return nil, errors.New("serialisasi gagal")
	}
	r := e.o.Readings[0]
	return fmt.Appendf(nil, "%d@%s=%v", r.SensorID, r.ObservedAt.Format(time.RFC3339), r.Value), nil
}

func (e event) Encode(m ports.FetchMeta) ([]byte, error) {
	c, _ := e.Content()
	return fmt.Appendf(nil, "%s|%s|%s", c, m.Connector, m.ArchiveKey), nil
}

// fetcher menjawab per URL; galat per URL dipakai berurutan sebelum jawaban.
type fetcher struct {
	bodies map[string]string
	errs   map[string][]error
	calls  []string
}

func (f *fetcher) Fetch(ctx context.Context, r ports.Request) (ports.Response, error) {
	f.calls = append(f.calls, r.URL)
	if errs := f.errs[r.URL]; len(errs) > 0 {
		f.errs[r.URL] = errs[1:]
		return ports.Response{}, errs[0]
	}
	b, ok := f.bodies[r.URL]
	if !ok {
		return ports.Response{}, statusError(404)
	}
	if b == "304" {
		return ports.Response{NotModified: true}, nil
	}
	return ports.Response{Body: []byte(b)}, nil
}

type statusError int

func (e statusError) Error() string   { return fmt.Sprintf("HTTP %d", int(e)) }
func (e statusError) HTTPStatus() int { return int(e) }

type archive struct {
	keys []string
	err  error
}

func (a *archive) Put(_ context.Context, k string, _ []byte) error {
	if a.err != nil {
		return a.err
	}
	a.keys = append(a.keys, k)
	return nil
}

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

type throttle struct{ waits int }

func (t *throttle) Wait(ctx context.Context) error { t.waits++; return ctx.Err() }

func page(ms ...Measurement) string {
	b, _ := json.Marshal(ms)
	return string(b)
}

func pm(h int, v float64) Measurement {
	return Measurement{
		Parameter: airquality.PM25, Unit: airquality.UnitMicrogram, Value: v,
		ObservedAt: time.Date(2026, 9, 26, h, 0, 0, 0, time.UTC),
	}
}

var (
	from = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	to   = time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
)

func setup() (*fakeSource, *fetcher) {
	src := &fakeSource{
		list: []stations.Station{
			station(2, now.Add(-time.Hour)), station(1, now.Add(-time.Hour)),
			station(1, now.Add(-time.Hour)),                          // ganda di daftar
			station(3, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)), // diam sejak sebelum rentang
			station(4, time.Time{}),                                  // tanpa laporan
		},
		sensors: map[string][]Sensor{
			"openaq:1": {{ID: 11, Parameter: airquality.PM25, Unit: airquality.UnitMicrogram}},
			"openaq:2": {
				{ID: 21, Parameter: airquality.PM25, Unit: airquality.UnitMicrogram},
				{ID: 22, Parameter: airquality.O3, Unit: airquality.UnitPPM},
			},
		},
	}
	o3 := pm(13, 0.03)
	o3.Parameter, o3.Unit = airquality.O3, airquality.UnitPPM
	f := &fetcher{
		bodies: map[string]string{
			"daftar": "DAFTAR",
			// 11 jam di luar rentang (11.00, 19.00) dilewati; 12–18 di rentang.
			"m/11/1": page(pm(11, 1), pm(12, 40), pm(13, 42.7), pm(14, 51.6), pm(18, 72.6), pm(19, 80)),
			// 21: satu nilai rusak, satu satuan berbeda dari daftar, satu gagal encode/serialisasi.
			"m/21/1": page(pm(12, 50), pm(13, -1), Measurement{Parameter: airquality.PM25, Unit: "ppm", Value: 1, ObservedAt: pm(14, 0).ObservedAt},
				pm(15, 13), pm(16, 14)),
			"m/22/1": page(o3),
		},
		errs: map[string][]error{},
	}
	return src, f
}

func TestRun(t *testing.T) {
	src, f := setup()
	arc, pub, clk, thr := &archive{}, &publisher{}, &clock{now: now}, &throttle{}
	rep, err := New(src, f, arc, pub, clk, thr).Run(t.Context(), Options{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stations != 2 || rep.Silent != 2 || len(rep.ListErrors) != 1 || len(rep.Sensors) != 3 {
		t.Fatalf("%+v", rep)
	}
	s11, s21, s22 := rep.Sensors[0], rep.Sensors[1], rep.Sensors[2]
	if s11.Station != "openaq:1" || s11.Rows != 6 || s11.InRange != 4 || s11.Published != 4 || s11.Rejected != 0 ||
		!s11.First.Equal(pm(12, 0).ObservedAt) || !s11.Last.Equal(pm(18, 0).ObservedAt) || s11.Pages != 1 {
		t.Fatalf("11 %+v", s11)
	}
	if s21.InRange != 5 || s21.Published != 1 || s21.Rejected != 4 || len(s21.Samples) != 4 {
		t.Fatalf("21 %+v", s21)
	}
	if s22.Published != 1 || s22.Parameter != "o3" {
		t.Fatalf("22 %+v", s22)
	}
	if tot := rep.Totals(); tot.Published != 6 || tot.Rejected != 4 || tot.Pages != 3 {
		t.Fatalf("%+v", tot)
	}
	// Daftar diarsipkan dengan nama konektor polling, halaman dengan openaq-jam.
	if len(arc.keys) != 4 || !strings.HasPrefix(arc.keys[0], "openaq-stasiun/2026/09/28/") || !strings.HasPrefix(arc.keys[1], "openaq-jam/") {
		t.Fatal(arc.keys)
	}
	m := pub.msgs[0]
	if !strings.HasPrefix(string(m.Data), "11@2026-09-26T12:00:00Z=40|openaq-jam|openaq-jam/") || m.Subject != "raw.aq.openaq" || !m.FetchedAt.Equal(now) {
		t.Fatalf("%s %+v", m.Data, m)
	}
	if thr.waits != 4 {
		t.Fatalf("izin anggaran %d", thr.waits)
	}

	// Diulang: semua duplikat.
	src, f = setup()
	rep, err = New(src, f, nil, pub, clk, thr).Run(t.Context(), Options{From: from, To: to, Stations: []string{"openaq:1"}})
	if err != nil || rep.Stations != 1 || rep.Totals().Duplicates != 4 || rep.Totals().Published != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestRunPagesAndRetries(t *testing.T) {
	src, f := setup()
	full := make([]Measurement, PageLimit)
	for i := range full {
		full[i] = pm(12, float64(i%500)) // nilai sama per jam: konten berbeda per nilai
		full[i].ObservedAt = from.Add(time.Duration(i) * time.Second)
	}
	f.bodies["m/11/1"] = page(full...)
	f.bodies["m/11/2"] = page(pm(17, 5))
	f.errs["m/11/2"] = []error{&ports.RetryAfterError{Status: 429, After: 45 * time.Second}, errors.New("reset")}
	f.errs["m/21/1"] = []error{statusError(500), statusError(502), statusError(503)}
	f.errs["m/22/1"] = []error{statusError(401)}
	clk := &clock{now: now}
	rep, err := New(src, f, nil, &publisher{}, clk, &throttle{}).Run(t.Context(), Options{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	s11, s21, s22 := rep.Sensors[0], rep.Sensors[1], rep.Sensors[2]
	if s11.Pages != 2 || s11.Rows != PageLimit+1 || s11.Error != "" {
		t.Fatalf("11 %+v", s11)
	}
	if !strings.Contains(s21.Error, "halaman 1") || !strings.Contains(s21.Error, "503") || s21.Pages != 0 {
		t.Fatalf("21 %+v", s21)
	}
	if !strings.Contains(s22.Error, "401") {
		t.Fatalf("22 %+v", s22)
	}
	// Jeda: Retry-After 45 dtk dihormati, lalu 20 dtk; 500 → 10, 20 dtk; 401 tidak diulang.
	if want := []time.Duration{45 * time.Second, 20 * time.Second, 10 * time.Second, 20 * time.Second}; fmt.Sprint(clk.sleeps) != fmt.Sprint(want) {
		t.Fatalf("jeda %v", clk.sleeps)
	}

	// Halaman tak berujung dan halaman rusak.
	src, f = setup()
	for p := 1; p <= MaxPages; p++ {
		f.bodies[fmt.Sprintf("m/11/%d", p)] = page(full...)
	}
	f.bodies["m/21/1"] = "bukan json"
	f.bodies["m/22/1"] = "304"
	rep, err = New(src, f, nil, &publisher{}, &clock{now: now}, &throttle{}).Run(t.Context(), Options{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if s := rep.Sensors[0]; s.Pages != MaxPages || !strings.Contains(s.Error, "persempit") {
		t.Fatalf("%+v", s)
	}
	if s := rep.Sensors[1]; s.Pages != 1 || s.Error == "" {
		t.Fatalf("%+v", s)
	}
	if s := rep.Sensors[2]; !strings.Contains(s.Error, "304") {
		t.Fatalf("%+v", s)
	}
}

func TestRunErrors(t *testing.T) {
	run := func(mut func(*fakeSource, *fetcher, *archive, *publisher), ctx context.Context, opts Options) error {
		src, f := setup()
		arc, pub := &archive{}, &publisher{}
		mut(src, f, arc, pub)
		_, err := New(src, f, arc, pub, &clock{now: now}, &throttle{}).Run(ctx, opts)
		return err
	}
	none := func(*fakeSource, *fetcher, *archive, *publisher) {}
	ok := Options{From: from, To: to}
	for name, opts := range map[string]Options{
		"tanpa awal":  {To: to},
		"terbalik":    {From: to, To: from},
		"terlalu tua": {From: now.Add(-8 * 24 * time.Hour)},
		"masa depan":  {From: from, To: now.Add(time.Hour)},
		"stasiun":     {From: from, To: to, Stations: []string{"openaq:1", "openaq:77"}},
	} {
		if err := run(none, t.Context(), opts); !errors.Is(err, ErrOptions) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// To kosong = sekarang.
	if err := run(none, t.Context(), Options{From: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		mut  func(*fakeSource, *fetcher, *archive, *publisher)
		want string
	}{
		"daftar gagal": {func(_ *fakeSource, f *fetcher, _ *archive, _ *publisher) {
			f.errs["daftar"] = []error{statusError(403)}
		}, "403"},
		"daftar rusak": {func(_ *fakeSource, f *fetcher, _ *archive, _ *publisher) { f.bodies["daftar"] = "x" }, "daftar rusak"},
		"daftar galat": {func(s *fakeSource, _ *fetcher, _ *archive, _ *publisher) { s.listErr = boom }, "boom"},
		"arsip":        {func(_ *fakeSource, _ *fetcher, a *archive, _ *publisher) { a.err = boom }, "boom"},
		"terbit":       {func(_ *fakeSource, _ *fetcher, _ *archive, p *publisher) { p.err = boom }, "boom"},
	} {
		err := run(tc.mut, t.Context(), ok)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(none, ctx, ok); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Batal saat mengambil halaman sensor.
	src, f := setup()
	ctx, cancel = context.WithCancel(t.Context())
	f.errs["m/11/1"] = []error{errors.New("putus")}
	cancelOn := &cancelFetcher{fetcher: f, url: "m/11/1", cancel: cancel}
	if _, err := New(src, cancelOn, nil, &publisher{}, &clock{now: now}, &throttle{}).Run(ctx, ok); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type cancelFetcher struct {
	*fetcher
	url    string
	cancel func()
}

func (c *cancelFetcher) Fetch(ctx context.Context, r ports.Request) (ports.Response, error) {
	if r.URL == c.url {
		c.cancel()
	}
	return c.fetcher.Fetch(ctx, r)
}
