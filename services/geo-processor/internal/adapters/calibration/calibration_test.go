package calibration

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

func date(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

func TestLoadEmbedded(t *testing.T) {
	c, err := Load(flood.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Discharge) != 38 || len(c.Rainfall) != 7 {
		t.Fatalf("%d titik, %d sub-DAS", len(c.Discharge), len(c.Rainfall))
	}
	if p := c.DischargeSource.Period; !p.From.Equal(date("1997-01-01")) || !p.To.Equal(date("2024-12-31")) {
		t.Fatalf("periode debit %v", p)
	}
	if p := c.RainfallSource.Period; !p.From.Equal(date("2017-01-01")) || !p.To.Equal(date("2024-12-31")) {
		t.Fatalf("periode hujan %v", p)
	}
	corrected := 0
	for id, th := range c.Discharge {
		if th.Correction != 1 {
			corrected++
			if id != "river:citarum-nanjung" || th.Correction != 0.87 {
				t.Errorf("%s dikoreksi %v", id, th.Correction)
			}
		}
	}
	if corrected != 1 {
		t.Fatalf("%d titik dikoreksi", corrected)
	}
	for _, id := range []string{"river:citarum-hilir-jatiluhur", "river:citarum-karawang", "river:citarum-muara"} {
		if c.Discharge[id].MaxLevel != hazard.LevelSiaga {
			t.Errorf("%s: tingkat tertinggi %d", id, c.Discharge[id].MaxLevel)
		}
	}
	if d := c.Discharge["river:citarum-dayeuhkolot"]; d.River != "Citarum" || d.Name != "Dayeuhkolot" || d.Climatology[2] != 190.20 || d.MaxLevel != hazard.LevelBahaya {
		t.Fatalf("%+v", d)
	}
	if r := c.Rainfall["catchment:cikapundung"]; r.Name != "Cikapundung" || r.Windows[2][2] != 48.69 || r.MaxLevel != hazard.LevelSiaga {
		t.Fatalf("%+v", r)
	}
}

// TestEmbeddedMatchesCalibration memastikan salinan sama persis dengan
// keluaran alat kalibrasi di docs/calibration.
func TestEmbeddedMatchesCalibration(t *testing.T) {
	docs := os.DirFS("../../../../../docs/calibration")
	for _, name := range []string{DischargeFile, RainfallFile, StaticFile} {
		src, err := fs.ReadFile(docs, name)
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := data.ReadFile("data/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(src, embedded) {
			t.Errorf("data/%s berbeda dari docs/calibration/%s; jalankan make calibration-copy", name, name)
		}
	}
}

const (
	goodDischarge = "# Debit harian, 1997-01-01..2024-12-31 (10227 hari).\n# 1 titik\n" + dischargeHeader + "\n" +
		"citarum-nanjung,Citarum,Nanjung,-6.9750,107.5250,95.83,215.77,271.90,378.73,479.70,534.19,10227,0.87\n"
	goodRain = "# Hujan, 2017-01-01..2024-12-31.\n# 3 baris\n" + rainfallHeader + "\n" +
		"ciwidey,Ciwidey,3,1.60,5.37,8.04,16.96,27.84,2922\n" +
		"ciwidey,Ciwidey,6,2.44,7.75,11.39,24.23,38.74,2922\n" +
		"ciwidey,Ciwidey,24,5.04,14.80,21.83,47.23,68.52,2922\n"
)

func TestParseErrors(t *testing.T) {
	p := flood.DefaultPolicy()
	if c, err := Parse(p, []byte(goodDischarge), []byte(goodRain)); err != nil || c.Discharge["river:citarum-nanjung"].Correction != 0.87 {
		t.Fatalf("%+v %v", c, err)
	}
	for name, tc := range map[string][2]string{
		"header debit":   {strings.Replace(goodDischarge, "rasio_p98_seamless", "rasio", 1), goodRain},
		"jumlah debit":   {strings.Replace(goodDischarge, "# 1 titik", "# 2 titik", 1), goodRain},
		"tanpa periode":  {strings.Replace(goodDischarge, "1997-01-01..2024-12-31", "", 1), goodRain},
		"periode rusak":  {strings.Replace(goodDischarge, "1997-01-01", "1997-13-01", 1), goodRain},
		"angka debit":    {strings.Replace(goodDischarge, "95.83", "x", 1), goodRain},
		"rasio":          {strings.Replace(goodDischarge, ",0.87", ",y", 1), goodRain},
		"ambang debit":   {strings.Replace(goodDischarge, "271.90", "200", 1), goodRain},
		"titik ganda":    {strings.Replace(goodDischarge, "# 1 titik", "# 2 titik", 1) + strings.SplitN(goodDischarge, "\n", 4)[3], goodRain},
		"kolom":          {strings.Replace(goodDischarge, ",0.87", "", 1), goodRain},
		"jam hujan":      {goodDischarge, strings.Replace(goodRain, ",24,", ",x,", 1)},
		"angka hujan":    {goodDischarge, strings.Replace(goodRain, "68.52", "z", 1)},
		"jendela kurang": {goodDischarge, strings.Replace(goodRain, "# 3 baris", "# 2 baris", 1)[:strings.LastIndex(goodRain, "ciwidey,Ciwidey,24")-1]},
		"header hujan":   {goodDischarge, strings.Replace(goodRain, "hari_berisi", "hari", 1)},
	} {
		if _, err := Parse(p, []byte(tc[0]), []byte(tc[1])); !errors.Is(err, flood.ErrInvalid) {
			t.Errorf("%s harus ditolak dengan ErrInvalid: %v", name, err)
		}
	}
	bad := p
	bad.Horizon = -1
	if _, err := Parse(bad, []byte(goodDischarge), []byte(goodRain)); !errors.Is(err, flood.ErrInvalid) {
		t.Error("aturan tidak valid harus ditolak")
	}
}

func TestLoadFire(t *testing.T) {
	c, err := LoadFire()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Static) < 100 || !c.Period.From.Equal(date("2012-01-10")) || c.Period.To.Before(date("2026-10-01")) {
		t.Fatalf("%d sumber, periode %v", len(c.Static), c.Period)
	}
	// Kilang Balongan dan Indocement Citeureup tersaring; puncak Ciremai tidak.
	for _, p := range [][2]float64{{-6.378, 108.398}, {-6.479, 106.898}} {
		if _, ok := c.Static.Match(p[0], p[1]); !ok {
			t.Errorf("%v tidak tersaring", p)
		}
	}
	if s, ok := c.Static.Match(-6.892, 108.400); ok {
		t.Errorf("puncak Ciremai tersaring oleh %s", s.ID)
	}
}

func TestParseFireErrors(t *testing.T) {
	head := "# Arsip FIRMS 2012-01-10..2026-10-10 (UTC)\n# 1 sumber\n" + staticHeader + "\n"
	row := "\"-6.37815,108.39848\",-6.37815,108.39848,2.43,keduanya,18,2572,1387,15,12,182,0,2369,17,1922,1200,3.4,2012-01-21,2026-10-02\n"
	if c, err := ParseFire([]byte(head + row)); err != nil || len(c.Static) != 1 || c.Static[0].RadiusKm != 2.43 {
		t.Fatalf("%+v %v", c, err)
	}
	for name, s := range map[string]string{
		"header":  "# Arsip FIRMS 2012-01-10..2026-10-10 (UTC)\n# 0 sumber\nid,lintang\n",
		"jumlah":  strings.Replace(head, "# 1 sumber", "# 2 sumber", 1) + row,
		"periode": strings.Replace(head, "# Arsip FIRMS 2012-01-10..2026-10-10 (UTC)\n", "", 1) + row,
		"tanggal": strings.Replace(head, "2012-01-10", "2012-13-10", 1) + row,
		"angka":   head + strings.Replace(row, "2.43", "x", 1),
		"id":      head + strings.Replace(row, "\"-6.37815", "\"-6.37816", 1),
		"alasan":  head + strings.Replace(row, "keduanya", "lain", 1),
		"ganda":   strings.Replace(head, "# 1 sumber", "# 2 sumber", 1) + row + row,
		"kutip":   head + "\"x\n",
	} {
		if _, err := ParseFire([]byte(s)); !errors.Is(err, fire.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
