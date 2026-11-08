package firethreshold

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/firecluster"
)

var update = flag.Bool("update", false, "tulis ulang berkas acuan hazard.fire.* geo-processor")

// Berkas acuan dipakai bersama test kecocokan geo-processor
// (internal/app/fires): deteksi asli Agustus 2023 dinilai di sini dengan
// firecluster, dan geo-processor harus menghasilkan kejadian yang sama.
const (
	goldenFixture = "../../../../geo-processor/internal/app/fires/testdata/firms-2023-08.csv"
	goldenFile    = "../../../../geo-processor/internal/app/fires/testdata/firms-2023-08.golden.json"
	staticFile    = "../../../../../docs/calibration/sumber-panas-tetap-32.csv"
)

// GoldenEvent adalah satu kejadian di berkas acuan.
type goldenEvent struct {
	Members   []string `json:"members"`
	Level     string   `json:"level"`
	Peak      int      `json:"peak"`
	ReachedAt string   `json:"reached_at"`
}

type golden struct {
	Rule     string        `json:"rule"`
	Excluded int           `json:"excluded"`
	Masked   int           `json:"masked"`
	Events   []goldenEvent `json:"events"`
}

func readFixture(t *testing.T) []firecluster.Point {
	t.Helper()
	b, err := os.ReadFile(goldenFixture)
	if err != nil {
		t.Fatal(err)
	}
	cr := csv.NewReader(bytes.NewReader(b))
	cr.Comment = '#'
	recs, err := cr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(recs[0], ",") != "id,product,lat,lon,detected_at,confidence,frp_mw,daytime" {
		t.Fatal(recs[0])
	}
	var ps []firecluster.Point
	for _, r := range recs[1:] {
		lat, _ := strconv.ParseFloat(r[2], 64)
		lon, _ := strconv.ParseFloat(r[3], 64)
		at, err := time.Parse(time.RFC3339, r[4])
		conf, ok := fire.ParseVIIRSConfidence(r[5][:1])
		if err != nil || !ok {
			t.Fatalf("baris %v", r)
		}
		ps = append(ps, firecluster.Point{ID: r[0], Satellite: fire.SatelliteOf(fire.Product(r[1])), Lat: lat, Lon: lon, At: at, Confidence: conf})
	}
	return ps
}

// TestGoldenForGeoProcessor menghitung kejadian acuan dengan aturan
// terpilih. Jalankan dengan -update setelah aturan atau saringan berubah,
// lalu test geo-processor harus ikut lulus.
func TestGoldenForGeoProcessor(t *testing.T) {
	ps := readFixture(t)
	f, err := os.Open(staticFile)
	if err != nil {
		t.Fatal(err)
	}
	static, err := ReadStatic(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	mask := firecluster.Mask(static)
	g := golden{Rule: firecluster.SelectedRule.Name}
	var sel []firecluster.Point
	for _, p := range ps {
		switch {
		case slices.Contains(firecluster.ExcludedSatellites, p.Satellite):
			g.Excluded++
		case func() bool { _, ok := mask.Match(p.Lat, p.Lon); return ok }():
			g.Masked++
		default:
			sel = append(sel, p)
		}
	}
	firecluster.SortPoints(sel)
	groups, err := firecluster.Cluster(sel, firecluster.DefaultClusterRule)
	if err != nil {
		t.Fatal(err)
	}
	for _, idx := range groups {
		members := make([]firecluster.Point, len(idx))
		ev := goldenEvent{}
		for j, i := range idx {
			members[j] = sel[i]
			ev.Members = append(ev.Members, sel[i].ID)
		}
		o, err := firecluster.Evaluate(members, firecluster.SelectedRule)
		if err != nil {
			t.Fatal(err)
		}
		ev.Level, ev.Peak, ev.ReachedAt = o.Level.String(), o.Peak, o.ReachedAt.UTC().Format(time.RFC3339)
		g.Events = append(g.Events, ev)
	}
	got, err := json.MarshalIndent(g, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.WriteFile(goldenFile, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("berkas acuan %s usang; jalankan go test ./internal/app/firethreshold -run Golden -update", goldenFile)
	}
	// Rentang ini memuat kebakaran Sarimukti dan Ciremai (Siaga).
	siaga := 0
	for _, ev := range g.Events {
		if ev.Level == "Siaga" {
			siaga++
		}
	}
	if g.Excluded == 0 || g.Masked == 0 || siaga < 2 {
		t.Fatalf("acuan tidak mewakili: %d dibuang, %d tersaring, %d Siaga", g.Excluded, g.Masked, siaga)
	}
}

func TestReadStatic(t *testing.T) {
	res, err := Analyze(synthetic(), nil, firecluster.DefaultStaticRule, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := WriteStatic(&b, res); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatic(&b)
	if err != nil || len(got) != 1 || got[0].ID() != res.Static[0].ID() || got[0].RadiusKm != res.Static[0].RadiusKm || got[0].Reason != firecluster.ReasonBoth {
		t.Fatalf("%+v %v", got, err)
	}
	head := strings.Join(staticColumns, ",") + "\n"
	row := func(id, lat, lon, r, reason string) string {
		return head + strings.Join([]string{id, lat, lon, r, reason, "1", "1", "1", "1", "1", "0", "0", "0", "0", "0", "0", "1", "2020-01-01", "2020-01-01"}, ",") + "\n"
	}
	for name, s := range map[string]string{
		"header": "id,lintang\n",
		"angka":  row(`"-6.00000,107.00000"`, "x", "107", "1", "persisten"),
		"id":     row(`"-6.00000,107.00000"`, "-6.1", "107", "1", "persisten"),
		"radius": row(`"-6.00000,107.00000"`, "-6", "107", "0", "persisten"),
		"alasan": row(`"-6.00000,107.00000"`, "-6", "107", "1", "lain"),
		"kosong": "",
	} {
		if _, err := ReadStatic(strings.NewReader(s)); err == nil {
			t.Errorf("%s diterima", name)
		}
	}
}
