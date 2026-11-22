package dyfi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Nilai asli kejadian Cianjur 2022 (us7000ir9t) dari layanan FDSN USGS,
// dipangkas.
const listJSON = `{"type":"FeatureCollection","features":[
{"type":"Feature","properties":{"mag":5.6,"place":"11 km NE of Sukabumi, Indonesia","time":1669011667066,"felt":169,"magType":"mww"},
 "geometry":{"type":"Point","coordinates":[106.9968,-6.836,10]},"id":"us7000ir9t"},
{"type":"Feature","properties":{"mag":4.6,"place":"Banda Sea","time":1100000000000,"felt":null,"magType":"mb"},
 "geometry":{"type":"Point","coordinates":[124.868,-8.152,10]},"id":"usp000d85g"}]}`

const detailJSON = `{"type":"Feature","properties":{"products":{"origin":[{"type":"origin"}],"dyfi":[
{"id":"urn:usgs-product:us:dyfi:us7000ir9t:1776587923910","type":"dyfi","code":"us7000ir9t","source":"us","updateTime":1776587923910,"status":"UPDATE",
 "properties":{"maxmmi":"5.1","num-responses":"169","numResp":"169"},
 "contents":{"cdi_geo.txt":{"contentType":"text/plain","lastModified":1776587923000,"length":2995,"url":"https://earthquake.usgs.gov/product/dyfi/us7000ir9t/us/1776587923910/cdi_geo.txt","sha256":"BesQ"},
  "cdi_zip.txt":{"url":"https://example.invalid/cdi_zip.txt"}}},
{"type":"dyfi","code":"lama","contents":{}}]}}}`

const cdiNew = `# Columns: Geocoded box,CDI,No. of responses,Hypocentral distance,Latitude,Longitude,Suspect?,Standard deviation,City,State
"UTM:(48M 072 923 10000)",6.5,1,14,-6.9165,107.0363,0,0.330,"Sukabumi",""
"UTM:(48M 070 930 10000)",5.1,5,63,-6.2843,106.8530,0,0.293,"Depok",""
`

const cdiNoStdDev = `# Columns: Geocoded box,CDI,No. of responses,Hypocentral distance,Latitude,Longitude,Suspect?,City,State
"UTM:(51L 078 905 10000)",4.8,3,90,-8.5404,125.5889,1,"Dili",""
`

const cdiOld = `# Columns: ZIP/Location,CDI,No. of responses,Epicentral distance,Latitude,Longitude,Suspect?,City,State
"UTM:(47N 009 061 10000)",3.8,5,414,5.5526,95.3458,0,"UTM:(47N 009 061 10000)",""
`

func TestParseList(t *testing.T) {
	evs, err := ParseList([]byte(listJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("%d kejadian", len(evs))
	}
	e := evs[0]
	if e.ID != "us7000ir9t" || e.Magnitude != 5.6 || e.MagnitudeType != "mww" || e.Latitude != -6.836 || e.Longitude != 106.9968 ||
		e.DepthKm != 10 || e.Felt != 169 || !e.Time.Equal(time.UnixMilli(1669011667066)) || e.Time.Location() != time.UTC {
		t.Fatalf("%+v", e)
	}
	if evs[1].Felt != 0 {
		t.Fatalf("felt null: %+v", evs[1])
	}
	for name, body := range map[string]string{
		"bukan JSON":    "<html>",
		"bukan koleksi": `{"type":"Feature"}`,
		"tanpa mag":     `{"type":"FeatureCollection","features":[{"id":"a","properties":{"time":1},"geometry":{"coordinates":[1,2,3]}}]}`,
		"koordinat":     `{"type":"FeatureCollection","features":[{"id":"a","properties":{"time":1,"mag":1},"geometry":{"coordinates":[1,2]}}]}`,
	} {
		if _, err := ParseList([]byte(body)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseDetailTrimsToCDI(t *testing.T) {
	p, ok, err := ParseDetail([]byte(detailJSON))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if p.Code != "us7000ir9t" || p.UpdateTime != 1776587923910 || len(p.Contents) != 1 || p.Responses() != 169 ||
		p.CDIURL() != "https://earthquake.usgs.gov/product/dyfi/us7000ir9t/us/1776587923910/cdi_geo.txt" {
		t.Fatalf("%+v", p)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	back, ok, err := ParseProduct(b)
	if err != nil || !ok || back.CDIURL() != p.CDIURL() || back.Contents[CDIFile].SHA256 != "BesQ" {
		t.Fatalf("bolak-balik: %+v %v %v", back, ok, err)
	}
	if _, ok, err := ParseDetail([]byte(`{"type":"Feature","properties":{"products":{"origin":[]}}}`)); ok || err != nil {
		t.Fatal("tanpa dyfi harus ok=false tanpa galat")
	}
	if _, _, err := ParseDetail([]byte(`{"type":"FeatureCollection"}`)); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	if _, _, err := ParseDetail([]byte(`[`)); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	if _, ok, err := ParseProduct([]byte(" null\n")); ok || err != nil {
		t.Fatal("null = tanpa produk")
	}
	if _, _, err := ParseProduct([]byte(`{"type":"shakemap"}`)); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	if _, _, err := ParseProduct([]byte(`{`)); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	if (Product{}).Responses() != -1 || Trim(Product{Contents: map[string]Content{"x": {}}}).Contents == nil {
		t.Fatal("produk kosong")
	}
}

func TestParseCellsFormats(t *testing.T) {
	cells, err := ParseCells([]byte(cdiNew))
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 || cells[0].CDI != 6.5 || cells[0].DistanceKm != 14 || cells[0].Epicentral || cells[1].Responses != 5 ||
		cells[1].City != "Depok" || cells[0].Box != "UTM:(48M 072 923 10000)" {
		t.Fatalf("%+v", cells)
	}
	cells, err = ParseCells([]byte(cdiNoStdDev))
	if err != nil || len(cells) != 1 || !cells[0].Suspect || cells[0].DistanceKm != 90 {
		t.Fatalf("%+v %v", cells, err)
	}
	cells, err = ParseCells([]byte(cdiOld))
	if err != nil || len(cells) != 1 || !cells[0].Epicentral || cells[0].DistanceKm != 414 {
		t.Fatalf("berkas lama: %+v %v", cells, err)
	}
	if cells, err := ParseCells(nil); err != nil || len(cells) != 0 {
		t.Fatal("berkas kosong")
	}
	if cells, err := ParseCells([]byte("# Columns: Geocoded box,CDI,No. of responses,Hypocentral distance,Latitude,Longitude,Suspect?,City,State\n\n")); err != nil || len(cells) != 0 {
		t.Fatal("hanya header", err)
	}
}

func TestParseCellsRejects(t *testing.T) {
	head := strings.SplitN(cdiNew, "\n", 2)[0] + "\n"
	for name, body := range map[string]string{
		"tanpa header":    `"UTM",2,1,10,1,1,0,0.3,"A",""` + "\n",
		"kolom berubah":   "# Columns: Box,CDI\n",
		"jumlah kolom":    head + `"UTM",2,1` + "\n",
		"CDI":             head + `"UTM",0.5,1,10,1,1,0,0.3,"A",""` + "\n",
		"laporan":         head + `"UTM",2,0,10,1,1,0,0.3,"A",""` + "\n",
		"laporan pecahan": head + `"UTM",2,1.5,10,1,1,0,0.3,"A",""` + "\n",
		"jarak":           head + `"UTM",2,1,-1,1,1,0,0.3,"A",""` + "\n",
		"koordinat":       head + `"UTM",2,1,10,91,1,0,0.3,"A",""` + "\n",
		"bukan angka":     head + `"UTM",x,1,10,1,1,0,0.3,"A",""` + "\n",
		"kutip":           head + `"UTM",2,1,10,1,1,0,0.3,"A"x"",""` + "\n\"",
	} {
		if _, err := ParseCells([]byte(body)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Properti: sel yang lolos selalu sah, dan ParseCells tidak pernah panik.
func FuzzParseCells(f *testing.F) {
	for _, s := range []string{cdiNew, cdiNoStdDev, cdiOld, ""} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		cells, err := ParseCells(b)
		if err != nil {
			if !errors.Is(err, ErrFormat) {
				t.Fatalf("galat tanpa ErrFormat: %v", err)
			}
			return
		}
		for _, c := range cells {
			if c.CDI < 1 || c.CDI > 12 || c.Responses < 1 || c.DistanceKm < 0 {
				t.Fatalf("sel tidak sah lolos: %+v", c)
			}
		}
	})
}
