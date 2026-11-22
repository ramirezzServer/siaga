package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testdata = "../../internal/adapters/cahyadsn/testdata"

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	bmkg := write(t, dir, "bmkg.csv", `bmkg_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,wilayah,dirasakan
20260925080442,2026-09-25T08:04:42Z,-6.95,107.61,7,2.4,Darat,"III Coblong, II Kota Bandung, II Dago Atas"
20260926080442,2026-09-26T08:04:42Z,-6.70,107.61,10,4.4,Darat,"II Coblong, II Kota Lama"
20260927080442,2026-09-27T08:04:42Z,-6.70,107.61,10,3.0,Darat,"II Dago"
`)
	dyfi := write(t, dir, "dyfi.csv", `usgs_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,tipe_magnitudo,laporan_kejadian,sel,cdi,laporan,jarak_km,jenis_jarak,lintang_sel,bujur_sel,mencurigakan,kota
us1,2022-11-21T06:21:07.066Z,-6.8,107,10,5.6,mww,3,UTM,3.1,2,40,hiposenter,-6.9,107.1,0,Bandung
us1,2022-11-21T06:21:07.066Z,-6.8,107,10,5.6,mww,3,UTM,1.0,1,900,hiposenter,-6.9,107.1,0,Jauh
`)
	alias := write(t, dir, "alias.csv", "nama,kode,alasan\nKota Lama,32.73.02,uji\n")
	out, matches := filepath.Join(dir, "r.md"), filepath.Join(dir, "m.csv")
	args := []string{
		"-bmkg", bmkg, "-dyfi", dyfi, "-alias", alias, "-names", testdata + "/wilayah.sql", "-boundaries", testdata + "/db",
		"-out", out, "-out-matches", matches, "-bootstrap", "20",
	}
	var log bytes.Buffer
	if err := run(args, &log); err != nil {
		t.Fatal(err, log.String())
	}
	md, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		t.Fatal(err)
	}
	csv, err := os.ReadFile(filepath.Clean(matches))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"3 gempa dirasakan", "6 penyebutan wilayah (5 cocok", "| Fit τ = 0,50 |", "Dago Atas (1×)"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("laporan tanpa %q:\n%s", want, md)
		}
	}
	if !strings.Contains(string(csv), "20260926080442,4.4,10,2,2,Kota Lama,alias,32.73.02,Coblong,1,32.73.02,") {
		t.Fatalf("%s", csv)
	}
	if !strings.Contains(log.String(), "3 gempa, 6 penyebutan (1 tidak cocok") {
		t.Fatal(log.String())
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	ok := write(t, dir, "ok.csv", "bmkg_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,wilayah,dirasakan\n")
	bad := write(t, dir, "bad.csv", "a,b\n")
	alias := write(t, dir, "alias.csv", "nama,kode,alasan\n")
	dyfi := write(t, dir, "dyfi.csv", "usgs_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,tipe_magnitudo,laporan_kejadian,sel,cdi,laporan,jarak_km,jenis_jarak,lintang_sel,bujur_sel,mencurigakan,kota\n")
	base := func(over ...string) []string {
		a := []string{
			"-bmkg", ok, "-dyfi", dyfi, "-alias", alias, "-names", testdata + "/wilayah.sql", "-boundaries", testdata + "/db",
			"-out", filepath.Join(dir, "r.md"), "-out-matches", filepath.Join(dir, "m.csv"),
		}
		return append(a, over...)
	}
	for name, args := range map[string][]string{
		"argumen":    append(base(), "x"),
		"flag":       base("-tidak-ada"),
		"bmkg":       base("-bmkg", bad),
		"dyfi":       base("-dyfi", bad),
		"alias":      base("-alias", bad),
		"alias kode": base("-alias", write(t, dir, "a2.csv", "nama,kode,alasan\nX,99.99,uji\n")),
		"nama":       base("-names", filepath.Join(dir, "tidak-ada.sql")),
		"batas":      base("-boundaries", dir),
		"tanpa data": base(),
		"berkas":     base("-bmkg", filepath.Join(dir, "tidak-ada.csv")),
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: harus galat", name)
		}
	}
}
