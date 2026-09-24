// Package sitelist menyediakan titik pantau keluaran model grid per
// provinsi: simpul grid 0,25° untuk cuaca dan kualitas udara, dan titik pantau
// sungai untuk debit. Keduanya disematkan ke binary seperti daftar adm4
// (regionlist), jadi ingest tidak membaca schema layanan lain.
//
// Daftar grid dibuat `make grid-list` dari data batas wilayah yang sama dengan
// ref.region. Daftar sungai berasal dari PRD (bagian Sungai yang dipantau)
// dengan koordinat pusat sel GloFAS terpilih (docs/calibration/titik-sungai.md).
package sitelist

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

//go:embed data/*
var data embed.FS

// ErrUnknownProvince berarti belum ada daftar untuk provinsi itu.
var ErrUnknownProvince = errors.New("daftar titik pantau provinsi tidak ada")

// GridStep adalah jarak simpul grid dalam derajat (dokumen arsitektur: 0,25°,
// karena model CAMS global beresolusi sekitar 0,4°).
const GridStep = 0.25

var (
	countLine = regexp.MustCompile(`^#\s*([0-9]+) titik\s*$`)
	slug      = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// Grid mengembalikan simpul grid satu provinsi, urut seperti di file.
func Grid(province string) ([]series.Site, error) {
	b, err := data.ReadFile("data/grid025_" + province + ".txt")
	if err != nil {
		return nil, fmt.Errorf("%w: grid %q", ErrUnknownProvince, province)
	}
	return ParseGrid(b)
}

// Rivers mengembalikan titik pantau sungai satu provinsi, urut seperti di file.
func Rivers(province string) ([]series.Site, error) {
	b, err := data.ReadFile("data/rivers_" + province + ".csv")
	if err != nil {
		return nil, fmt.Errorf("%w: sungai %q", ErrUnknownProvince, province)
	}
	return ParseRivers(b)
}

// ParseGrid membaca daftar simpul "lintang,bujur" satu per baris. Baris
// komentar "# N titik" wajib ada dan cocok dengan jumlah simpul, supaya file
// yang terpotong ketahuan. Simpul harus kelipatan GridStep dan tidak ganda.
func ParseGrid(b []byte) ([]series.Site, error) {
	body, want, err := stripHeader(b)
	if err != nil {
		return nil, err
	}
	var out []series.Site
	seen := map[string]bool{}
	for _, l := range body {
		lat, lon, ok := strings.Cut(l.text, ",")
		if !ok {
			return nil, fmt.Errorf("baris %d: %q bukan lintang,bujur", l.n, l.text)
		}
		p, err := point(lat, lon)
		if err != nil {
			return nil, fmt.Errorf("baris %d: %w", l.n, err)
		}
		if !onGrid(p.Lat) || !onGrid(p.Lon) {
			return nil, fmt.Errorf("baris %d: (%v, %v) bukan simpul grid %v°", l.n, p.Lat, p.Lon, GridStep)
		}
		id := series.GridID(p)
		if seen[id] {
			return nil, fmt.Errorf("baris %d: simpul %s ganda", l.n, id)
		}
		seen[id] = true
		out = append(out, series.Site{ID: id, Requested: p})
	}
	if want != len(out) {
		return nil, fmt.Errorf("daftar grid berisi %d titik, header menyebut %d", len(out), want)
	}
	return out, nil
}

// ParseRivers membaca CSV id,sungai,nama,lat,lon dengan header kolom dan
// komentar "# N titik" seperti ParseGrid.
func ParseRivers(b []byte) ([]series.Site, error) {
	body, want, err := stripHeader(b)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for _, l := range body {
		text.WriteString(l.text)
		text.WriteByte('\n')
	}
	r := csv.NewReader(strings.NewReader(text.String()))
	r.FieldsPerRecord = 5
	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("header CSV sungai: %w", err)
	}
	if strings.Join(head, ",") != "id,sungai,nama,lat,lon" {
		return nil, fmt.Errorf("header CSV sungai %q, harus id,sungai,nama,lat,lon", strings.Join(head, ","))
	}
	var out []series.Site
	seen := map[string]bool{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV sungai: %w", err)
		}
		row := len(out) + 1
		if !slug.MatchString(rec[0]) {
			return nil, fmt.Errorf("titik %d: id %q bukan slug", row, rec[0])
		}
		p, err := point(rec[3], rec[4])
		if err != nil {
			return nil, fmt.Errorf("titik %s: %w", rec[0], err)
		}
		s := series.Site{ID: series.RiverID(rec[0]), River: strings.TrimSpace(rec[1]), Name: strings.TrimSpace(rec[2]), Requested: p}
		switch {
		case s.River == "" || s.Name == "":
			return nil, fmt.Errorf("titik %s: sungai dan nama wajib diisi", rec[0])
		case seen[s.ID]:
			return nil, fmt.Errorf("titik %s ganda", rec[0])
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	if want != len(out) {
		return nil, fmt.Errorf("daftar sungai berisi %d titik, header menyebut %d", len(out), want)
	}
	return out, nil
}

type textLine struct {
	n    int
	text string
}

// stripHeader membuang baris kosong dan komentar, dan membaca "# N titik".
func stripHeader(b []byte) ([]textLine, int, error) {
	var out []textLine
	want := -1
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		t := strings.TrimSpace(sc.Text())
		if m := countLine.FindStringSubmatch(t); m != nil {
			want, _ = strconv.Atoi(m[1])
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, textLine{n: n, text: t})
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}
	if want < 0 {
		return nil, 0, errors.New(`baris komentar "# N titik" tidak ada`)
	}
	return out, want, nil
}

func point(lat, lon string) (series.LatLon, error) {
	la, err1 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	lo, err2 := strconv.ParseFloat(strings.TrimSpace(lon), 64)
	if err := errors.Join(err1, err2); err != nil {
		return series.LatLon{}, fmt.Errorf("koordinat %q,%q: %w", lat, lon, err)
	}
	p := series.LatLon{Lat: la, Lon: lo}
	if !series.InIndonesia(p) {
		return series.LatLon{}, fmt.Errorf("(%v, %v) di luar Indonesia", la, lo)
	}
	return p, nil
}

func onGrid(v float64) bool {
	k := v / GridStep
	return math.Abs(k-math.Round(k)) < 1e-9
}
