package catalog

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

const labelHead = "dasar_label,kejadian_label,sumber,id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,tipe_magnitudo,magnitudo_lain,catatan\n"

func TestReadLabels(t *testing.T) {
	in := "# komentar\n# komentar lain\n" + labelHead +
		"isc,625610809,bmkg,631703691,2023-01-18T06:19:14.50Z,2.8200,127.1000,28.0,5.52,MLv,Mjma 6.04; mb 5.37,\n" +
		"isc,625610809,usgs,620012961,2023-01-18T06:19:13.68Z,2.8239,127.0179,,5.10,mb,,\"catatan, berkoma\"\n"
	got, err := ReadLabels(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d baris", len(got))
	}
	b := got[0]
	if b.Basis != "isc" || b.Label != "625610809" || b.Key != (quake.IdentityKey{Source: quake.SourceBMKG, EventID: "631703691"}) ||
		!b.OccurredAt.Equal(time.Date(2023, 1, 18, 6, 19, 14, 500_000_000, time.UTC)) || b.Magnitude != 5.52 || b.DepthKm != 28 || b.MagnitudeType != "MLv" {
		t.Fatalf("%+v", b)
	}
	if !math.IsNaN(got[1].DepthKm) || got[1].Note != "catatan, berkoma" {
		t.Fatalf("%+v", got[1])
	}
}

func TestReadLabelsRepoFile(t *testing.T) {
	f, err := os.Open("../../../../../docs/calibration/dedup-arsip.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := ReadLabels(f)
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[string]int{}
	for _, o := range got {
		pairs[o.Label]++
		if o.Basis != "arsip" {
			t.Fatalf("%+v", o)
		}
	}
	if len(got) != 20 || len(pairs) != 10 {
		t.Fatalf("%d origin, %d pasangan", len(got), len(pairs))
	}
	for l, n := range pairs {
		if n != 2 {
			t.Errorf("label %s berisi %d origin", l, n)
		}
	}
}

func TestReadLabelsRejects(t *testing.T) {
	row := "isc,1,bmkg,o1,2023-01-18T06:19:14.50Z,2.82,127.1,28,5.5,MLv,,\n"
	cases := map[string]string{
		"header":       "a,b,c\n" + row,
		"kosong":       "",
		"dasar kosong": labelHead + strings.Replace(row, "isc,", ",", 1),
		"sumber":       labelHead + strings.Replace(row, "bmkg", "jma", 1),
		"id":           labelHead + strings.Replace(row, "o1", "", 1),
		"waktu":        labelHead + strings.Replace(row, "2023-01-18T06:19:14.50Z", "kemarin", 1),
		"lintang":      labelHead + strings.Replace(row, "2.82", "x", 1),
		"kedalaman":    labelHead + strings.Replace(row, ",28,", ",dalam,", 1),
		"koordinat":    labelHead + strings.Replace(row, "2.82", "95", 1),
		"ganda":        labelHead + row + row,
		"kolom kurang": labelHead + "isc,1\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadLabels(strings.NewReader(in))
			if err == nil {
				t.Fatal("harus galat")
			}
			if name != "kosong" && name != "kolom kurang" && !errors.Is(err, ErrFormat) {
				t.Fatalf("galat %v bukan ErrFormat", err)
			}
		})
	}
}
