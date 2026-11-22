package catalog

import (
	"errors"
	"math"
	"strings"
	"testing"
)

const dyfiCSV = `# komentar
usgs_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,tipe_magnitudo,laporan_kejadian,sel,cdi,laporan,jarak_km,jenis_jarak,lintang_sel,bujur_sel,mencurigakan,kota
us7000ir9t,2022-11-21T06:21:07.066Z,-6.8360,106.9968,10.0,5.6,mww,169,UTM:(48M 072 923 10000),6.5,1,14,hiposenter,-6.9165,107.0363,0,Sukabumi
usp000j7nn,2004-12-26T00:58:53.450Z,3.2950,95.9820,30.0,9.1,mw,5,UTM:(47N 009 061 10000),3.8,5,40,episenter,5.5526,95.3458,1,"Banda Aceh, Aceh"
`

const bmkgCSV = `# komentar
bmkg_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,wilayah,dirasakan,pertama_terlihat_utc,terakhir_terlihat_utc,versi_dirasakan
20260925080442,2026-09-25T08:04:42Z,-6.90,107.11,7,2.4,Darat,"II - III Kota Cianjur, II Cibeber",2026-09-25T09:00:00Z,2026-09-25T10:00:00Z,2
`

func TestReadDYFICells(t *testing.T) {
	cells, err := ReadDYFICells(strings.NewReader(dyfiCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 || cells[0].ID != "us7000ir9t" || cells[0].CDI != 6.5 || cells[0].HypocentralKm != 14 || cells[0].Suspect ||
		cells[0].Magnitude != 5.6 || cells[0].MagnitudeType != "mww" || cells[0].Place != "Sukabumi" {
		t.Fatalf("%+v", cells[0])
	}
	if math.Abs(cells[1].HypocentralKm-50) > 1e-9 || !cells[1].Suspect {
		t.Fatalf("jarak episenter harus jadi hiposenter √(40²+30²): %+v", cells[1])
	}
	for name, body := range map[string]string{
		"header":       "a,b\n",
		"jenis":        strings.Replace(dyfiCSV, ",hiposenter,", ",lain,", 1),
		"cdi":          strings.Replace(dyfiCSV, ",6.5,1,14,", ",0.5,1,14,", 1),
		"angka":        strings.Replace(dyfiCSV, ",6.5,1,14,", ",x,1,14,", 1),
		"waktu":        strings.Replace(dyfiCSV, "2022-11-21T06:21:07.066Z", "kemarin", 1),
		"lintang":      strings.Replace(dyfiCSV, "-6.8360", "-96", 1),
		"magnitudo":    strings.Replace(dyfiCSV, ",5.6,mww", ",15,mww", 1),
		"id":           strings.Replace(dyfiCSV, "us7000ir9t,", ",", 1),
		"kosong":       "",
		"kolom kurang": "usgs_id,waktu_utc,lintang,bujur,kedalaman_km,magnitudo,tipe_magnitudo,laporan_kejadian,sel,cdi,laporan,jarak_km,jenis_jarak,lintang_sel,bujur_sel,mencurigakan,kota\nus1,2022\n",
	} {
		if _, err := ReadDYFICells(strings.NewReader(body)); err == nil {
			t.Errorf("%s: harus galat", name)
		}
	}
}

func TestReadBMKGFelt(t *testing.T) {
	got, err := ReadBMKGFelt(strings.NewReader(bmkgCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "20260925080442" || got[0].Felt != "II - III Kota Cianjur, II Cibeber" || got[0].DepthKm != 7 || got[0].Region != "Darat" {
		t.Fatalf("%+v", got)
	}
	dup := bmkgCSV + strings.SplitN(bmkgCSV, "\n", 4)[2] + "\n"
	if _, err := ReadBMKGFelt(strings.NewReader(dup)); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}

func TestReadAliases(t *testing.T) {
	got, err := ReadAliases(strings.NewReader("# komentar\nnama,kode,alasan\nAnyer,36.04.30,\"ejaan lama, Serang\"\n"))
	if err != nil || len(got) != 1 || got[0].Code != "36.04.30" || got[0].Reason != "ejaan lama, Serang" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ReadAliases(strings.NewReader("nama,kode\n")); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
}
