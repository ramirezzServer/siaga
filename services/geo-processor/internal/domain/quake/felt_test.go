package quake

import (
	"slices"
	"strings"
	"testing"
)

func TestParseFelt(t *testing.T) {
	// Isi asli kolom "Dirasakan" dari arsip gempadirasakan (Sep–Nov 2026).
	got, bad := ParseFelt("III-IV Manggarai, II - III Kota Bima,II Kab. Lombok Barat , IV Kab. Nagekeo, I-II Kab. Nagekeo, XII Uji, V  Cianjur")
	want := []FeltMention{
		{3, 4, "Manggarai"},
		{2, 3, "Kota Bima"},
		{2, 2, "Kab. Lombok Barat"},
		{4, 4, "Kab. Nagekeo"},
		{1, 2, "Kab. Nagekeo"},
		{12, 12, "Uji"},
		{5, 5, "Cianjur"},
	}
	if !slices.Equal(got, want) || len(bad) != 0 {
		t.Fatalf("%+v %v", got, bad)
	}
	if got[0].MMI() != 3 || got[4].MMI() != 1 {
		t.Fatal("MMI harus batas bawah rentang")
	}
	got, bad = ParseFelt("Indramayu, II-I Bandung, III, XIII Cirebon, IIIBandung, II - Garut, VI-VII 123, , II Sumedang")
	if len(got) != 1 || got[0].Name != "Sumedang" {
		t.Fatalf("%+v", got)
	}
	if !slices.Equal(bad, []string{"Indramayu", "II-I Bandung", "III", "XIII Cirebon", "IIIBandung", "II - Garut", "VI-VII 123"}) {
		t.Fatalf("tidak terbaca %q", bad)
	}
	if got, bad := ParseFelt(""); got != nil || bad != nil {
		t.Fatal("kosong")
	}
}

// Properti: ParseFelt tidak pernah panik, setiap penyebutan sah, dan setiap
// bagian non-kosong muncul sebagai penyebutan atau sebagai bagian tak terbaca.
func FuzzParseFelt(f *testing.F) {
	f.Add("III-IV Manggarai, II - III Kota Bima")
	f.Add("I-II Kab. Nagekeo,,IV")
	f.Fuzz(func(t *testing.T, s string) {
		got, bad := ParseFelt(s)
		parts := 0
		for p := range strings.SplitSeq(s, ",") {
			if strings.TrimSpace(p) != "" && len(strings.Fields(p)) > 0 {
				parts++
			}
		}
		if len(got)+len(bad) != parts {
			t.Fatalf("%q: %d penyebutan + %d tak terbaca, %d bagian", s, len(got), len(bad), parts)
		}
		for _, m := range got {
			if m.MMIMin < 1 || m.MMIMax > MaxMMI || m.MMIMin > m.MMIMax || strings.TrimSpace(m.Name) != m.Name || m.Name == "" {
				t.Fatalf("penyebutan tidak sah %+v", m)
			}
		}
	})
}
