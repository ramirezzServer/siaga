// Package openmeteo membaca Open-Meteo (non-komersial, CC BY 4.0) untuk
// banyak titik sekaligus: prakiraan cuaca per jam di grid 0,25°, prakiraan
// kualitas udara CAMS di grid yang sama, dan debit sungai GloFAS di titik
// pantau sungai. Satu request memuat semua titik; tiap titik menjadi satu
// event raw (ADR 0011).
package openmeteo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Endpoint bawaan. Bisa diganti lewat konfigurasi untuk uji replay.
const (
	DefaultWeatherURL = "https://api.open-meteo.com/v1/forecast"
	DefaultAirURL     = "https://air-quality-api.open-meteo.com/v1/air-quality"
	DefaultFloodURL   = "https://flood-api.open-meteo.com/v1/flood"
)

// Model yang diminta. Nama ini ikut di event dan di ts.* geo-processor.
const (
	// ModelWeather adalah gabungan model terbaik Open-Meteo untuk tiap lokasi.
	ModelWeather = "best_match"
	// ModelAir adalah CAMS global (±0,4°), satu-satunya domain CAMS untuk Indonesia.
	ModelAir = "cams_global"
	// ModelFlood adalah GloFAS v4 (sel ±5 km) versi seamless Open-Meteo.
	ModelFlood = "glofas_v4"
)

// ErrStructure menandai payload yang strukturnya tidak lagi sesuai format Open-Meteo.
var ErrStructure = errors.New("struktur payload Open-Meteo berubah")

// Payload grid 80 titik × 96 jam × 10 variabel sekitar 1 MB.
const maxPayload = 16 << 20

// Horizon permintaan. Cuaca dan udara memakai jendela tanggal UTC yang tetap
// sepanjang hari (hari ini sampai 3 hari ke depan, 96 jam), jadi isi hanya
// berubah bila model diperbarui atau tanggal berganti; itu yang membuat
// deteksi "tidak berubah" di poll bekerja. Debit: 4 hari lalu + 10 hari ke
// depan (14 hari: batas satu "API call" Open-Meteo per titik).
const (
	aheadDays      = 3
	floodPastDays  = 4
	floodAheadDays = 10
)

// Connector adalah satu endpoint Open-Meteo untuk sekumpulan titik.
type Connector struct {
	name     string
	endpoint string
	spec     series.Spec
	model    string
	sites    []series.Site
	params   url.Values
	window   func(now time.Time) url.Values
	now      func() time.Time
	newEvent func(series.Series) (ports.Event, error)
	ext      string
}

var _ ports.Connector = (*Connector)(nil)

// NewWeatherConnector membuat konektor prakiraan cuaca per jam untuk sites.
func NewWeatherConnector(endpoint string, sites []series.Site, now func() time.Time) (*Connector, error) {
	return newConnector("openmeteo-cuaca", endpoint, series.WeatherSpec, ModelWeather, sites, now,
		url.Values{"cell_selection": {"nearest"}}, dateWindow, rawpb.NewGridWeatherEvent)
}

// NewAirQualityConnector membuat konektor prakiraan kualitas udara CAMS per jam.
func NewAirQualityConnector(endpoint string, sites []series.Site, now func() time.Time) (*Connector, error) {
	return newConnector("openmeteo-udara", endpoint, series.AirQualitySpec, ModelAir, sites, now,
		url.Values{"cell_selection": {"nearest"}, "domains": {"cams_global"}}, dateWindow, rawpb.NewAirQualityEvent)
}

// NewDischargeConnector membuat konektor debit harian GloFAS untuk titik pantau sungai.
func NewDischargeConnector(endpoint string, sites []series.Site, now func() time.Time) (*Connector, error) {
	return newConnector("openmeteo-sungai", endpoint, series.DischargeSpec, ModelFlood, sites, now,
		url.Values{}, floodWindow, rawpb.NewDischargeEvent)
}

func newConnector(name, endpoint string, spec series.Spec, model string, sites []series.Site, now func() time.Time,
	params url.Values, window func(time.Time) url.Values, newEvent func(series.Series) (ports.Event, error),
) (*Connector, error) {
	if len(sites) == 0 {
		return nil, fmt.Errorf("%s: tanpa titik pantau", name)
	}
	seen := map[string]bool{}
	for _, s := range sites {
		if !series.ValidSiteID(s.ID) || seen[s.ID] {
			return nil, fmt.Errorf("%s: ID titik %q tidak valid atau ganda", name, s.ID)
		}
		seen[s.ID] = true
	}
	if _, err := url.Parse(endpoint); err != nil || endpoint == "" {
		return nil, fmt.Errorf("%s: endpoint %q tidak valid", name, endpoint)
	}
	return &Connector{
		name: name, endpoint: endpoint, spec: spec, model: model, sites: sites, params: params,
		window: window, now: now, newEvent: newEvent, ext: "json",
	}, nil
}

// Name mengembalikan nama konektor.
func (c *Connector) Name() string { return c.name }

// ArchiveExt mengembalikan "json".
func (c *Connector) ArchiveExt() string { return c.ext }

// Sites mengembalikan jumlah titik; dipakai untuk menghitung kuota Open-Meteo.
func (c *Connector) Sites() int { return len(c.sites) }

// Request membentuk satu request untuk semua titik. Jendela waktunya
// bergantung pada tanggal UTC saat request dibuat.
func (c *Connector) Request() ports.Request {
	q := url.Values{}
	lats := make([]string, len(c.sites))
	lons := make([]string, len(c.sites))
	for i, s := range c.sites {
		lats[i] = strconv.FormatFloat(s.Requested.Lat, 'f', -1, 64)
		lons[i] = strconv.FormatFloat(s.Requested.Lon, 'f', -1, 64)
	}
	q.Set("latitude", strings.Join(lats, ","))
	q.Set("longitude", strings.Join(lons, ","))
	names := make([]string, len(c.spec.Vars))
	for i, v := range c.spec.Vars {
		names[i] = v.Name
	}
	q.Set(c.resolution(), strings.Join(names, ","))
	q.Set("timeformat", "unixtime")
	q.Set("timezone", "GMT")
	for k, v := range c.params {
		q[k] = v
	}
	for k, v := range c.window(c.now().UTC()) {
		q[k] = v
	}
	// Koma dibiarkan apa adanya supaya URL tetap terbaca di log dan arsip.
	return ports.Request{
		URL:      c.endpoint + "?" + strings.ReplaceAll(q.Encode(), "%2C", ","),
		Accept:   "application/json",
		MaxBytes: maxPayload,
	}
}

func (c *Connector) resolution() string {
	if c.spec.Step >= 24*time.Hour {
		return "daily"
	}
	return "hourly"
}

func dateWindow(now time.Time) url.Values {
	return url.Values{
		"start_date": {now.Format(time.DateOnly)},
		"end_date":   {now.AddDate(0, 0, aheadDays).Format(time.DateOnly)},
	}
}

func floodWindow(time.Time) url.Values {
	return url.Values{
		"past_days":     {strconv.Itoa(floodPastDays)},
		"forecast_days": {strconv.Itoa(floodAheadDays)},
	}
}

// location adalah satu elemen respons.
type location struct {
	Error     bool                  `json:"error"`
	Reason    string                `json:"reason"`
	Latitude  *float64              `json:"latitude"`
	Longitude *float64              `json:"longitude"`
	Elevation *float64              `json:"elevation"`
	UTCOffset *int                  `json:"utc_offset_seconds"`
	Hourly    map[string][]*float64 `json:"hourly"`
	Daily     map[string][]*float64 `json:"daily"`
	HourlyU   map[string]string     `json:"hourly_units"`
	DailyU    map[string]string     `json:"daily_units"`
}

func (l location) data(daily bool) (map[string][]*float64, map[string]string) {
	if daily {
		return l.Daily, l.DailyU
	}
	return l.Hourly, l.HourlyU
}

// Parse membaca respons: satu objek untuk satu titik, daftar objek untuk
// banyak titik, urut sama dengan request. Satuan dan jumlah titik yang tidak
// sesuai membuat seluruh payload ditolak; titik yang nilainya melanggar
// invarian ditolak sendiri-sendiri.
func (c *Connector) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	locs, err := decode(body)
	if err != nil {
		return nil, nil, err
	}
	if len(locs) != len(c.sites) {
		return nil, nil, fmt.Errorf("%w: %d lokasi dijawab untuk %d titik", ErrStructure, len(locs), len(c.sites))
	}
	daily := c.resolution() == "daily"
	if err := c.checkUnits(locs[0], daily); err != nil {
		return nil, nil, err
	}
	var events []ports.Event
	var rejections []ports.Rejection
	for i, loc := range locs {
		site := c.sites[i]
		s, err := c.toSeries(site, loc, daily)
		if err == nil {
			s = s.Compact()
			err = s.Validate(c.spec, fetchedAt)
		}
		var ev ports.Event
		if err == nil {
			ev, err = c.newEvent(s)
		}
		if err != nil {
			rejections = append(rejections, ports.Rejection{Key: site.ID, Reason: err})
			continue
		}
		events = append(events, ev)
	}
	return events, rejections, nil
}

func decode(body []byte) ([]location, error) {
	trimmed := bytes.TrimSpace(body)
	var locs []location
	switch {
	case bytes.HasPrefix(trimmed, []byte("[")):
		if err := json.Unmarshal(trimmed, &locs); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStructure, err)
		}
	case bytes.HasPrefix(trimmed, []byte("{")):
		var l location
		if err := json.Unmarshal(trimmed, &l); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStructure, err)
		}
		locs = []location{l}
	default:
		return nil, fmt.Errorf("%w: bukan objek atau daftar JSON", ErrStructure)
	}
	for i, l := range locs {
		if l.Error {
			return nil, fmt.Errorf("%w: lokasi %d: sumber menjawab galat: %s", ErrStructure, i, l.Reason)
		}
	}
	return locs, nil
}

// checkUnits memastikan satuan setiap variabel sama dengan spesifikasi.
func (c *Connector) checkUnits(l location, daily bool) error {
	_, units := l.data(daily)
	if got := units["time"]; got != "unixtime" {
		return fmt.Errorf("%w: satuan waktu %q, harus unixtime", ErrStructure, got)
	}
	for _, v := range c.spec.Vars {
		got, ok := units[v.Name]
		if !ok {
			return fmt.Errorf("%w: variabel %s tidak ada di respons", ErrStructure, v.Name)
		}
		if got != v.Unit {
			return fmt.Errorf("%w: satuan %s %q, harus %q", ErrStructure, v.Name, got, v.Unit)
		}
	}
	return nil
}

func (c *Connector) toSeries(site series.Site, l location, daily bool) (series.Series, error) {
	if l.Latitude == nil || l.Longitude == nil {
		return series.Series{}, fmt.Errorf("%w: koordinat sel kosong", ErrStructure)
	}
	if l.UTCOffset != nil && *l.UTCOffset != 0 {
		return series.Series{}, fmt.Errorf("%w: utc_offset_seconds %d, harus 0", ErrStructure, *l.UTCOffset)
	}
	cols, _ := l.data(daily)
	times := cols["time"]
	s := series.Series{
		Site:      site,
		Cell:      series.LatLon{Lat: *l.Latitude, Lon: *l.Longitude},
		Elevation: finite(l.Elevation),
		Model:     c.model,
		Times:     make([]time.Time, len(times)),
		Values:    make([][]*float64, len(c.spec.Vars)),
	}
	for i, t := range times {
		if t == nil || *t != math.Trunc(*t) || math.Abs(*t) > 1e11 {
			return series.Series{}, fmt.Errorf("%w: waktu langkah %d tidak valid", ErrStructure, i)
		}
		s.Times[i] = time.Unix(int64(*t), 0).UTC()
	}
	for v, spec := range c.spec.Vars {
		col, ok := cols[spec.Name]
		if !ok {
			return series.Series{}, fmt.Errorf("%w: variabel %s tidak ada", ErrStructure, spec.Name)
		}
		if len(col) != len(times) {
			return series.Series{}, fmt.Errorf("%w: %s berisi %d nilai untuk %d langkah", ErrStructure, spec.Name, len(col), len(times))
		}
		s.Values[v] = col
	}
	return s, nil
}

// finite mengubah elevasi NaN (Open-Meteo menulis NaN untuk sel laut di
// sebagian model) menjadi kosong.
func finite(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	return v
}

// CellValues adalah deret satu variabel di satu sel, untuk alat kalibrasi
// (make river-snap) yang tidak butuh validasi spesifikasi lengkap.
type CellValues struct {
	Cell   series.LatLon
	Values []*float64
}

// ParseDaily membaca satu variabel harian dari respons banyak lokasi, urut
// sama dengan request.
func ParseDaily(body []byte, variable string) ([]CellValues, error) {
	locs, err := decode(body)
	if err != nil {
		return nil, err
	}
	out := make([]CellValues, len(locs))
	for i, l := range locs {
		col, ok := l.Daily[variable]
		if l.Latitude == nil || l.Longitude == nil || !ok {
			return nil, fmt.Errorf("%w: lokasi %d tanpa koordinat atau %s", ErrStructure, i, variable)
		}
		out[i] = CellValues{Cell: series.LatLon{Lat: *l.Latitude, Lon: *l.Longitude}, Values: col}
	}
	return out, nil
}
