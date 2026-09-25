package firms

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
)

const key = "KUNCIUJIKUNCIUJIKUNCIUJIKUNCIUJI"

var fetched = time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)

func connector(t *testing.T, p fire.Product) *Connector {
	t.Helper()
	cs, err := NewConnectors(DefaultBaseURL+"/", key, area.JawaBarat)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.product == p {
			return c
		}
	}
	t.Fatalf("produk %s tidak ada", p)
	return nil
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRequestHidesKey(t *testing.T) {
	c := connector(t, fire.VIIRSNOAA20)
	req := c.Request()
	if req.URL != DefaultBaseURL+"/"+key+"/VIIRS_NOAA20_NRT/106.3,-7.9,108.9,-5.7/2" {
		t.Fatalf("URL %s", req.URL)
	}
	if strings.Contains(req.Redacted(), key) || !strings.Contains(req.Redacted(), "/***/") {
		t.Fatalf("Redacted %s", req.Redacted())
	}
	if c.Name() != "firms-viirs-noaa20-nrt" || c.ArchiveExt() != "csv" {
		t.Fatal(c.Name())
	}
	for _, bad := range []string{"", "pendek", key + "!"} {
		if _, err := NewConnectors(DefaultBaseURL, bad, area.JawaBarat); err == nil {
			t.Errorf("key %q diterima", bad)
		}
	}
	if _, err := NewConnectors(DefaultBaseURL, key, area.Box{West: 1, South: 2, East: 3, North: 4}); err == nil {
		t.Error("kotak di luar Indonesia diterima")
	}
}

func TestParseVIIRS(t *testing.T) {
	events, rejected, err := connector(t, fire.VIIRSSNPP).Parse(read(t, "viirs-sintetis.csv"), fetched)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || len(rejected) != 2 {
		t.Fatalf("%d event, penolakan %v", len(events), rejected)
	}
	first := events[0].(*rawpb.ModelEvent).Message().(*rawv1.FireDetection)
	if first.GetId() != "VIIRS_SNPP_NRT:20260923T0612:-6.83712:107.44157" || first.GetConfidence() != rawv1.FireConfidence_FIRE_CONFIDENCE_NOMINAL ||
		!first.GetDaytime() || first.ConfidencePct != nil || first.GetBackgroundBrightnessK() != 297.12 || first.GetFrpMw() != 5.83 ||
		events[0].Subject() != "raw.fire.firms" || events[0].Key() != first.GetId() {
		t.Fatalf("%v", first)
	}
	last := events[2].(*rawpb.ModelEvent).Message().(*rawv1.FireDetection)
	if last.BackgroundBrightnessK != nil || last.FrpMw != nil || last.GetConfidence() != rawv1.FireConfidence_FIRE_CONFIDENCE_LOW {
		t.Fatalf("nilai kosong harus tetap kosong: %v", last)
	}
}

func TestParseMODIS(t *testing.T) {
	events, rejected, err := connector(t, fire.MODISProduct).Parse(read(t, "modis-sintetis.csv"), fetched)
	if err != nil || len(events) != 1 || len(rejected) != 1 {
		t.Fatalf("%d event, %v, %v", len(events), rejected, err)
	}
	d := events[0].(*rawpb.ModelEvent).Message().(*rawv1.FireDetection)
	if d.GetConfidencePct() != 72 || d.GetConfidence() != rawv1.FireConfidence_FIRE_CONFIDENCE_NOMINAL || d.GetInstrument() != "MODIS" ||
		d.GetDetectedAt().AsTime() != time.Date(2026, 9, 23, 3, 5, 0, 0, time.UTC) {
		t.Fatalf("%v", d)
	}
}

func TestParseStructure(t *testing.T) {
	c := connector(t, fire.VIIRSSNPP)
	if ev, rej, err := c.Parse(read(t, "kosong.csv"), fetched); err != nil || len(ev) != 0 || len(rej) != 0 {
		t.Fatalf("kosong: %d %v %v", len(ev), rej, err)
	}
	for name, body := range map[string][]byte{
		"key salah":    read(t, "key-salah.txt"),
		"kosong":       nil,
		"header MODIS": read(t, "modis-sintetis.csv"),
		"kutip rusak":  []byte("\"latitude,longitude\n"),
	} {
		if _, _, err := c.Parse(body, fetched); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Baris pendek dan tanggal rusak ditolak sendiri-sendiri.
	body := string(read(t, "kosong.csv")) + "-6.1,107.1\n-6.1,107.1,330,0.4,0.4,2026-13-01,0612,N,VIIRS,n,2.0NRT,290,1,D\n"
	if ev, rej, err := c.Parse([]byte(body), fetched); err != nil || len(ev) != 0 || len(rej) != 2 {
		t.Fatalf("%d %v %v", len(ev), rej, err)
	}
}

func TestAcquired(t *testing.T) {
	for in, want := range map[[2]string]time.Time{
		{"2026-09-23", "5"}:    time.Date(2026, 9, 23, 0, 5, 0, 0, time.UTC),
		{"2026-09-23", "0536"}: time.Date(2026, 9, 23, 5, 36, 0, 0, time.UTC),
		{"2026-09-23", "2359"}: time.Date(2026, 9, 23, 23, 59, 0, 0, time.UTC),
	} {
		if got, err := acquired(in[0], in[1]); err != nil || !got.Equal(want) {
			t.Errorf("%v: %v %v", in, got, err)
		}
	}
	for _, bad := range [][2]string{{"2026-09-23", ""}, {"2026-09-23", "12345"}, {"2026-09-23", "2460"}, {"23/09/2026", "0100"}} {
		if _, err := acquired(bad[0], bad[1]); err == nil {
			t.Errorf("%v diterima", bad)
		}
	}
}

// Parse tidak pernah panik, dan setiap event yang keluar valid serta unik.
func FuzzParseFIRMS(f *testing.F) {
	for _, name := range []string{"viirs-sintetis.csv", "modis-sintetis.csv", "kosong.csv", "key-salah.txt", "jabar-VIIRS_NOAA20_NRT-2026-09-24.csv", "jabar-MODIS_NRT-2026-09-24.csv"} {
		b, err := fs.ReadFile(os.DirFS("testdata"), name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, strings.Contains(name, "modis") || strings.Contains(name, "MODIS"))
	}
	f.Fuzz(func(t *testing.T, body []byte, modis bool) {
		p := fire.VIIRSSNPP
		if modis {
			p = fire.MODISProduct
		}
		c := &Connector{Parser: Parser{product: p}, base: DefaultBaseURL, key: key, box: area.JawaBarat}
		events, _, err := c.Parse(body, fetched)
		if err != nil && !errors.Is(err, ErrStructure) {
			t.Fatalf("galat tanpa ErrStructure: %v", err)
		}
		seen := map[string]bool{}
		for _, ev := range events {
			if seen[ev.Key()] {
				t.Fatalf("kunci ganda %s", ev.Key())
			}
			seen[ev.Key()] = true
			if _, err := ev.Content(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// Rekaman asli 2026-09-24 17.04 UTC: kotak Jawa Barat 2 hari dan potongan
// 300 baris Kalimantan 1 hari (musim kemarau). Semua baris harus lolos.
func TestRecordedPayloads(t *testing.T) {
	fetched := time.Date(2026, 9, 24, 17, 4, 0, 0, time.UTC)
	total := 0
	for file, p := range map[string]fire.Product{
		"jabar-VIIRS_SNPP_NRT-2026-09-24.csv":                 fire.VIIRSSNPP,
		"jabar-VIIRS_NOAA20_NRT-2026-09-24.csv":               fire.VIIRSNOAA20,
		"jabar-VIIRS_NOAA21_NRT-2026-09-24.csv":               fire.VIIRSNOAA21,
		"jabar-MODIS_NRT-2026-09-24.csv":                      fire.MODISProduct,
		"kalimantan-VIIRS_NOAA20_NRT-2026-09-24-300baris.csv": fire.VIIRSNOAA20,
		"kalimantan-MODIS_NRT-2026-09-24-300baris.csv":        fire.MODISProduct,
	} {
		events, rejected, err := connector(t, p).Parse(read(t, file), fetched)
		if err != nil || len(rejected) != 0 || len(events) == 0 {
			t.Fatalf("%s: %d event, %v, %v", file, len(events), rejected, err)
		}
		total += len(events)
	}
	if total < 600 {
		t.Fatalf("hanya %d deteksi", total)
	}
}

func TestParsersMatchConnectors(t *testing.T) {
	conns, err := NewConnectors(DefaultBaseURL, key, area.JawaBarat)
	if err != nil {
		t.Fatal(err)
	}
	parsers := NewParsers()
	if len(parsers) != len(conns) {
		t.Fatal(len(parsers), len(conns))
	}
	for i, p := range parsers {
		if p.Name() != conns[i].Name() {
			t.Fatalf("%s != %s", p.Name(), conns[i].Name())
		}
	}
}
