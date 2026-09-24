package sitelist

import (
	"errors"
	"strings"
	"testing"
)

func TestEmbeddedLists(t *testing.T) {
	grid, err := Grid("32")
	if err != nil {
		t.Fatal(err)
	}
	if len(grid) != 70 || grid[0].ID != "grid:-7.75:107.75" {
		t.Fatalf("%d simpul, pertama %v", len(grid), grid[0])
	}
	rivers, err := Rivers("32")
	if err != nil {
		t.Fatal(err)
	}
	if len(rivers) != 38 || rivers[1].ID != "river:citarum-dayeuhkolot" || rivers[1].River != "Citarum" || rivers[1].Name != "Dayeuhkolot" {
		t.Fatalf("%d titik, %+v", len(rivers), rivers[1])
	}
	for _, p := range []string{"99", "3"} {
		if _, err := Grid(p); !errors.Is(err, ErrUnknownProvince) {
			t.Error(err)
		}
		if _, err := Rivers(p); !errors.Is(err, ErrUnknownProvince) {
			t.Error(err)
		}
	}
}

func TestParseGridErrors(t *testing.T) {
	ok := "# 2 titik\n-7.00,107.50\n\n# komentar\n-6.75,107.25\n"
	if g, err := ParseGrid([]byte(ok)); err != nil || len(g) != 2 {
		t.Fatalf("%v %v", g, err)
	}
	for name, body := range map[string]string{
		"tanpa header":   "-7.00,107.50\n",
		"jumlah salah":   "# 3 titik\n-7.00,107.50\n",
		"bukan koma":     "# 1 titik\n-7.00 107.50\n",
		"bukan angka":    "# 1 titik\n-7.00,abc\n",
		"bukan simpul":   "# 1 titik\n-7.10,107.50\n",
		"luar Indonesia": "# 1 titik\n52.50,13.25\n",
		"ganda":          "# 2 titik\n-7.00,107.50\n-7.0,107.5\n",
	} {
		if _, err := ParseGrid([]byte(body)); err == nil {
			t.Errorf("%s: seharusnya gagal", name)
		}
	}
}

func TestParseRiversErrors(t *testing.T) {
	head := "# 1 titik\nid,sungai,nama,lat,lon\n"
	if r, err := ParseRivers([]byte(head + "citarum-a,Citarum,A,-6.975,107.625\n")); err != nil || len(r) != 1 || r[0].ID != "river:citarum-a" {
		t.Fatalf("%v %v", r, err)
	}
	for name, body := range map[string]string{
		"header kolom": "# 1 titik\nid,river,name,lat,lon\ncitarum-a,Citarum,A,-6.975,107.625\n",
		"slug":         head + "Citarum A,Citarum,A,-6.975,107.625\n",
		"koordinat":    head + "citarum-a,Citarum,A,x,107.625\n",
		"nama kosong":  head + "citarum-a,Citarum, ,-6.975,107.625\n",
		"kolom kurang": head + "citarum-a,Citarum,A,-6.975\n",
		"jumlah":       strings.Replace(head, "1 titik", "2 titik", 1) + "citarum-a,Citarum,A,-6.975,107.625\n",
		"ganda":        strings.Replace(head, "1 titik", "2 titik", 1) + "citarum-a,Citarum,A,-6.975,107.625\ncitarum-a,Citarum,B,-6.975,107.625\n",
		"tanpa header": "citarum-a,Citarum,A,-6.975,107.625\n",
		"kosong":       "# 0 titik\n",
	} {
		if _, err := ParseRivers([]byte(body)); err == nil {
			t.Errorf("%s: seharusnya gagal", name)
		}
	}
}
