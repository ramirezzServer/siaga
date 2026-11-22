// Package dyfi membaca data "Did You Feel It?" (DYFI) USGS untuk kalibrasi
// radius dirasakan (make felt-radius, ADR 0026): daftar kejadian dari
// layanan FDSN event (GeoJSON, producttype=dyfi), produk DYFI dari detail
// kejadian, dan berkas cdi_geo.txt berisi intensitas per sel UTM 10 km.
package dyfi

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrFormat menandai jawaban USGS yang bentuknya tidak dikenali.
var ErrFormat = errors.New("format DYFI USGS tidak dikenali")

// Event adalah satu kejadian di daftar FDSN (origin pilihan USGS).
type Event struct {
	ID            string
	Time          time.Time
	Latitude      float64
	Longitude     float64
	DepthKm       float64
	Magnitude     float64
	MagnitudeType string
	Place         string
	// Felt adalah jumlah laporan DYFI menurut ringkasan kejadian.
	Felt int
}

// ParseList membaca FeatureCollection FDSN event (format=geojson).
func ParseList(body []byte) ([]Event, error) {
	var doc struct {
		Type     string `json:"type"`
		Features []struct {
			ID         string `json:"id"`
			Properties struct {
				Mag     *float64 `json:"mag"`
				MagType string   `json:"magType"`
				Place   string   `json:"place"`
				Time    *int64   `json:"time"`
				Felt    *int     `json:"felt"`
			} `json:"properties"`
			Geometry *struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: daftar: %w", ErrFormat, err)
	}
	if doc.Type != "FeatureCollection" {
		return nil, fmt.Errorf("%w: daftar bukan FeatureCollection", ErrFormat)
	}
	out := make([]Event, 0, len(doc.Features))
	for i, f := range doc.Features {
		p := f.Properties
		if f.ID == "" || p.Mag == nil || p.Time == nil || f.Geometry == nil || len(f.Geometry.Coordinates) < 3 {
			return nil, fmt.Errorf("%w: kejadian #%d (%q) tanpa ID, waktu, magnitudo, atau koordinat", ErrFormat, i, f.ID)
		}
		c := f.Geometry.Coordinates
		e := Event{
			ID: f.ID, Time: time.UnixMilli(*p.Time).UTC(), Longitude: c[0], Latitude: c[1], DepthKm: c[2],
			Magnitude: *p.Mag, MagnitudeType: p.MagType, Place: p.Place,
		}
		if p.Felt != nil {
			e.Felt = *p.Felt
		}
		out = append(out, e)
	}
	return out, nil
}

// Content adalah satu berkas produk.
type Content struct {
	ContentType  string `json:"contentType,omitempty"`
	LastModified int64  `json:"lastModified,omitempty"`
	Length       int64  `json:"length,omitempty"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256,omitempty"`
}

// Product adalah produk DYFI terpilih (yang pertama di detail kejadian, yaitu
// versi berlaku), dipangkas menjadi keterangan produk dan berkas cdi_geo.txt.
// Bentuk ini yang disimpan di cache (detail kejadian utuh ±30–60 KB).
type Product struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Code       string             `json:"code"`
	Source     string             `json:"source"`
	UpdateTime int64              `json:"updateTime"`
	Status     string             `json:"status"`
	Properties map[string]string  `json:"properties"`
	Contents   map[string]Content `json:"contents"`
}

// CDIFile adalah nama berkas intensitas per sel 10 km.
const CDIFile = "cdi_geo.txt"

// CDIURL adalah alamat cdi_geo.txt, "" bila produk tidak memilikinya.
func (p Product) CDIURL() string { return p.Contents[CDIFile].URL }

// Responses adalah jumlah laporan menurut produk DYFI (-1 bila tidak ada).
func (p Product) Responses() int {
	for _, k := range []string{"num-responses", "numResp"} {
		if n, err := strconv.Atoi(strings.TrimSpace(p.Properties[k])); err == nil {
			return n
		}
	}
	return -1
}

// ParseDetail mengambil produk DYFI dari detail kejadian FDSN
// (eventid=…&format=geojson). ok=false bila kejadian tidak punya produk DYFI.
func ParseDetail(body []byte) (Product, bool, error) {
	var doc struct {
		Type       string `json:"type"`
		Properties struct {
			Products map[string][]Product `json:"products"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Product{}, false, fmt.Errorf("%w: detail: %w", ErrFormat, err)
	}
	if doc.Type != "Feature" {
		return Product{}, false, fmt.Errorf("%w: detail bukan Feature", ErrFormat)
	}
	list := doc.Properties.Products["dyfi"]
	if len(list) == 0 {
		return Product{}, false, nil
	}
	return Trim(list[0]), true, nil
}

// Trim membuang semua berkas produk kecuali cdi_geo.txt.
func Trim(p Product) Product {
	c, ok := p.Contents[CDIFile]
	p.Contents = map[string]Content{}
	if ok {
		p.Contents[CDIFile] = c
	}
	return p
}

// ParseProduct membaca produk yang sudah dipangkas (isi cache). "null"
// berarti kejadian tanpa produk DYFI.
func ParseProduct(body []byte) (Product, bool, error) {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return Product{}, false, nil
	}
	var p Product
	if err := json.Unmarshal(body, &p); err != nil {
		return Product{}, false, fmt.Errorf("%w: produk: %w", ErrFormat, err)
	}
	if p.Type != "dyfi" {
		return Product{}, false, fmt.Errorf("%w: produk bertipe %q, bukan dyfi", ErrFormat, p.Type)
	}
	return p, true, nil
}

// Cell adalah satu sel di cdi_geo.txt.
type Cell struct {
	Box       string // misal "UTM:(48M 072 923 10000)"; berkas lama memakai kode pos/lokasi
	CDI       float64
	Responses int
	// DistanceKm adalah jarak sumber ke pusat sel menurut USGS: hiposenter
	// (berkas baru) atau episenter (berkas lama, Epicentral = true).
	DistanceKm float64
	Epicentral bool
	Latitude   float64
	Longitude  float64
	Suspect    bool
	City       string
	State      string
}

// Nama kolom cdi_geo.txt (baris "# Columns:"). Tiga bentuk yang dikenal:
// dengan dan tanpa "Standard deviation", dan bentuk lama dengan kolom
// "ZIP/Location" dan "Epicentral distance".
const (
	colBox, colZIP    = "Geocoded box", "ZIP/Location"
	colCDI            = "CDI"
	colResponses      = "No. of responses"
	colHypo, colEpi   = "Hypocentral distance", "Epicentral distance"
	colLat, colLon    = "Latitude", "Longitude"
	colSuspect        = "Suspect?"
	colCity, colState = "City", "State"
	columnsPrefix     = "# Columns: "
)

// ParseCells membaca cdi_geo.txt. Berkas tanpa baris data berarti tidak ada sel.
func ParseCells(body []byte) ([]Cell, error) {
	var out []Cell
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var col map[string]int
	for line := 1; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: cdi_geo baris %d: %w", ErrFormat, line, err)
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if strings.HasPrefix(rec[0], "#") {
			if first, ok := strings.CutPrefix(rec[0], columnsPrefix); ok {
				if col, err = columns(append([]string{first}, rec[1:]...)); err != nil {
					return nil, err
				}
			}
			continue
		}
		if col == nil {
			return nil, fmt.Errorf("%w: cdi_geo tanpa baris # Columns", ErrFormat)
		}
		if len(rec) != len(col) {
			return nil, fmt.Errorf("%w: cdi_geo baris %d berisi %d kolom, bukan %d", ErrFormat, line, len(rec), len(col))
		}
		c, err := parseCell(rec, col)
		if err != nil {
			return nil, fmt.Errorf("%w: cdi_geo baris %d: %w", ErrFormat, line, err)
		}
		out = append(out, c)
	}
	return out, nil
}

func columns(names []string) (map[string]int, error) {
	col := map[string]int{}
	for i, n := range names {
		col[strings.TrimSpace(n)] = i
	}
	has := func(n string) bool { _, ok := col[n]; return ok }
	missing := []string{}
	for _, need := range [][]string{{colBox, colZIP}, {colCDI}, {colResponses}, {colHypo, colEpi}, {colLat}, {colLon}, {colSuspect}, {colCity}, {colState}} {
		ok := false
		for _, n := range need {
			ok = ok || has(n)
		}
		if !ok {
			missing = append(missing, strings.Join(need, "/"))
		}
	}
	if len(missing) > 0 || len(col) != len(names) {
		return nil, fmt.Errorf("%w: kolom cdi_geo tidak dikenal %q (tidak ada: %s)", ErrFormat, strings.Join(names, ","), strings.Join(missing, ", "))
	}
	return col, nil
}

func parseCell(rec []string, col map[string]int) (Cell, error) {
	var errs []error
	field := func(names ...string) (string, string) {
		for _, n := range names {
			if i, ok := col[n]; ok {
				return n, strings.TrimSpace(rec[i])
			}
		}
		return "", ""
	}
	num := func(names ...string) float64 {
		n, s := field(names...)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			errs = append(errs, fmt.Errorf("%s %q bukan angka", n, s))
		}
		return v
	}
	_, box := field(colBox, colZIP)
	_, city := field(colCity)
	_, state := field(colState)
	_, epi := col[colEpi]
	c := Cell{
		Box: box, CDI: num(colCDI), DistanceKm: num(colHypo, colEpi), Epicentral: epi && !hasKey(col, colHypo),
		Latitude: num(colLat), Longitude: num(colLon), City: city, State: state,
	}
	n := num(colResponses)
	c.Responses = int(n)
	c.Suspect = num(colSuspect) != 0
	switch {
	case len(errs) > 0:
	case c.CDI < 1 || c.CDI > 12:
		errs = append(errs, fmt.Errorf("CDI %v di luar 1–12", c.CDI))
	case n < 1 || n != math.Trunc(n):
		errs = append(errs, fmt.Errorf("jumlah laporan %v tidak sah", n))
	case c.DistanceKm < 0:
		errs = append(errs, fmt.Errorf("jarak %v negatif", c.DistanceKm))
	case c.Latitude < -90 || c.Latitude > 90 || c.Longitude < -180 || c.Longitude > 180:
		errs = append(errs, errors.New("koordinat sel di luar rentang"))
	}
	return c, errors.Join(errs...)
}

func hasKey(m map[string]int, k string) bool { _, ok := m[k]; return ok }
