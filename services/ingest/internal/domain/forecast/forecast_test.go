package forecast

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

var (
	analysis  = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	fetchedAt = time.Date(2026, 9, 24, 7, 20, 0, 0, time.UTC)
)

func valid() Forecast {
	vis := 10000.0
	f := Forecast{
		RegionCode: "32.73.01.1001", Province: "Jawa Barat", Regency: "Kota Bandung", District: "Sukasari", Village: "Sukarasa",
		Latitude: -6.874, Longitude: 107.585, AnalysisTime: analysis,
	}
	for i := range 3 {
		f.Steps = append(f.Steps, Step{
			ValidTime: analysis.Add(time.Duration(7+3*i) * time.Hour), TemperatureC: 28, HumidityPct: 52,
			CloudCoverPct: 64, WeatherCode: 2, WeatherDesc: "Cerah Berawan", WeatherDescEN: "Partly Cloudy",
			WindSpeedKmh: 1.4, WindFromDeg: 336, WindFrom: "NW",
		})
	}
	f.Steps[1].VisibilityM = &vis
	return f
}

func TestValidAccepted(t *testing.T) {
	if err := valid().Validate(fetchedAt); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	neg := -1.0
	cases := map[string]func(*Forecast){
		"kode bukan adm4":         func(f *Forecast) { f.RegionCode = "32.73.01" },
		"titik di luar Indonesia": func(f *Forecast) { f.Latitude = 40 },
		"bujur NaN":               func(f *Forecast) { f.Longitude = math.NaN() },
		"nama kepanjangan":        func(f *Forecast) { f.Village = strings.Repeat("a", maxTextLen+1) },
		"nama bukan UTF-8":        func(f *Forecast) { f.District = "\xff" },
		"analisis kosong":         func(f *Forecast) { f.AnalysisTime = time.Time{} },
		"analisis bukan UTC":      func(f *Forecast) { f.AnalysisTime = f.AnalysisTime.In(time.FixedZone("WIB", 7*3600)) },
		"analisis di masa depan":  func(f *Forecast) { f.AnalysisTime = fetchedAt.Add(2 * time.Hour) },
		"analisis terlalu tua":    func(f *Forecast) { f.AnalysisTime = fetchedAt.Add(-MaxAnalysisAge - time.Hour) },
		"tanpa langkah":           func(f *Forecast) { f.Steps = nil },
		"langkah tidak urut":      func(f *Forecast) { f.Steps[0], f.Steps[1] = f.Steps[1], f.Steps[0] },
		"langkah ganda":           func(f *Forecast) { f.Steps[1].ValidTime = f.Steps[0].ValidTime },
		"langkah kejauhan":        func(f *Forecast) { f.Steps[2].ValidTime = analysis.Add(MaxHorizon + time.Hour) },
		"langkah jauh sebelum":    func(f *Forecast) { f.Steps[0].ValidTime = analysis.Add(-48 * time.Hour) },
		"waktu langkah bukan UTC": func(f *Forecast) { f.Steps[0].ValidTime = f.Steps[0].ValidTime.In(time.FixedZone("x", 3600)) },
		"suhu mustahil":           func(f *Forecast) { f.Steps[0].TemperatureC = 80 },
		"kelembapan > 100":        func(f *Forecast) { f.Steps[0].HumidityPct = 101 },
		"awan negatif":            func(f *Forecast) { f.Steps[0].CloudCoverPct = -1 },
		"hujan tak terhingga":     func(f *Forecast) { f.Steps[0].PrecipitationMM = math.Inf(1) },
		"angin negatif":           func(f *Forecast) { f.Steps[0].WindSpeedKmh = -3 },
		"arah angin > 360":        func(f *Forecast) { f.Steps[0].WindFromDeg = 361 },
		"jarak pandang negatif":   func(f *Forecast) { f.Steps[0].VisibilityM = &neg },
		"kode cuaca negatif":      func(f *Forecast) { f.Steps[0].WeatherCode = -1 },
		"deskripsi bukan UTF-8":   func(f *Forecast) { f.Steps[0].WeatherDesc = "\xfe" },
		"terlalu banyak langkah": func(f *Forecast) {
			for i := range MaxSteps {
				f.Steps = append(f.Steps, Step{ValidTime: analysis.Add(time.Duration(i+20) * time.Hour)})
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := valid()
			f.Steps = slices.Clone(f.Steps)
			mutate(&f)
			if err := f.Validate(fetchedAt); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, ingin ErrInvalid", err)
			}
		})
	}
}

func TestValidCode(t *testing.T) {
	for _, c := range []string{"32.73.01.1001", "11.01.01.2001"} {
		if !ValidCode(c) {
			t.Errorf("%s seharusnya valid", c)
		}
	}
	for _, c := range []string{"", "32", "32.73.01", "32.73.01.100", "32.73.01.10011", "32-73-01-1001", "32.73.01.1001 "} {
		if ValidCode(c) {
			t.Errorf("%q seharusnya tidak valid", c)
		}
	}
}

func TestOrderFocusFirst(t *testing.T) {
	codes := []string{"32.01.01.2001", "32.73.02.1001", "32.04.01.2001", "32.73.01.1001", "32.77.01.1001", "32.01.01.2001", "32.730.1.1001"}
	got := Order(codes, []string{"32.73", "32.04", "32.77", ""})
	want := []string{"32.73.01.1001", "32.73.02.1001", "32.04.01.2001", "32.77.01.1001", "32.01.01.2001", "32.730.1.1001"}
	if !slices.Equal(got, want) {
		t.Fatalf("Order = %v\ningin   %v", got, want)
	}
	if !slices.Equal(Order(codes, nil), []string{"32.01.01.2001", "32.04.01.2001", "32.73.01.1001", "32.73.02.1001", "32.730.1.1001", "32.77.01.1001"}) {
		t.Fatal("tanpa fokus harus urut kode")
	}
	if codes[0] != "32.01.01.2001" || len(codes) != 7 {
		t.Fatal("Order mengubah masukan")
	}
}

// Properti Order: hasilnya himpunan kode yang sama tanpa ganda, tidak
// bergantung urutan masukan, dan tidak ada kode fokus yang muncul setelah
// kode non-fokus.
func FuzzOrder(f *testing.F) {
	f.Add("32.73.01.1001,32.01.01.2001,32.04.01.2001,32.73.01.1001", "32.73,32.04", uint8(3))
	f.Add("a,b,c", "", uint8(0))
	f.Fuzz(func(t *testing.T, list, focusList string, rot uint8) {
		codes := strings.Split(list, ",")
		focus := strings.Split(focusList, ",")
		got := Order(codes, focus)
		rotated := slices.Clone(codes)
		if len(rotated) > 0 {
			k := int(rot) % len(rotated)
			rotated = append(rotated[k:], rotated[:k]...)
			slices.Reverse(rotated)
		}
		if !slices.Equal(got, Order(rotated, focus)) {
			t.Fatal("hasil bergantung urutan masukan")
		}
		uniq := slices.Compact(slices.Sorted(slices.Values(codes)))
		if !slices.Equal(slices.Sorted(slices.Values(got)), uniq) {
			t.Fatal("himpunan kode berubah atau masih ganda")
		}
		isFocus := func(c string) bool {
			for _, p := range focus {
				if p != "" && (c == p || strings.HasPrefix(c, p+".")) {
					return true
				}
			}
			return false
		}
		seenOther := false
		for _, c := range got {
			if !isFocus(c) {
				seenOther = true
			} else if seenOther {
				t.Fatalf("kode fokus %s muncul setelah kode non-fokus: %v", c, got)
			}
		}
	})
}
