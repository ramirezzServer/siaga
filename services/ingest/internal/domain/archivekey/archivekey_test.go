package archivekey

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildParseRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 24, 7, 20, 5, 900, time.FixedZone("WIB", 7*3600))
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key := Build("usgs-2.5-day", "json", sum, at)
	if key != "usgs-2.5-day/2026/09/24/002005Z-0123456789ab.json.gz" {
		t.Fatal(key)
	}
	k, err := Parse(key)
	if err != nil {
		t.Fatal(err)
	}
	want := Key{Connector: "usgs-2.5-day", FetchedAt: at.UTC().Truncate(time.Second), Sum: "0123456789ab", Ext: "json"}
	if k != want || k.String() != key {
		t.Fatalf("%+v", k)
	}
	if !k.Matches(sum) || k.Matches("ff"+sum) || (Key{}).Matches(sum) {
		t.Fatal("Matches")
	}
	// Potongan pendek (hash pendek di test) tetap bisa dibaca.
	if k, err := Parse(Build("c", "x", "ab", at)); err != nil || k.Sum != "ab" {
		t.Fatal(k, err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, key := range []string{
		"",
		"bmkg/2026/09/24/002005Z-0123456789ab.json",       // tanpa .gz
		"bmkg/2026/09/24/002005-0123456789ab.json.gz",     // tanpa Z
		"bmkg/2026/09/24/0020051Z-0123456789ab.json.gz",   // jam 7 digit
		"bmkg/2026/13/24/002005Z-0123456789ab.json.gz",    // bulan 13
		"bmkg/2026/9/24/002005Z-0123456789ab.json.gz",     // bulan tanpa nol
		"bmkg/2026/09/24/002005Z-0123456789abc.json.gz",   // hash 13 digit
		"bmkg/2026/09/24/002005Z-XYZ.json.gz",             // bukan heksadesimal
		"bmkg/2026/09/24/002005Z-.json.gz",                // hash kosong
		"bmkg/2026/09/24/002005Z-0123456789ab.gz",         // tanpa ekstensi
		"bmkg/2026/09/24/002005Z-0123456789ab.JSON.gz",    // ekstensi kapital
		"BMKG/2026/09/24/002005Z-0123456789ab.json.gz",    // konektor kapital
		"a/b/2026/09/24/002005Z-0123456789ab.json.gz",     // enam bagian
		"bmkg/2026/09/24/246000Z-0123456789ab.json.gz",    // jam 24
		"bmkg/2026/09/24/002005Z-0123456789ab.tar.gz.gz",  // ekstensi bertitik
		"bmkg/+026/09/24/002005Z-0123456789ab.json.gz",    // tahun bertanda
		"../2026/09/24/002005Z-0123456789ab.json.gz",      // konektor ..
		"-bmkg/2026/09/24/002005Z-0123456789ab.json.gz",   // awal tanda hubung
		"bmkg/2026/09/24/002005Z-0123456789ab.json.gz/x",  // bagian lebih
		"bmkg//2026/09/24/002005Z-0123456789ab.json.gz",   // bagian kosong
		"bmkg/2026/02/30/002005Z-0123456789ab.json.gz",    // 30 Februari
		"bmkg/2026/09/24/002005Z-0123456789ab.js on.gz",   // spasi
		"bmkg/2026/09/24/002005Z-0123456789ab..json.gz",   // ekstensi kosong
		"bmkg/2026/09/24/002005Z-0123456789ab.json.gz.gz", // .gz ganda
	} {
		if _, err := Parse(key); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %v, ingin ErrInvalid", key, err)
		}
	}
}

func TestBounds(t *testing.T) {
	day := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if p := DayPrefix("c", day.In(time.FixedZone("WIB", 7*3600))); p != "c/2026/09/24/" {
		t.Fatal(p)
	}
	lo := Lower("c", day)
	for _, tc := range []struct {
		at    time.Time
		after bool
	}{
		{day.Add(-time.Second), false},
		{day, true},
		{day.Add(time.Second), true},
		{day.Add(24 * time.Hour), true},
		{day.Add(-24 * time.Hour), false},
	} {
		for _, sum := range []string{"000000000000", "ffffffffffff", "0"} {
			key := Build("c", "json", sum, tc.at)
			if (key > lo) != tc.after {
				t.Errorf("%s > %s = %v, ingin %v", key, lo, key > lo, tc.after)
			}
		}
	}
	// Kunci konektor lain yang namanya berawalan sama tidak ikut.
	if k := Build("c-latest", "json", "0", day); strings.HasPrefix(k, DayPrefix("c", day)) {
		t.Fatal(k)
	}
}

// FuzzParse: setiap kunci yang diterima Parse adalah bentuk kanonik, dan
// setiap kunci dari Build bisa dibaca kembali dengan isi yang sama.
func FuzzParse(f *testing.F) {
	f.Add("bmkg-autogempa/2026/09/24/002005Z-0123456789ab.json.gz", "bmkg-autogempa", "json", "0123456789ab", int64(1790000000))
	f.Add("x", "firms-viirs-snpp-nrt", "csv", "ff", int64(0))
	f.Fuzz(func(t *testing.T, key, conn, ext, sum string, unix int64) {
		if k, err := Parse(key); err == nil && k.String() != key {
			t.Fatalf("Parse(%q) menerima bentuk non-kanonik %q", key, k.String())
		}
		at := time.Unix(unix%(1<<34), 0).UTC()
		if at.Year() < 1 || at.Year() > 9999 {
			return
		}
		if !connectorRe.MatchString(conn) || !extRe.MatchString(ext) || !sumRe.MatchString(sum) {
			return
		}
		built := Build(conn, ext, sum, at)
		k, err := Parse(built)
		if err != nil {
			t.Fatalf("Parse(Build) = %v untuk %q", err, built)
		}
		if k.Connector != conn || k.Ext != ext || k.Sum != sum[:min(SumLen, len(sum))] || !k.FetchedAt.Equal(at) {
			t.Fatalf("%+v dari %q", k, built)
		}
	})
}
