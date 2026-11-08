package fires

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

var t0 = time.Date(2026, 8, 20, 6, 30, 0, 0, time.UTC)

func cal() fire.Calibration {
	return fire.Calibration{
		Static: fire.Mask{{ID: "-6.37815,108.39848", Lat: -6.37815, Lon: 108.39848, RadiusKm: 2.43, Reason: fire.ReasonBoth, Detections: 10, First: t0, Last: t0}},
		Period: fire.Period{From: time.Date(2012, 1, 10, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)},
	}
}

// det membuat deteksi NOAA-20 di (lat, lon) pada t0+h, diterima 3 jam kemudian.
func det(product string, lat, lon float64, h time.Duration, c hotspot.Confidence) hotspot.Detection {
	at := t0.Add(h)
	frp := 4.5
	d := hotspot.Detection{
		Product: product, Satellite: "N20", Instrument: "VIIRS", Lat: lat, Lon: lon, DetectedAt: at, Confidence: c,
		BrightnessK: 340, FRPMW: &frp, ScanKm: 0.4, TrackKm: 0.4, Daytime: true, Version: "2.0NRT", FetchedAt: at.Add(3 * time.Hour),
	}
	if product == "MODIS_NRT" {
		pct := map[hotspot.Confidence]int{hotspot.Low: 20, hotspot.Nominal: 60, hotspot.High: 90}[c]
		d.Instrument, d.Satellite, d.ConfidencePct, d.ScanKm, d.TrackKm = "MODIS", "Aqua", &pct, 1, 1
	}
	d.ID = fmt.Sprintf("%s:%s:%.5f:%.5f", product, at.Format("20060102T1504"), lat, lon)
	return d
}

func newService(t *testing.T, store *memStore, clock *time.Time) *Service {
	t.Helper()
	svc, err := New(store, fakeEncoder{}, fire.DefaultPolicy(), cal(), func() time.Time { return *clock })
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func process(t *testing.T, svc *Service, clock *time.Time, d hotspot.Detection) Result {
	t.Helper()
	*clock = d.FetchedAt
	res, err := svc.Process(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLifecycle(t *testing.T) {
	store := newMemStore()
	var clock time.Time
	svc := newService(t, store, &clock)
	const n20, n21 = "VIIRS_NOAA20_NRT", "VIIRS_NOAA21_NRT"

	// Deteksi pertama membuka kejadian Info.
	a := det(n20, -6.80, 107.35, 0, hotspot.Nominal)
	res := process(t, svc, &clock, a)
	if !res.Changed || res.Created != 1 || res.Level != hazard.LevelInfo || res.Event != fire.EventIDFor(a.ID) {
		t.Fatalf("%+v", res)
	}
	// Pesan ulangan tidak mengubah apa pun.
	if res := process(t, svc, &clock, a); res.Changed || res.Skipped != SkipSeen {
		t.Fatalf("ulangan %+v", res)
	}
	// Lintasan kedua (NOAA-21, 50 menit kemudian, 1 km) → Waspada.
	b := det(n21, -6.809, 107.35, 50*time.Minute, hotspot.Nominal)
	if res := process(t, svc, &clock, b); res.Updated != 1 || res.Level != hazard.LevelWaspada || res.Event != fire.EventIDFor(a.ID) {
		t.Fatalf("lintasan kedua %+v", res)
	}
	// Kejadian kedua 5 km di timur, lalu deteksi di tengah menyambung
	// keduanya: kejadian yang lebih muda digabung ke yang pertama.
	c := det(n20, -6.80, 107.383, 2*time.Hour, hotspot.Nominal)
	if res := process(t, svc, &clock, c); res.Created != 1 {
		t.Fatalf("kejadian kedua %+v", res)
	}
	bridge := det(n20, -6.80, 107.3665, 12*time.Hour, hotspot.Low)
	res = process(t, svc, &clock, bridge)
	if res.Merged != 1 || res.Updated != 1 || res.Event != fire.EventIDFor(a.ID) {
		t.Fatalf("jembatan %+v", res)
	}
	second := store.state.events[fire.EventIDFor(c.ID)]
	if second.status != ports.StatusMerged || second.mergedInto != fire.EventIDFor(a.ID) {
		t.Fatalf("kejadian kedua %+v", second)
	}
	// Lima deteksi dalam 24 jam → Siaga.
	if res := process(t, svc, &clock, det("MODIS_NRT", -6.80, 107.36, 13*time.Hour, hotspot.Nominal)); res.Level != hazard.LevelSiaga {
		t.Fatalf("siaga %+v", res)
	}
	ev := store.state.events[fire.EventIDFor(a.ID)].rec
	if ev.Detections != 5 || ev.PeakDetections != 5 || ev.Overpasses != 5 || len(ev.Products) != 3 ||
		!ev.ExpiresAt.Equal(t0.Add(16*time.Hour+24*time.Hour)) || !strings.Contains(ev.Summary, "Siaga: 5 deteksi") {
		t.Fatalf("%+v", ev)
	}
	// S-NPP tidak dinilai; deteksi di kilang tersaring.
	if res := process(t, svc, &clock, det("VIIRS_SNPP_NRT", -6.80, 107.35, 14*time.Hour, hotspot.High)); res.Skipped != SkipProduct || res.Changed {
		t.Fatalf("S-NPP %+v", res)
	}
	if res := process(t, svc, &clock, det(n20, -6.379, 108.398, 14*time.Hour, hotspot.Nominal)); res.Skipped != SkipStatic || res.Static != "-6.37815,108.39848" {
		t.Fatalf("kilang %+v", res)
	}
	// Deteksi yang tiba setelah kejadian habis masa aktifnya (putaran
	// kedaluwarsa belum jalan) mengakhiri kejadian lama dan membuka yang baru.
	late := det(n20, -6.80, 107.35, 36*time.Hour, hotspot.Nominal)
	late.FetchedAt = t0.Add(41 * time.Hour)
	res = process(t, svc, &clock, late)
	if res.Ended != 1 || res.Created != 1 || res.Event == fire.EventIDFor(a.ID) {
		t.Fatalf("terlambat %+v", res)
	}
	if store.state.events[fire.EventIDFor(a.ID)].status != ports.StatusExpired {
		t.Fatal("kejadian lama tidak berakhir")
	}
	// Putaran kedaluwarsa mengakhiri sisanya.
	if n, err := svc.Expire(t.Context(), t0.Add(100*time.Hour), 10); err != nil || n != 1 || len(store.active()) != 0 {
		t.Fatalf("%d %v", n, err)
	}
	var subjects []string
	for _, m := range store.state.outbox {
		subjects = append(subjects, strings.TrimPrefix(m.Subject, "hazard.fire."))
	}
	want := "created updated created expired updated updated expired created expired"
	if strings.Join(subjects, " ") != want {
		t.Fatalf("outbox %v", subjects)
	}
}

func TestReplayOldDetection(t *testing.T) {
	store := newMemStore()
	clock := t0.Add(30 * 24 * time.Hour)
	svc := newService(t, store, &clock)
	d := det("VIIRS_NOAA21_NRT", -7.3, 107.7, 0, hotspot.High)
	d.FetchedAt = t0.Add(time.Hour)
	res, err := svc.Process(t.Context(), d)
	if err != nil || res.Created != 1 || res.Ended != 1 || res.Level != hazard.LevelWaspada || len(store.active()) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestErrors(t *testing.T) {
	var clock time.Time
	good := det("VIIRS_NOAA20_NRT", -6.8, 107.35, 0, hotspot.Nominal)
	bad := good
	bad.Confidence = "x"
	svc := newService(t, newMemStore(), &clock)
	clock = good.FetchedAt
	if _, err := svc.Process(t.Context(), bad); !errors.Is(err, hotspot.ErrInvalid) {
		t.Fatalf("deteksi tidak sah: %v", err)
	}
	for _, op := range []string{"lock", "has", "nearby", "save", "add", "enqueue"} {
		store := newMemStore()
		store.failOn = op
		svc := newService(t, store, &clock)
		if _, err := svc.Process(t.Context(), good); !errors.Is(err, errInjected) {
			t.Errorf("%s: %v", op, err)
		}
		if len(store.state.events) != 0 {
			t.Errorf("%s: transaksi tidak dibatalkan", op)
		}
	}
	// Galat di jalur gabung dan perbarui.
	second := det("VIIRS_NOAA21_NRT", -6.8, 107.361, time.Hour, hotspot.Nominal)
	for _, op := range []string{"episode", "members", "move", "end"} {
		store := newMemStore()
		svc := newService(t, store, &clock)
		clock = good.FetchedAt
		if _, err := svc.Process(t.Context(), good); err != nil {
			t.Fatal(err)
		}
		other := det("VIIRS_NOAA20_NRT", -6.8, 107.372, time.Hour, hotspot.Nominal)
		if _, err := svc.Process(t.Context(), other); err != nil {
			t.Fatal(err)
		}
		store.failOn = op
		if _, err := svc.Process(t.Context(), second); !errors.Is(err, errInjected) {
			t.Errorf("%s: %v", op, err)
		}
	}
	store := newMemStore()
	svcEnc, _ := New(store, fakeEncoder{fail: true}, fire.DefaultPolicy(), cal(), func() time.Time { return clock })
	if _, err := svcEnc.Process(t.Context(), good); !errors.Is(err, errInjected) {
		t.Fatalf("encoder: %v", err)
	}
	store.failOn = "due"
	if _, err := svcEnc.Expire(t.Context(), clock, 10); !errors.Is(err, errInjected) {
		t.Fatalf("due: %v", err)
	}
	if _, err := New(store, fakeEncoder{}, fire.Policy{}, cal(), time.Now); !errors.Is(err, fire.ErrInvalid) {
		t.Fatal("aturan kosong diterima")
	}
	if _, err := New(store, fakeEncoder{}, fire.DefaultPolicy(), fire.Calibration{}, time.Now); !errors.Is(err, fire.ErrInvalid) {
		t.Fatal("kalibrasi kosong diterima")
	}
}

func TestSyncAndRunExpiry(t *testing.T) {
	store := newMemStore()
	clock := t0
	svc := newService(t, store, &clock)
	if got := svc.Calibration(); len(got.Static) != 1 {
		t.Fatal(got)
	}
	if s, err := svc.SyncStatic(t.Context()); err != nil || s.Inserted != 1 || store.synced != 1 {
		t.Fatal(s, err)
	}
	d := det("VIIRS_NOAA20_NRT", -6.8, 107.35, 0, hotspot.Nominal)
	clock = d.FetchedAt
	if _, err := svc.Process(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	clock = t0.Add(48 * time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)
	go svc.RunExpiry(ctx, time.Millisecond, 10, func(n int, err error) {
		if err == nil {
			done <- n
		}
	})
	select {
	case n := <-done:
		if n != 1 {
			t.Fatal(n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunExpiry tidak mengakhiri kejadian")
	}
	cancel()
}

// FuzzServiceOrderIndependent: selama tidak ada kejadian yang kedaluwarsa,
// urutan kedatangan deteksi tidak mengubah pengelompokan dan tingkat
// (penggabungan membuat hasilnya sama dengan pengelompokan berantai sekaligus).
func FuzzServiceOrderIndependent(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, uint64(1))
	f.Add([]byte{0, 0, 0, 30, 0, 0, 60, 0, 0, 15, 0, 0}, uint64(42))
	f.Fuzz(func(t *testing.T, data []byte, seed uint64) {
		products := []string{"VIIRS_NOAA20_NRT", "VIIRS_NOAA21_NRT", "MODIS_NRT"}
		confs := []hotspot.Confidence{hotspot.Low, hotspot.Nominal, hotspot.High}
		var ds []hotspot.Detection
		seen := map[string]bool{}
		for i := 0; i+2 < len(data) && len(ds) < 30; i += 3 {
			lon := 107.30 + float64(data[i]%64)*0.004
			h := time.Duration(data[i+1]%120) * time.Hour
			d := det(products[data[i+2]%3], -6.8, lon, h, confs[(data[i+2]/3)%3])
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			ds = append(ds, d)
		}
		last := t0.Add(120 * time.Hour)
		for i := range ds {
			ds[i].FetchedAt = last
		}
		partition := func(order []hotspot.Detection) map[string]string {
			store := newMemStore()
			clock := last
			svc := newService(t, store, &clock)
			for _, d := range order {
				if _, err := svc.Process(t.Context(), d); err != nil {
					t.Fatal(err)
				}
			}
			out := map[string]string{}
			for _, e := range store.state.events {
				if e.status == ports.StatusMerged {
					continue
				}
				a := e.rec.Assessment
				key := fmt.Sprintf("%d:%v", a.Level, a.ReachedAt.Unix())
				for _, m := range a.Members {
					out[m.ID] = a.Members[0].ID + "|" + key
				}
			}
			return out
		}
		want := partition(ds)
		shuffled := append([]hotspot.Detection(nil), ds...)
		rand.New(rand.NewPCG(seed, seed^7)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := partition(shuffled)
		if len(got) != len(ds) || !maps.Equal(got, want) {
			t.Fatalf("urutan mengubah hasil:\n%v\n%v", want, got)
		}
	})
}
