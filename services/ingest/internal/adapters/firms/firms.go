// Package firms adalah konektor NASA FIRMS Area API: deteksi titik panas
// near real-time VIIRS dan MODIS dalam satu kotak wilayah, format CSV.
//
// MAP_KEY FIRMS berada di path URL, jadi setiap Request menandainya sebagai
// rahasia (ports.Request.Secrets) supaya tidak tertulis di galat dan log.
package firms

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultBaseURL adalah awalan Area API berformat CSV.
const DefaultBaseURL = "https://firms.modaps.eosdis.nasa.gov/api/area/csv"

// ErrStructure menandai payload yang bukan CSV FIRMS (misal key salah atau
// parameter ditolak); seluruh payload tidak bisa dipakai.
var ErrStructure = errors.New("payload FIRMS tidak dikenali")

// Days adalah rentang hari per request (1..5 menurut FIRMS). Dua hari supaya
// deteksi larut malam UTC yang terbit terlambat tetap terambil.
const Days = 2

// Parser membaca CSV satu produk FIRMS. Tidak butuh key, jadi dipakai juga
// untuk replay payload dari arsip.
type Parser struct {
	product fire.Product
}

// NewParsers membuat satu Parser per produk NRT, bernama sama dengan konektornya.
func NewParsers() []*Parser {
	out := make([]*Parser, len(fire.Products))
	for i, p := range fire.Products {
		out[i] = &Parser{product: p}
	}
	return out
}

// Connector mengambil satu produk FIRMS untuk satu kotak.
type Connector struct {
	Parser
	base, key string
	box       area.Box
}

var _ ports.Connector = (*Connector)(nil)

// NewConnectors membuat satu konektor per produk NRT. key wajib diisi.
func NewConnectors(base, key string, box area.Box) ([]*Connector, error) {
	if !mapKey.MatchString(key) {
		return nil, errors.New("FIRMS_MAP_KEY kosong atau formatnya bukan MAP_KEY FIRMS (huruf/angka)")
	}
	if err := box.Validate(); err != nil {
		return nil, err
	}
	out := make([]*Connector, len(fire.Products))
	for i, p := range fire.Products {
		out[i] = &Connector{Parser: Parser{product: p}, base: strings.TrimRight(base, "/"), key: key, box: box}
	}
	return out, nil
}

var mapKey = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

// Name misal "firms-viirs-snpp-nrt".
func (p *Parser) Name() string {
	return "firms-" + strings.ReplaceAll(strings.ToLower(string(p.product)), "_", "-")
}

// Request membentuk URL Area API. Tidak ada ETag dari FIRMS.
func (c *Connector) Request() ports.Request {
	return ports.Request{
		URL:      fmt.Sprintf("%s/%s/%s/%s/%d", c.base, c.key, c.product, c.box, Days),
		Accept:   "text/csv",
		MaxBytes: 16 << 20,
		Secrets:  []string{c.key},
	}
}

// ArchiveExt adalah "csv".
func (c *Connector) ArchiveExt() string { return "csv" }

// Kolom wajib per instrumen (header Area API).
var (
	viirsColumns = []string{"latitude", "longitude", "bright_ti4", "scan", "track", "acq_date", "acq_time", "satellite", "instrument", "confidence", "version", "bright_ti5", "frp", "daynight"}
	modisColumns = []string{"latitude", "longitude", "brightness", "scan", "track", "acq_date", "acq_time", "satellite", "instrument", "confidence", "version", "bright_t31", "frp", "daynight"}
)

// Parse membaca CSV menjadi event raw.fire.firms. Baris yang rusak ditolak
// sendiri-sendiri; header yang tidak dikenali menolak seluruh payload.
func (p *Parser) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	inst, _ := p.product.Instrument()
	want := viirsColumns
	if inst == fire.MODIS {
		want = modisColumns
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\ufeff"))))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrStructure, snippet(body))
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(strings.ToLower(h))] = i
	}
	for _, name := range want {
		if _, ok := col[name]; !ok {
			return nil, nil, fmt.Errorf("%w: kolom %q tidak ada: %s", ErrStructure, name, snippet(body))
		}
	}

	var events []ports.Event
	var rejected []ports.Rejection
	seen := map[string]bool{}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		key := fmt.Sprintf("%s:baris-%d", p.product, line)
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		get := func(name string) string {
			if i := col[name]; i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		d, err := p.detection(inst, get)
		if err == nil {
			key = d.ID()
			err = d.Validate(fetchedAt)
		}
		if err == nil && seen[key] {
			err = errors.New("deteksi ganda dalam satu payload")
		}
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		seen[key] = true
		ev, err := rawpb.NewFireEvent(d)
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		events = append(events, ev)
	}
	return events, rejected, nil
}

// detection membaca satu baris tanpa memvalidasi.
func (p *Parser) detection(inst fire.Instrument, get func(string) string) (fire.Detection, error) {
	var errs []error
	num := func(name string) float64 {
		v, err := strconv.ParseFloat(get(name), 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %q bukan angka", name, get(name)))
		}
		return v
	}
	optional := func(name string) *float64 {
		if get(name) == "" {
			return nil
		}
		v := num(name)
		return &v
	}
	d := fire.Detection{
		Product: p.product, Satellite: get("satellite"), Instrument: fire.Instrument(strings.ToUpper(get("instrument"))),
		Lat: round5(num("latitude")), Lon: round5(num("longitude")),
		ScanKm: num("scan"), TrackKm: num("track"), Version: get("version"),
	}
	switch get("daynight") {
	case "D":
		d.Daytime = true
	case "N":
	default:
		errs = append(errs, fmt.Errorf("daynight %q bukan D/N", get("daynight")))
	}
	at, err := acquired(get("acq_date"), get("acq_time"))
	if err != nil {
		errs = append(errs, err)
	}
	d.DetectedAt = at
	if inst == fire.MODIS {
		d.BrightnessK, d.BackgroundK = num("brightness"), optional("bright_t31")
		pct, err := strconv.Atoi(get("confidence"))
		if err != nil {
			errs = append(errs, fmt.Errorf("keyakinan MODIS %q bukan bilangan", get("confidence")))
		}
		d.ConfidencePct, d.Confidence = &pct, fire.ConfidenceFromPct(pct)
	} else {
		d.BrightnessK, d.BackgroundK = num("bright_ti4"), optional("bright_ti5")
		conf, ok := fire.ParseVIIRSConfidence(get("confidence"))
		if !ok {
			errs = append(errs, fmt.Errorf("keyakinan VIIRS %q tidak dikenal", get("confidence")))
		}
		d.Confidence = conf
	}
	d.FRPMW = optional("frp")
	return d, errors.Join(errs...)
}

// acquired menggabungkan acq_date (YYYY-MM-DD) dan acq_time (HHMM UTC, nol
// di depan kadang hilang, misal "536").
func acquired(date, hhmm string) (time.Time, error) {
	if len(hhmm) < 1 || len(hhmm) > 4 {
		return time.Time{}, fmt.Errorf("acq_time %q tidak valid", hhmm)
	}
	hhmm = strings.Repeat("0", 4-len(hhmm)) + hhmm
	t, err := time.Parse("2006-01-02 1504", date+" "+hhmm)
	if err != nil {
		return time.Time{}, fmt.Errorf("waktu akuisisi %q %q: %w", date, hhmm, err)
	}
	return t.UTC(), nil
}

// round5 membulatkan ke lima desimal; nol negatif dijadikan nol supaya ID
// tidak memuat "-0.00000".
func round5(v float64) float64 {
	r := math.Round(v*1e5) / 1e5
	if r == 0 {
		return 0
	}
	return r
}

// snippet adalah potongan awal payload untuk pesan galat (payload FIRMS
// tidak memuat MAP_KEY).
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b[:min(len(b), 120)]))
	if s == "" {
		return "(kosong)"
	}
	return strconv.Quote(s)
}
