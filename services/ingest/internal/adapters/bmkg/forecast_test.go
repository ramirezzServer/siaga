package bmkg

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
)

var forecastFetchedAt = time.Date(2026, 9, 24, 7, 21, 0, 0, time.UTC)

func TestForecastRequest(t *testing.T) {
	s := NewForecastSource("http://replay.test/prakiraan-cuaca?")
	if s.Name() != "bmkg-prakiraan" || s.ArchiveExt() != "json" {
		t.Fatal(s.Name(), s.ArchiveExt())
	}
	if r := s.Request("32.73.01.1001"); r.URL != "http://replay.test/prakiraan-cuaca?adm4=32.73.01.1001" || r.MaxBytes == 0 {
		t.Fatalf("%+v", r)
	}
}

func TestParseForecastReal(t *testing.T) {
	s := NewForecastSource(DefaultForecastURL)
	for code, village := range map[string]string{
		"32.73.01.1001": "Sukarasa", "32.04.05.2001": "Cileunyi Kulon", "32.18.01.2001": "Parigi",
	} {
		ev, err := s.Parse(code, fixture(t, "prakiraan-"+code+".json"), forecastFetchedAt)
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if ev.Subject() != "raw.forecast.bmkg" || ev.Key() != code {
			t.Fatal(ev.Subject(), ev.Key())
		}
		f := ev.(*rawpb.ForecastEvent).Forecast()
		if f.GetVillage() != village || f.GetProvince() != "Jawa Barat" || len(f.GetSteps()) != 20 ||
			!f.GetAnalysisTime().AsTime().Equal(time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("%s: %v", code, f)
		}
		for i, st := range f.GetSteps() {
			if i > 0 && !st.GetValidTime().AsTime().After(f.GetSteps()[i-1].GetValidTime().AsTime()) {
				t.Fatalf("%s: langkah %d tidak urut", code, i)
			}
			if st.VisibilityM != nil {
				t.Fatalf("%s: vs null harus kosong, dapat %v", code, st.GetVisibilityM())
			}
		}
	}
	f := func() *forecast.Forecast {
		p, err := parseForecast("32.73.01.1001", fixture(t, "prakiraan-32.73.01.1001.json"))
		if err != nil {
			t.Fatal(err)
		}
		return &p
	}()
	first := f.Steps[0]
	if !first.ValidTime.Equal(time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)) || first.TemperatureC != 28 || first.CloudCoverPct != 64 ||
		first.WeatherCode != 2 || first.WeatherDesc != "Cerah Berawan" || first.WeatherDescEN != "Partly Cloudy" ||
		first.WindFromDeg != 336 || first.WindFrom != "NW" || first.WindSpeedKmh != 1.4 || first.HumidityPct != 52 ||
		f.Latitude != -6.8742274703 || f.Longitude != 107.5853961716 || f.District != "Sukasari" || f.Regency != "Kota Bandung" {
		t.Fatalf("%+v %+v", first, f)
	}
}

func TestParseForecastErrors(t *testing.T) {
	s := NewForecastSource(DefaultForecastURL)
	real := string(fixture(t, "prakiraan-32.73.01.1001.json"))
	for name, c := range map[string]struct {
		code, body string
		want       error
	}{
		"jawaban 404":        {"32.73.99.9999", string(fixture(t, "prakiraan-404.json")), ErrStructure},
		"bukan JSON":         {"32.73.01.1001", "<html>", ErrStructure},
		"wilayah lain":       {"32.73.01.1002", real, ErrRegionMismatch},
		"tanpa lokasi":       {"32.73.01.1001", `{"data":[{"cuaca":[]}]}`, ErrStructure},
		"suhu null":          {"32.73.01.1001", strings.Replace(real, `"t":28,`, `"t":null,`, 1), ErrStructure},
		"kode cuaca pecahan": {"32.73.01.1001", strings.Replace(real, `"weather":2,`, `"weather":2.5,`, 1), ErrStructure},
		"waktu rusak": {"32.73.01.1001", strings.Replace(strings.Replace(real, `"datetime":"2026-09-24T07:00:00Z"`, `"datetime":"x"`, 1),
			`"utc_datetime":"2026-09-24 07:00:00"`, `"utc_datetime":"y"`, 1), ErrStructure},
		"analysis rusak":   {"32.73.01.1001", strings.Replace(real, `"analysis_date":"2026-09-24T00:00:00"`, `"analysis_date":"x"`, 1), ErrStructure},
		"kelembapan > 100": {"32.73.01.1001", strings.Replace(real, `"hu":52`, `"hu":520`, 1), forecast.ErrInvalid},
	} {
		if _, err := s.Parse(c.code, []byte(c.body), forecastFetchedAt); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, ingin %v", name, err, c.want)
		}
	}
	// datetime rusak tetapi utc_datetime ada: tetap terbaca.
	alt := strings.Replace(real, `"datetime":"2026-09-24T07:00:00Z"`, `"datetime":null`, 1)
	if _, err := s.Parse("32.73.01.1001", []byte(alt), forecastFetchedAt); err != nil {
		t.Fatalf("utc_datetime cadangan: %v", err)
	}
	// Lokasi atas dipakai bila lokasi di data kosong.
	top := strings.Replace(real, `"data":[{"lokasi":{`, `"data":[{"lokasi_lama":{`, 1)
	if _, err := s.Parse("32.73.01.1001", []byte(top), forecastFetchedAt); err != nil {
		t.Fatalf("lokasi atas: %v", err)
	}
}

// Properti: Parse tidak pernah panik, dan hasil yang diterima selalu untuk
// kode yang diminta dengan langkah yang urut.
func FuzzParseForecastDocument(f *testing.F) {
	f.Add([]byte(`{"lokasi":{"adm4":"32.73.01.1001"},"data":[{"cuaca":[[{"datetime":"2026-09-24T07:00:00Z","t":1,"tcc":1,"tp":0,"weather":1,"wd_deg":1,"ws":1,"hu":1,"analysis_date":"2026-09-24T00:00:00"}]]}]}`))
	f.Add([]byte(`{"data":[]}`))
	s := NewForecastSource(DefaultForecastURL)
	f.Fuzz(func(t *testing.T, body []byte) {
		ev, err := s.Parse("32.73.01.1001", body, forecastFetchedAt)
		if err != nil {
			return
		}
		fc := ev.(*rawpb.ForecastEvent).Forecast()
		if fc.GetRegionCode() != "32.73.01.1001" {
			t.Fatalf("kode %s", fc.GetRegionCode())
		}
		for i := 1; i < len(fc.GetSteps()); i++ {
			if !fc.GetSteps()[i].GetValidTime().AsTime().After(fc.GetSteps()[i-1].GetValidTime().AsTime()) {
				t.Fatal("langkah tidak urut")
			}
		}
	})
}
