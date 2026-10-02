package archivebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
)

var d1 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func TestKeys(t *testing.T) {
	if got := BundleKey("bmkg-prakiraan", d1.Add(5*time.Hour), 1); got != "bmkg-prakiraan/2026/10/01.p1.tar.zst" {
		t.Fatal(got)
	}
	if got := IndexKey("usgs-2.5-day", d1, 12, 733); got != "usgs-2.5-day/2026/10/01.p12.n733.idx.json.zst" {
		t.Fatal(got)
	}
	if got := DayPrefix("c", d1); got != "c/2026/10/01." {
		t.Fatal(got)
	}
	// Awalan hari tidak menangkap hari lain yang diawali digit sama.
	if strings.HasPrefix(BundleKey("c", d1.AddDate(0, 0, 9), 1), DayPrefix("c", d1)) {
		t.Fatal("awalan 01. menangkap 10.")
	}
}

func TestParse(t *testing.T) {
	for _, n := range []Name{
		{Kind: KindBundle, Connector: "bmkg-prakiraan", Day: d1, Part: 1},
		{Kind: KindIndex, Connector: "usgs-2.5-day", Day: d1, Part: 3, Objects: 733},
	} {
		got, err := Parse(n.String())
		if err != nil || got != n {
			t.Fatalf("Parse(%s) = %+v, %v", n, got, err)
		}
	}
	for _, bad := range []string{
		"", "c/2026/10/01.p1.tar.gz", "c/2026/10/01.p0.tar.zst", "c/2026/10/01.p01.tar.zst",
		"c/2026/10/01.p1.n3.tar.zst", "c/2026/10/01.p1.idx.json.zst", "c/2026/10/01.p1.n0.idx.json.zst",
		"c/2026/13/01.p1.tar.zst", "c/2026/02/30.p1.tar.zst", "C/2026/10/01.p1.tar.zst",
		"raw/c/2026/10/01.p1.tar.zst", "c/2026/10/01/120000Z-abc.json.gz", "c/26/10/01.p1.tar.zst",
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %v, ingin ErrInvalid", bad, err)
		}
	}
}

func entry(conn string, at time.Time, payload string) Entry {
	sum := sha(payload)
	return Entry{Key: archivekey.Build(conn, "json", sum, at), Bytes: int64(len(payload)), SHA256: sum}
}

func validIndex() (Index, Name) {
	ix := Index{
		Format: Format, Connector: "c", Day: "2026-10-01", Part: 2,
		BundleBytes: 10, BundleSHA256: sha("bundel"),
		Entries: []Entry{
			entry("c", d1.Add(time.Hour), "satu"),
			entry("c", d1.Add(2*time.Hour), "dua"),
		},
	}
	n, err := ix.Name()
	if err != nil {
		panic(err)
	}
	return ix, n
}

func TestValidate(t *testing.T) {
	ix, n := validIndex()
	if n.String() != "c/2026/10/01.p2.n2.idx.json.zst" {
		t.Fatal(n)
	}
	if err := ix.Validate(n); err != nil {
		t.Fatal(err)
	}
	if ix.Entries[0].Member() != strings.TrimSuffix(ix.Entries[0].Key, ".gz") {
		t.Fatal(ix.Entries[0].Member())
	}
	mutations := map[string]func(*Index, *Name){
		"format":           func(ix *Index, _ *Name) { ix.Format = 9 },
		"tanggal rusak":    func(ix *Index, _ *Name) { ix.Day = "1 Okt" },
		"nama beda bagian": func(_ *Index, n *Name) { n.Part = 1 },
		"nama bundel":      func(_ *Index, n *Name) { n.Kind = KindBundle },
		"jumlah objek":     func(ix *Index, _ *Name) { ix.Entries = ix.Entries[:1] },
		"tanpa entri":      func(ix *Index, n *Name) { ix.Entries = nil; n.Objects = 0 },
		"ukuran bundel":    func(ix *Index, _ *Name) { ix.BundleBytes = 0 },
		"sha bundel":       func(ix *Index, _ *Name) { ix.BundleSHA256 = "xyz" },
		"tidak urut":       func(ix *Index, _ *Name) { ix.Entries[0], ix.Entries[1] = ix.Entries[1], ix.Entries[0] },
		"ganda":            func(ix *Index, _ *Name) { ix.Entries[1] = ix.Entries[0] },
		"kunci asing":      func(ix *Index, _ *Name) { ix.Entries[1].Key = "c/x.gz" },
		"konektor lain": func(ix *Index, _ *Name) {
			ix.Entries[1] = entry("d", d1.Add(2*time.Hour), "dua")
		},
		"hari lain": func(ix *Index, _ *Name) { ix.Entries[1] = entry("c", d1.Add(25*time.Hour), "dua") },
		"sha entri": func(ix *Index, _ *Name) { ix.Entries[1].SHA256 = sha("lain") },
		"ukuran negatif": func(ix *Index, _ *Name) {
			ix.Entries[1].Bytes = -1
		},
	}
	for name, mut := range mutations {
		ix, n := validIndex()
		mut(&ix, &n)
		if err := ix.Validate(n); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, ingin ErrInvalid", name, err)
		}
	}
}

// FuzzParse: kunci yang diterima selalu kanonik (String mengembalikan kunci
// yang sama), jadi satu bagian tidak pernah punya dua nama.
func FuzzParse(f *testing.F) {
	f.Add("bmkg-prakiraan/2026/10/01.p1.tar.zst")
	f.Add("usgs-2.5-day/2026/10/01.p2.n733.idx.json.zst")
	f.Add("c/2026/10/01.p1.n01.idx.json.zst")
	f.Fuzz(func(t *testing.T, key string) {
		n, err := Parse(key)
		if err != nil {
			return
		}
		if n.String() != key {
			t.Fatalf("Parse(%q).String() = %q", key, n.String())
		}
		if !strings.HasPrefix(key, DayPrefix(n.Connector, n.Day)) {
			t.Fatalf("%q tidak berawalan %q", key, DayPrefix(n.Connector, n.Day))
		}
	})
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
