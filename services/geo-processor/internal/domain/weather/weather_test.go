package weather

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

var t0 = time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)

func square(lat, lon, d float64) Ring {
	return Ring{{lat, lon}, {lat, lon + d}, {lat + d, lon + d}, {lat + d, lon}, {lat, lon}}
}

func key(id string) Key { return Key{Source: SourceBMKG, Identifier: id} }

// msg membuat pesan valid: sent = t0 + menit, berlaku 2 jam sejak 15 menit
// setelah kirim.
func msg(id string, minute int, typ MsgType, refs ...string) Message {
	sent := t0.Add(time.Duration(minute) * time.Minute)
	m := Message{
		Key: key(id), Sender: "cuaca.ekstrem@bmkg.go.id", Sent: sent, MsgType: typ,
		Category: "Met", EventCode: "OET-194", Urgency: "Immediate", Certainty: "Observed", Severity: SeverityModerate,
		Effective: sent.Add(15 * time.Minute), Expires: sent.Add(135 * time.Minute),
		Texts: []Text{
			{Language: "en", Event: "Thunderstorm", Headline: "Thunderstorm in West Java"},
			{Language: "id", Event: "Hujan Lebat dan Petir", Headline: "Hujan Lebat disertai Petir di Jawa Barat", Description: "a\nb"},
		},
		Web: "https://nowcasting.bmkg.go.id/infografis/CJB/x.jpg", SourceURL: "https://www.bmkg.go.id/alerts/nowcast/id/" + id + ".xml",
		AreaDesc: "Jawa Barat", Polygons: []Ring{square(-6.9, 107.6, 0.05)}, HasArea: true,
		FetchedAt: sent.Add(time.Minute), FirstSeenAt: sent.Add(time.Minute),
	}
	for _, r := range refs {
		parts := strings.SplitN(r, "@", 2)
		refMinute := 0
		if len(parts) == 2 {
			for _, c := range parts[1] {
				refMinute = refMinute*10 + int(c-'0')
			}
		}
		m.References = append(m.References, Reference{Key: key(parts[0]), Sender: m.Sender, Sent: t0.Add(time.Duration(refMinute) * time.Minute)})
	}
	slices.SortFunc(m.References, func(a, b Reference) int { return a.Compare(b.Key) })
	if typ == MsgCancel {
		m.Polygons, m.HasArea = nil, false
	}
	m.Digest = m.ContentDigest()
	return m
}

func TestValidMessages(t *testing.T) {
	for _, m := range []Message{msg("A", 0, MsgAlert), msg("B", 60, MsgUpdate, "A@0"), msg("C", 90, MsgCancel, "B@60")} {
		if err := m.Validate(); err != nil {
			t.Fatalf("%s: %v", m.Key, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Message){
		"sumber lain":             func(m *Message) { m.Source = "usgs" },
		"identifier berspasi":     func(m *Message) { m.Identifier = "a b" },
		"sender kosong":           func(m *Message) { m.Sender = "" },
		"sent kosong":             func(m *Message) { m.Sent = time.Time{} },
		"sent bukan UTC":          func(m *Message) { m.Sent = m.Sent.In(time.FixedZone("WIB", 7*3600)) },
		"sent di masa depan":      func(m *Message) { m.Sent = m.FetchedAt.Add(time.Hour) },
		"first_seen setelah":      func(m *Message) { m.FirstSeenAt = m.FetchedAt.Add(time.Second) },
		"fetched kosong":          func(m *Message) { m.FetchedAt = time.Time{} },
		"expires sebelum":         func(m *Message) { m.Expires = m.Effective },
		"berlaku terlalu lama":    func(m *Message) { m.Expires = m.Effective.Add(8 * 24 * time.Hour) },
		"onset = expires":         func(m *Message) { m.Onset = m.Expires },
		"msgType tak dikenal":     func(m *Message) { m.MsgType = "ack" },
		"update tanpa rujukan":    func(m *Message) { m.MsgType = MsgUpdate },
		"severity tak dikenal":    func(m *Message) { m.Severity = "parah" },
		"rujuk diri sendiri":      func(m *Message) { m.References = []Reference{{Key: m.Key, Sent: m.Sent}} },
		"rujukan sumber lain":     func(m *Message) { m.References = []Reference{{Key: Key{"usgs", "x"}, Sent: m.Sent}} },
		"rujukan dari masa depan": func(m *Message) { m.References = []Reference{{Key: key("x"), Sent: m.Sent.Add(time.Minute)}} },
		"rujukan tanpa waktu":     func(m *Message) { m.References = []Reference{{Key: key("x")}} },
		"rujukan ganda": func(m *Message) {
			m.References = []Reference{{Key: key("x"), Sent: m.Sent}, {Key: key("x"), Sent: m.Sent}}
		},
		"terlalu banyak rujukan": func(m *Message) {
			for i := range MaxReferences + 1 {
				m.References = append(m.References, Reference{Key: key(string(rune('A'+i/26)) + string(rune('a'+i%26))), Sent: m.Sent})
			}
		},
		"tanpa teks id":       func(m *Message) { m.Texts = m.Texts[:1] },
		"teks tidak urut":     func(m *Message) { m.Texts[0], m.Texts[1] = m.Texts[1], m.Texts[0] },
		"teks bukan UTF-8":    func(m *Message) { m.Texts[1].Description = "\xff" },
		"tanpa poligon":       func(m *Message) { m.Polygons, m.HasArea = nil, false },
		"HasArea tidak cocok": func(m *Message) { m.HasArea = false },
		"cincin terbuka":      func(m *Message) { m.Polygons[0] = m.Polygons[0][:4] },
		"cincin pendek":       func(m *Message) { m.Polygons[0] = Ring{{0, 0}, {0, 1}, {0, 0}} },
		"titik NaN":           func(m *Message) { m.Polygons[0][1].Lat = math.NaN() },
		"titik sama":          func(m *Message) { m.Polygons[0] = Ring{{1, 1}, {1, 1}, {1, 1}, {1, 1}} },
		"terlalu banyak poligon": func(m *Message) {
			for range MaxPolygons {
				m.Polygons = append(m.Polygons, square(0, 0, 1))
			}
		},
		"terlalu banyak titik": func(m *Message) {
			big := make(Ring, 0, MaxPoints+1)
			for i := range MaxPoints {
				big = append(big, Point{0, float64(i%10) / 10})
			}
			m.Polygons = []Ring{append(big, big[0])}
		},
		"web bukan https":   func(m *Message) { m.Web = "http://x" },
		"source_url rusak":  func(m *Message) { m.SourceURL = "://" },
		"area_desc panjang": func(m *Message) { m.AreaDesc = strings.Repeat("x", maxShortText+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := msg("A", 0, MsgAlert)
			m.Texts = slices.Clone(m.Texts)
			m.Polygons = slices.Clone(m.Polygons)
			m.Polygons[0] = slices.Clone(m.Polygons[0])
			mutate(&m)
			if err := m.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, ingin ErrInvalid", err)
			}
		})
	}
}

func TestSeverity(t *testing.T) {
	for s, want := range map[Severity]hazard.Level{
		SeverityMinor: hazard.LevelInfo, SeverityModerate: hazard.LevelWaspada, SeveritySevere: hazard.LevelSiaga,
		SeverityExtreme: hazard.LevelBahaya, SeverityUnknown: hazard.LevelInfo, "": hazard.LevelInfo,
	} {
		if s.Level() != want {
			t.Errorf("%q → %v", s, s.Level())
		}
	}
	if SeverityModerate.CAP() != "Moderate" || Severity("").CAP() != "" {
		t.Fatal(SeverityModerate.CAP())
	}
}

func TestContentDigestAndSupersedes(t *testing.T) {
	a := msg("A", 0, MsgAlert)
	b := a
	b.FetchedAt, b.FirstSeenAt, b.ArchiveKey = b.FetchedAt.Add(time.Hour), b.FirstSeenAt.Add(time.Hour), "lain"
	if a.ContentDigest() != b.ContentDigest() {
		t.Fatal("digest tidak boleh bergantung keterangan pengambilan")
	}
	c := a
	c.Polygons = []Ring{square(-6.9, 107.6, 0.06)}
	if a.ContentDigest() == c.ContentDigest() {
		t.Fatal("digest harus berubah bila poligon berubah")
	}
	c.Digest = c.ContentDigest()
	// Sent sama: digest lebih besar menang, simetris.
	if Supersedes(a, c) == Supersedes(c, a) {
		t.Fatal("Supersedes tidak antisimetris")
	}
	later := msg("A", 5, MsgAlert)
	if !Supersedes(later, a) || Supersedes(a, later) {
		t.Fatal("sent lebih baru harus menang")
	}
}

func TestRootAndDerive(t *testing.T) {
	a := msg("A", 0, MsgAlert)
	b := msg("B", 60, MsgUpdate, "A@0")
	b.Severity = SeveritySevere
	b.Onset = b.Effective.Add(5 * time.Minute)
	b.Digest = b.ContentDigest()
	root, ok := Root([]Message{b})
	if !ok || root.Key != key("A") || !root.Sent.Equal(t0) {
		t.Fatalf("pendiri dari rujukan: %+v", root)
	}
	if _, ok := Root(nil); ok {
		t.Fatal("rantai kosong tanpa pendiri")
	}
	v, err := Derive([]Message{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != EventIDFor(key("A")) || v.Current.Key != key("B") || v.Content.Key != key("B") || v.Messages != 2 ||
		v.Level != hazard.LevelSiaga || v.Cancelled || !v.OccurredAt.Equal(a.Effective) || !v.ExpiresAt.Equal(b.Expires) ||
		!v.DetectedAt.Equal(a.FirstSeenAt) || v.Title != "Hujan Lebat disertai Petir di Jawa Barat" {
		t.Fatalf("%+v", v)
	}
	if !strings.Contains(v.Summary, "tingkat Siaga (CAP Severe)") || !strings.Contains(v.Summary, "Diperbarui 1 kali") ||
		!strings.Contains(v.Summary, "24 Sep 2026 15.20–17.15 WIB") {
		t.Fatalf("ringkasan %q", v.Summary)
	}
	if v.Status(b.Expires.Add(-time.Second)) != StatusActive || v.Status(b.Expires) != StatusExpired {
		t.Fatal("status menurut waktu")
	}
	// Cancel tanpa area: isi dan area dari pesan berarea terakhir, status retracted.
	c := msg("C", 90, MsgCancel, "B@60")
	v, err = Derive([]Message{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Cancelled || v.Content.Key != key("B") || v.Current.Key != key("C") || v.Status(t0) != StatusRetracted ||
		!strings.Contains(v.Summary, "Dibatalkan BMKG pada 24 Sep 2026 15.30 WIB") {
		t.Fatalf("%+v", v)
	}
	if _, err := Derive([]Message{c}); !errors.Is(err, ErrNoArea) {
		t.Fatalf("cancel saja: %v", err)
	}
	if _, err := Derive(nil); !errors.Is(err, ErrNoArea) {
		t.Fatalf("kosong: %v", err)
	}
}

func TestTitleFallbacksAndLongRange(t *testing.T) {
	m := msg("A", 0, MsgAlert)
	m.Texts = []Text{{Language: "id", Event: "Angin Kencang"}}
	m.Expires = m.Effective.Add(26 * time.Hour)
	v, _ := Derive([]Message{m})
	if v.Title != "Peringatan dini cuaca: Angin Kencang" || !strings.Contains(v.Summary, "24 Sep 2026 14.15 – 25 Sep 2026 16.15 WIB") {
		t.Fatalf("%q / %q", v.Title, v.Summary)
	}
	m.Texts = []Text{{Language: "id", Description: "x"}}
	v, _ = Derive([]Message{m})
	if v.Title != "Peringatan dini cuaca" || !strings.HasPrefix(v.Summary, "Peringatan dini cuaca, tingkat Waspada") {
		t.Fatalf("%q / %q", v.Title, v.Summary)
	}
}

func TestAssess(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []float64{0, -1, 1.5, math.NaN()} {
		if (Policy{MinCoverage: bad}).Validate() == nil {
			t.Errorf("MinCoverage %v harus ditolak", bad)
		}
	}
	v, _ := Derive([]Message{msg("A", 0, MsgAlert)})
	fp := Footprint{AreaGeoJSON: `{"type":"MultiPolygon"}`, Latitude: -6.87, Longitude: 107.62, AreaKm2: 30, Regions: []RegionCoverage{
		{"32.73.01.1002", "B", 0.5}, {"32.73.01.1001", "A", 1.0000001}, {"32.73.01.1003", "C", 0.5}, {"32.73.01.1004", "Tipis", 0.02},
	}}
	a := DefaultPolicy().Assess(v, fp)
	if !a.Relevant || len(a.Impacts) != 3 || a.Impacts[0].Code != "32.73.01.1001" || a.Impacts[0].Coverage != 1 ||
		a.Impacts[1].Code != "32.73.01.1002" || a.Impacts[2].Code != "32.73.01.1003" || a.Impacts[0].Level != hazard.LevelWaspada ||
		a.Regions != nil {
		t.Fatalf("%+v", a.Impacts)
	}
	d := a.Digest()
	if d != DefaultPolicy().Assess(v, fp).Digest() {
		t.Fatal("digest tidak deterministik")
	}
	fp2 := fp
	fp2.AreaKm2 = 31
	if DefaultPolicy().Assess(v, fp2).Digest() == d {
		t.Fatal("digest harus berubah bila area berubah")
	}
	// Info (Minor): tanpa daftar wilayah terdampak, tetap relevan.
	m := msg("A", 0, MsgAlert)
	m.Severity = SeverityMinor
	vi, _ := Derive([]Message{m})
	if ai := DefaultPolicy().Assess(vi, fp); len(ai.Impacts) != 0 || !ai.Relevant {
		t.Fatalf("%+v", ai)
	}
	if DefaultPolicy().Assess(v, Footprint{}).Relevant {
		t.Fatal("tanpa irisan wilayah harus tidak relevan")
	}
}

// Properti rantai: View (tanpa waktu deteksi) tidak bergantung urutan pesan,
// dan pendiri selalu node paling awal di antara pesan dan rujukannya.
func FuzzDeriveOrderIndependent(f *testing.F) {
	f.Add(uint64(1), uint8(4))
	f.Add(uint64(99), uint8(7))
	f.Fuzz(func(t *testing.T, seed uint64, n uint8) {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		count := int(n%8) + 1
		var msgs []Message
		for i := range count {
			id := string(rune('A' + i))
			minute := rng.IntN(300)
			typ := MsgAlert
			var refs []string
			if i > 0 && rng.IntN(3) > 0 {
				typ = MsgUpdate
				if rng.IntN(4) == 0 {
					typ = MsgCancel
				}
				j := rng.IntN(i)
				refMinute := min(minute, int(msgs[j].Sent.Sub(t0)/time.Minute))
				refs = append(refs, msgs[j].Identifier+"@"+itoa(refMinute))
			}
			m := msg(id, minute, typ, refs...)
			if rng.IntN(3) == 0 {
				m.Severity = []Severity{SeverityMinor, SeveritySevere, SeverityExtreme}[rng.IntN(3)]
				m.Digest = m.ContentDigest()
			}
			msgs = append(msgs, m)
		}
		base, err := Derive(msgs)
		shuffled := slices.Clone(msgs)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		again, err2 := Derive(shuffled)
		if (err == nil) != (err2 == nil) {
			t.Fatalf("galat bergantung urutan: %v vs %v", err, err2)
		}
		if err != nil {
			return
		}
		if base.ID != again.ID || base.Current.Key != again.Current.Key || base.Content.Key != again.Content.Key ||
			base.Summary != again.Summary || !base.OccurredAt.Equal(again.OccurredAt) || base.Level != again.Level {
			t.Fatalf("View bergantung urutan:\n%+v\n%+v", base, again)
		}
		root, _ := Root(msgs)
		for _, m := range msgs {
			if m.Sent.Before(root.Sent) {
				t.Fatalf("pesan %s lebih awal dari pendiri %s", m.Key, root.Key)
			}
		}
		if !base.ExpiresAt.After(base.OccurredAt) {
			t.Fatalf("expires %v tidak setelah occurred %v", base.ExpiresAt, base.OccurredAt)
		}
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
