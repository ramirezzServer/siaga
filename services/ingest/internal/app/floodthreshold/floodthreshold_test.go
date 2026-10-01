package floodthreshold

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo/openmeteotest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/threshold"
)

const days = 10227 // 1997-01-01..2024-12-31

// ramp memberi setiap hari sejak 1997-01-01 nilai unik 0..days-1 (permutasi),
// jadi persentil periode klimatologi diketahui. Sel di bujur 108 kering.
func ramp(_, lon float64, day time.Time, _ int) float64 {
	if lon == 108 {
		return 0
	}
	d := int(day.Sub(DefaultFrom) / (24 * time.Hour))
	return float64(((d%days + days) * 7919) % days)
}

var sites = []series.Site{
	{ID: "river:hulu", River: "Uji", Name: "Hulu", Requested: series.LatLon{Lat: -7.075, Lon: 107.725}},
	{ID: "river:hilir", River: "Uji", Name: "Hilir", Requested: series.LatLon{Lat: -6.125, Lon: 106.975}},
}

const eventsCSV = `# uji
id,titik,dari,sampai,model,sumber
lama,hulu,2016-09-18,2016-09-24,consolidated_v4,https://contoh.test/a
baru,hilir;hulu,2026-01-15,2026-02-03,seamless_v4,https://contoh.test/b
`

type opener struct {
	fake    *openmeteotest.Fake
	sources []reanalysis.Source
}

func (o *opener) open(src reanalysis.Source) (*reanalysis.Loader, error) {
	o.sources = append(o.sources, src)
	return &reanalysis.Loader{Fetch: o.fake, Clock: &openmeteotest.Clock{}, Cache: reanalysis.NewCache(src)}, nil
}

func TestRun(t *testing.T) {
	events, err := ReadEvents(strings.NewReader(eventsCSV))
	if err != nil {
		t.Fatal(err)
	}
	o := &opener{fake: &openmeteotest.Fake{Value: ramp}}
	src := Source("https://flood.test/v1/flood", ModelReanalysis, DefaultFrom, DefaultTo)
	c := &Calibrator{Endpoint: "https://flood.test/v1/flood", Source: src, Open: o.open, OverlapFrom: DefaultOverlapFrom, OverlapTo: DefaultOverlapTo}
	results, checks, err := c.Run(context.Background(), sites, events)
	if err != nil {
		t.Fatal(err)
	}
	want := threshold.Set{P50: 5113, Levels: [4]float64{8180.8, 9203.4, 10021.48, 10174.87}}
	for _, r := range results {
		if r.Thresholds != want || r.Valid != days || r.Years != 28 {
			t.Fatalf("%s: %+v valid %d tahun %d", r.Site.ID, r.Thresholds, r.Valid, r.Years)
		}
		// Sumber tiruan menjawab nilai yang sama untuk kedua model.
		if r.Max.Value != days-1 || r.MedianAnnualMax < 9000 || r.SeamlessRatio != 1 {
			t.Fatalf("%s: puncak %+v median puncak tahunan %v", r.Site.ID, r.Max, r.MedianAnnualMax)
		}
	}
	// Klimatologi diminta sekali; kejadian lama di dalam sampel (tanpa
	// request), kejadian baru diminta dengan seamless_v4 untuk jendelanya saja.
	// Masing-masing dua request: titik uji satu sel, lalu sel kedua.
	if len(o.sources) != 3 || o.sources[1].Model != ModelSeamless || reanalysis.Date(o.sources[1].From) != "2026-01-15" ||
		reanalysis.Date(o.sources[2].From) != "2022-08-01" || o.fake.Requests != 6 {
		t.Fatalf("sumber %v, request %d", o.sources, o.fake.Requests)
	}
	if len(checks) != 3 || !checks[0].InSample || checks[1].InSample {
		t.Fatalf("%+v", checks)
	}
	for _, ch := range checks {
		// Puncak = nilai ramp terbesar di jendela, tingkat sesuai ambang.
		peak := math.Inf(-1)
		var at time.Time
		for d := ch.Event.From; !d.After(ch.Event.To); d = d.AddDate(0, 0, 1) {
			if v := ramp(0, 0, d, 0); v > peak {
				peak, at = v, d
			}
		}
		if ch.Peak.Value != peak || !ch.Peak.Day.Equal(at) || ch.Level != want.Level(peak) {
			t.Fatalf("%s %s: %+v, ingin %v %s", ch.Event.ID, ch.Site.ID, ch.Peak, peak, reanalysis.Date(at))
		}
	}

	var out, rep bytes.Buffer
	if err := WriteThresholds(&out, "32", src, results); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(stripComments(out.String()))).ReadAll()
	if err != nil || len(recs) != 3 || strings.Join(recs[1], ",") != "hulu,Uji,Hulu,-7.0750,107.7250,5113.00,8180.80,9203.40,10021.48,10174.87,"+recs[1][10]+",10227,1.00" {
		t.Fatalf("%v %v", recs, err)
	}
	if !strings.Contains(out.String(), "# 2 titik") {
		t.Fatal(out.String())
	}
	if err := WriteReport(&rep, time.Unix(0, 0), src, results, checks); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"1997-01-01 sampai 2024-12-31 (10227 hari)", "| [lama](https://contoh.test/a) | `hulu` | `consolidated_v4` | di dalam sampel |", "di luar sampel", "8180,8"} {
		if !strings.Contains(rep.String(), s) {
			t.Errorf("laporan tanpa %q", s)
		}
	}
	rep.Reset()
	if err := WriteReport(&rep, time.Unix(0, 0), src, results, nil); err != nil || !strings.Contains(rep.String(), "Tidak ada kejadian") {
		t.Fatal(err)
	}
}

func stripComments(s string) string {
	var b strings.Builder
	for l := range strings.SplitSeq(s, "\n") {
		if !strings.HasPrefix(l, "#") {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()
	src := Source("https://flood.test/v1/flood", ModelReanalysis, DefaultFrom, DefaultTo)
	o := &opener{fake: &openmeteotest.Fake{Value: ramp}}

	early := src
	early.From = time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, _, err := (&Calibrator{Source: early, Open: o.open}).Run(ctx, sites, nil); err == nil || o.fake.Requests != 0 {
		t.Fatalf("periode sebelum reanalisis: %v", err)
	}
	dry := []series.Site{{ID: "river:kering", River: "Uji", Name: "Kering", Requested: series.LatLon{Lat: -7, Lon: 108}}}
	if _, _, err := (&Calibrator{Source: src, Open: o.open}).Run(ctx, dry, nil); !errors.Is(err, threshold.ErrInvalid) {
		t.Fatalf("sel kering: %v", err)
	}
	boom := errors.New("cache rusak")
	if _, _, err := (&Calibrator{Source: src, Open: func(reanalysis.Source) (*reanalysis.Loader, error) { return nil, boom }}).Run(ctx, sites, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	unknown := []Event{{ID: "x", Stations: []string{"tidak-ada"}, From: DefaultFrom, To: DefaultFrom, Model: ModelReanalysis}}
	if _, _, err := (&Calibrator{Source: src, Open: o.open}).Run(ctx, sites, unknown); err == nil {
		t.Fatal("titik kejadian tidak dikenal harus ditolak")
	}
	// Kejadian di luar sampel yang gagal diambil menyebut id kejadian.
	failing := &opener{fake: &openmeteotest.Fake{Value: ramp}}
	calls := 0
	open := func(s reanalysis.Source) (*reanalysis.Loader, error) {
		calls++
		if calls > 1 {
			failing.fake.Fail = boom
		}
		return failing.open(s)
	}
	late := []Event{{ID: "akhir", Stations: []string{"hulu"}, From: DefaultTo.AddDate(0, 0, 1), To: DefaultTo.AddDate(0, 0, 2), Model: ModelReanalysis}}
	if _, _, err := (&Calibrator{Endpoint: "https://flood.test/v1/flood", Source: src, Open: open}).Run(ctx, sites, late); !errors.Is(err, boom) || !strings.Contains(err.Error(), "akhir") {
		t.Fatal(err)
	}
	bad := &Calibrator{Endpoint: "https://flood.test/v1/flood", Source: src, Open: o.open, OverlapFrom: DefaultOverlapFrom, OverlapTo: DefaultTo.AddDate(0, 0, 1)}
	if _, _, err := bad.Run(ctx, sites, nil); err == nil {
		t.Fatal("periode pembanding di luar klimatologi harus ditolak")
	}
	o.fake.Fail = boom
	bad.OverlapTo = DefaultTo
	bad.Open = func(s reanalysis.Source) (*reanalysis.Loader, error) {
		if s.Model == ModelSeamless {
			return o.open(s)
		}
		return (&opener{fake: &openmeteotest.Fake{Value: ramp}}).open(s)
	}
	if _, _, err := bad.Run(ctx, sites, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := Compute(src, sites, map[series.LatLon][]float64{}); err == nil {
		t.Fatal("deret tidak ada")
	}
	if _, err := Evaluate(Event{ID: "x", Stations: []string{"hulu"}}, []Result{{Site: sites[0]}}, true, nil); err == nil {
		t.Fatal("deret kejadian tidak ada")
	}
}

func TestReadEvents(t *testing.T) {
	head := "id,titik,dari,sampai,model,sumber\n"
	for name, row := range map[string]string{
		"id":       "Garut 2016,a,2016-09-18,2016-09-24,consolidated_v4,https://a.test/",
		"titik":    "a,cimanuk garut,2016-09-18,2016-09-24,consolidated_v4,https://a.test/",
		"tanggal":  "a,b,2016-09-31,2016-10-01,consolidated_v4,https://a.test/",
		"terbalik": "a,b,2016-09-24,2016-09-18,consolidated_v4,https://a.test/",
		"panjang":  "a,b,2016-01-01,2016-03-01,consolidated_v4,https://a.test/",
		"model":    "a,b,2016-09-18,2016-09-24,forecast_v4,https://a.test/",
		"sumber":   "a,b,2016-09-18,2016-09-24,consolidated_v4,http://a.test/",
	} {
		if _, err := ReadEvents(strings.NewReader(head + row + "\n")); err == nil {
			t.Errorf("%s harus ditolak", name)
		}
	}
	dup := head + "a,b,2016-09-18,2016-09-24,consolidated_v4,https://a.test/\n"
	if _, err := ReadEvents(strings.NewReader(dup + strings.TrimPrefix(dup, head))); err == nil {
		t.Error("id ganda harus ditolak")
	}
	if _, err := ReadEvents(strings.NewReader("id,titik\na,b\n")); err == nil {
		t.Error("header salah harus ditolak")
	}
}

// TestRepoFiles memastikan daftar kejadian di repo cocok dengan titik pantau.
func TestRepoFiles(t *testing.T) {
	b, err := os.ReadFile("../../adapters/sitelist/data/rivers_32.csv")
	if err != nil {
		t.Fatal(err)
	}
	all, err := sitelist.ParseRivers(b)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../../../../../docs/calibration/banjir-tercatat.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	events, err := ReadEvents(f)
	if err != nil || len(events) < 3 {
		t.Fatalf("%d kejadian: %v", len(events), err)
	}
	src := Source(DefaultFloodURL, ModelReanalysis, DefaultFrom, DefaultTo)
	for _, ev := range events {
		if len(ev.Cells(all)) != len(ev.Stations) {
			t.Errorf("kejadian %s menyebut titik yang tidak ada", ev.ID)
		}
		// Bekasi 2025 sengaja di luar sampel, Garut 2016 di dalamnya.
		if (ev.ID == "bekasi-2025" && ev.InSample(src)) || (ev.ID == "garut-2016" && !ev.InSample(src)) {
			t.Errorf("kejadian %s salah sampel", ev.ID)
		}
	}
	if err := src.Validate(); err != nil || src.Days() != days || math.Ceil(float64(len(all))*src.Weight()) > 2800 {
		t.Fatalf("%v hari %d bobot %v", err, src.Days(), src.Weight())
	}
}
