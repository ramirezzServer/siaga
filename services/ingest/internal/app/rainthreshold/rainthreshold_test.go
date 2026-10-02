package rainthreshold

import (
	"bytes"
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo/openmeteotest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/catchment"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/threshold"
)

const ndays = 2922 // 2017-01-01..2024-12-31

// storm memberi hujan hanya pada pukul 10 UTC: nilai unik per hari, tiga
// kali lipat di sel bujur 107,6.
func storm(_, lon float64, day time.Time, step int) float64 {
	if step%24 != 10 {
		return 0
	}
	d := int(day.Sub(DefaultFrom) / (24 * time.Hour))
	v := float64((d*7919)%ndays) / 100
	if lon == 107.6 {
		v *= 3
	}
	return v
}

func TestCompute(t *testing.T) {
	basins, err := catchment.Parse(strings.NewReader(`# uji
subdas,nama,sel_lat,sel_lon,bobot
uji,Uji,-7.0,107.5,0.5
uji,Uji,-7.0,107.6,0.5
lain,Lain,-7.0,107.5,1
`))
	if err != nil {
		t.Fatal(err)
	}
	src := Source("https://archive.test/v1/archive", DefaultModel, DefaultFrom, DefaultTo)
	f := &openmeteotest.Fake{Value: storm}
	l := &reanalysis.Loader{Fetch: f, Clock: &openmeteotest.Clock{}, Cache: reanalysis.NewCache(src)}
	cells := catchment.Cells(basins)
	if len(cells) != 2 {
		t.Fatalf("%v", cells)
	}
	data, err := l.Load(context.Background(), cells)
	if err != nil {
		t.Fatal(err)
	}
	results, err := Compute(src, basins, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 || results[0].Hours != 3 || results[2].Hours != 24 || results[3].Basin.ID != "lain" {
		t.Fatalf("%d hasil", len(results))
	}
	// Sub-DAS uji = rata-rata 1× dan 3× = 2× nilai harian; jendela 3 dan 6
	// jam memuat satu jam hujan, jadi ambangnya ambang nilai harian.
	daily := make([]float64, ndays)
	for d := range daily {
		daily[d] = 2 * float64((d*7919)%ndays) / 100
	}
	want, err := threshold.Compute(daily, MinDays)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results[:2] {
		if !sameSet(r.Thresholds, want) || r.Valid != ndays {
			t.Fatalf("%d jam: %+v, ingin %+v (valid %d)", r.Hours, r.Thresholds, want, r.Valid)
		}
	}
	// Jendela 24 jam bisa memuat jam hujan hari sebelumnya, jadi tidak lebih kecil.
	if r := results[2]; r.Thresholds.Levels[0] < want.Levels[0] || len(r.Top) != TopDays || r.Top[0].Value < r.Top[4].Value {
		t.Fatalf("%+v", r)
	}
	if top := results[0].Top[0]; top.Value != 2*float64(ndays-1)/100 {
		t.Fatalf("puncak %+v", top)
	}

	var out, rep bytes.Buffer
	if err := WriteThresholds(&out, src, results); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "# 6 baris\nsubdas,nama,jam,p50,p80,p90,p98,p99_5,hari_berisi\nuji,Uji,3,") {
		t.Fatal(out.String())
	}
	if err := WriteReport(&rep, time.Unix(0, 0), src, results); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"| Uji | 3 jam |", "## Hari dengan hujan 24 jam terbesar", "| Lain | 20"} {
		if !strings.Contains(rep.String(), s) {
			t.Errorf("laporan tanpa %q", s)
		}
	}
	if _, err := Compute(src, basins, map[series.LatLon][]float64{}); err == nil {
		t.Fatal("deret tidak ada")
	}
	short := Source("https://archive.test/v1/archive", DefaultModel, DefaultFrom, DefaultFrom.AddDate(1, 0, 0))
	shortData := map[series.LatLon][]float64{}
	for _, c := range cells {
		shortData[c] = make([]float64, short.Steps())
	}
	if _, err := Compute(short, basins, shortData); err == nil {
		t.Fatal("periode pendek harus ditolak")
	}
}

func sameSet(a, b threshold.Set) bool {
	if math.Abs(a.P50-b.P50) > 1e-6 {
		return false
	}
	for i := range a.Levels {
		if math.Abs(a.Levels[i]-b.Levels[i]) > 1e-6 {
			return false
		}
	}
	return true
}

// TestRepoBasins memastikan daftar sub-DAS di repo utuh.
func TestRepoBasins(t *testing.T) {
	f, err := os.Open("../../../../../docs/calibration/sub-das-citarum-hulu.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	basins, err := catchment.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(basins))
	rows := 0
	for i, b := range basins {
		ids[i] = b.ID
		rows += len(b.Cells)
	}
	if strings.Join(ids, ",") != "cirasea,cisangkuy,ciwidey,citarik,ciminyak,cihaur,cikapundung" || rows != 79 || len(catchment.Cells(basins)) != 47 {
		t.Fatalf("%v, %d baris, %d sel", ids, rows, len(catchment.Cells(basins)))
	}
	src := Source(DefaultArchiveURL, DefaultModel, DefaultFrom, DefaultTo)
	if calls := float64(len(catchment.Cells(basins))) * src.Weight(); calls > 1000 {
		t.Fatalf("±%.0f panggilan", calls)
	}
}
