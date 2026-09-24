package regionlist

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLoadJawaBarat(t *testing.T) {
	if !slices.Contains(Provinces(), "32") {
		t.Fatalf("provinsi tersedia %v", Provinces())
	}
	codes, err := Load("32")
	if err != nil {
		t.Fatal(err)
	}
	// 5.311 desa + 646 kelurahan menurut data batas wilayah yang sama dengan ref.region.
	if len(codes) != 5957 || codes[0] != "32.01.01.1001" || !slices.IsSorted(codes) {
		t.Fatalf("%d kode, pertama %s", len(codes), codes[0])
	}
	for _, c := range []string{"32.73.01.1001", "32.04.05.2001", "32.18.01.2001"} {
		if !slices.Contains(codes, c) {
			t.Errorf("%s tidak ada di daftar", c)
		}
	}
	if _, err := Load("99"); !errors.Is(err, ErrUnknownProvince) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsBadLists(t *testing.T) {
	ok := "# 2 kode\n32.73.01.1001\n\n# komentar\n32.73.01.1002\n"
	if codes, err := Parse([]byte(ok), "32"); err != nil || len(codes) != 2 {
		t.Fatal(codes, err)
	}
	for name, body := range map[string]string{
		"bukan adm4":            "# 1 kode\n32.73.01\n",
		"provinsi lain":         "# 1 kode\n33.73.01.1001\n",
		"ganda":                 "# 2 kode\n32.73.01.1001\n32.73.01.1001\n",
		"jumlah beda":           "# 3 kode\n32.73.01.1001\n",
		"tanpa header":          "32.73.01.1001\n",
		"baris terlalu panjang": "# 1 kode\n" + strings.Repeat("9", 70_000) + "\n",
	} {
		if _, err := Parse([]byte(body), "32"); err == nil {
			t.Errorf("%s: seharusnya ditolak", name)
		}
	}
}
