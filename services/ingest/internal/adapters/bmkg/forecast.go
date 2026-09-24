package bmkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultForecastURL adalah API prakiraan cuaca BMKG per kelurahan/desa
// (parameter adm4). Bisa diganti lewat konfigurasi untuk uji replay.
const DefaultForecastURL = "https://api.bmkg.go.id/publik/prakiraan-cuaca"

// ErrRegionMismatch berarti BMKG menjawab untuk wilayah lain dari yang diminta.
var ErrRegionMismatch = errors.New("prakiraan untuk wilayah lain")

// Payload asli sekitar 9 KB per desa.
const maxForecast = 1 << 20

// ForecastSource membaca prakiraan cuaca BMKG per kelurahan/desa.
type ForecastSource struct {
	url string
}

// NewForecastSource membuat pembaca untuk endpoint prakiraan di baseURL.
func NewForecastSource(baseURL string) *ForecastSource {
	return &ForecastSource{url: strings.TrimRight(baseURL, "?")}
}

// Name adalah nama konektor.
func (s *ForecastSource) Name() string { return "bmkg-prakiraan" }

// ArchiveExt mengembalikan "json".
func (s *ForecastSource) ArchiveExt() string { return "json" }

// Request adalah permintaan prakiraan satu kode adm4.
func (s *ForecastSource) Request(code string) ports.Request {
	return ports.Request{URL: s.url + "?adm4=" + url.QueryEscape(code), Accept: "application/json", MaxBytes: maxForecast}
}

type forecastDoc struct {
	Lokasi *forecastLocation `json:"lokasi"`
	Data   []struct {
		Lokasi *forecastLocation   `json:"lokasi"`
		Cuaca  [][]forecastStepDoc `json:"cuaca"`
	} `json:"data"`
}

type forecastLocation struct {
	ADM4      text    `json:"adm4"`
	Provinsi  text    `json:"provinsi"`
	Kotkab    text    `json:"kotkab"`
	Kecamatan text    `json:"kecamatan"`
	Desa      text    `json:"desa"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
}

// Angka wajib memakai pointer supaya null dari BMKG terdeteksi, bukan
// diam-diam menjadi nol (suhu 0 °C di Bandung jelas salah).
type forecastStepDoc struct {
	Datetime      text     `json:"datetime"`
	UTCDatetime   text     `json:"utc_datetime"`
	T             *float64 `json:"t"`
	TCC           *float64 `json:"tcc"`
	TP            *float64 `json:"tp"`
	Weather       *float64 `json:"weather"`
	WeatherDesc   text     `json:"weather_desc"`
	WeatherDescEN text     `json:"weather_desc_en"`
	WDDeg         *float64 `json:"wd_deg"`
	WD            text     `json:"wd"`
	WS            *float64 `json:"ws"`
	HU            *float64 `json:"hu"`
	VS            *float64 `json:"vs"`
	VSText        text     `json:"vs_text"`
	AnalysisDate  text     `json:"analysis_date"`
}

// Parse membaca prakiraan satu kode adm4 dan memvalidasinya. Langkah dari
// ketiga hari digabung dan diurutkan; galat berarti record ditolak.
func (s *ForecastSource) Parse(code string, body []byte, fetchedAt time.Time) (ports.Event, error) {
	f, err := parseForecast(code, body)
	if err != nil {
		return nil, err
	}
	if err := f.Validate(fetchedAt); err != nil {
		return nil, err
	}
	return rawpb.NewForecastEvent(f)
}

func parseForecast(code string, body []byte) (forecast.Forecast, error) {
	var doc forecastDoc
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&doc); err != nil {
		return forecast.Forecast{}, fmt.Errorf("%w: prakiraan: %w", ErrStructure, err)
	}
	if len(doc.Data) != 1 {
		return forecast.Forecast{}, fmt.Errorf("%w: prakiraan berisi %d data, harus 1", ErrStructure, len(doc.Data))
	}
	loc := doc.Data[0].Lokasi
	if loc == nil {
		loc = doc.Lokasi
	}
	if loc == nil {
		return forecast.Forecast{}, fmt.Errorf("%w: prakiraan tanpa lokasi", ErrStructure)
	}
	if got := loc.ADM4.String(); got != code {
		return forecast.Forecast{}, fmt.Errorf("%w: diminta %s, dijawab %s", ErrRegionMismatch, code, got)
	}
	f := forecast.Forecast{
		RegionCode: code,
		Province:   loc.Provinsi.String(),
		Regency:    loc.Kotkab.String(),
		District:   loc.Kecamatan.String(),
		Village:    loc.Desa.String(),
		Latitude:   loc.Lat,
		Longitude:  loc.Lon,
	}
	for _, day := range doc.Data[0].Cuaca {
		for _, st := range day {
			step, analysis, err := st.toStep()
			if err != nil {
				return forecast.Forecast{}, err
			}
			// Semua langkah berasal dari satu analisis; bila berbeda, ambil yang terbaru.
			if analysis.After(f.AnalysisTime) {
				f.AnalysisTime = analysis
			}
			f.Steps = append(f.Steps, step)
		}
	}
	sortSteps(f.Steps)
	return f, nil
}

func (d forecastStepDoc) toStep() (forecast.Step, time.Time, error) {
	valid, err := time.Parse(time.RFC3339, d.Datetime.String())
	if err != nil {
		// utc_datetime tanpa zona, selalu UTC menurut dokumentasi BMKG.
		valid, err = time.Parse(time.DateTime, d.UTCDatetime.String())
		if err != nil {
			return forecast.Step{}, time.Time{}, fmt.Errorf("%w: waktu langkah %q/%q", ErrStructure, d.Datetime, d.UTCDatetime)
		}
	}
	// analysis_date tanpa zona; BMKG menulisnya dalam UTC.
	analysis, err := time.Parse("2006-01-02T15:04:05", d.AnalysisDate.String())
	if err != nil {
		return forecast.Step{}, time.Time{}, fmt.Errorf("%w: analysis_date %q", ErrStructure, d.AnalysisDate)
	}
	var missing []string
	num := func(name string, v *float64) float64 {
		if v == nil {
			missing = append(missing, name)
			return 0
		}
		return *v
	}
	step := forecast.Step{
		ValidTime:       valid.UTC(),
		TemperatureC:    num("t", d.T),
		HumidityPct:     num("hu", d.HU),
		CloudCoverPct:   num("tcc", d.TCC),
		PrecipitationMM: num("tp", d.TP),
		WeatherCode:     int(num("weather", d.Weather)),
		WeatherDesc:     d.WeatherDesc.String(),
		WeatherDescEN:   d.WeatherDescEN.String(),
		WindSpeedKmh:    num("ws", d.WS),
		WindFromDeg:     num("wd_deg", d.WDDeg),
		WindFrom:        d.WD.String(),
		VisibilityM:     d.VS,
		VisibilityText:  d.VSText.String(),
	}
	if len(missing) > 0 {
		return forecast.Step{}, time.Time{}, fmt.Errorf("%w: langkah %s tanpa nilai %s", ErrStructure, step.ValidTime.Format(time.RFC3339), strings.Join(missing, ", "))
	}
	if d.Weather != nil && *d.Weather != float64(step.WeatherCode) {
		return forecast.Step{}, time.Time{}, fmt.Errorf("%w: kode cuaca %v bukan bilangan bulat", ErrStructure, *d.Weather)
	}
	return step, analysis.UTC(), nil
}

// sortSteps mengurutkan langkah menurut waktu; langkah ganda tetap
// berdampingan supaya ditolak Validate.
func sortSteps(steps []forecast.Step) {
	slices.SortStableFunc(steps, func(a, b forecast.Step) int { return a.ValidTime.Compare(b.ValidTime) })
}
