package calibration

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

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
	for _, name := range []string{DischargeFile, RainfallFile} {
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
