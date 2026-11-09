package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesReport(t *testing.T) {
	dir := t.TempDir()
	bmkg := filepath.Join(dir, "bmkg.csv")
	usgs := filepath.Join(dir, "usgs.csv")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(bmkg, "tgl,ot,lat,lon,depth,mag\n2022/11/21,06:21:09.874,-6.85,107.03,11,5.6\n2022/11/22,00:00:00.000,-7,107,10,3.0\n")
	write(usgs, "time,latitude,longitude,depth,mag,id,type\n2022-11-21T06:21:07.066Z,-6.836,106.9968,10,5.6,us7000ir9t,earthquake\n2022-11-22T01:00:00.000Z,-7,107,10,3.0,us2,earthquake\n")
	var out bytes.Buffer
	if err := run([]string{"-bmkg", bmkg, "-usgs", usgs}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "| Gempa USGS | 2 |") || !strings.Contains(out.String(), "sha256") {
		t.Fatalf("laporan:\n%s", out.String())
	}
	md := filepath.Join(dir, "out.md")
	if err := run([]string{"-bmkg", bmkg, "-usgs", usgs, "-from", "2022-11-01", "-to", "2022-12-01", "-out", md}, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Clean(md)); !bytes.Contains(b, []byte("2022-11-01 s.d. 2022-12-01")) {
		t.Fatalf("file keluaran:\n%s", b)
	}
	for _, args := range [][]string{
		{},
		{"-bmkg", bmkg, "-usgs", usgs, "-box", "1,2,3"},
		{"-bmkg", bmkg, "-usgs", usgs, "-box", "2,1,3,4"},
		{"-bmkg", bmkg, "-usgs", usgs, "-box", "a,1,3,4"},
		{"-bmkg", bmkg, "-usgs", usgs, "-from", "2023-01-01", "-to", "2022-01-01"},
		{"-bmkg", bmkg, "-usgs", usgs, "-from", "kemarin"},
		{"-bmkg", bmkg, "-usgs", usgs, "-to", "besok"},
		{"-bmkg", filepath.Join(dir, "tidak-ada.csv"), "-usgs", usgs},
		{"-bmkg", usgs, "-usgs", usgs},
		{"-bmkg", bmkg, "-usgs", bmkg},
		{"-tidak-dikenal"},
	} {
		if err := run(args, &out); err == nil {
			t.Errorf("%v seharusnya gagal", args)
		}
	}
}

func TestRunLabels(t *testing.T) {
	dir := t.TempDir()
	labels := filepath.Join(dir, "berlabel.csv")
	head := "# komentar\ndasar_label,kejadian_label,sumber,id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,tipe_magnitudo,magnitudo_lain,catatan\n"
	if err := os.WriteFile(labels, []byte(head+
		"isc,1,bmkg,b1,2023-01-18T06:19:14.50Z,2.82,127.1,28,5.5,MLv,,\n"+
		"isc,1,usgs,u1,2023-01-18T06:19:13.68Z,2.8239,127.0179,35,5.1,mb,,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	arsip := "../../../../docs/calibration/dedup-arsip.csv"
	md := filepath.Join(dir, "laporan.md")
	if err := run([]string{"-labels", labels + "," + arsip, "-out", md}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Clean(md))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("## Basis `isc`")) || !bytes.Contains(b, []byte("## Basis `arsip`")) || !bytes.Contains(b, []byte("dedup-arsip.csv` (sha256")) {
		t.Fatalf("laporan:\n%s", b)
	}
	var out bytes.Buffer
	if err := run([]string{"-labels", labels}, &out); err != nil || !strings.Contains(out.String(), "Basis `isc`") {
		t.Fatalf("stdout: %v\n%s", err, out.String())
	}
	for _, args := range [][]string{
		{"-labels", labels, "-bmkg", labels},
		{"-labels", filepath.Join(dir, "tidak-ada.csv")},
		{"-labels", labels + "," + labels},
		{"-labels", md},
	} {
		if err := run(args, &out); err == nil {
			t.Errorf("%v seharusnya gagal", args)
		}
	}
}
