package series

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 24, 12, 40, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

// valid membuat deret cuaca 3 jam yang lolos semua invarian.
func valid() Series {
	site := Site{ID: "grid:-7.00:107.50", Requested: LatLon{Lat: -7, Lon: 107.5}}
	s := Series{Site: site, Cell: LatLon{Lat: -6.99, Lon: 107.47}, Elevation: f(838), Model: "best_match"}
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	for h := range 3 {
		s.Times = append(s.Times, day.Add(time.Duration(h)*time.Hour))
	}
	s.Values = make([][]*float64, len(WeatherSpec.Vars))
	for v, spec := range WeatherSpec.Vars {
		mid := math.Floor((spec.Min + spec.Max) / 2)
		s.Values[v] = []*float64{f(mid), f(mid + 1), nil}
	}
	return s
}

func TestValidateAcceptsValid(t *testing.T) {
	if err := valid().Validate(WeatherSpec, now); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	for name, mut := range map[string]func(*Series){
		"ID":            func(s *Series) { s.Site.ID = "grid:-7.0:107.5" },
		"model":         func(s *Series) { s.Model = "Best Match" },
		"nama spasi":    func(s *Series) { s.Site.Name = " Majalaya" },
		"nama panjang":  func(s *Series) { s.Site.Name = strings.Repeat("a", 101) },
		"UTF-8":         func(s *Series) { s.Site.River = "\xff" },
		"titik":         func(s *Series) { s.Site.Requested = LatLon{Lat: 52, Lon: 13} },
		"sel jauh":      func(s *Series) { s.Cell = LatLon{Lat: -6.2, Lon: 107.5} },
		"sel NaN":       func(s *Series) { s.Cell = LatLon{Lat: math.NaN(), Lon: 107.5} },
		"elevasi":       func(s *Series) { s.Elevation = f(9000) },
		"tanpa langkah": func(s *Series) { s.Times = nil },
		"bukan UTC":     func(s *Series) { s.Times[1] = s.Times[1].In(time.FixedZone("WIB", 7*3600)) },
		"tidak sejajar": func(s *Series) { s.Times[1] = s.Times[1].Add(time.Minute) },
		"tidak urut":    func(s *Series) { s.Times[2] = s.Times[0] },
		"terlalu lama":  func(s *Series) { s.Times[0] = s.Times[0].AddDate(0, 0, -5) },
		"kolom kurang":  func(s *Series) { s.Values = s.Values[1:] },
		"nilai kurang":  func(s *Series) { s.Values[2] = s.Values[2][:1] },
		"di luar batas": func(s *Series) { s.Values[1][0] = f(101) },
		"Inf":           func(s *Series) { s.Values[0][0] = f(math.Inf(1)) },
		"kode pecahan":  func(s *Series) { s.Values[WeatherSpec.Index("weather_code")][0] = f(2.5) },
		"semua kosong": func(s *Series) {
			for v := range s.Values {
				s.Values[v] = []*float64{nil, nil, nil}
			}
		},
		"terlalu banyak": func(s *Series) { s.Times = make([]time.Time, WeatherSpec.MaxSteps+1) },
	} {
		s := valid()
		mut(&s)
		err := s.Validate(WeatherSpec, now)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCompactDropsEmptySteps(t *testing.T) {
	s := valid()
	c := s.Compact()
	if len(c.Times) != 2 || len(c.Values[0]) != 2 || len(s.Times) != 3 {
		t.Fatalf("%d langkah, asli %d", len(c.Times), len(s.Times))
	}
	s.Values[0] = s.Values[0][:1] // kolom pendek dianggap kosong di langkah yang hilang
	if c := s.Compact(); len(c.Times) != 2 {
		t.Fatalf("%d langkah", len(c.Times))
	}
}

func TestSiteIDs(t *testing.T) {
	if id := GridID(LatLon{Lat: -6.75, Lon: 107.5}); id != "grid:-6.75:107.50" || !ValidSiteID(id) {
		t.Fatal(id)
	}
	if id := RiverID("citarum-dayeuhkolot"); !ValidSiteID(id) {
		t.Fatal(id)
	}
	for _, bad := range []string{"grid:-6.750:107.50", "grid:-06.75:107.50", "river:Citarum", "river:", "adm4:32.73.01.1001", strings.Repeat("river:a", 20)} {
		if ValidSiteID(bad) {
			t.Errorf("%q seharusnya tidak valid", bad)
		}
	}
	if WeatherSpec.Index("tidak_ada") != -1 || AirQualitySpec.Index("pm10") != 1 {
		t.Fatal("Index")
	}
}

func TestSpecsConsistent(t *testing.T) {
	for _, spec := range []Spec{WeatherSpec, AirQualitySpec, DischargeSpec} {
		seen := map[string]bool{}
		for _, v := range spec.Vars {
			if seen[v.Name] || !(v.Min < v.Max) {
				t.Errorf("%s: variabel %s ganda atau batas salah", spec.Dataset, v.Name)
			}
			seen[v.Name] = true
		}
		if spec.MaxSteps < 1 || spec.Step <= 0 || spec.MaxCellOffset <= 0 {
			t.Errorf("%s: spesifikasi tidak lengkap", spec.Dataset)
		}
	}
}
