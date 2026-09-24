package warning

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

var fetchedAt = time.Date(2026, 9, 24, 7, 20, 0, 0, time.UTC)

func square(lat, lon, d float64) Ring {
	return Ring{{lat, lon}, {lat, lon + d}, {lat + d, lon + d}, {lat + d, lon}, {lat, lon}}
}

// valid adalah peringatan Jawa Barat sintetis yang lolos semua invarian.
func valid() Warning {
	sent := time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)
	return Warning{
		Identifier: "2.49.0.1.360.0.2026.09.24.07.32.001",
		Sender:     "cuaca.ekstrem@bmkg.go.id",
		Sent:       sent,
		Status:     StatusActual,
		MsgType:    MsgAlert,
		Category:   "Met",
		EventCode:  "OET-194",
		Urgency:    UrgencyImmediate,
		Severity:   SeverityModerate,
		Certainty:  CertaintyObserved,
		Effective:  sent.Add(15 * time.Minute),
		Expires:    sent.Add(3 * time.Hour),
		Texts: []Text{
			{Language: "en", Headline: "Thunderstorm in West Java"},
			{Language: "id", Event: "Hujan Lebat dan Petir", Headline: "Hujan Lebat disertai Petir di Jawa Barat", Description: "Hujan lebat...\nMasyarakat dihimbau..."},
		},
		Web:       "https://nowcasting.bmkg.go.id/infografis/CJB/2026/09/24/infografis.jpg",
		Areas:     []Area{{Desc: "Jawa Barat", Polygons: []Ring{square(-6.9, 107.6, 0.05)}}},
		SourceURL: "https://www.bmkg.go.id/alerts/nowcast/id/CJB20260924001_alert.xml",
	}
}

func TestValidAccepted(t *testing.T) {
	w := valid()
	if err := w.Validate(fetchedAt); err != nil {
		t.Fatal(err)
	}
	if !w.Relevant() || w.PolygonCount() != 1 || w.PointCount() != 5 {
		t.Fatalf("relevan=%v poligon=%d titik=%d", w.Relevant(), w.PolygonCount(), w.PointCount())
	}
	if tx, ok := w.Text("en"); !ok || tx.Headline == "" {
		t.Fatal("teks en tidak ditemukan")
	}
	if _, ok := w.Text("fr"); ok {
		t.Fatal("teks fr seharusnya tidak ada")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Warning){
		"identifier kosong":     func(w *Warning) { w.Identifier = "" },
		"identifier berspasi":   func(w *Warning) { w.Identifier = "a b" },
		"identifier berkoma":    func(w *Warning) { w.Identifier = "a,b" },
		"sender kosong":         func(w *Warning) { w.Sender = "" },
		"sent kosong":           func(w *Warning) { w.Sent = time.Time{} },
		"sent bukan UTC":        func(w *Warning) { w.Sent = w.Sent.In(time.FixedZone("WIB", 7*3600)) },
		"sent di masa depan":    func(w *Warning) { w.Sent = fetchedAt.Add(time.Hour) },
		"expires sebelum mulai": func(w *Warning) { w.Expires = w.Effective },
		"berlaku terlalu lama":  func(w *Warning) { w.Expires = w.Effective.Add(8 * 24 * time.Hour) },
		"onset setelah expires": func(w *Warning) { w.Onset = w.Expires.Add(time.Minute) },
		"status tak dikenal":    func(w *Warning) { w.Status = StatusUnknown },
		"msgType tak dikenal":   func(w *Warning) { w.MsgType = MsgUnknown },
		"severity tak dikenal":  func(w *Warning) { w.Severity = SeverityUnspecified },
		"urgency tak dikenal":   func(w *Warning) { w.Urgency = UrgencyUnspecified },
		"certainty tak dikenal": func(w *Warning) { w.Certainty = CertaintyUnspecified },
		"update tanpa rujukan":  func(w *Warning) { w.MsgType = MsgUpdate },
		"rujuk diri sendiri":    func(w *Warning) { w.References = []Reference{{Sender: "x", Identifier: w.Identifier, Sent: w.Sent}} },
		"rujukan dari masa depan": func(w *Warning) {
			w.References = []Reference{{Sender: "x", Identifier: "y", Sent: w.Sent.Add(time.Minute)}}
		},
		"rujukan tanpa waktu": func(w *Warning) { w.References = []Reference{{Sender: "x", Identifier: "y"}} },
		"rujukan tidak urut": func(w *Warning) {
			w.References = []Reference{{Sender: "x", Identifier: "b", Sent: w.Sent}, {Sender: "x", Identifier: "a", Sent: w.Sent}}
		},
		"terlalu banyak rujukan": func(w *Warning) {
			for i := range MaxReferences + 1 {
				w.References = append(w.References, Reference{Sender: "x", Identifier: fmt.Sprintf("r%03d", i), Sent: w.Sent})
			}
		},
		"tanpa teks id":         func(w *Warning) { w.Texts = w.Texts[:1] },
		"bahasa ganda":          func(w *Warning) { w.Texts = append(w.Texts, w.Texts[1]) },
		"bahasa tidak urut":     func(w *Warning) { w.Texts[0], w.Texts[1] = w.Texts[1], w.Texts[0] },
		"kode bahasa aneh":      func(w *Warning) { w.Texts[0].Language = "e n" },
		"teks kosong":           func(w *Warning) { w.Texts[1].Headline, w.Texts[1].Description = "", " " },
		"headline kepanjangan":  func(w *Warning) { w.Texts[1].Headline = strings.Repeat("a", maxShortText+1) },
		"teks bukan UTF-8":      func(w *Warning) { w.Texts[1].Description = "\xff" },
		"tanpa poligon":         func(w *Warning) { w.Areas = nil },
		"cincin terbuka":        func(w *Warning) { w.Areas[0].Polygons[0] = w.Areas[0].Polygons[0][:4] },
		"cincin terlalu pendek": func(w *Warning) { w.Areas[0].Polygons[0] = Ring{{0, 0}, {0, 1}, {0, 0}} },
		"titik NaN":             func(w *Warning) { w.Areas[0].Polygons[0][1].Lat = math.NaN() },
		"bujur di luar rentang": func(w *Warning) { w.Areas[0].Polygons[0][2].Lon = 181 },
		"semua titik sama":      func(w *Warning) { w.Areas[0].Polygons[0] = Ring{{1, 1}, {1, 1}, {1, 1}, {1, 1}} },
		"web bukan https":       func(w *Warning) { w.Web = "http://bmkg.go.id/x" },
		"source_url rusak":      func(w *Warning) { w.SourceURL = "://" },
		"kategori kepanjangan":  func(w *Warning) { w.Category = strings.Repeat("x", maxShortText+1) },
		"terlalu banyak poligon": func(w *Warning) {
			for range MaxPolygons {
				w.Areas[0].Polygons = append(w.Areas[0].Polygons, square(0, 0, 1))
			}
		},
		"terlalu banyak titik": func(w *Warning) {
			big := make(Ring, 0, MaxPoints+1)
			for i := range MaxPoints {
				big = append(big, Point{Lat: 0, Lon: float64(i%100) / 100})
			}
			big = append(big, big[0])
			w.Areas[0].Polygons = []Ring{big}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := valid()
			mutate(&w)
			err := w.Validate(fetchedAt)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, ingin ErrInvalid", err)
			}
		})
	}
}

func TestCancelWithoutAreaAllowed(t *testing.T) {
	w := valid()
	w.MsgType = MsgCancel
	w.Areas = nil
	w.References = []Reference{{Sender: w.Sender, Identifier: "2.49.0.1.360.0.2026.09.24.06.32.001", Sent: w.Sent.Add(-time.Hour)}}
	if err := w.Validate(fetchedAt); err != nil {
		t.Fatal(err)
	}
}

func TestRelevant(t *testing.T) {
	for _, c := range []struct {
		status Status
		msg    MsgType
		want   bool
	}{
		{StatusActual, MsgAlert, true},
		{StatusActual, MsgUpdate, true},
		{StatusActual, MsgCancel, true},
		{StatusActual, MsgAck, false},
		{StatusActual, MsgError, false},
		{StatusExercise, MsgAlert, false},
		{StatusTest, MsgAlert, false},
		{StatusDraft, MsgAlert, false},
		{StatusSystem, MsgAlert, false},
	} {
		w := Warning{Status: c.status, MsgType: c.msg}
		if w.Relevant() != c.want {
			t.Errorf("Relevant(%v, %v) = %v", c.status, c.msg, !c.want)
		}
	}
}

func TestParseEnums(t *testing.T) {
	if ParseStatus(" Actual ") != StatusActual || ParseStatus("Exercise") != StatusExercise || ParseStatus("System") != StatusSystem ||
		ParseStatus("Test") != StatusTest || ParseStatus("Draft") != StatusDraft || ParseStatus("x") != StatusUnknown {
		t.Error("ParseStatus")
	}
	if ParseMsgType("Alert") != MsgAlert || ParseMsgType("UPDATE") != MsgUpdate || ParseMsgType("cancel") != MsgCancel ||
		ParseMsgType("Ack") != MsgAck || ParseMsgType("Error") != MsgError || ParseMsgType("") != MsgUnknown {
		t.Error("ParseMsgType")
	}
	if ParseSeverity("Extreme") != SeverityExtreme || ParseSeverity("Severe") != SeveritySevere || ParseSeverity("Moderate") != SeverityModerate ||
		ParseSeverity("Minor") != SeverityMinor || ParseSeverity("Unknown") != SeverityUnknown || ParseSeverity("parah") != SeverityUnspecified {
		t.Error("ParseSeverity")
	}
	if ParseUrgency("Immediate") != UrgencyImmediate || ParseUrgency("Expected") != UrgencyExpected || ParseUrgency("Future") != UrgencyFuture ||
		ParseUrgency("Past") != UrgencyPast || ParseUrgency("Unknown") != UrgencyUnknown || ParseUrgency("?") != UrgencyUnspecified {
		t.Error("ParseUrgency")
	}
	if ParseCertainty("Observed") != CertaintyObserved || ParseCertainty("Very  Likely") != CertaintyLikely || ParseCertainty("Likely") != CertaintyLikely ||
		ParseCertainty("Possible") != CertaintyPossible || ParseCertainty("Unlikely") != CertaintyUnlikely ||
		ParseCertainty("Unknown") != CertaintyUnknown || ParseCertainty("") != CertaintyUnspecified {
		t.Error("ParseCertainty")
	}
}

func TestParseReferences(t *testing.T) {
	refs, err := ParseReferences(" bmkg,B,2026-09-24T15:00:00+08:00\n bmkg,A,2026-09-24T14:00:00+07:00 bmkg,B,2026-09-24T07:00:00Z ")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Identifier != "A" || refs[1].Identifier != "B" || refs[1].Sent.Location() != time.UTC ||
		!refs[0].Sent.Equal(time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", refs)
	}
	if refs, err := ParseReferences("   "); err != nil || refs != nil {
		t.Fatalf("kosong: %v %v", refs, err)
	}
	for _, bad := range []string{"a,b", "a,b,c,d", "a,b,kemarin"} {
		if _, err := ParseReferences(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseReferences(%q) err = %v", bad, err)
		}
	}
}

func TestBMKGProvince(t *testing.T) {
	for id, want := range map[string]string{
		"2.49.0.1.360.0.2026.09.24.07.75.002": "75",
		"2.49.0.1.360.0.2026.09.24.07.32.001": "32",
	} {
		if got, ok := BMKGProvince(id); !ok || got != want {
			t.Errorf("BMKGProvince(%q) = %q, %v", id, got, ok)
		}
	}
	for _, id := range []string{
		"", "urn:oid:2.49.0.1.360.0.2026", "2.49.0.1.360.0.2026.09.24.07.3.001", "2.49.0.1.360.0.2026.09.24.07.32",
		"2.49.0.1.360.0.2026.09.24.07.3x.001", "2.49.0.1.360.0.2026.09.24..32.001", "2.49.0.1.840.0.2026.09.24.07.32.001",
	} {
		if p, ok := BMKGProvince(id); ok {
			t.Errorf("BMKGProvince(%q) = %q, seharusnya tidak terbaca", id, p)
		}
	}
}

func TestWithTranslation(t *testing.T) {
	id := valid()
	id.Texts = id.Texts[1:]
	en := valid()
	en.Texts = []Text{{Language: "en", Headline: "Thunderstorm"}, {Language: "id", Headline: "tidak boleh menimpa"}}
	got, err := id.WithTranslation(en)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Texts) != 2 || got.Texts[0].Language != "en" || got.Texts[1].Headline != "Hujan Lebat disertai Petir di Jawa Barat" {
		t.Fatalf("%+v", got.Texts)
	}
	if len(id.Texts) != 1 {
		t.Fatal("WithTranslation mengubah pesan asal")
	}
	other := en
	other.Identifier = "lain"
	if _, err := id.WithTranslation(other); !errors.Is(err, ErrInvalid) {
		t.Fatalf("identifier beda: %v", err)
	}
	other = en
	other.Severity = SeveritySevere
	if _, err := id.WithTranslation(other); !errors.Is(err, ErrInvalid) {
		t.Fatalf("severity beda: %v", err)
	}
}

// Properti ParseReferences: tidak panik, hasilnya urut tanpa ganda, dan
// menulis ulang lalu membaca lagi menghasilkan daftar yang sama.
func FuzzParseReferences(f *testing.F) {
	f.Add("bmkg,A,2026-09-24T07:00:00Z bmkg,B,2026-09-24T15:00:00+08:00")
	f.Add("a,b,c")
	f.Add(" ,, ")
	f.Fuzz(func(t *testing.T, s string) {
		refs, err := ParseReferences(s)
		if err != nil {
			return
		}
		var parts []string
		for i, r := range refs {
			if r.Sent.Location() != time.UTC {
				t.Fatal("waktu rujukan bukan UTC")
			}
			if i > 0 && refs[i-1].Identifier == r.Identifier && refs[i-1].Sender == r.Sender && refs[i-1].Sent.Equal(r.Sent) {
				t.Fatal("rujukan ganda")
			}
			parts = append(parts, r.Sender+","+r.Identifier+","+r.Sent.Format(time.RFC3339Nano))
		}
		again, err := ParseReferences(strings.Join(parts, " "))
		if err != nil || len(again) != len(refs) {
			t.Fatalf("tulis ulang tidak stabil: %v (%d vs %d)", err, len(again), len(refs))
		}
		for i := range refs {
			if again[i].Identifier != refs[i].Identifier || !again[i].Sent.Equal(refs[i].Sent) {
				t.Fatalf("rujukan %d berubah", i)
			}
		}
	})
}
