package capfeed_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/capfeed"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/replay"
)

// testdata/arsip adalah potongan arsip asli 2026-10-01 (sampel-arsip-2026-10-01):
// RSS 11.46.57 dan 11.48.55 UTC, dua peringatan Jawa Barat beserta
// terjemahannya; RSS, dokumen id, dan dokumen en 12.04.08 berbagi detik dan
// urut kunci arsip menempatkan dokumen id sebelum RSS-nya.
func TestReplayRealArchive(t *testing.T) {
	archive, err := fsarchive.New("testdata/arsip")
	if err != nil {
		t.Fatal(err)
	}
	pub := &recPub{}
	feed := capfeed.NewReplay(bmkg.NewCAPFeed(bmkg.DefaultCAPBaseURL), capfeed.ReplayOptions{Languages: []string{"en"}})
	rep, err := replay.New(archive, pub, &clock{}).Run(t.Context(), []replay.Feed{feed}, replay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := rep.Feeds[0]
	if f.Payloads != 7 || f.Events != 2 || f.Published != 2 || f.Rejected+f.Corrupt+f.ParseErrors != 0 {
		t.Fatalf("laporan %+v", f)
	}
	want := []struct{ id, key, fetched string }{
		{"2.49.0.1.360.0.2026.10.01.11.32.005", "bmkg-cap/2026/10/01/114657Z-d69840876a13.xml.gz", "2026-10-01T11:46:57Z"},
		{"2.49.0.1.360.0.2026.10.01.12.32.005", "bmkg-cap/2026/10/01/120408Z-8f8a973b1401.xml.gz", "2026-10-01T12:04:08Z"},
	}
	for i, m := range pub.msgs {
		var ww rawv1.WeatherWarning
		if err := proto.Unmarshal(m.Data, &ww); err != nil {
			t.Fatal(err)
		}
		w := want[i]
		var langs []string
		for _, tx := range ww.GetTexts() {
			langs = append(langs, tx.GetLanguage())
		}
		meta := ww.GetMeta()
		if ww.GetIdentifier() != w.id || meta.GetArchiveKey() != w.key || meta.GetConnector() != "bmkg-cap" ||
			m.FetchedAt.Format(time.RFC3339) != w.fetched || !slices.Equal(langs, []string{"en", "id"}) {
			t.Errorf("pesan %d: %s %s %s %s %v", i, ww.GetIdentifier(), meta.GetConnector(), meta.GetArchiveKey(), m.FetchedAt, langs)
		}
		// BMKG menimpa file yang sama untuk pesan baru (identifier lain),
		// jadi pemasangan memakai identifier = guid, bukan nama file.
		if ww.GetSourceUrl() != "https://www.bmkg.go.id/alerts/nowcast/id/CJB20261001005_alert.xml" {
			t.Errorf("pesan %d: source_url %q", i, ww.GetSourceUrl())
		}
	}
}

// put mengarsipkan body di bawah bmkg-cap pada waktu at.
func put(t *testing.T, a *memArchive, at time.Time, body string) string {
	t.Helper()
	key, err := emit.Archive(t.Context(), a, "bmkg-cap", "xml", emit.Sum([]byte(body)), []byte(body), at)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestReplayMissingContext(t *testing.T) {
	a := &alert{id: "2.49.0.1.360.0.2026.10.01.11.32.001", file: "CJB1_alert.xml", listed: true}
	w := &world{alerts: []*alert{a}}
	archive := &memArchive{objs: map[string][]byte{}}
	// Dokumen sebelum RSS apa pun (replay -from di tengah hari), terjemahan
	// tanpa dokumen utama, lalu pasangan lengkap setelah RSS.
	put(t, archive, base, a.doc("id"))
	put(t, archive, base.Add(time.Minute), a.doc("en"))
	put(t, archive, base.Add(2*time.Minute), w.rss())
	put(t, archive, base.Add(2*time.Minute+time.Second), a.doc("id"))
	put(t, archive, base.Add(2*time.Minute+2*time.Second), a.doc("en"))
	put(t, archive, base.Add(3*time.Minute), "bukan xml")
	put(t, archive, base.Add(4*time.Minute), `<rss><channel><item><guid></guid></item></channel></rss>`)
	put(t, archive, base.Add(5*time.Minute), `<rss/>`)
	got, pub := runReplay(t, archive, &clock{}, replay.Options{})
	if got.Published != 1 || got.Rejected != 3 || got.ParseErrors != 2 {
		t.Fatalf("laporan %+v", got)
	}
	noContext := 0
	for _, s := range got.Samples {
		if strings.Contains(s, capfeed.ErrNoContext.Error()) {
			noContext++
		}
	}
	if noContext != 2 || len(pub.msgs) != 1 {
		t.Fatalf("contoh %q, pesan %d", got.Samples, len(pub.msgs))
	}
}

func TestReplayLanguages(t *testing.T) {
	a := &alert{id: "2.49.0.1.360.0.2026.10.01.11.32.001", file: "CJB1_alert.xml", listed: true}
	w := &world{alerts: []*alert{a}}
	archive := &memArchive{objs: map[string][]byte{}}
	put(t, archive, base, w.rss())
	put(t, archive, base.Add(time.Second), a.doc("id"))
	put(t, archive, base.Add(2*time.Second), strings.Replace(a.doc("en"), "<language>en</language>", "<language>EN-us</language>", 1))
	src := bmkg.NewCAPFeed(bmkg.DefaultCAPBaseURL)
	run := func(langs []string) *recPub {
		pub := &recPub{}
		feed := capfeed.NewReplay(src, capfeed.ReplayOptions{Languages: langs, Window: time.Hour})
		if _, err := replay.New(archive, pub, &clock{}).Run(t.Context(), []replay.Feed{feed}, replay.Options{}); err != nil {
			t.Fatal(err)
		}
		return pub
	}
	texts := func(pub *recPub) []string {
		var out []string
		for _, m := range pub.msgs {
			var ww rawv1.WeatherWarning
			if err := proto.Unmarshal(m.Data, &ww); err != nil {
				t.Fatal(err)
			}
			var ls []string
			for _, tx := range ww.GetTexts() {
				ls = append(ls, tx.GetLanguage())
			}
			out = append(out, strings.Join(ls, "+"))
		}
		return out
	}
	// Bahasa dinormalisasi; dokumen en-US terpasang sebagai terjemahan "en"
	// (bahasa teksnya tetap dari dokumen).
	if got := texts(run([]string{" EN ", "en", "id", ""})); !slices.Equal(got, []string{"en-us+id"}) {
		t.Errorf("en: %q", got)
	}
	// Tanpa bahasa tambahan: dokumen en dianggap dokumen utama baru (sama
	// dengan permintaan id saat polling) dan ditolak karena tanpa teks id;
	// pesan id terbit tanpa menunggu.
	if got := texts(run(nil)); !slices.Equal(got, []string{"id"}) {
		t.Errorf("tanpa bahasa: %q", got)
	}
	// Bahasa yang tidak pernah datang: menunggu sampai akhir replay.
	if got := texts(run([]string{"en", "jv"})); !slices.Equal(got, []string{"en-us+id"}) {
		t.Errorf("en, jv: %q", got)
	}
}

func TestReplayFeedInterface(t *testing.T) {
	feed := capfeed.NewReplay(bmkg.NewCAPFeed(bmkg.DefaultCAPBaseURL), capfeed.ReplayOptions{})
	if feed.Name() != "bmkg-cap" {
		t.Fatal(feed.Name())
	}
	if _, _, err := feed.Parse(nil, base); err == nil {
		t.Fatal("Parse seharusnya galat")
	}
	var _ replay.DeferredFeed = feed
}
