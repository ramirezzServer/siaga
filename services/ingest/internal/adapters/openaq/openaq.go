// Package openaq adalah sumber stasiun kualitas udara OpenAQ API v3: daftar
// lokasi dalam satu kotak wilayah (/v3/locations?bbox=…) dan nilai terbaru
// per lokasi (/v3/locations/{id}/latest). Key API dikirim lewat header
// X-API-Key dan ditandai rahasia di setiap Request.
package openaq

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultBaseURL adalah awalan API v3.
const DefaultBaseURL = "https://api.openaq.org/v3"

// ListLimit adalah jumlah lokasi per halaman. Satu kotak provinsi jauh di
// bawah angka ini; bila penuh, daftar dianggap mungkin terpotong.
const ListLimit = 1000

// ErrStructure menandai payload yang bukan jawaban OpenAQ v3.
var ErrStructure = errors.New("payload OpenAQ tidak dikenali")

// Source memenuhi stations.Source.
type Source struct {
	base, key string
	box       area.Box
	// sensors memetakan ID sensor ke parameter dan satuan dari daftar
	// terakhir, untuk membaca nilai terbaru yang hanya menyebut ID sensor.
	sensors map[int64]sensorInfo
}

var _ stations.Source = (*Source)(nil)

type sensorInfo struct {
	location  int64
	parameter airquality.Parameter
	unit      string
}

var apiKey = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// New membuat Source. key wajib diisi.
func New(base, key string, box area.Box) (*Source, error) {
	if !apiKey.MatchString(key) {
		return nil, errors.New("OPENAQ_API_KEY kosong atau formatnya tidak dikenali")
	}
	if err := box.Validate(); err != nil {
		return nil, err
	}
	return &Source{base: strings.TrimRight(base, "/"), key: key, box: box, sensors: map[int64]sensorInfo{}}, nil
}

// Name adalah "openaq-stasiun".
func (s *Source) Name() string { return "openaq-stasiun" }

func (s *Source) request(u string) ports.Request {
	return ports.Request{
		URL: u, Accept: "application/json", MaxBytes: 16 << 20,
		Header: map[string]string{"X-API-Key": s.key}, Secrets: []string{s.key},
	}
}

// ListRequest meminta semua lokasi dalam kotak.
func (s *Source) ListRequest() ports.Request {
	q := url.Values{"bbox": {s.box.String()}, "limit": {strconv.Itoa(ListLimit)}}
	return s.request(s.base + "/locations?" + q.Encode())
}

// DetailRequest meminta nilai terbaru satu lokasi.
func (s *Source) DetailRequest(st stations.Station) ports.Request {
	id := strings.TrimPrefix(st.ID, "openaq:")
	return s.request(s.base + "/locations/" + id + "/latest?limit=100")
}

// Event membungkus pengukuran sebagai raw.aq.openaq.
func (s *Source) Event(o airquality.Observation) (ports.Event, error) {
	return rawpb.NewAirQualityObservationEvent(o)
}

type utcTime struct {
	UTC string `json:"utc"`
}

type location struct {
	ID        int64    `json:"id"`
	Name      *string  `json:"name"`
	Locality  *string  `json:"locality"`
	Timezone  *string  `json:"timezone"`
	Owner     *named   `json:"owner"`
	Provider  *named   `json:"provider"`
	IsMobile  bool     `json:"isMobile"`
	IsMonitor bool     `json:"isMonitor"`
	Sensors   []sensor `json:"sensors"`
	Coords    *struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	} `json:"coordinates"`
	DatetimeLast *utcTime `json:"datetimeLast"`
}

type named struct {
	Name string `json:"name"`
}

type sensor struct {
	ID        int64 `json:"id"`
	Parameter struct {
		Name  string `json:"name"`
		Units string `json:"units"`
	} `json:"parameter"`
}

type envelope[T any] struct {
	Meta    *json.RawMessage `json:"meta"`
	Results *[]T             `json:"results"`
}

func decode[T any](body []byte) ([]T, error) {
	var env envelope[T]
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStructure, err)
	}
	if env.Meta == nil || env.Results == nil {
		return nil, fmt.Errorf("%w: tanpa meta/results: %s", ErrStructure, snippet(body))
	}
	return *env.Results, nil
}

// ParseList membaca daftar lokasi. Lokasi bergerak (isMobile) dan lokasi
// tanpa sensor parameter SIAGA dilewati diam-diam; lokasi yang rusak ditolak.
func (s *Source) ParseList(body []byte) ([]stations.Station, []ports.Rejection, error) {
	locs, err := decode[location](body)
	if err != nil {
		return nil, nil, err
	}
	var out []stations.Station
	var rejected []ports.Rejection
	if len(locs) >= ListLimit {
		rejected = append(rejected, ports.Rejection{Key: "daftar", Reason: fmt.Errorf("%d lokasi, daftar mungkin terpotong", len(locs))})
	}
	sensors := map[int64]sensorInfo{}
	for _, l := range locs {
		if l.IsMobile {
			continue
		}
		key := airquality.StationID(l.ID)
		var own []int64
		for _, sn := range l.Sensors {
			p := airquality.Parameter(strings.ToLower(strings.TrimSpace(sn.Parameter.Name)))
			if !airquality.Known(p) {
				continue
			}
			sensors[sn.ID] = sensorInfo{location: l.ID, parameter: p, unit: normalizeUnit(sn.Parameter.Units)}
			own = append(own, sn.ID)
		}
		if len(own) == 0 {
			continue
		}
		st, err := station(l)
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		out = append(out, st)
	}
	s.sensors = sensors
	return out, rejected, nil
}

func station(l location) (stations.Station, error) {
	var errs []error
	st := stations.Station{Station: airquality.Station{
		ID: airquality.StationID(l.ID), Name: clip(deref(l.Name)), Locality: clip(deref(l.Locality)),
		Timezone: strings.TrimSpace(deref(l.Timezone)), IsMonitor: l.IsMonitor,
	}}
	if l.ID <= 0 {
		errs = append(errs, fmt.Errorf("ID lokasi %d tidak valid", l.ID))
	}
	if l.Provider != nil {
		st.Provider = clip(l.Provider.Name)
	}
	if l.Owner != nil {
		st.Owner = clip(l.Owner.Name)
	}
	if st.Name == "" {
		st.Name = st.ID
	}
	if l.Coords == nil || l.Coords.Latitude == nil || l.Coords.Longitude == nil {
		errs = append(errs, errors.New("koordinat kosong"))
	} else {
		st.Lat, st.Lon = *l.Coords.Latitude, *l.Coords.Longitude
	}
	if l.DatetimeLast != nil && l.DatetimeLast.UTC != "" {
		t, err := time.Parse(time.RFC3339, l.DatetimeLast.UTC)
		if err != nil {
			errs = append(errs, fmt.Errorf("datetimeLast %q: %w", l.DatetimeLast.UTC, err))
		}
		st.LastReport = t.UTC()
	}
	return st, errors.Join(errs...)
}

type latest struct {
	Datetime    *utcTime `json:"datetime"`
	Value       *float64 `json:"value"`
	SensorsID   int64    `json:"sensorsId"`
	LocationsID int64    `json:"locationsId"`
}

// ParseDetail membaca nilai terbaru. Nilai sensor yang bukan parameter SIAGA
// dilewati; nilai yang rusak membuat seluruh payload ditolak karena berarti
// format sumber berubah.
func (s *Source) ParseDetail(st stations.Station, body []byte) ([]airquality.Reading, error) {
	rows, err := decode[latest](body)
	if err != nil {
		return nil, err
	}
	want, err := strconv.ParseInt(strings.TrimPrefix(st.ID, "openaq:"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("ID stasiun %q: %w", st.ID, err)
	}
	var out []airquality.Reading
	var errs []error
	for _, r := range rows {
		info, ok := s.sensors[r.SensorsID]
		if !ok || info.location != want {
			continue
		}
		if r.LocationsID != want {
			errs = append(errs, fmt.Errorf("sensor %d: lokasi %d, bukan %d", r.SensorsID, r.LocationsID, want))
			continue
		}
		if r.Value == nil || r.Datetime == nil {
			errs = append(errs, fmt.Errorf("sensor %d: nilai atau waktu kosong", r.SensorsID))
			continue
		}
		t, err := time.Parse(time.RFC3339, r.Datetime.UTC)
		if err != nil {
			errs = append(errs, fmt.Errorf("sensor %d: waktu %q: %w", r.SensorsID, r.Datetime.UTC, err))
			continue
		}
		out = append(out, airquality.Reading{
			SensorID: r.SensorsID, Parameter: info.parameter, Unit: info.unit, Value: *r.Value, ObservedAt: t.UTC(),
		})
	}
	return out, errors.Join(errs...)
}

// normalizeUnit menyamakan penulisan satuan (mu Yunani dan tanda mikro).
func normalizeUnit(u string) string {
	u = strings.TrimSpace(u)
	switch strings.ToLower(strings.ReplaceAll(u, "μ", "µ")) {
	case "µg/m³", "µg/m3", "ug/m3", "ug/m³":
		return airquality.UnitMicrogram
	case "ppm":
		return airquality.UnitPPM
	case "ppb":
		return airquality.UnitPPB
	}
	return u
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// clip merapikan spasi dan memotong teks ke 100 byte tanpa memecah karakter.
func clip(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < 0x20 || r == ' ' }), " ")
	if len(s) <= 100 {
		return s
	}
	s = s[:100]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b[:min(len(b), 120)]))
	if s == "" {
		return "(kosong)"
	}
	return strconv.Quote(s)
}
