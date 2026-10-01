package threshold

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPercentile(t *testing.T) {
	// Nilai pembanding dari numpy.percentile (metode linear, bawaan).
	vs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := map[float64]float64{0: 1, 0.5: 5.5, 0.8: 8.2, 0.9: 9.1, 0.98: 9.82, 0.995: 9.955, 1: 10}
	for q, want := range cases {
		if got := Percentile(vs, q); !near(got, want) {
			t.Errorf("q=%v: %v, ingin %v", q, got, want)
		}
	}
	if !math.IsNaN(Percentile(nil, 0.5)) || !math.IsNaN(Percentile(vs, 1.5)) || !math.IsNaN(Percentile(vs, -0.1)) {
		t.Fatal("deret kosong atau q di luar 0..1 harus NaN")
	}
	if Percentile([]float64{7}, 0.995) != 7 {
		t.Fatal("satu nilai")
	}
}

// ramp adalah 0..n-1 diacak urutannya, jadi persentilnya diketahui.
func ramp(n int) []float64 {
	vs := make([]float64, n)
	for i := range vs {
		vs[(i*7919)%n] = float64(i)
	}
	return vs
}

func TestCompute(t *testing.T) {
	vs := append(ramp(4001), math.NaN(), math.NaN())
	s, err := Compute(vs, MinSamples)
	if err != nil {
		t.Fatal(err)
	}
	want := Set{P50: 2000, Levels: [4]float64{3200, 3600, 3920, 3980}}
	if s != want {
		t.Fatalf("%+v, ingin %+v", s, want)
	}
	if _, err := Compute(ramp(100), MinSamples); !errors.Is(err, ErrTooFew) {
		t.Fatal(err)
	}
	if _, err := Compute(ramp(10), 0); err != nil {
		t.Fatalf("minSamples 0 berarti paling sedikit 1: %v", err)
	}
	if _, err := Compute(append(ramp(4000), -1), MinSamples); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Compute(append(ramp(4000), math.Inf(1)), MinSamples); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// Deret yang hampir selalu nol (sel kering) tidak punya ambang yang naik.
	flat := make([]float64, 4000)
	flat[0] = 5
	if _, err := Compute(flat, MinSamples); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	good := Set{P50: 1, Levels: [4]float64{2, 3, 4, 5}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Set{
		"sama":       {P50: 1, Levels: [4]float64{2, 3, 3, 5}},
		"turun":      {P50: 1, Levels: [4]float64{2, 4, 3, 5}},
		"nol":        {P50: 0, Levels: [4]float64{0, 3, 4, 5}},
		"nan":        {P50: 1, Levels: [4]float64{2, math.NaN(), 4, 5}},
		"tak hingga": {P50: 1, Levels: [4]float64{2, 3, 4, math.Inf(1)}},
		"median":     {P50: 3, Levels: [4]float64{2, 3.5, 4, 5}},
		"median nan": {P50: math.NaN(), Levels: [4]float64{2, 3, 4, 5}},
	} {
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLevel(t *testing.T) {
	s := Set{P50: 1, Levels: [4]float64{10, 20, 30, 40}}
	cases := map[float64]Level{0: Normal, 9.99: Normal, 10: Info, 19.9: Info, 20: Waspada, 30: Siaga, 39.9: Siaga, 40: Bahaya, 1e6: Bahaya, math.NaN(): Normal}
	for v, want := range cases {
		if got := s.Level(v); got != want {
			t.Errorf("%v: %v, ingin %v", v, got, want)
		}
	}
	names := []string{"normal", "Info", "Waspada", "Siaga", "Bahaya", "Level(9)"}
	for i, l := range []Level{Normal, Info, Waspada, Siaga, Bahaya, 9} {
		if l.String() != names[i] {
			t.Errorf("%d: %s", l, l)
		}
	}
}

func TestAnnualMax(t *testing.T) {
	start := time.Date(2019, 12, 30, 0, 0, 0, 0, time.UTC)
	daily := make([]float64, 2+366+365)
	for i := range daily {
		daily[i] = 1
	}
	daily[1] = 50        // 2019-12-31, tahun pendek dilewati
	daily[2+59] = 9      // 2020-02-29
	daily[2+366+100] = 7 // 2021-04-11
	daily[2+366+101] = math.NaN()
	peaks := AnnualMax(start, daily, 330)
	want := []Peak{
		{Day: time.Date(2020, 2, 29, 0, 0, 0, 0, time.UTC), Value: 9},
		{Day: time.Date(2021, 4, 11, 0, 0, 0, 0, time.UTC), Value: 7},
	}
	if !slices.Equal(peaks, want) {
		t.Fatalf("%v", peaks)
	}
	if got := AnnualMax(start, daily, 0); len(got) != 3 || got[0].Value != 50 {
		t.Fatalf("%v", got)
	}
	// Tahun yang semua harinya kosong tidak menghasilkan puncak walau minDays 0.
	allNaN := []float64{math.NaN(), math.NaN()}
	if got := AnnualMax(start, allNaN, 0); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestMedian(t *testing.T) {
	vs := []float64{5, 1, 3}
	if Median(vs) != 3 || vs[0] != 5 {
		t.Fatal("median atau urutan asli berubah")
	}
	if !math.IsNaN(Median(nil)) {
		t.Fatal("kosong")
	}
}

// bruteDailyMaxSum adalah definisi DailyMaxSum tanpa jendela geser.
func bruteDailyMaxSum(hourly []float64, hours int) []float64 {
	out := make([]float64, (len(hourly)+23)/24)
	for i := range out {
		out[i] = math.NaN()
	}
	if hours <= 0 {
		return out
	}
	for end := hours - 1; end < len(hourly); end++ {
		s, ok := 0.0, true
		for k := end - hours + 1; k <= end; k++ {
			if math.IsNaN(hourly[k]) {
				ok = false
				break
			}
			s += hourly[k]
		}
		if !ok {
			continue
		}
		if d := end / 24; math.IsNaN(out[d]) || s > out[d]+1e-9 {
			out[d] = s
		}
	}
	return out
}

func sameSeries(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.IsNaN(a[i]) != math.IsNaN(b[i]) || (!math.IsNaN(a[i]) && math.Abs(a[i]-b[i]) > 1e-6) {
			return false
		}
	}
	return true
}

func TestDailyMaxSum(t *testing.T) {
	h := make([]float64, 72)
	h[22], h[23], h[24] = 4, 5, 6 // badai melewati tengah malam
	h[50] = math.NaN()
	got := DailyMaxSum(h, 3)
	// Hari 0: jendela 21–23 = 9. Hari 1: jendela 22–24 = 15. Hari 2: jendela
	// yang memuat jam 50 (48–50, 49–51, 50–52) tidak dihitung, sisanya 0.
	if !sameSeries(got, []float64{9, 15, 0}) {
		t.Fatalf("%v", got)
	}
	// Jam pertama hari 0 tidak punya jendela utuh; hari tanpa jendela utuh NaN.
	if got := DailyMaxSum([]float64{1, 2}, 3); len(got) != 1 || !math.IsNaN(got[0]) {
		t.Fatalf("%v", got)
	}
	if got := DailyMaxSum(h, 0); len(got) != 3 || !math.IsNaN(got[0]) {
		t.Fatalf("%v", got)
	}
	if got := DailyMaxSum(h[:25], 24); !sameSeries(got, bruteDailyMaxSum(h[:25], 24)) {
		t.Fatalf("%v", got)
	}
}

// FuzzDailyMaxSum memastikan jendela geser sama dengan definisinya.
func FuzzDailyMaxSum(f *testing.F) {
	f.Add([]byte{1, 2, 3, 255, 4, 0, 9, 9, 9, 9}, uint8(3))
	f.Add([]byte{}, uint8(24))
	f.Fuzz(func(t *testing.T, raw []byte, hours uint8) {
		h := make([]float64, len(raw))
		for i, b := range raw {
			h[i] = float64(b) / 10
			if b == 255 {
				h[i] = math.NaN()
			}
		}
		n := int(hours % 30)
		if got, want := DailyMaxSum(h, n), bruteDailyMaxSum(h, n); !sameSeries(got, want) {
			t.Fatalf("jam %d: %v, ingin %v", n, got, want)
		}
	})
}

// FuzzLevel memastikan tingkat tidak pernah turun saat nilai naik.
func FuzzLevel(f *testing.F) {
	f.Add(1.0, 2.0, 3.0, 4.0, 2.5, 3.5)
	f.Fuzz(func(t *testing.T, a, b, c, d, x, y float64) {
		s := Set{Levels: [4]float64{a, b, c, d}}
		if s.Validate() != nil || math.IsNaN(x) || math.IsNaN(y) {
			return
		}
		if x > y {
			x, y = y, x
		}
		if s.Level(x) > s.Level(y) {
			t.Fatalf("tingkat %v > %v untuk %v ≤ %v", s.Level(x), s.Level(y), x, y)
		}
	})
}

func TestWeightedMean(t *testing.T) {
	got, err := WeightedMean([][]float64{{1, 2, math.NaN()}, {3, 4, 5}}, []float64{1, 3})
	if err != nil || !sameSeries(got, []float64{2.5, 3.5, math.NaN()}) {
		t.Fatalf("%v %v", got, err)
	}
	for name, c := range map[string]struct {
		cells [][]float64
		w     []float64
	}{
		"kosong":       {nil, nil},
		"jumlah beda":  {[][]float64{{1}}, []float64{1, 1}},
		"bobot nol":    {[][]float64{{1}, {2}}, []float64{1, 0}},
		"bobot inf":    {[][]float64{{1}}, []float64{math.Inf(1)}},
		"panjang beda": {[][]float64{{1, 2}, {3}}, []float64{1, 1}},
	} {
		if _, err := WeightedMean(c.cells, c.w); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
