// Package calibration membaca ambang banjir yang disematkan ke binary:
// salinan persis docs/calibration/ambang-banjir-32.csv (make flood-threshold)
// dan ambang-hujan-sub-das.csv (make rain-threshold), disalin dengan
// `make calibration-copy` dan diperiksa test. Ambang ikut versi image, jadi
// kalibrasi ulang tahunan cukup commit hasil alat lalu deploy; geo-processor
// menyelaraskan schema ref saat start (ADR 0021).
package calibration

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/flood"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

//go:embed data/*.csv
var data embed.FS

// Nama file di data/, sama dengan nama di docs/calibration.
const (
	DischargeFile = "ambang-banjir-32.csv"
	RainfallFile  = "ambang-hujan-sub-das.csv"
)

var (
	period    = regexp.MustCompile(`([0-9]{4}-[0-9]{2}-[0-9]{2})\.\.([0-9]{4}-[0-9]{2}-[0-9]{2})`)
	countLine = regexp.MustCompile(`^#\s*([0-9]+) (titik|baris)\s*$`)
)

// Load membaca kedua daftar ambang yang disematkan dan menerapkan policy.
func Load(policy flood.Policy) (flood.Calibration, error) {
	d, err := data.ReadFile("data/" + DischargeFile)
	if err != nil {
		return flood.Calibration{}, err
	}
	r, err := data.ReadFile("data/" + RainfallFile)
	if err != nil {
		return flood.Calibration{}, err
	}
	return Parse(policy, d, r)
}

// Parse membaca isi kedua file ambang.
func Parse(policy flood.Policy, discharge, rainfall []byte) (flood.Calibration, error) {
	if err := policy.Validate(); err != nil {
		return flood.Calibration{}, err
	}
	var c flood.Calibration
	var err error
	if c.Discharge, c.DischargeSource, err = parseDischarge(policy, discharge); err != nil {
		return flood.Calibration{}, fmt.Errorf("%s: %w", DischargeFile, err)
	}
	if c.Rainfall, c.RainfallSource, err = parseRainfall(policy, rainfall); err != nil {
		return flood.Calibration{}, fmt.Errorf("%s: %w", RainfallFile, err)
	}
	return c, c.Validate()
}

// table memisahkan komentar dan isi CSV, membaca periode klimatologi dari
// komentar, dan memeriksa jumlah baris terhadap "# N titik|baris".
func table(b []byte, header string) ([][]string, flood.Source, error) {
	src := flood.Source{SHA256: sha256.Sum256(b)}
	var body bytes.Buffer
	want := -1
	for line := range strings.SplitSeq(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case countLine.MatchString(t):
			want, _ = strconv.Atoi(countLine.FindStringSubmatch(t)[1])
		case strings.HasPrefix(t, "#"):
			if m := period.FindStringSubmatch(t); m != nil && src.Period.From.IsZero() {
				from, err1 := time.Parse(time.DateOnly, m[1])
				to, err2 := time.Parse(time.DateOnly, m[2])
				if err := errors.Join(err1, err2); err != nil {
					return nil, src, fmt.Errorf("%w: periode %q: %w", flood.ErrInvalid, m[0], err)
				}
				src.Period = flood.Period{From: from, To: to}
			}
		case t != "":
			body.WriteString(t)
			body.WriteByte('\n')
		}
	}
	cr := csv.NewReader(&body)
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, src, fmt.Errorf("%w: CSV: %w", flood.ErrInvalid, err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != header {
		return nil, src, fmt.Errorf("%w: header harus %s", flood.ErrInvalid, header)
	}
	if want != len(recs)-1 {
		return nil, src, fmt.Errorf("%w: %d baris data, komentar menyebut %d", flood.ErrInvalid, len(recs)-1, want)
	}
	if src.Period.From.IsZero() {
		return nil, src, fmt.Errorf("%w: periode klimatologi tidak ada di komentar", flood.ErrInvalid)
	}
	return recs[1:], src, nil
}

func floats(rec []string, from, n int) ([]float64, error) {
	out := make([]float64, n)
	var errs []error
	for i := range n {
		v, err := strconv.ParseFloat(rec[from+i], 64)
		out[i] = v
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

const dischargeHeader = "id,sungai,nama,lat,lon,p50,p80,p90,p98,p99_5,maks_tahunan_median,hari_berisi,rasio_p98_seamless"

func parseDischarge(policy flood.Policy, b []byte) (map[string]flood.DischargeThreshold, flood.Source, error) {
	recs, src, err := table(b, dischargeHeader)
	if err != nil {
		return nil, src, err
	}
	out := make(map[string]flood.DischargeThreshold, len(recs))
	for _, rec := range recs {
		v, err := floats(rec, 3, 7)
		ratio, err2 := strconv.ParseFloat(rec[12], 64)
		if err := errors.Join(err, err2); err != nil {
			return nil, src, fmt.Errorf("%w: %s: %w", flood.ErrInvalid, rec[0], err)
		}
		th, err := policy.Discharge(flood.DischargeRow{
			SiteID: "river:" + rec[0], River: rec[1], Name: rec[2], Cell: series.Point{Lat: v[0], Lon: v[1]},
			P50: v[2], Climatology: flood.Set{v[3], v[4], v[5], v[6]}, SeamlessRatio: ratio,
		})
		if err != nil {
			return nil, src, err
		}
		if _, dup := out[th.SiteID]; dup {
			return nil, src, fmt.Errorf("%w: titik %s ganda", flood.ErrInvalid, th.SiteID)
		}
		out[th.SiteID] = th
	}
	return out, src, nil
}

const rainfallHeader = "subdas,nama,jam,p50,p80,p90,p98,p99_5,hari_berisi"

func parseRainfall(policy flood.Policy, b []byte) (map[string]flood.RainfallThreshold, flood.Source, error) {
	recs, src, err := table(b, rainfallHeader)
	if err != nil {
		return nil, src, err
	}
	rows := make([]flood.RainfallRow, len(recs))
	for i, rec := range recs {
		hours, err1 := strconv.Atoi(rec[2])
		v, err2 := floats(rec, 3, 5)
		if err := errors.Join(err1, err2); err != nil {
			return nil, src, fmt.Errorf("%w: %s: %w", flood.ErrInvalid, rec[0], err)
		}
		rows[i] = flood.RainfallRow{
			SiteID: "catchment:" + rec[0], Name: rec[1], Hours: hours, P50: v[0], Levels: flood.Set{v[1], v[2], v[3], v[4]},
		}
	}
	out, err := policy.Rainfall(rows)
	return out, src, err
}
