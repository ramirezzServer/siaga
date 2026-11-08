package calibration

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
)

// StaticFile adalah saringan sumber panas tetap (make fire-threshold), nama
// sama dengan di docs/calibration.
const StaticFile = "sumber-panas-tetap-32.csv"

const staticHeader = "id,lintang,bujur,radius_km,alasan,sel,deteksi,hari,tahun,bulan,type_vegetasi,type_gunung_api," +
	"type_sumber_tetap,type_lepas_pantai,malam,musim_kebakaran,frp_rata_mw,pertama,terakhir"

var staticCount = regexp.MustCompile(`^#\s*([0-9]+) sumber\s*$`)

// LoadFire membaca saringan sumber panas tetap yang disematkan.
func LoadFire() (fire.Calibration, error) {
	b, err := data.ReadFile("data/" + StaticFile)
	if err != nil {
		return fire.Calibration{}, err
	}
	return ParseFire(b)
}

// ParseFire membaca isi sumber-panas-tetap-32.csv: periode arsip dan jumlah
// sumber dari komentar, lalu satu sumber per baris.
func ParseFire(b []byte) (fire.Calibration, error) {
	c := fire.Calibration{SHA256: sha256.Sum256(b)}
	var body bytes.Buffer
	want := -1
	for line := range strings.SplitSeq(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case staticCount.MatchString(t):
			want, _ = strconv.Atoi(staticCount.FindStringSubmatch(t)[1])
		case strings.HasPrefix(t, "#"):
			if m := period.FindStringSubmatch(t); m != nil && c.Period.From.IsZero() {
				from, err1 := time.Parse(time.DateOnly, m[1])
				to, err2 := time.Parse(time.DateOnly, m[2])
				if err := errors.Join(err1, err2); err != nil {
					return c, fmt.Errorf("%s: %w: periode %q: %w", StaticFile, fire.ErrInvalid, m[0], err)
				}
				c.Period = fire.Period{From: from, To: to}
			}
		case t != "":
			body.WriteString(t)
			body.WriteByte('\n')
		}
	}
	recs, err := csv.NewReader(&body).ReadAll()
	switch {
	case err != nil:
		return c, fmt.Errorf("%s: %w: CSV: %w", StaticFile, fire.ErrInvalid, err)
	case len(recs) == 0 || strings.Join(recs[0], ",") != staticHeader:
		return c, fmt.Errorf("%s: %w: header harus %s", StaticFile, fire.ErrInvalid, staticHeader)
	case want != len(recs)-1:
		return c, fmt.Errorf("%s: %w: %d sumber, komentar menyebut %d", StaticFile, fire.ErrInvalid, len(recs)-1, want)
	}
	for _, rec := range recs[1:] {
		v, err := floats(rec, 1, 3)
		n, err2 := strconv.Atoi(rec[6])
		first, err3 := time.Parse(time.DateOnly, rec[17])
		last, err4 := time.Parse(time.DateOnly, rec[18])
		if err := errors.Join(err, err2, err3, err4); err != nil {
			return c, fmt.Errorf("%s: %w: %s: %w", StaticFile, fire.ErrInvalid, rec[0], err)
		}
		c.Static = append(c.Static, fire.StaticSource{
			ID: rec[0], Lat: v[0], Lon: v[1], RadiusKm: v[2], Reason: fire.Reason(rec[4]), Detections: n, First: first, Last: last,
		})
	}
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", StaticFile, err)
	}
	return c, nil
}
