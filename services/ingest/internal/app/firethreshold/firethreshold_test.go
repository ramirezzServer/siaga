package firethreshold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/firecluster"
)

func p(id, sat string, lat, lon float64, at time.Time, c fire.Confidence) firecluster.Point {
	return firecluster.Point{ID: id, Satellite: sat, Instrument: fire.VIIRS, Lat: lat, Lon: lon, At: at, Confidence: c, Type: fire.UnknownType, Daytime: true, FRPMW: 3}
}

// synthetic: kilang menyala tiap 20 hari 2016–2019 (malam, type 2 di SP),
// kebakaran Ciremai Oktober 2019 (besar, tiga wahana, ada piksel jenuh),
// satu deteksi rendah di Papandayan, dan kebakaran sawah kecil.
func synthetic() []firecluster.Point {
	var ps []firecluster.Point
	for i := range 73 {
		at := time.Date(2016, 1, 1, 18, 40, 0, 0, time.UTC).AddDate(0, 0, 20*i)
		q := p(fmt.Sprintf("kilang-%d", i), "S-NPP", -6.3851+0.002*float64(i%3-1), 108.3702, at, fire.Nominal)
		q.Daytime, q.Type, q.FRPMW = false, fire.StaticSource, 25
		ps = append(ps, q)
	}
	start := time.Date(2019, 10, 21, 5, 30, 0, 0, time.UTC)
	for d := range 5 {
		for k, sat := range []string{"S-NPP", "NOAA-20", "MODIS"} {
			for j := range 3 {
				conf := fire.Nominal
				if d == 2 && k == 0 && j == 0 {
					conf = fire.High
				}
				at := start.AddDate(0, 0, d).Add(time.Duration(k*50) * time.Minute)
				ps = append(ps, p(fmt.Sprintf("ciremai-%d-%d-%d", d, k, j), sat, -6.875+0.003*float64(j), 108.38+0.002*float64(d), at, conf))
			}
		}
	}
	ps = append(ps, p("papandayan", "NOAA-20", -7.316, 107.731, time.Date(2019, 10, 6, 18, 0, 0, 0, time.UTC), fire.Low))
	ps = append(ps, p("sawah", "MODIS", -6.4, 107.5, time.Date(2019, 8, 1, 6, 0, 0, 0, time.UTC), fire.Low))
	// Duplikat ID (SP dan NRT tumpang tindih) dihitung sekali.
	ps = append(ps, ps[len(ps)-1])
	return ps
}

const recordedCSV = `# catatan
id,mulai,selesai,lintang,bujur,radius_km,luas_ha,sumber,catatan
ciremai-2019-10,2019-10-21,2019-10-26,-6.880,108.385,7,190,https://contoh.test/a,lereng
papandayan-2019-10,2019-10-06,2019-10-07,-7.315,107.730,4,1.5,https://contoh.test/b,kecil
guntur-2024-08,2024-08-19,2024-08-22,-7.143,107.841,5,,https://contoh.test/c,tidak ada di data
kilang,2017-03-01,2017-03-31,-6.385,108.370,2,,https://contoh.test/d,sumber tetap
`

func TestReadRecorded(t *testing.T) {
	fires, err := ReadRecorded(strings.NewReader(recordedCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(fires) != 4 || fires[0].AreaHa != 190 || fires[2].AreaHa != 0 || fires[0].To.Day() != 26 || fires[1].RadiusKm != 4 {
		t.Fatalf("%+v", fires)
	}
	from, to := fires[0].window()
	if !from.Equal(time.Date(2019, 10, 19, 17, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2019, 10, 28, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("jendela %v %v", from, to)
	}
	head := "id,mulai,selesai,lintang,bujur,radius_km,luas_ha,sumber,catatan\n"
	for name, row := range map[string]string{
		"header":   "id,mulai\n",
		"kolom":    head + "a,2019-01-01\n",
		"tanggal":  head + "a,kemarin,2019-01-01,-6.9,107.6,5,,https://x.test,\n",
		"angka":    head + "a,2019-01-01,2019-01-02,x,107.6,5,,https://x.test,\n",
		"terbalik": head + "a,2019-01-02,2019-01-01,-6.9,107.6,5,,https://x.test,\n",
		"lokasi":   head + "a,2019-01-01,2019-01-02,-1,107.6,5,,https://x.test,\n",
		"radius":   head + "a,2019-01-01,2019-01-02,-6.9,107.6,50,,https://x.test,\n",
		"luas":     head + "a,2019-01-01,2019-01-02,-6.9,107.6,5,-1,https://x.test,\n",
		"sumber":   head + "a,2019-01-01,2019-01-02,-6.9,107.6,5,,http://x.test,\n",
		"id":       head + "a b,2019-01-01,2019-01-02,-6.9,107.6,5,,https://x.test,\n",
		"ganda":    head + "a,2019-01-01,2019-01-02,-6.9,107.6,5,,https://x.test,\na,2019-01-01,2019-01-02,-6.9,107.6,5,,https://x.test,\n",
		"kosong":   "",
	} {
		if _, err := ReadRecorded(strings.NewReader(row)); err == nil {
			t.Errorf("%s diterima", name)
		}
	}
}

func TestAnalyze(t *testing.T) {
	fires, _ := ReadRecorded(strings.NewReader(recordedCSV))
	res, err := Analyze(synthetic(), fires, firecluster.DefaultStaticRule, DefaultVariants())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Static) != 1 || res.Static[0].Reason != firecluster.ReasonBoth || res.Masked != 73 {
		t.Fatalf("sumber tetap %+v, tersaring %d", res.Static, res.Masked)
	}
	if res.Monthly[9] != 46 || res.Monthly[7] != 1 {
		t.Fatalf("bulanan %v", res.Monthly)
	}
	// Inventaris: S-NPP 2016–2019 kilang + Ciremai; NOAA-20 dan MODIS 2019.
	var snpp2019 InventoryRow
	for _, r := range res.Inventory {
		if r.Satellite == "S-NPP" && r.Year == 2019 {
			snpp2019 = r
		}
	}
	if res.Inventory[0].Satellite != "S-NPP" || snpp2019.Detections != 18+15 || snpp2019.ByConfidence[fire.High] != 1 || snpp2019.Typed[fire.StaticSource] != 18 {
		t.Fatalf("%+v", res.Inventory)
	}
	prd, noSNPP, noMask, nominal, passes := res.Variants[0], res.Variants[1], res.Variants[2], res.Variants[3], res.Variants[4]
	// PRD: Ciremai Siaga, Papandayan Info, sawah Info.
	if got := prd.Total(); got != [4]int{0, 2, 0, 1} || prd.Points != 47 {
		t.Fatalf("PRD %v, %d deteksi", got, prd.Points)
	}
	if top := prd.Top(1)[0]; top.Level != firecluster.Siaga || top.Detections != 45 || top.Peak != 9 || len(top.Satellites) != 3 {
		t.Fatalf("terbesar %+v", top)
	}
	if noSNPP.Points != 32 || noMask.Points != 120 || nominal.Total()[firecluster.Siaga] != 1 {
		t.Fatalf("varian %d %d %v", noSNPP.Points, noMask.Points, nominal.Total())
	}
	// Tanpa saringan, kilang menjadi kejadian sendiri (setiap 20 hari: jeda >
	// 24 jam, jadi 73 kejadian Info).
	if got := noMask.Total(); got[firecluster.Info] != 2+73 {
		t.Fatalf("tanpa saringan %v", got)
	}
	// Lintasan: Ciremai dilihat 2 wahana tanpa S-NPP per hari → < 3 lintasan.
	if got := passes.Total(); got[firecluster.Siaga] != 0 || got[firecluster.Waspada] != 1 {
		t.Fatalf("lintasan %v", got)
	}
	if len(res.Checks) != 4 {
		t.Fatal(res.Checks)
	}
	ciremai, papandayan, guntur, kilang := res.Checks[0], res.Checks[1], res.Checks[2], res.Checks[3]
	if r := ciremai.Results[0]; !r.Found || r.Level != firecluster.Siaga || r.Detections != 45 || !r.FirstAt.Equal(time.Date(2019, 10, 21, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("ciremai %+v", r)
	}
	if r := papandayan.Results[0]; !r.Found || r.Level != firecluster.Info || r.Detections != 1 {
		t.Fatalf("papandayan %+v", r)
	}
	if r := guntur.Results[0]; r.Found {
		t.Fatalf("guntur %+v", r)
	}
	if r := kilang.Results[0]; r.Found || r.Masked != 1 {
		t.Fatalf("kilang PRD %+v", r)
	}
	if r := kilang.Results[2]; !r.Found || r.Masked != 0 {
		t.Fatalf("kilang tanpa saringan %+v", r)
	}

	var rep bytes.Buffer
	cov := []Coverage{{Satellite: "S-NPP", Product: "VIIRS_SNPP_SP", From: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2019, 12, 31, 0, 0, 0, 0, time.UTC)}}
	if err := WriteReport(&rep, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), "106.3,-7.9,108.9,-5.7", cov, res); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Dibuat `make fire-threshold` pada 2026-10-20 19.00 WIB",
		"| S-NPP | `VIIRS_SNPP_SP` | 2016-01-01 | 2019-12-31 |",
		"**1 sumber**, menyaring 73 dari 120 deteksi (61%)",
		"| -6.38513,108.37020 |",
		"| PRD | - | ya | 2.0 km / 24 jam | high | ≥ 5 deteksi | 47 | 3 | 2 | 0 | 1 |",
		"| 2019 | 0 / 1 |",
		"| [ciremai-2019-10](https://contoh.test/a) | 2019-10-21 s.d. 10-26 | 190 ha | Siaga, 45 deteksi, pertama 2019-10-21 12.30 |",
		"| [kilang](https://contoh.test/d) | 2017-03-01 s.d. 03-31 | - | tidak terdeteksi (1 tersaring) |",
		"## 25 kejadian terbesar (PRD)",
	} {
		if !strings.Contains(rep.String(), want) {
			t.Errorf("laporan tanpa %q", want)
		}
	}
	if t.Failed() {
		t.Log(rep.String())
	}

	var csvOut, geo bytes.Buffer
	if err := WriteStatic(&csvOut, res.Static); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(csvOut.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "id,lintang,bujur,radius_km,alasan") || !strings.HasPrefix(lines[1], "\"-6.38513,108.37020\",-6.38513,108.37020,") {
		t.Fatalf("CSV\n%s", csvOut.String())
	}
	if err := WriteGeoJSON(&geo, res.Static); err != nil {
		t.Fatal(err)
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(geo.Bytes(), &fc); err != nil || len(fc.Features) != 1 || fc.Features[0].Geometry.Coordinates[0] != 108.3702 ||
		fc.Features[0].Properties["marker-color"] != "#111111" || fc.Features[0].Properties["marker-size"] != "medium" {
		t.Fatalf("%s %v", geo.String(), err)
	}
}

func TestAnalyzeEdges(t *testing.T) {
	if _, err := Analyze(nil, nil, firecluster.DefaultStaticRule, DefaultVariants()); err == nil {
		t.Fatal("arsip kosong diterima")
	}
	ps := synthetic()
	if _, err := Analyze(ps, nil, firecluster.StaticRule{}, nil); err == nil {
		t.Fatal("aturan sumber tetap kosong diterima")
	}
	bad := DefaultVariants()[:1]
	bad[0].Cluster.LinkKm = 0
	if _, err := Analyze(ps, nil, firecluster.DefaultStaticRule, bad); err == nil {
		t.Fatal("varian salah diterima")
	}
	bad = DefaultVariants()[:1]
	bad[0].Level.SiagaCount = 0
	if _, err := Analyze(ps, nil, firecluster.DefaultStaticRule, bad); err == nil {
		t.Fatal("aturan tingkat salah diterima")
	}
	// Tanpa varian dan tanpa uji, laporan tetap bisa ditulis.
	res, err := Analyze(ps, nil, firecluster.DefaultStaticRule, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rep bytes.Buffer
	if err := WriteReport(&rep, time.Now(), "x", nil, res); err != nil || !strings.Contains(rep.String(), "Tidak ada daftar kebakaran tercatat") {
		t.Fatal(err)
	}
	res.Static = nil
	rep.Reset()
	if err := WriteReport(&rep, time.Now(), "x", nil, res); err != nil || strings.Contains(rep.String(), "| Pusat |") {
		t.Fatal(err)
	}
	if pct(1, 0) != "-" || wib(time.Time{}) != "-" || markerSize(300) != "large" || markerSize(1) != "small" {
		t.Fatal("pembantu")
	}
}
