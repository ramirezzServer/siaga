package region_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

func TestParseCodeValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		level  region.Level
		kind   region.Kind
		parent string
	}{
		{"32", region.LevelProvinsi, region.KindProvinsi, ""},
		{"32.04", region.LevelKabKota, region.KindKabupaten, "32"},
		{"32.70", region.LevelKabKota, region.KindKabupaten, "32"},
		{"32.71", region.LevelKabKota, region.KindKota, "32"},
		{"32.73", region.LevelKabKota, region.KindKota, "32"},
		{"32.73.02", region.LevelKecamatan, region.KindKecamatan, "32.73"},
		{"32.73.02.1003", region.LevelDesaKelurahan, region.KindKelurahan, "32.73.02"},
		{"32.04.10.2001", region.LevelDesaKelurahan, region.KindDesa, "32.04.10"},
	}
	for _, tc := range cases {
		c, err := region.ParseCode(tc.in)
		if err != nil {
			t.Fatalf("ParseCode(%q): %v", tc.in, err)
		}
		if c.String() != tc.in || c.Level() != tc.level || c.Kind() != tc.kind || c.Province() != "32" {
			t.Errorf("ParseCode(%q) = %s level=%d kind=%s", tc.in, c, c.Level(), c.Kind())
		}
		p, ok := c.Parent()
		if ok != (tc.parent != "") || p.String() != tc.parent {
			t.Errorf("Parent(%q) = %q, %v; want %q", tc.in, p, ok, tc.parent)
		}
	}
}

func TestParseCodeInvalid(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"", "3", "320", "32.", ".32", "32.7", "32.073", "32.73.2", "32.73.02.103",
		"32.73.02.3001", "32.73.02.1003.01", "3a", "32.73.02.10O3", "32 .73", "٣٢",
	} {
		if _, err := region.ParseCode(in); !errors.Is(err, region.ErrInvalidCode) {
			t.Errorf("ParseCode(%q) error = %v; want ErrInvalidCode", in, err)
		}
	}
}

func TestZeroCode(t *testing.T) {
	t.Parallel()
	var c region.Code
	if !c.IsZero() || c.Level() != 0 || c.Kind() != "" || c.Province() != "" {
		t.Errorf("nilai nol berperilaku salah: %+v", c)
	}
	if _, ok := c.Parent(); ok {
		t.Error("nilai nol tidak boleh punya induk")
	}
}

// FuzzParseCode memeriksa properti: setiap kode yang diterima harus bolak-balik
// dengan tepat, level sama dengan jumlah segmen, dan rantai induknya berakhir di provinsi
// dengan level turun satu per langkah.
func FuzzParseCode(f *testing.F) {
	for _, seed := range []string{"32", "32.73", "32.73.02", "32.73.02.1003", "32.04.10.2001", "x", "32..1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := region.ParseCode(s)
		if err != nil {
			return
		}
		if c.String() != s {
			t.Fatalf("round trip %q -> %q", s, c)
		}
		if int(c.Level()) != strings.Count(s, ".")+1 {
			t.Fatalf("level %d tidak cocok dengan %q", c.Level(), s)
		}
		if c.Kind() == "" {
			t.Fatalf("kode valid %q tanpa jenis", s)
		}
		for cur := c; ; {
			p, ok := cur.Parent()
			if !ok {
				if cur.Level() != region.LevelProvinsi {
					t.Fatalf("rantai induk %q berhenti di level %d", s, cur.Level())
				}
				break
			}
			if p.Level() != cur.Level()-1 || !strings.HasPrefix(cur.String(), p.String()+".") {
				t.Fatalf("induk %q dari %q salah", p, cur)
			}
			if _, err := region.ParseCode(p.String()); err != nil {
				t.Fatalf("induk %q dari %q tidak valid: %v", p, cur, err)
			}
			cur = p
		}
	})
}
