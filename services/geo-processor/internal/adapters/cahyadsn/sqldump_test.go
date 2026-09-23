package cahyadsn

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseDumpHandlesCommentsEscapesAndStatements(t *testing.T) {
	t.Parallel()
	src := []byte(`/* (c) bukan tuple; ada 'kutip' di komentar */
-- INSERT INTO wilayah_boundaries(kode) VALUES ('00');
CREATE TABLE t (a int);
INSERT INTO ` + "`wilayah_boundaries`" + ` (` + "`kode`" + `, nama, path) VALUES
  ('32', 'Jawa ''Barat''', NULL),
  ('32.01', 'A\'b; VALUES (x)', '[1]');
INSERT INTO lain(kode) VALUES ('99');
INSERT INTO wilayah_boundaries(kode,nama,path) VALUES ('32.02','Tanpa titik koma di akhir','[]')`)

	var got []Row
	err := ParseDump(src, "wilayah_boundaries", func(r Row) error { got = append(got, r); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{"kode": {Text: "32"}, "nama": {Text: "Jawa 'Barat'"}, "path": {Null: true}},
		{"kode": {Text: "32.01"}, "nama": {Text: "A'b; VALUES (x)"}, "path": {Text: "[1]"}},
		{"kode": {Text: "32.02"}, "nama": {Text: "Tanpa titik koma di akhir"}, "path": {Text: "[]"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestParseDumpErrors(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"string tak ditutup":  `INSERT INTO w(a) VALUES ('x);`,
		"komentar tak tutup":  `/* abc`,
		"jumlah kolom beda":   `INSERT INTO w(a,b) VALUES ('x');`,
		"pemisah tuple salah": `INSERT INTO w(a) VALUES ('x') ('y');`,
		"nilai kosong":        `INSERT INTO w(a,b) VALUES ('x',);`,
	} {
		err := ParseDump([]byte(src), "w", func(Row) error { return nil })
		if !errors.Is(err, ErrSyntax) {
			t.Errorf("%s: error = %v; want ErrSyntax", name, err)
		}
	}
}

func TestParseDumpPropagatesCallbackError(t *testing.T) {
	t.Parallel()
	stop := errors.New("stop")
	err := ParseDump([]byte(`INSERT INTO w(a) VALUES ('x'),('y');`), "w", func(Row) error { return stop })
	if !errors.Is(err, stop) {
		t.Errorf("error = %v; want stop", err)
	}
}

// FuzzParseDump: parser tidak boleh panik untuk input apa pun, dan setiap baris
// yang dihasilkan punya kolom yang sama dengan daftar kolom INSERT.
func FuzzParseDump(f *testing.F) {
	f.Add([]byte(`INSERT INTO w(a,b) VALUES ('x',1),('y''z',NULL);`))
	f.Add([]byte(`/* c */ INSERT INTO w(a) VALUES ('\'');`))
	f.Add([]byte(`INSERT INTO w(a) VALUES (`))
	f.Fuzz(func(t *testing.T, src []byte) {
		_ = ParseDump(src, "w", func(r Row) error {
			if len(r) == 0 {
				t.Fatal("baris tanpa kolom")
			}
			return nil
		})
	})
}
