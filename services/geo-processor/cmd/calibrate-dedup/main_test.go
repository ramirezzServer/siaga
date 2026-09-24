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
