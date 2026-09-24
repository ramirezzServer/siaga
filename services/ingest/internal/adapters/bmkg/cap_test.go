package bmkg

import (
	"errors"
	"strings"
	"testing"
	"time"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var capFetchedAt = time.Date(2026, 9, 24, 7, 20, 0, 0, time.UTC)

func TestCAPRequests(t *testing.T) {
	c := NewCAPFeed("http://replay.test/nowcast")
	if c.Name() != "bmkg-cap" || c.ArchiveExt() != "xml" {
		t.Fatal(c.Name(), c.ArchiveExt())
	}
	if r := c.FeedRequest(); r.URL != "http://replay.test/nowcast/id/rss.xml" || r.MaxBytes == 0 {
		t.Fatalf("%+v", r)
	}
	it := ports.FeedItem{Key: "k", File: "CJB20260924001_alert.xml"}
	if r := c.DetailRequest(it, "en"); r.URL != "http://replay.test/nowcast/en/CJB20260924001_alert.xml" || r.MaxBytes == 0 {
		t.Fatalf("%+v", r)
	}
	if SourceURL(it.File) != "https://www.bmkg.go.id/alerts/nowcast/id/CJB20260924001_alert.xml" {
		t.Fatal(SourceURL(it.File))
	}
}

func TestParseFeedRealRSS(t *testing.T) {
	c := NewCAPFeed(DefaultCAPBaseURL)
	items, rej, err := c.ParseFeed(fixture(t, "nowcast-rss-id.xml"))
	if err != nil || len(rej) != 0 {
		t.Fatalf("err=%v rej=%v", err, rej)
	}
	if len(items) != 11 {
		t.Fatalf("ingin 11 peringatan, dapat %d", len(items))
	}
	first := items[0]
	if first.Key != "2.49.0.1.360.0.2026.09.24.07.75.002" || first.File != "CGT20260924002_alert.xml" ||
		first.Title != "Hujan Lebat disertai Petir di Gorontalo" || len(first.Digest) != 64 {
		t.Fatalf("%+v", first)
	}
	provinces := map[string]bool{}
	for _, it := range items {
		p, ok := warning.BMKGProvince(it.Key)
		if !ok {
			t.Fatalf("provinsi %s tidak terbaca", it.Key)
		}
		provinces[p] = true
	}
	if len(provinces) != 10 || !provinces["12"] || provinces["32"] {
		t.Fatalf("provinsi %v", provinces)
	}
	// Digest stabil dan berubah bila isi entri berubah.
	again, _, _ := c.ParseFeed(fixture(t, "nowcast-rss-id.xml"))
	if again[0].Digest != first.Digest {
		t.Fatal("digest tidak stabil")
	}
	edited := strings.Replace(string(fixture(t, "nowcast-rss-id.xml")), "DENGILO, MANANGGU", "DENGILO", 1)
	changed, _, _ := c.ParseFeed([]byte(edited))
	if changed[0].Digest == first.Digest {
		t.Fatal("digest tidak berubah saat deskripsi berubah")
	}
}

func TestParseFeedRejectsBadItemsAndStructure(t *testing.T) {
	c := NewCAPFeed(DefaultCAPBaseURL)
	rss := `<rss><channel>
<item><guid>a</guid><link>https://www.bmkg.go.id/alerts/nowcast/id/CJB1_alert.xml</link></item>
<item><guid></guid><link>https://www.bmkg.go.id/alerts/nowcast/id/CJB2_alert.xml</link></item>
<item><guid>b</guid><link>https://www.bmkg.go.id/alerts/nowcast/id/</link></item>
<item><guid>c</guid><link>%zz</link></item>
<item><guid>a</guid><link>https://www.bmkg.go.id/alerts/nowcast/id/CJB3_alert.xml</link></item>
</channel></rss>`
	items, rej, err := c.ParseFeed([]byte(rss))
	if err != nil || len(items) != 1 || len(rej) != 4 {
		t.Fatalf("items=%v rej=%v err=%v", items, rej, err)
	}
	for _, bad := range []string{"", "<html></html>", "<rss></rss>", "<rss><channel>"} {
		if _, _, err := c.ParseFeed([]byte(bad)); !errors.Is(err, ErrStructure) {
			t.Errorf("ParseFeed(%q) err = %v", bad, err)
		}
	}
}

func parseCAP(t *testing.T, file, lang string) warning.Warning {
	t.Helper()
	c := NewCAPFeed(DefaultCAPBaseURL)
	w, err := c.ParseDetail(ports.FeedItem{File: strings.TrimPrefix(strings.TrimPrefix(file, "cap-id-"), "cap-en-")}, lang, fixture(t, file))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestParseDetailRealCAP(t *testing.T) {
	id := parseCAP(t, "cap-id-CGT20260924002_alert.xml", "id")
	en := parseCAP(t, "cap-en-CGT20260924002_alert.xml", "en")
	w, err := id.WithTranslation(en)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(capFetchedAt); err != nil {
		t.Fatal(err)
	}
	sent := time.Date(2026, 9, 24, 7, 7, 0, 0, time.UTC)
	switch {
	case w.Identifier != "2.49.0.1.360.0.2026.09.24.07.75.002", w.Sender != "cuaca.ekstrem@bmkg.go.id",
		!w.Sent.Equal(sent), w.Status != warning.StatusActual, w.MsgType != warning.MsgAlert,
		w.Severity != warning.SeverityModerate, w.Urgency != warning.UrgencyImmediate, w.Certainty != warning.CertaintyObserved,
		w.EventCode != "OET-194", w.Category != "Met", w.Contact != "06221 196",
		!w.Effective.Equal(sent.Add(20 * time.Minute)), !w.Expires.Equal(sent.Add(140 * time.Minute)), !w.Onset.IsZero(),
		w.Web != "https://nowcasting.bmkg.go.id/infografis/CGT/2026/09/24/infografis.jpg",
		w.SourceURL != "https://www.bmkg.go.id/alerts/nowcast/id/CGT20260924002_alert.xml":
		t.Fatalf("%+v", w)
	}
	if len(w.Texts) != 2 || w.Texts[0].Language != "en" || w.Texts[1].Language != "id" {
		t.Fatalf("teks %+v", w.Texts)
	}
	tid := w.Texts[1]
	if tid.Event != "Hujan Lebat dan Petir" || tid.Headline != "Hujan Lebat disertai Petir di Gorontalo" ||
		tid.SenderName != "Badan Meteorologi Klimatologi dan Geofisika" || strings.Count(tid.Description, "\n") != 3 ||
		!strings.HasPrefix(tid.Description, "Hujan lebat disertai petir akan terjadi pada 24 September 2026, 15:27 WITA") {
		t.Fatalf("teks id %+v", tid)
	}
	if w.Texts[0].Headline != "Thunderstorm This Evening in Gorontalo" {
		t.Fatal(w.Texts[0].Headline)
	}
	if len(w.Areas) != 1 || w.Areas[0].Desc != "Gorontalo" || w.PolygonCount() != 4 || w.PointCount() != 88 {
		t.Fatalf("area %d poligon %d titik", w.PolygonCount(), w.PointCount())
	}
	if p := w.Areas[0].Polygons[0][0]; p.Lat != 0.919 || p.Lon != 122.117 {
		t.Fatalf("titik pertama %+v (CAP menulis lat,lon)", p)
	}
}

func TestParseDetailLargestRealCAP(t *testing.T) {
	w := parseCAP(t, "cap-id-CSU20260924001_alert.xml", "id")
	if err := w.Validate(capFetchedAt); err != nil {
		t.Fatal(err)
	}
	if w.PolygonCount() != 150 || w.PointCount() != 3072 {
		t.Fatalf("%d poligon, %d titik", w.PolygonCount(), w.PointCount())
	}
	ev, err := NewCAPFeed(DefaultCAPBaseURL).Event(w)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ev.Content()
	if err != nil {
		t.Fatal(err)
	}
	// Pesan NATS default dibatasi 1 MiB; peringatan terbesar harus jauh di bawahnya.
	if len(b) > 256<<10 {
		t.Fatalf("pesan %d byte terlalu besar", len(b))
	}
	if ev.Subject() != "raw.weather.bmkg" || ev.Key() != w.Identifier {
		t.Fatal(ev.Subject(), ev.Key())
	}
}

// capJabar adalah dokumen CAP sintetis untuk Jawa Barat (tidak ada di
// rekaman asli), dengan struktur yang sama persis dengan dokumen BMKG.
const capJabar = `<?xml version="1.0" ?>
<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2">
  <identifier>2.49.0.1.360.0.2026.09.24.08.32.002</identifier>
  <sender>cuaca.ekstrem@bmkg.go.id</sender>
  <sent>2026-09-24T14:10:00+07:00</sent>
  <status>Actual</status>
  <msgType>Update</msgType>
  <scope>Public</scope>
  <references>cuaca.ekstrem@bmkg.go.id,2.49.0.1.360.0.2026.09.24.07.32.001,2026-09-24T13:30:00+07:00</references>
  <info>
    <language>id</language>
    <category>Met</category>
    <event>Hujan Lebat dan Petir</event>
    <urgency>Immediate</urgency>
    <severity>Severe</severity>
    <certainty>Likely</certainty>
    <eventCode><valueName>OET:v1.2</valueName><value>OET-194</value></eventCode>
    <onset>2026-09-24T14:25:00+07:00</onset>
    <expires>2026-09-24T17:00:00+07:00</expires>
    <senderName>Badan Meteorologi Klimatologi dan Geofisika</senderName>
    <headline>Hujan Lebat disertai Petir di Jawa Barat</headline>
    <description>Baris satu.
  Baris   dua.</description>
    <instruction>Hindari   pohon.</instruction>
    <web>http://tidak-aman.example/infografis.jpg</web>
    <area>
      <areaDesc>Jawa Barat</areaDesc>
      <polygon>-6.90,107.60 -6.90,107.65 -6.85,107.65 -6.90,107.60</polygon>
    </area>
  </info>
  <info>
    <language>en</language>
    <event>Thunderstorm</event>
    <headline>Thunderstorm in West Java</headline>
  </info>
</alert>`

func TestParseDetailSyntheticUpdate(t *testing.T) {
	c := NewCAPFeed(DefaultCAPBaseURL)
	w, err := c.ParseDetail(ports.FeedItem{File: "CJB20260924002_alert.xml"}, "id", []byte(capJabar))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(capFetchedAt); err != nil {
		t.Fatal(err)
	}
	if w.MsgType != warning.MsgUpdate || len(w.References) != 1 || w.References[0].Identifier != "2.49.0.1.360.0.2026.09.24.07.32.001" ||
		!w.References[0].Sent.Equal(time.Date(2026, 9, 24, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("%+v", w.References)
	}
	// effective kosong = sent; onset dibaca; web non-https dibuang, bukan menolak peringatan.
	if !w.Effective.Equal(w.Sent) || !w.Onset.Equal(time.Date(2026, 9, 24, 7, 25, 0, 0, time.UTC)) || w.Web != "" {
		t.Fatalf("effective=%v onset=%v web=%q", w.Effective, w.Onset, w.Web)
	}
	if w.Severity != warning.SeveritySevere || w.Certainty != warning.CertaintyLikely {
		t.Fatal(w.Severity, w.Certainty)
	}
	// Dua info dalam satu dokumen menjadi dua teks.
	if len(w.Texts) != 2 || w.Texts[1].Description != "Baris satu.\nBaris dua." || w.Texts[1].Instruction != "Hindari pohon." || w.Texts[0].Headline != "Thunderstorm in West Java" {
		t.Fatalf("%+v", w.Texts)
	}
	// Info bahasa yang diminta menjadi info utama: info en di dokumen ini
	// tidak lengkap (tanpa expires), jadi dokumen ditolak bila diminta en.
	if _, err := c.ParseDetail(ports.FeedItem{File: "x.xml"}, "en", []byte(capJabar)); err == nil || !strings.Contains(err.Error(), "expires") {
		t.Fatalf("info utama en seharusnya dipakai dan ditolak: %v", err)
	}
	ev, err := c.Event(w)
	if err != nil {
		t.Fatal(err)
	}
	msg := ev.(*rawpb.WeatherEvent).Warning()
	if msg.GetMsgType() != rawv1.CapMsgType_CAP_MSG_TYPE_UPDATE || msg.GetSeverity() != rawv1.CapSeverity_CAP_SEVERITY_SEVERE ||
		msg.GetOnset() == nil || len(msg.GetReferences()) != 1 || len(msg.GetAreas()[0].GetPolygons()[0].GetPoints()) != 4 {
		t.Fatalf("%v", msg)
	}
}

func TestParseDetailErrors(t *testing.T) {
	c := NewCAPFeed(DefaultCAPBaseURL)
	it := ports.FeedItem{File: "x.xml"}
	for name, doc := range map[string]string{
		"bukan XML":      "{}",
		"akar lain":      `<feed xmlns="urn:oasis:names:tc:emergency:cap:1.2"></feed>`,
		"namespace lain": `<alert xmlns="urn:lain"><info/></alert>`,
		"tanpa info":     `<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"><identifier>x</identifier></alert>`,
		"charset asing":  `<?xml version="1.0" encoding="ISO-8859-1"?><alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"/>`,
	} {
		if _, err := c.ParseDetail(it, "id", []byte(doc)); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: err = %v, ingin ErrStructure", name, err)
		}
	}
	base := capJabar
	for name, doc := range map[string]string{
		"sent rusak":        strings.Replace(base, "2026-09-24T14:10:00+07:00", "kemarin", 1),
		"rujukan rusak":     strings.Replace(base, ",2026-09-24T13:30:00+07:00</references>", "</references>", 1),
		"onset rusak":       strings.Replace(base, "<onset>2026-09-24T14:25:00+07:00", "<onset>nanti", 1),
		"expires rusak":     strings.Replace(base, "<expires>2026-09-24T17:00:00+07:00", "<expires>", 1),
		"effective rusak":   strings.Replace(base, "<onset>", "<effective>besok</effective><onset>", 1),
		"titik rusak":       strings.Replace(base, "-6.90,107.60 -6.90,107.65", "-6.90;107.60 -6.90,107.65", 1),
		"titik bukan angka": strings.Replace(base, "-6.90,107.60 -6.90,107.65", "-6.90,x -6.90,107.65", 1),
	} {
		if _, err := c.ParseDetail(it, "id", []byte(doc)); err == nil {
			t.Errorf("%s: seharusnya gagal", name)
		}
	}
}

// Properti: ParseFeed dan ParseDetail tidak pernah panik; dokumen yang
// terbaca dan lolos Validate selalu bisa dibungkus menjadi event.
func FuzzParseCAPDocuments(f *testing.F) {
	f.Add([]byte(capJabar))
	f.Add([]byte(`<rss><channel><item><guid>a</guid><link>https://x/id/a.xml</link></item></channel></rss>`))
	f.Add([]byte(`<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"><info><area><polygon>1,1 1,2 2,2 1,1</polygon></area></info></alert>`))
	c := NewCAPFeed(DefaultCAPBaseURL)
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _, _ = c.ParseFeed(body)
		w, err := c.ParseDetail(ports.FeedItem{File: "x.xml"}, "id", body)
		if err != nil || w.Validate(capFetchedAt) != nil {
			return
		}
		if _, err := c.Event(w); err != nil {
			t.Fatalf("peringatan valid gagal dibungkus: %v", err)
		}
	})
}
