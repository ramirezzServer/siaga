package catchment

import (
	"bytes"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

func TestParseErrors(t *testing.T) {
	head := Header + "\n"
	for name, body := range map[string]string{
		"header":  "a,b\n",
		"kosong":  head,
		"angka":   head + "a,A,x,107,1\n",
		"slug":    head + "Sub DAS,A,-7,107,1\n",
		"nama":    head + "a, ,-7,107,1\n",
		"lokasi":  head + "a,A,40,107,1\n",
		"bobot":   head + "a,A,-7,107,0\n",
		"jumlah":  head + "a,A,-7,107,0.6\na,A,-7.1,107,0.3\n",
		"ganda":   head + "a,A,-7,107,0.5\na,A,-7.00001,107,0.5\n",
		"beda":    head + "a,A,-7,107,0.5\na,B,-7.1,107,0.5\n",
		"kolom":   head + "a,A,-7,107\n",
		"panjang": head + strings.Repeat("a", 60) + ",A,-7,107,1\n",
	} {
		if _, err := Parse(strings.NewReader(body)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s harus ditolak dengan ErrInvalid: %v", name, err)
		}
	}
}

func TestParseCentroidAndAreal(t *testing.T) {
	basins, err := Parse(strings.NewReader("# komentar\n" + Header + "\n" +
		"hulu,Hulu,-7,107,0.25\nhulu,Hulu,-7.1,107.2,0.75\nhilir,Hilir,-7.1,107.2,1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(basins) != 2 || basins[0].SiteID() != "catchment:hulu" || basins[1].Name != "Hilir" {
		t.Fatalf("%+v", basins)
	}
	if c := basins[0].Centroid(); c != (series.LatLon{Lat: -7.075, Lon: 107.15}) {
		t.Fatalf("titik berat %v", c)
	}
	if cells := Cells(basins); len(cells) != 2 {
		t.Fatalf("%v", cells)
	}
	data := map[series.LatLon][]float64{
		{Lat: -7, Lon: 107}:     {4, 0, math.NaN()},
		{Lat: -7.1, Lon: 107.2}: {0, 2, 1},
	}
	got, err := basins[0].Areal(data)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 1 || got[1] != 1.5 || !math.IsNaN(got[2]) {
		t.Fatalf("rata-rata wilayah %v", got)
	}
	delete(data, series.LatLon{Lat: -7, Lon: 107})
	if _, err := basins[0].Areal(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sel hilang: %v", err)
	}
	data[series.LatLon{Lat: -7, Lon: 107}] = []float64{1}
	if _, err := basins[0].Areal(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("panjang beda: %v", err)
	}
}

// TestRepoBasins memastikan daftar sub-DAS di repo utuh.
func TestRepoBasins(t *testing.T) {
	b, err := os.ReadFile("../../../../../docs/calibration/sub-das-citarum-hulu.csv")
	if err != nil {
		t.Fatal(err)
	}
	basins, err := Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(basins))
	rows := 0
	for i, b := range basins {
		ids[i] = b.ID
		rows += len(b.Cells)
		if c := b.Centroid(); !series.InIndonesia(c) || math.Abs(c.Lat+6.95) > 0.3 || math.Abs(c.Lon-107.6) > 0.35 {
			t.Errorf("titik berat %s %v di luar Citarum Hulu", b.ID, c)
		}
	}
	if strings.Join(ids, ",") != "cirasea,cisangkuy,ciwidey,citarik,ciminyak,cihaur,cikapundung" || rows != 79 || len(Cells(basins)) != 47 {
		t.Fatalf("%v, %d baris, %d sel", ids, rows, len(Cells(basins)))
	}
}
