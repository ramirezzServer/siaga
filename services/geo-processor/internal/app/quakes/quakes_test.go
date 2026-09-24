package quakes

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

var t0 = time.Date(2026, 9, 23, 12, 16, 56, 0, time.UTC)

func bmkg(id string, dt time.Duration, lat, lon, mag float64, fetched time.Duration) quake.Report {
	return quake.Report{
		Source: quake.SourceBMKG, Feed: quake.FeedBMKGLatest, EventID: id, OccurredAt: t0.Add(dt),
		Latitude: lat, Longitude: lon, Magnitude: mag, DepthKm: 10,
		Place: "Pusat gempa berada di darat 5 km Cianjur", Tsunami: quake.TsunamiNone,
		FetchedAt: t0.Add(fetched),
	}
}

func usgs(id string, dt time.Duration, lat, lon, mag float64, updated time.Duration, status string) quake.Report {
	return quake.Report{
		Source: quake.SourceUSGS, Feed: quake.FeedUSGSSummary, EventID: id, OccurredAt: t0.Add(dt),
		Latitude: lat, Longitude: lon, Magnitude: mag, MagnitudeType: "mb", DepthKm: 10,
		SourceUpdatedAt: t0.Add(updated), ReviewStatus: status, FetchedAt: t0.Add(updated + time.Minute),
	}
}

func newService(t *testing.T, store *memStore, enc fakeEncoder) *Service {
	t.Helper()
	s, err := New(store, enc, quake.DefaultRules(), quake.DefaultPolicy(), func() time.Time { return t0.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func subjects(store *memStore) []string {
	var out []string
	for _, m := range store.state.outbox {
		out = append(out, strings.TrimPrefix(m.Subject, "hazard.quake.")+" "+string(m.Payload))
	}
	return out
}

func mustProcess(t *testing.T, s *Service, r quake.Report) Result {
	t.Helper()
	res, err := s.Process(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestProcessCreatesThenCorroboratesThenIgnoresRepeats(t *testing.T) {
	store := newMemStore()
	store.regions = []quake.RegionDistance{
		{Code: "32.03.01.2001", Name: "Pusat", DistanceKm: 0},
		{Code: "32.03.01.2002", Name: "Jauh", DistanceKm: 300},
	}
	s := newService(t, store, fakeEncoder{})
	ctx := context.Background()

	b := bmkg("20260923121656", 0, -6.85, 107.03, 5.6, 3*time.Minute)
	if res := mustProcess(t, s, b); !res.Changed || res.Created != 1 {
		t.Fatalf("laporan pertama: %+v", res)
	}
	if res := mustProcess(t, s, b); res.Changed || res.Created+res.Updated != 0 {
		t.Fatalf("ulangan harus diabaikan: %+v", res)
	}
	felt := b
	felt.Feed = quake.FeedBMKGFelt
	felt.Felt = "V Cianjur"
	felt.FetchedAt = b.FetchedAt.Add(time.Minute)
	if res := mustProcess(t, s, felt); res.Updated != 1 {
		t.Fatalf("feed dirasakan menambah isi: %+v", res)
	}
	u := usgs("us7000ir9t", -3*time.Second, -6.836, 106.997, 5.6, 20*time.Minute, "reviewed")
	if res := mustProcess(t, s, u); res.Updated != 1 || res.Created != 0 {
		t.Fatalf("USGS harus bergabung: %+v", res)
	}
	if len(store.state.events) != 1 {
		t.Fatalf("harus satu kejadian, ada %d", len(store.state.events))
	}
	for _, ev := range store.state.events {
		if ev.rec.Primary.Key.Source != quake.SourceBMKG || len(ev.rec.Corroborating) != 1 || ev.rec.Revision != 3 ||
			ev.rec.Level != quake.LevelSiaga || len(ev.rec.Impacts) != 1 {
			t.Fatalf("kejadian %+v", ev.rec)
		}
	}
	want := []string{"created siaga", "updated siaga→siaga", "updated siaga→siaga"}
	if got := subjects(store); !slices.Equal(got, want) {
		t.Fatalf("outbox %v, ingin %v", got, want)
	}
	// Laporan lama yang datang terlambat tidak mengubah apa pun.
	old := u
	old.SourceUpdatedAt, old.FetchedAt, old.Magnitude = t0, t0.Add(30*time.Minute), 5.2
	if res := mustProcess(t, s, old); res.Updated != 0 {
		t.Fatalf("revisi lama tidak boleh menerbitkan: %+v", res)
	}
	_ = ctx
}

func TestProcessMergesRevisedReportAndRetractsDeleted(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{})

	mustProcess(t, s, bmkg("20260923121656", 0, -7, 107, 5.0, time.Minute))
	// USGS otomatis meleset 150 km: kejadian sendiri.
	if res := mustProcess(t, s, usgs("us1", 0, -8.35, 107, 5.0, 2*time.Minute, "automatic")); res.Created != 1 {
		t.Fatalf("USGS jauh harus berdiri sendiri: %+v", res)
	}
	// Revisi USGS masuk jangkauan: digabung, kejadian USGS diakhiri sebagai merged.
	res := mustProcess(t, s, usgs("us1", time.Second, -7.1, 107, 5.1, 10*time.Minute, "reviewed"))
	if res.Updated != 1 || res.Ended != 1 {
		t.Fatalf("revisi harus menggabung: %+v", res)
	}
	var merged, active int
	for _, ev := range store.state.events {
		switch ev.status {
		case ports.StatusMerged:
			merged++
			if ev.mergedInto.IsZero() || ev.endedAt.IsZero() {
				t.Errorf("merged tanpa tujuan/waktu: %+v", ev)
			}
		case ports.StatusActive:
			active++
		case ports.StatusExpired, ports.StatusRetracted:
			t.Errorf("status tak terduga %s", ev.status)
		}
	}
	if merged != 1 || active != 1 {
		t.Fatalf("merged=%d active=%d", merged, active)
	}
	last := subjects(store)
	if !strings.HasPrefix(last[len(last)-1], "expired reason=2 into=") {
		t.Fatalf("pesan terakhir %q", last[len(last)-1])
	}

	// USGS lain yang berdiri sendiri lalu ditarik: kejadiannya retracted.
	mustProcess(t, s, usgs("us9", time.Hour, -6, 108, 4.0, time.Hour+time.Minute, "automatic"))
	res = mustProcess(t, s, usgs("us9", time.Hour, -6, 108, 4.0, time.Hour+5*time.Minute, quake.ReviewDeleted))
	if res.Ended != 1 {
		t.Fatalf("laporan ditarik harus mengakhiri kejadian: %+v", res)
	}
	if _, ok := store.state.member[quake.IdentityKey{Source: quake.SourceUSGS, EventID: "us9"}]; ok {
		t.Error("laporan ditarik harus lepas dari kejadian")
	}
	if got := subjects(store); got[len(got)-1] != "expired reason=3" {
		t.Fatalf("pesan terakhir %q", got[len(got)-1])
	}
}

func TestExpireEndsElapsedEvents(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{})
	mustProcess(t, s, bmkg("20260923121656", 0, -7, 107, 5.0, time.Minute))
	ctx := context.Background()
	if n, err := s.Expire(ctx, t0.Add(5*time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("belum waktunya: %d %v", n, err)
	}
	if n, err := s.Expire(ctx, t0.Add(6*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("masa aktif habis: %d %v", n, err)
	}
	if n, _ := s.Expire(ctx, t0.Add(7*time.Hour), 10); n != 0 {
		t.Fatal("kejadian yang sudah berakhir tidak diakhiri lagi")
	}
	for _, ev := range store.state.events {
		if ev.status != ports.StatusExpired || ev.rec.Revision != 2 {
			t.Fatalf("kejadian %+v", ev)
		}
	}
	// Revisi setelah kedaluwarsa tetap tersimpan dan diterbitkan, status tetap expired.
	late := bmkg("20260923121656", 0, -7, 107, 5.2, 2*time.Minute)
	if res := mustProcess(t, s, late); res.Updated != 1 {
		t.Fatalf("revisi kejadian kedaluwarsa: %+v", res)
	}
	for _, ev := range store.state.events {
		if ev.status != ports.StatusExpired {
			t.Fatal("status tidak boleh kembali aktif")
		}
	}
}

func TestErrorsRollBackAndInvalidIsPermanent(t *testing.T) {
	ctx := context.Background()
	b := bmkg("20260923121656", 0, -7, 107, 5.0, time.Minute)
	for _, op := range []string{"lock", "report", "save-report", "clusters", "exists", "regions", "save-event", "enqueue"} {
		store := newMemStore()
		store.failOn = op
		s := newService(t, store, fakeEncoder{})
		if _, err := s.Process(ctx, b); !errors.Is(err, errInjected) {
			t.Errorf("%s: err = %v", op, err)
		}
		if len(store.state.reports) != 0 || len(store.state.outbox) != 0 {
			t.Errorf("%s: transaksi gagal harus di-rollback", op)
		}
	}
	for _, kind := range []string{"created"} {
		s := newService(t, newMemStore(), fakeEncoder{failOn: kind})
		if _, err := s.Process(ctx, b); !errors.Is(err, errInjected) {
			t.Errorf("encoder %s: %v", kind, err)
		}
	}
	store := newMemStore()
	s := newService(t, store, fakeEncoder{})
	mustProcess(t, s, b)
	store.failOn = "due"
	if _, err := s.Expire(ctx, t0.Add(7*time.Hour), 10); !errors.Is(err, errInjected) {
		t.Errorf("Expire: %v", err)
	}
	store.failOn = ""
	s.enc = fakeEncoder{failOn: "expired"}
	if _, err := s.Expire(ctx, t0.Add(7*time.Hour), 10); !errors.Is(err, errInjected) {
		t.Errorf("Expire encoder: %v", err)
	}
	s.enc = fakeEncoder{failOn: "updated"}
	if _, err := s.Process(ctx, bmkg("20260923121656", 0, -7, 107, 5.3, 2*time.Minute)); !errors.Is(err, errInjected) {
		t.Errorf("encoder updated: %v", err)
	}

	bad := b
	bad.Magnitude = 11
	if _, err := s.Process(ctx, bad); !errors.Is(err, quake.ErrInvalid) {
		t.Errorf("laporan tidak valid: %v", err)
	}
	if _, err := New(store, fakeEncoder{}, quake.Rules{}, quake.DefaultPolicy(), time.Now); err == nil {
		t.Error("aturan kosong harus ditolak")
	}
}

func TestRunExpiryStopsWithContext(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{})
	s.now = func() time.Time { return t0.Add(7 * time.Hour) }
	mustProcess(t, s, bmkg("20260923121656", 0, -7, 107, 5.0, time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan int, 4)
	done := make(chan struct{})
	go func() {
		s.RunExpiry(ctx, time.Millisecond, 10, func(n int, _ error) { calls <- n })
		close(done)
	}()
	if n := <-calls; n != 1 {
		t.Fatalf("putaran pertama mengakhiri %d", n)
	}
	cancel()
	<-done
}

// Properti tingkat use case: urutan kedatangan tidak mengubah pengelompokan
// akhir, setiap kejadian aktif punya pesan terakhir created/updated dengan
// revisi yang sama dengan penyimpanan, dan memproses ulang semua laporan
// tidak menambah pesan outbox.
func FuzzServiceOrderIndependent(f *testing.F) {
	f.Add(uint64(3), uint8(5))
	f.Add(uint64(11), uint8(9))
	f.Fuzz(func(t *testing.T, seed uint64, n uint8) {
		rng := rand.New(rand.NewPCG(seed, seed+1))
		var reports []quake.Report
		for q := range 1 + int(n%7) {
			base := time.Duration(q) * 10 * time.Minute
			lat, lon, mag := -8+rng.Float64()*2, 106+rng.Float64()*3, 3+rng.Float64()*3
			j := func(scale float64) float64 { return (rng.Float64()*2 - 1) * scale }
			if rng.IntN(5) > 0 {
				at := base + time.Duration(j(2)*float64(time.Second))
				id := t0.Add(at).Truncate(time.Second).Format("20060102150405")
				reports = append(reports, bmkg(id, at.Truncate(time.Second), lat+j(0.05), lon+j(0.05), mag+j(0.2), base+time.Minute))
			}
			if rng.IntN(5) > 1 {
				id := fmt.Sprintf("us%d", q)
				for rev := range 1 + rng.IntN(3) {
					at := base + time.Duration(j(5)*float64(time.Second))
					reports = append(reports, usgs(id, at, lat+j(0.2), lon+j(0.2), mag+j(0.3), base+time.Duration(rev+2)*time.Minute, "automatic"))
				}
			}
		}
		var want string
		for round := range 3 {
			order := slices.Clone(reports)
			if round > 0 {
				rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			}
			store := newMemStore()
			s := newService(t, store, fakeEncoder{})
			for _, r := range order {
				mustProcess(t, s, r)
			}
			got := partition(store)
			if round == 0 {
				want = got
			} else if got != want {
				t.Fatalf("bergantung urutan:\n %s\n %s", want, got)
			}
			checkOutbox(t, store)
			before := len(store.state.outbox)
			for _, r := range order {
				if res := mustProcess(t, s, r); res.Changed {
					t.Fatalf("ulangan mengubah keadaan: %+v", r)
				}
			}
			if len(store.state.outbox) != before {
				t.Fatal("ulangan menambah pesan outbox")
			}
		}
	})
}

func partition(store *memStore) string {
	groups := map[quake.EventID][]string{}
	for k, id := range store.state.member {
		if ev := store.state.events[id]; ev.status == ports.StatusActive {
			groups[id] = append(groups[id], k.String())
		}
	}
	var out []string
	for _, g := range groups {
		slices.Sort(g)
		out = append(out, strings.Join(g, "+"))
	}
	slices.Sort(out)
	return strings.Join(out, " | ")
}

func checkOutbox(t *testing.T, store *memStore) {
	t.Helper()
	last := map[string]ports.OutboxMessage{}
	for _, m := range store.state.outbox {
		last[strings.Split(m.MsgID, ":")[0]] = m
	}
	for id, ev := range store.state.events {
		m, ok := last[id.String()]
		if !ok {
			t.Fatalf("kejadian %s tanpa pesan", id)
		}
		wantRev := fmt.Sprintf(":r%d", ev.rec.Revision)
		if !strings.HasSuffix(m.MsgID, wantRev) {
			t.Fatalf("pesan terakhir %s tidak sesuai revisi %d", m.MsgID, ev.rec.Revision)
		}
		isExpired := strings.HasSuffix(m.Subject, ".expired")
		if isExpired != (ev.status != ports.StatusActive) {
			t.Fatalf("kejadian %s status %s tetapi pesan terakhir %s", id, ev.status, m.Subject)
		}
	}
}
