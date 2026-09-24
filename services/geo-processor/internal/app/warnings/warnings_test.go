package warnings

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

var t0 = time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)

func key(id string) weather.Key { return weather.Key{Source: weather.SourceBMKG, Identifier: id} }

// msg membuat pesan valid: sent = t0 + menit, berlaku 2 jam sejak 15 menit setelah kirim.
func msg(id string, minute int, typ weather.MsgType, refs ...string) weather.Message {
	sent := t0.Add(time.Duration(minute) * time.Minute)
	m := weather.Message{
		Key: key(id), Sender: "cuaca.ekstrem@bmkg.go.id", Sent: sent, MsgType: typ,
		Urgency: "Immediate", Certainty: "Observed", Severity: weather.SeverityModerate,
		Effective: sent.Add(15 * time.Minute), Expires: sent.Add(135 * time.Minute),
		Texts:    []weather.Text{{Language: "id", Event: "Hujan Lebat dan Petir", Headline: "Hujan Lebat di Jawa Barat " + id}},
		AreaDesc: "Jawa Barat",
		Polygons: []weather.Ring{{{Lat: -6.9, Lon: 107.6}, {Lat: -6.9, Lon: 107.7}, {Lat: -6.8, Lon: 107.7}, {Lat: -6.9, Lon: 107.6}}},
		HasArea:  true, FetchedAt: sent.Add(time.Minute), FirstSeenAt: sent.Add(time.Minute),
	}
	for _, r := range refs {
		id, at, _ := strings.Cut(r, "@")
		var refMinute int
		_, _ = fmt.Sscan(at, &refMinute)
		m.References = append(m.References, weather.Reference{Key: key(id), Sender: m.Sender, Sent: t0.Add(time.Duration(refMinute) * time.Minute)})
	}
	slices.SortFunc(m.References, func(a, b weather.Reference) int { return a.Compare(b.Key) })
	if typ == weather.MsgCancel {
		m.Polygons, m.HasArea = nil, false
	}
	return m
}

func newService(t *testing.T, store *memStore, enc fakeEncoder, now time.Time) *Service {
	t.Helper()
	s, err := New(store, enc, weather.DefaultPolicy(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func subjects(store *memStore) []string {
	var out []string
	for _, m := range store.state.outbox {
		out = append(out, strings.TrimPrefix(m.Subject, "hazard.weather.")+"@"+m.MsgID[strings.Index(m.MsgID, ":")+1:]+"="+string(m.Payload))
	}
	return out
}

func mustProcess(t *testing.T, s *Service, m weather.Message) Result {
	t.Helper()
	res, err := s.Process(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAlertUpdateCancel(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{}, t0.Add(30*time.Minute))
	a := msg("A", 0, weather.MsgAlert)
	if res := mustProcess(t, s, a); !res.Changed || res.Created != 1 {
		t.Fatalf("%+v", res)
	}
	id := weather.EventIDFor(key("A"))
	ev := store.state.events[id]
	if ev == nil || ev.status != ports.StatusActive || ev.rec.Level != hazard.LevelWaspada || len(ev.rec.Impacts) != 2 ||
		ev.rec.Impacts[0].Code != "32.73.01.1001" || store.state.msgs[key("A")].Event != id {
		t.Fatalf("%+v", ev)
	}
	// Pesan yang sama lagi (misal setelah restart): tidak ada perubahan.
	if res := mustProcess(t, s, a); res.Changed {
		t.Fatalf("ulangan: %+v", res)
	}
	b := msg("B", 30, weather.MsgUpdate, "A@0")
	b.Severity = weather.SeveritySevere
	if res := mustProcess(t, s, b); res.Updated != 1 || res.Created != 0 {
		t.Fatalf("%+v", res)
	}
	if ev := store.state.events[id]; ev.rec.Level != hazard.LevelSiaga || ev.rec.Current.Key != key("B") || ev.rec.Revision != 2 {
		t.Fatalf("%+v", ev.rec.View)
	}
	c := msg("C", 40, weather.MsgCancel, "B@30")
	if res := mustProcess(t, s, c); res.Ended != 1 {
		t.Fatalf("%+v", res)
	}
	if ev := store.state.events[id]; ev.status != ports.StatusRetracted || ev.rec.Revision != 3 {
		t.Fatalf("%+v %d", ev.status, ev.rec.Revision)
	}
	want := []string{"created@r1=waspada", "updated@r2=waspada→siaga", "expired@r3=3:00000000-0000-0000-0000-000000000000"}
	if got := subjects(store); !slices.Equal(got, want) {
		t.Fatalf("outbox %v", got)
	}
}

func TestOutOfOrderChainKeepsOneEvent(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{}, t0.Add(40*time.Minute))
	b := msg("B", 30, weather.MsgUpdate, "A@0")
	mustProcess(t, s, b)
	id := weather.EventIDFor(key("A"))
	if store.state.events[id] == nil {
		t.Fatal("pembaruan tanpa pesan awal harus membentuk kejadian dengan ID pendiri")
	}
	// Pesan awal datang belakangan: tetap satu kejadian, isi tetap dari pembaruan.
	res := mustProcess(t, s, msg("A", 0, weather.MsgAlert))
	if res.Created != 0 || len(store.state.events) != 1 || store.state.events[id].rec.Content.Key != key("B") ||
		store.state.events[id].rec.Messages != 2 {
		t.Fatalf("%+v %+v", res, store.state.events[id].rec.View)
	}
	// Revisi lama dengan identifier yang sama diabaikan.
	old := msg("B", 30, weather.MsgUpdate, "A@0")
	old.Sent = old.Sent.Add(-time.Minute)
	old.FetchedAt, old.FirstSeenAt = old.FetchedAt.Add(-time.Minute), old.FirstSeenAt.Add(-time.Minute)
	if res := mustProcess(t, s, old); res.Changed {
		t.Fatalf("revisi lama: %+v", res)
	}
	// Koreksi dengan sent lebih baru menggantikan, first_seen tetap yang terlama.
	fix := msg("B", 31, weather.MsgUpdate, "A@0")
	fix.Identifier = "B"
	if res := mustProcess(t, s, fix); !res.Changed {
		t.Fatalf("koreksi: %+v", res)
	}
	if got := store.state.msgs[key("B")].FirstSeenAt; !got.Equal(b.FirstSeenAt) {
		t.Fatalf("first_seen %v", got)
	}
}

func TestExpiredOnArrivalAndReactivation(t *testing.T) {
	store := newMemStore()
	late := t0.Add(5 * time.Hour)
	s := newService(t, store, fakeEncoder{}, late)
	res := mustProcess(t, s, msg("A", 0, weather.MsgAlert))
	if res.Created != 1 || res.Ended != 1 {
		t.Fatalf("%+v", res)
	}
	id := weather.EventIDFor(key("A"))
	if ev := store.state.events[id]; ev.status != ports.StatusExpired || ev.rec.Revision != 2 {
		t.Fatalf("%+v", ev)
	}
	// Pembaruan yang masih berlaku mengaktifkan lagi kejadian (updated, bukan created).
	upd := msg("B", 280, weather.MsgUpdate, "A@0")
	if res := mustProcess(t, s, upd); res.Updated != 1 {
		t.Fatalf("%+v", res)
	}
	if ev := store.state.events[id]; ev.status != ports.StatusActive || ev.rec.Revision != 3 {
		t.Fatalf("%+v", ev)
	}
	// Koreksi isi kejadian yang sudah berakhir: disimpan tanpa diterbitkan.
	store2 := newMemStore()
	s2 := newService(t, store2, fakeEncoder{}, late)
	mustProcess(t, s2, msg("A", 0, weather.MsgAlert))
	n := len(store2.state.outbox)
	a2 := msg("A", 1, weather.MsgAlert)
	a2.Severity = weather.SeveritySevere
	if res := mustProcess(t, s2, a2); res.Created+res.Updated+res.Ended != 0 || len(store2.state.outbox) != n {
		t.Fatalf("%+v", res)
	}
	if ev := store2.state.events[id]; ev.rec.Level != hazard.LevelSiaga || ev.rec.Revision != 3 || ev.status != ports.StatusExpired {
		t.Fatalf("%+v", ev)
	}
}

func TestExpireJob(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{}, t0)
	mustProcess(t, s, msg("A", 0, weather.MsgAlert))
	mustProcess(t, s, msg("X", 60, weather.MsgAlert))
	n, err := s.Expire(context.Background(), t0.Add(150*time.Minute), 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if ev := store.state.events[weather.EventIDFor(key("A"))]; ev.status != ports.StatusExpired || ev.rec.Revision != 2 {
		t.Fatalf("%+v", ev)
	}
	if n, _ := s.Expire(context.Background(), t0.Add(150*time.Minute), 10); n != 0 {
		t.Fatal("kejadian yang sama diakhiri dua kali")
	}
	store.failOn = "due"
	if _, err := s.Expire(context.Background(), t0.Add(10*time.Hour), 10); !errors.Is(err, errInjected) {
		t.Fatalf("err = %v", err)
	}
}

func TestChainsMergeWhenLinked(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{}, t0.Add(50*time.Minute))
	mustProcess(t, s, msg("X", 0, weather.MsgAlert))
	mustProcess(t, s, msg("A", 10, weather.MsgAlert))
	// M memperbarui keduanya: rantai tersambung, pendirinya X (paling awal).
	res := mustProcess(t, s, msg("M", 40, weather.MsgUpdate, "A@10", "X@0"))
	idX, idA := weather.EventIDFor(key("X")), weather.EventIDFor(key("A"))
	if res.Updated != 1 || res.Ended != 1 {
		t.Fatalf("%+v", res)
	}
	if store.state.events[idA].status != ports.StatusMerged || store.state.events[idA].mergedInto != idX ||
		store.state.events[idX].status != ports.StatusActive || store.state.msgs[key("A")].Event != idX {
		t.Fatalf("A=%+v X=%+v", store.state.events[idA], store.state.events[idX])
	}
	last := store.state.outbox[len(store.state.outbox)-1]
	if last.Subject != "hazard.weather.expired" || !strings.HasPrefix(string(last.Payload), "2:"+idX.String()) {
		t.Fatalf("%+v %s", last, last.Payload)
	}
	// Kejadian yang sudah berakhir digabung tanpa pesan expired baru.
	store2 := newMemStore()
	s2 := newService(t, store2, fakeEncoder{}, t0.Add(4*time.Hour))
	mustProcess(t, s2, msg("X", 0, weather.MsgAlert))
	mustProcess(t, s2, msg("A", 10, weather.MsgAlert))
	n := len(store2.state.outbox)
	res = mustProcess(t, s2, msg("M", 200, weather.MsgUpdate, "A@10", "X@0"))
	if res.Ended != 0 || store2.state.events[weather.EventIDFor(key("A"))].status != ports.StatusMerged || len(store2.state.outbox) != n+1 {
		t.Fatalf("%+v %v", res, subjects(store2)[n:])
	}
}

// Rujukan boleh membawa waktu kirim yang berbeda dari pesan aslinya, jadi
// pendiri rantai bisa bergeser lalu kembali ke node lama. Kejadian yang pernah
// digabung harus hidup lagi, tanpa siklus merged_into.
func TestRootShiftRevivesMergedEvent(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{}, t0.Add(90*time.Minute))
	idX, idY := weather.EventIDFor(key("X")), weather.EventIDFor(key("Y"))
	mustProcess(t, s, msg("B", 60, weather.MsgUpdate, "X@50"))
	mustProcess(t, s, msg("Y", 30, weather.MsgAlert))
	mustProcess(t, s, msg("C", 70, weather.MsgUpdate, "B@60", "Y@30"))
	if ev := store.state.events[idX]; ev.status != ports.StatusMerged || ev.mergedInto != idY {
		t.Fatalf("X=%+v", ev)
	}
	res := mustProcess(t, s, msg("D", 80, weather.MsgUpdate, "X@10"))
	if res.Updated != 1 || res.Ended != 1 {
		t.Fatalf("%+v", res)
	}
	x, y := store.state.events[idX], store.state.events[idY]
	if x.status != ports.StatusActive || !x.mergedInto.IsZero() || y.status != ports.StatusMerged || y.mergedInto != idX {
		t.Fatalf("X=%+v Y=%+v", x, y)
	}
	checkOutbox(t, store)

	// Sama, tetapi rantai sudah kedaluwarsa saat pendiri kembali: status
	// disesuaikan tanpa pesan baru untuk X.
	store = newMemStore()
	s = newService(t, store, fakeEncoder{}, t0.Add(10*time.Hour))
	for _, m := range []weather.Message{
		msg("B", 60, weather.MsgUpdate, "X@50"), msg("Y", 30, weather.MsgAlert),
		msg("C", 70, weather.MsgUpdate, "B@60", "Y@30"),
	} {
		mustProcess(t, s, m)
	}
	n := len(store.state.outbox)
	mustProcess(t, s, msg("D", 80, weather.MsgCancel, "X@10"))
	if x := store.state.events[idX]; x.status != ports.StatusRetracted || len(store.state.outbox) != n {
		t.Fatalf("X=%+v outbox %v", x, subjects(store)[n:])
	}
}

func TestIgnoredOutsideCoverageAndCancelFirst(t *testing.T) {
	store := newMemStore()
	store.regions = func(weather.Point) []weather.RegionCoverage { return nil }
	s := newService(t, store, fakeEncoder{}, t0)
	if res := mustProcess(t, s, msg("A", 0, weather.MsgAlert)); !res.Ignored || len(store.state.events) != 0 {
		t.Fatalf("%+v", res)
	}

	store = newMemStore()
	s = newService(t, store, fakeEncoder{}, t0.Add(50*time.Minute))
	// Cancel lebih dulu: disimpan, belum ada kejadian.
	if res := mustProcess(t, s, msg("C", 40, weather.MsgCancel, "A@0")); !res.Changed || len(store.state.events) != 0 {
		t.Fatalf("%+v", res)
	}
	// Peringatan awal datang: kejadian langsung tercatat lalu dibatalkan.
	res := mustProcess(t, s, msg("A", 0, weather.MsgAlert))
	if res.Created != 1 || res.Ended != 1 || store.state.events[weather.EventIDFor(key("A"))].status != ports.StatusRetracted {
		t.Fatalf("%+v", res)
	}
}

func TestErrorsRollBackAndInvalidIsPermanent(t *testing.T) {
	bad := msg("A", 0, weather.MsgAlert)
	bad.Expires = bad.Effective
	s := newService(t, newMemStore(), fakeEncoder{}, t0)
	if _, err := s.Process(context.Background(), bad); !errors.Is(err, weather.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	for _, op := range []string{"lock", "message", "save-message", "component", "footprint", "event", "save-event", "enqueue", "membership", "end"} {
		store := newMemStore()
		now := t0.Add(4 * time.Hour)
		m := msg("A", 10, weather.MsgUpdate, "X@0")
		if op == "end" {
			// Cancel untuk kejadian yang masih aktif harus mengakhirinya.
			now, m = t0.Add(20*time.Minute), msg("A", 10, weather.MsgCancel, "X@0")
		}
		s := newService(t, store, fakeEncoder{}, now)
		if op == "membership" || op == "end" {
			mustProcess(t, s, msg("X", 0, weather.MsgAlert))
		}
		store.failOn = op
		before := store.state.clone()
		if _, err := s.Process(context.Background(), m); !errors.Is(err, errInjected) {
			t.Fatalf("%s: err = %v", op, err)
		}
		if len(store.state.msgs) != len(before.msgs) || len(store.state.outbox) != len(before.outbox) {
			t.Fatalf("%s: transaksi tidak di-rollback", op)
		}
	}
	for name, now := range map[string]time.Time{"aktif": t0, "kedaluwarsa": t0.Add(10 * time.Hour)} {
		store := newMemStore()
		s := newService(t, store, fakeEncoder{fail: true}, now)
		if _, err := s.Process(context.Background(), msg("A", 0, weather.MsgAlert)); !errors.Is(err, errInjected) {
			t.Fatalf("encoder gagal (%s): %v", name, err)
		}
	}
	store := newMemStore()
	s = newService(t, store, fakeEncoder{}, t0)
	mustProcess(t, s, msg("A", 0, weather.MsgAlert))
	s.enc = fakeEncoder{fail: true}
	for _, m := range []weather.Message{msg("B", 5, weather.MsgUpdate, "A@0"), msg("C", 5, weather.MsgCancel, "A@0")} {
		if _, err := s.Process(context.Background(), m); !errors.Is(err, errInjected) {
			t.Fatalf("%s: %v", m.Key, err)
		}
	}
	if _, err := New(store, fakeEncoder{}, weather.Policy{}, time.Now); err == nil {
		t.Fatal("policy kosong harus ditolak")
	}
}

func TestRunExpiryStopsWithContext(t *testing.T) {
	store := newMemStore()
	s := newService(t, store, fakeEncoder{}, t0.Add(10*time.Hour))
	store.failOn = "due"
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	s.RunExpiry(ctx, time.Millisecond, 10, func(int, error) { calls++ })
	if calls == 0 {
		t.Fatal("after tidak pernah dipanggil untuk galat")
	}
}

// Properti utama: keadaan akhir kejadian yang tidak digabung (ID, status,
// isi) hanya bergantung pada himpunan pesan, bukan urutan kedatangan atau
// pengulangan; dan di outbox setiap kejadian selalu diawali created dengan
// revisi yang naik ketat.
func FuzzServiceOrderIndependent(f *testing.F) {
	f.Add(uint64(1), uint8(5), uint8(3))
	f.Add(uint64(7), uint8(8), uint8(0))
	f.Add(uint64(42), uint8(3), uint8(9))
	f.Fuzz(func(t *testing.T, seed uint64, n, nowStep uint8) {
		rng := rand.New(rand.NewPCG(seed, seed^0xabcdef))
		count := int(n%7) + 2
		var msgs []weather.Message
		for i := range count {
			id := string(rune('A' + i))
			minute := rng.IntN(240)
			typ := weather.MsgAlert
			var refs []string
			if i > 0 && rng.IntN(3) > 0 {
				typ = weather.MsgUpdate
				if rng.IntN(5) == 0 {
					typ = weather.MsgCancel
				}
				for range rng.IntN(2) + 1 {
					j := rng.IntN(i)
					ref := msgs[j].Identifier + "@" + fmt.Sprint(min(minute, int(msgs[j].Sent.Sub(t0)/time.Minute)))
					if !slices.ContainsFunc(refs, func(r string) bool { return strings.HasPrefix(r, msgs[j].Identifier+"@") }) {
						refs = append(refs, ref)
					}
				}
			}
			m := msg(id, minute, typ, refs...)
			m.Severity = []weather.Severity{weather.SeverityMinor, weather.SeverityModerate, weather.SeveritySevere, weather.SeverityExtreme}[rng.IntN(4)]
			msgs = append(msgs, m)
		}
		now := t0.Add(time.Duration(nowStep%12) * 30 * time.Minute)
		run := func(order []weather.Message) *memStore {
			store := newMemStore()
			s := newService(t, store, fakeEncoder{}, now)
			for _, m := range order {
				if _, err := s.Process(context.Background(), m); err != nil {
					t.Fatal(err)
				}
			}
			return store
		}
		base := run(msgs)
		shuffled := slices.Clone(msgs)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		shuffled = append(shuffled, shuffled[:len(shuffled)/2]...) // pengulangan
		other := run(shuffled)
		if a, b := liveState(base), liveState(other); a != b {
			t.Fatalf("keadaan akhir bergantung urutan:\n%s\n---\n%s", a, b)
		}
		checkOutbox(t, base)
		checkOutbox(t, other)
	})
}

// liveState meringkas kejadian yang tidak digabung: ID, status, digest isi.
func liveState(store *memStore) string {
	var lines []string
	for id, ev := range store.state.events {
		if ev.status == ports.StatusMerged {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s %x", id, ev.status, ev.rec.Digest[:6]))
	}
	for k, m := range store.state.msgs {
		ev := store.state.events[m.Event]
		for hops := 0; ev != nil && ev.status == ports.StatusMerged; hops++ {
			if hops > len(store.state.events) {
				return "siklus merged_into dari " + m.Event.String()
			}
			m.Event = ev.mergedInto
			ev = store.state.events[m.Event]
		}
		lines = append(lines, fmt.Sprintf("pesan %s → %s", k.Identifier, m.Event))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func checkOutbox(t *testing.T, store *memStore) {
	t.Helper()
	last := map[string]int{}
	for _, m := range store.state.outbox {
		id, revText, _ := strings.Cut(m.MsgID, ":r")
		var rev int
		_, _ = fmt.Sscan(revText, &rev)
		prev, seen := last[id]
		if !seen && m.Subject != "hazard.weather.created" {
			t.Fatalf("kejadian %s diawali %s", id, m.Subject)
		}
		if seen && rev <= prev {
			t.Fatalf("revisi %s tidak naik: %d setelah %d", id, rev, prev)
		}
		last[id] = rev
	}
}
