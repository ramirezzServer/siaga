package cahyadsn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadNames(t *testing.T) {
	got, err := ReadNames("testdata/wilayah.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[0].Code != "32" || got[3].Name != "Dago" || got[4].Name != "Regapu’u" || got[5].Name != "Ma'rang" {
		t.Fatalf("%+v", got)
	}
	if _, err := ReadNames("testdata/tidak-ada.sql"); err == nil {
		t.Fatal("berkas hilang harus galat")
	}
	empty := filepath.Join(t.TempDir(), "kosong.sql")
	if err := os.WriteFile(empty, []byte("-- kosong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNames(empty); err == nil {
		t.Fatal("tanpa baris harus galat")
	}
	broken := filepath.Join(t.TempDir(), "rusak.sql")
	if err := os.WriteFile(broken, []byte("INSERT INTO wilayah (kode, nama) VALUES ('32','Jawa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNames(broken); err == nil {
		t.Fatal("dump rusak harus galat")
	}
}

func TestReadBoundaries(t *testing.T) {
	b, st, err := ReadBoundaries("testdata/db")
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 3 || len(b) != st.Rows || b["32.73.02"] == nil || b["32.73"] == nil || st.Invalid != 0 {
		t.Fatalf("%+v %d", st, len(b))
	}
	if !b["32.73.02"].Contains(-6.89, 107.61) {
		t.Fatal("Coblong harus memuat titiknya")
	}

	root := t.TempDir()
	for dir, body := range map[string]string{
		"prov": "INSERT INTO wilayah_boundaries(kode,nama,lat,lng,path) VALUES ('32','A',0,0,'[[[-7,107],[-7,108],[-6,108],[-7,107]]]');",
		"kab":  "INSERT INTO wilayah_boundaries(kode,nama,lat,lng,path) VALUES ('32.01','B',0,0,'bukan json'),('32.02','C',0,0,'[[[-7,107],[-7,108],[-6,108]]]');",
		"kec":  "INSERT INTO wilayah_boundaries(kode,nama,lat,lng,path) VALUES ('32.01.01','D',0,0,'[[[-7,107],[-7,108],[-6,108],[-7,107]]]');",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "x.sql"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, st, err = ReadBoundaries(root)
	if err != nil || st.Invalid != 1 || st.Repaired != 1 || len(b) != 3 {
		t.Fatalf("%+v %d %v", st, len(b), err)
	}
	if err := os.Remove(filepath.Join(root, "kec", "x.sql")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadBoundaries(root); err == nil {
		t.Fatal("folder kec kosong harus galat")
	}
	if err := os.WriteFile(filepath.Join(root, "kec", "y.sql"), []byte("INSERT INTO wilayah_boundaries(kode) VALUES ('"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadBoundaries(root); err == nil {
		t.Fatal("dump rusak harus galat")
	}
}
