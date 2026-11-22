package gazetteer

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Nama dan kode asli Kemendagri (cahyadsn/wilayah), dipangkas.
var entries = []Entry{
	{"32", "Jawa Barat"},
	{"32.02", "Kabupaten Sukabumi"},
	{"32.02.01", "Palabuhanratu"},
	{"32.02.37", "Cibadak"},
	{"32.02.37.2006", "Cibadak"},
	{"32.03", "Kabupaten Cianjur"},
	{"32.03.01", "Cianjur"},
	{"32.03.03", "Cibeber"},
	{"36.02", "Kabupaten Lebak"},
	{"36.02.18", "Cibadak"},
	{"36.02.21", "Wanasalam"},
	{"36.02.21.2005", "Muara"},
	{"52.06", "Kabupaten Bima"},
	{"52.72", "Kota Bima"},
	{"53.10", "Kabupaten Manggarai"},
	{"73.01", "Kabupaten Kepulauan Selayar"},
	{"73.01.01", "Benteng"},
	{"73.01.06", "Pasimasunggu"},
	{"11.07.09", "Kota Sigli"},
	{"53.07.01.2021", "Regapu’u"},
}

var aliases = []Alias{
	{Name: "Muara Binuangeun", Code: "36.02.21.2005", Reason: "Desa Muara, pantai Binuangeun"},
	{Name: "Jampea", Code: "73.01.06", Reason: "Pulau Jampea"},
}

func index(t *testing.T) *Index {
	t.Helper()
	ix, err := New(entries, aliases)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func codes(c []Candidate) []string {
	out := make([]string, len(c))
	for i, x := range c {
		out[i] = x.Code
	}
	return out
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Kab. Manggarai":        "kabupaten manggarai",
		"  II-III Kota  BIMA ":  "ii iii kota bima",
		"Jampea Kep. Selayar":   "jampea kepulauan selayar",
		"Regapu’u":              "regapuu",
		"Ma'rang":               "marang",
		"Kec.Cibeber/Kel.Muara": "kecamatan cibeber kelurahan muara",
		"...":                   "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("%q → %q, ingin %q", in, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	ix := index(t)
	if ix.Len() != len(entries) {
		t.Fatal(ix.Len())
	}
	for _, c := range []struct {
		name string
		want []string
		kind Kind
	}{
		{"Kab. Manggarai", []string{"53.10"}, KindExact},
		{"Manggarai", []string{"53.10"}, KindExact},
		{"Kabupaten Bima", []string{"52.06"}, KindExact},
		{"Kota Bima", []string{"52.72"}, KindExact},
		{"Bima", []string{"52.06", "52.72"}, KindExact},
		{"Cibadak", []string{"32.02.37", "32.02.37.2006", "36.02.18"}, KindExact},
		{"Palabuhan Ratu", []string{"32.02.01"}, KindCompact},
		{"Kota Cianjur", []string{"32.03.01"}, KindTownAsSub},
		{"Sigli", []string{"11.07.09"}, KindExact},
		{"Selayar", []string{"73.01"}, KindExact},
		{"Benteng Selayar", []string{"73.01.01"}, KindQualified},
		{"Jampea Kep. Selayar", []string{"73.01.06"}, KindQualified},
		{"Muara Binuangeun", []string{"36.02.21.2005"}, KindAlias},
		{"Regapu'u", []string{"53.07.01.2021"}, KindExact},
	} {
		got := ix.Lookup(c.name)
		if !slices.Equal(codes(got), c.want) || got[0].Kind != c.kind {
			t.Errorf("%q: %v (%v), ingin %v (%v)", c.name, codes(got), got, c.want, c.kind)
		}
	}
	for _, name := range []string{"", "Darajat", "Kab. Cianjur Selatan", "Kota Manggarai Barat", "Benteng Bima"} {
		if got := ix.Lookup(name); len(got) != 0 {
			t.Errorf("%q harus tidak cocok: %v", name, got)
		}
	}
	if e, ok := ix.Entry("32.03.01"); !ok || e.Name != "Cianjur" || e.Level() != LevelDistrict || e.Parent() != "32.03" {
		t.Fatal(e)
	}
	if _, ok := ix.Entry("99"); ok || (Entry{Code: "32"}).Parent() != "" {
		t.Fatal("kode tidak ada")
	}
}

func TestResolveNearest(t *testing.T) {
	ix := index(t)
	dist := map[string]float64{"32.02.37": 40, "36.02.18": 120, "32.02": 0, "32.03.01": 7}
	d := func(code string) (float64, bool) { v, ok := dist[code]; return v, ok }
	m, ok := ix.Resolve(ix.Lookup("Cibadak"), d)
	// Desa 32.02.37.2006 memakai batas kecamatan induknya (40 km), seri dengan
	// kecamatan Cibadak: kode terkecil menang.
	if !ok || m.Code != "32.02.37" || m.DistanceKm != 40 || m.BoundaryCode != "32.02.37" || m.Candidates != 3 {
		t.Fatalf("%+v", m)
	}
	dist["32.02.37"] = 50
	dist["32.02.37.2006"] = 45
	m, _ = ix.Resolve(ix.Lookup("Cibadak"), d)
	if m.Code != "32.02.37.2006" || m.BoundaryCode != "32.02.37.2006" {
		t.Fatalf("batas desa sendiri: %+v", m)
	}
	if _, ok := ix.Resolve(ix.Lookup("Manggarai"), d); ok {
		t.Fatal("tanpa batas harus ok=false")
	}
	if _, ok := ix.Resolve(nil, d); ok {
		t.Fatal("tanpa kandidat")
	}
}

func TestNewRejects(t *testing.T) {
	for name, c := range map[string]struct {
		e []Entry
		a []Alias
	}{
		"kode":         {e: []Entry{{"3", "X"}}},
		"kode huruf":   {e: []Entry{{"3a", "X"}}},
		"segmen":       {e: []Entry{{"32.01.01.2001.1", "X"}}},
		"ganda":        {e: []Entry{{"32", "A"}, {"32", "B"}}},
		"nama":         {e: []Entry{{"32", " - "}}},
		"alias kosong": {e: entries, a: []Alias{{Name: "", Code: "32", Reason: "x"}}},
		"alasan":       {e: entries, a: []Alias{{Name: "A", Code: "32"}}},
		"kode alias":   {e: entries, a: []Alias{{Name: "A", Code: "99", Reason: "x"}}},
		"alias ganda":  {e: entries, a: []Alias{{Name: "A", Code: "32", Reason: "x"}, {Name: "a.", Code: "32", Reason: "y"}}},
	} {
		if _, err := New(c.e, c.a); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Properti: Normalize idempoten dan hanya berisi huruf kecil, angka, spasi
// tunggal; Lookup tidak pernah panik.
func FuzzNormalize(f *testing.F) {
	ix, err := New(entries, aliases)
	if err != nil {
		f.Fatal(err)
	}
	f.Add("Kab. Manggarai")
	f.Add("Jampea Kep. Selayar")
	f.Add("  Kota\tCianjur ")
	f.Fuzz(func(t *testing.T, s string) {
		n := Normalize(s)
		if Normalize(n) != n || strings.Contains(n, "  ") || strings.TrimSpace(n) != n {
			t.Fatalf("%q → %q", s, n)
		}
		_ = ix.Lookup(s)
	})
}
