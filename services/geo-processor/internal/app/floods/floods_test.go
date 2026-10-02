package floods

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

var t0 = time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func calibration(t testing.TB) flood.Calibration {
	t.Helper()
	p := flood.DefaultPolicy()
	nanjung, err := p.Discharge(flood.DischargeRow{
		SiteID: "river:citarum-nanjung", River: "Citarum", Name: "Nanjung", Cell: series.Point{Lat: -6.975, Lon: 107.525},
		P50: 95.83, Climatology: flood.Set{215.77, 271.90, 378.73, 479.70}, SeamlessRatio: 0.87,
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows []flood.RainfallRow
	for w, hours := range flood.Windows {
		base := float64(w + 1)
		rows = append(rows, flood.RainfallRow{SiteID: "catchment:cikapundung", Name: "Cikapundung", Hours: hours, P50: 1, Levels: flood.Set{5 * base, 10 * base, 20 * base, 30 * base}})
	}
	rain, err := p.Rainfall(rows)
	if err != nil {
		t.Fatal(err)
	}
	src := flood.Source{Period: flood.Period{From: time.Date(1997, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)}, SHA256: [32]byte{1}}
	return flood.Calibration{
		Discharge: map[string]flood.DischargeThreshold{nanjung.SiteID: nanjung}, Rainfall: rain,
		DischargeSource: src, RainfallSource: src,
	}
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func service(t testing.TB, store ports.FloodStore, enc ports.FloodEncoder, c *clock) *Service {
	t.Helper()
	s, err := New(store, enc, flood.DefaultPolicy(), calibration(t), c.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// discharge membuat keluaran debit Nanjung yang diterima at, dengan median
// ensemble hari ini; ambang efektif ≈ 187,7 / 236,6 / 329,5 / 417,3 m³/s.
func discharge(at time.Time, median float64) series.DischargeRun {
	return series.DischargeRun{Run: series.Run{
		Site:    series.Site{ID: "river:citarum-nanjung", Kind: series.SiteRiver, Name: "Nanjung", River: "Citarum", Location: series.Point{Lat: -6.941, Lon: 107.537}},
		Dataset: series.Discharge, Source: series.SourceOpenMeteo, Model: "glofas_v4", Cell: series.Point{Lat: -6.975, Lon: 107.525},
		IssuedAt: at, FetchedAt: at,
	}, Steps: []series.DischargeStep{{ValidDate: at.Truncate(24 * time.Hour), Discharge: f(median), Median: f(median), P75: f(median)}}}
}

func rainfall(at time.Time, mm float64) series.WeatherRun {
	start := at.Truncate(24 * time.Hour)
	r := series.WeatherRun{Run: series.Run{
		Site:    series.Site{ID: "catchment:cikapundung", Kind: series.SiteCatchment, Name: "Cikapundung", Location: series.Point{Lat: -6.82, Lon: 107.62}},
		Dataset: series.Weather, Source: series.SourceOpenMeteo, Model: "ecmwf_ifs", Cell: series.Point{Lat: -6.82, Lon: 107.62},
		IssuedAt: at, FetchedAt: at,
	}}
	for h := range 24 {
		v := 0.0
		if h == 1 {
			v = mm
		}
		r.Steps = append(r.Steps, series.WeatherStep{ValidTime: start.Add(time.Duration(h) * time.Hour), PrecipitationMM: f(v)})
	}
	return r
}

func subjects(s *memStore) []string {
	var out []string
	for _, m := range s.state.outbox {
		out = append(out, strings.TrimPrefix(m.Subject, "hazard.flood.")+":"+string(m.Payload))
	}
	return out
}

func TestDischargeLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	c := &clock{now: t0}
	s := service(t, store, fakeEncoder{}, c)

	// Di bawah Info: tidak ada kejadian, titik tercatat.
	res, err := s.Discharge(ctx, discharge(t0, 100))
	if err != nil || !res.Changed || res.Created != 0 || res.Level != flood.None {
		t.Fatalf("%+v %v", res, err)
	}
	// Ulangan: tidak ada perubahan.
	if res, _ := s.Discharge(ctx, discharge(t0, 100)); res.Changed {
		t.Fatalf("ulangan %+v", res)
	}

	// Waspada membuka kejadian.
	t1 := t0.Add(6 * time.Hour)
	c.now = t1
	res, err = s.Discharge(ctx, discharge(t1, 250))
	if err != nil || res.Created != 1 || res.Level != hazard.LevelWaspada {
		t.Fatalf("%+v %v", res, err)
	}
	id := flood.EventIDFor("river:citarum-nanjung", t1)
	ev := store.state.events[id]
	if ev == nil || ev.rec.Revision != 1 || ev.rec.Level != hazard.LevelWaspada || !ev.rec.ExpiresAt.Equal(t1.Add(24*time.Hour)) {
		t.Fatalf("%+v", ev)
	}

	// Keluaran yang lebih tua dari yang sudah dinilai diabaikan.
	if res, _ := s.Discharge(ctx, discharge(t0.Add(time.Hour), 1000)); res.Changed {
		t.Fatalf("keluaran lama %+v", res)
	}

	// Naik ke Siaga: diperbarui, masa aktif diperpanjang.
	t2 := t1.Add(6 * time.Hour)
	c.now = t2
	if res, err := s.Discharge(ctx, discharge(t2, 340)); err != nil || res.Updated != 1 || res.Created != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if ev := store.state.events[id]; ev.rec.Revision != 2 || ev.rec.Level != hazard.LevelSiaga || !ev.rec.ExpiresAt.Equal(t2.Add(24*time.Hour)) {
		t.Fatalf("%+v", ev.rec)
	}

	// Turun di bawah Info: tetap aktif sebagai Info, masa aktif tidak diperpanjang.
	t3 := t2.Add(6 * time.Hour)
	c.now = t3
	if res, err := s.Discharge(ctx, discharge(t3, 100)); err != nil || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if ev := store.state.events[id]; ev.rec.Level != hazard.LevelInfo || ev.rec.Outlook.Level != flood.None || !ev.rec.ExpiresAt.Equal(t2.Add(24*time.Hour)) || ev.status != ports.StatusActive {
		t.Fatalf("%+v", ev.rec)
	}

	// Putaran kedaluwarsa: belum waktunya, lalu berakhir.
	if n, err := s.Expire(ctx, t3, 10); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := s.Expire(ctx, t2.Add(24*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if ev := store.state.events[id]; ev.status != ports.StatusExpired || ev.rec.Revision != 4 {
		t.Fatalf("%+v", ev)
	}

	// Banjir berikutnya membuka kejadian baru.
	t4 := t2.Add(30 * time.Hour)
	c.now = t4
	if res, err := s.Discharge(ctx, discharge(t4, 250)); err != nil || res.Created != 1 || res.Ended != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if st := store.state.sites["river:citarum-nanjung"]; st.EventID != flood.EventIDFor("river:citarum-nanjung", t4) || !st.LastFetchedAt.Equal(t4) {
		t.Fatalf("%+v", st)
	}
	want := []string{"created:waspada", "updated:waspada→siaga", "updated:siaga→info", "expired:1", "created:waspada"}
	if got := subjects(store); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("outbox %v", got)
	}
}

// Kejadian yang masa aktifnya habis sebelum keluaran berikutnya diterima
// (putaran kedaluwarsa belum jalan) diakhiri lebih dulu, lalu kejadian baru
// dibuka dalam transaksi yang sama.
func TestLapsedEventIsEndedBeforeNewOne(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	c := &clock{now: t0}
	s := service(t, store, fakeEncoder{}, c)
	if _, err := s.Discharge(ctx, discharge(t0, 250)); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(30 * time.Hour)
	c.now = later
	res, err := s.Discharge(ctx, discharge(later, 340))
	if err != nil || res.Ended != 1 || res.Created != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Join(subjects(store), " "); got != "created:waspada expired:1 created:siaga" {
		t.Fatalf("%s", got)
	}
	// Tanpa banjir baru: kejadian lama tetap diakhiri, tidak ada yang dibuka.
	store = newMemStore()
	c.now = t0
	s = service(t, store, fakeEncoder{}, c)
	_, _ = s.Discharge(ctx, discharge(t0, 250))
	c.now = later
	if res, err := s.Discharge(ctx, discharge(later, 100)); err != nil || res.Ended != 1 || res.Created != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

// Keluaran lama (replay) yang masa aktifnya sudah habis saat diproses
// tercatat lengkap: created lalu expired.
func TestReplayOfOldOutput(t *testing.T) {
	store := newMemStore()
	s := service(t, store, fakeEncoder{}, &clock{now: t0.Add(25 * time.Hour)})
	res, err := s.Discharge(context.Background(), discharge(t0, 340))
	if err != nil || res.Created != 1 || res.Ended != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Join(subjects(store), " "); got != "created:siaga expired:1" {
		t.Fatalf("%s", got)
	}
}

func TestRainfallLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	c := &clock{now: t0}
	s := service(t, store, fakeEncoder{}, c)
	// Ambang uji 3 jam: 5/10/20/30; 24 jam: 15/30/60/90. 40 mm sejam: 3 jam
	// Bahaya dibatasi Siaga.
	res, err := s.Rainfall(ctx, rainfall(t0, 40))
	if err != nil || res.Created != 1 || res.Level != hazard.LevelSiaga {
		t.Fatalf("%+v %v", res, err)
	}
	ev := store.state.events[flood.EventIDFor("catchment:cikapundung", t0)]
	if ev == nil || ev.rec.Indicator != flood.Rainfall || ev.rec.MaxLevel != hazard.LevelSiaga || !strings.HasPrefix(ev.rec.Title, "Indikasi potensi banjir") {
		t.Fatalf("%+v", ev)
	}
}

func TestRejectsInvalidAndUnknown(t *testing.T) {
	ctx := context.Background()
	s := service(t, newMemStore(), fakeEncoder{}, &clock{now: t0})
	run := discharge(t0, 100)
	run.Site.ID, run.Site.Name = "river:cimanuk-garut", "Garut kota"
	if _, err := s.Discharge(ctx, run); !errors.Is(err, flood.ErrInvalid) || !errors.Is(err, flood.ErrNoThreshold) {
		t.Fatalf("titik tanpa ambang: %v", err)
	}
	r := rainfall(t0, 1)
	r.Site.ID = "catchment:ciwidey"
	if _, err := s.Rainfall(ctx, r); !errors.Is(err, flood.ErrNoThreshold) {
		t.Fatalf("sub-DAS tanpa ambang: %v", err)
	}
	bad := discharge(t0, 100)
	bad.Model = "Model Rusak"
	if _, err := s.Discharge(ctx, bad); !errors.Is(err, series.ErrInvalid) {
		t.Fatalf("deret rusak: %v", err)
	}
	if _, err := s.Rainfall(ctx, series.WeatherRun{}); !errors.Is(err, series.ErrInvalid) {
		t.Fatalf("hujan kosong: %v", err)
	}
	grid := rainfall(t0, 1)
	grid.Site = series.Site{ID: "grid:-6.75:107.50", Kind: series.SiteGrid, Location: series.Point{Lat: -6.75, Lon: 107.5}}
	if _, err := s.Rainfall(ctx, grid); !errors.Is(err, flood.ErrInvalid) {
		t.Fatalf("hujan grid: %v", err)
	}
}

// Galat di tengah transaksi tidak meninggalkan keadaan setengah jadi.
func TestFailuresRollBack(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"lock", "site", "episode", "save", "enqueue", "savesite", "end"} {
		store := newMemStore()
		c := &clock{now: t0}
		s := service(t, store, fakeEncoder{}, c)
		if _, err := s.Discharge(ctx, discharge(t0, 250)); err != nil {
			t.Fatal(err)
		}
		before := len(store.state.outbox)
		store.failOn = op
		later := t0.Add(30 * time.Hour)
		c.now = later
		if _, err := s.Discharge(ctx, discharge(later, 340)); !errors.Is(err, errInjected) {
			t.Errorf("%s: %v", op, err)
		}
		if len(store.state.outbox) != before || !store.state.sites["river:citarum-nanjung"].LastFetchedAt.Equal(t0) {
			t.Errorf("%s: keadaan berubah", op)
		}
	}
	store := newMemStore()
	s := service(t, store, fakeEncoder{fail: true}, &clock{now: t0})
	if _, err := s.Discharge(ctx, discharge(t0, 250)); !errors.Is(err, errInjected) || len(store.state.events) != 0 {
		t.Fatalf("encoder gagal: %v", err)
	}
	// Encoder gagal saat memperbarui dan mengakhiri.
	store = newMemStore()
	c := &clock{now: t0}
	s = service(t, store, fakeEncoder{}, c)
	_, _ = s.Discharge(ctx, discharge(t0, 250))
	s.enc = fakeEncoder{fail: true}
	c.now = t0.Add(time.Hour)
	if _, err := s.Discharge(ctx, discharge(c.now, 340)); !errors.Is(err, errInjected) {
		t.Fatalf("update: %v", err)
	}
	if _, err := s.Expire(ctx, t0.Add(48*time.Hour), 10); !errors.Is(err, errInjected) {
		t.Fatalf("expire: %v", err)
	}
	store.failOn = "due"
	if _, err := s.Expire(ctx, t0.Add(48*time.Hour), 10); !errors.Is(err, errInjected) {
		t.Fatalf("due: %v", err)
	}
	store.failOn = "lock"
	if _, err := s.Expire(ctx, t0.Add(48*time.Hour), 10); !errors.Is(err, errInjected) {
		t.Fatalf("lock: %v", err)
	}
}

func TestRunExpiryAndSync(t *testing.T) {
	store := newMemStore()
	c := &clock{now: t0}
	s := service(t, store, fakeEncoder{}, c)
	_, _ = s.Discharge(context.Background(), discharge(t0, 250))
	c.now = t0.Add(48 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan int, 1)
	go s.RunExpiry(ctx, time.Hour, 10, func(n int, err error) {
		if err == nil {
			got <- n
		}
		cancel()
	})
	if n := <-got; n != 1 {
		t.Fatalf("%d diakhiri", n)
	}
	res, err := s.SyncThresholds(context.Background())
	if err != nil || res.Inserted != 1+3 || store.synced != 1 || len(s.Calibration().Discharge) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	store.failOn = "sync"
	if _, err := s.SyncThresholds(context.Background()); !errors.Is(err, errInjected) {
		t.Fatal(err)
	}
}

func TestNewValidates(t *testing.T) {
	bad := flood.DefaultPolicy()
	bad.Linger = 0
	if _, err := New(newMemStore(), fakeEncoder{}, bad, calibration(t), time.Now); !errors.Is(err, flood.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := New(newMemStore(), fakeEncoder{}, flood.DefaultPolicy(), flood.Calibration{}, time.Now); !errors.Is(err, flood.ErrInvalid) {
		t.Fatal(err)
	}
}
