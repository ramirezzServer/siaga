// Package threshold menghitung ambang persentil dari deret klimatologi dan
// tingkat peringatan sebuah nilai terhadap ambang itu. Tingkat mengikuti PRD
// (bagian Aturan bisnis): Info ≥ p80, Waspada ≥ p90, Siaga ≥ p98, Bahaya ≥
// p99,5. Dipakai alat kalibrasi ambang banjir (debit harian GloFAS) dan indeks
// hujan sub-DAS (akumulasi hujan); fungsi di sini murni, tanpa I/O.
package threshold

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// Level adalah tingkat peringatan: 0 normal, 1 Info, 2 Waspada, 3 Siaga,
// 4 Bahaya (sama dengan siaga.hazard.v1.AlertLevel untuk 1..4).
type Level int

// Tingkat peringatan.
const (
	Normal Level = iota
	Info
	Waspada
	Siaga
	Bahaya
)

func (l Level) String() string {
	switch l {
	case Normal:
		return "normal"
	case Info:
		return "Info"
	case Waspada:
		return "Waspada"
	case Siaga:
		return "Siaga"
	case Bahaya:
		return "Bahaya"
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

// Quantiles adalah persentil ambang Info, Waspada, Siaga, dan Bahaya.
var Quantiles = [4]float64{0.80, 0.90, 0.98, 0.995}

// MinSamples adalah jumlah nilai bawaan paling sedikit supaya persentil 99,5
// masih bermakna (±18 nilai di atasnya): sepuluh tahun data harian.
const MinSamples = 3650

var (
	// ErrTooFew berarti deret terlalu pendek untuk persentil 99,5.
	ErrTooFew = errors.New("deret klimatologi terlalu pendek")
	// ErrInvalid berarti ambang tidak positif, tidak hingga, atau tidak naik.
	ErrInvalid = errors.New("ambang tidak valid")
)

// Set adalah ambang satu titik: median dan ambang tingkat 1..4.
type Set struct {
	P50 float64
	// Levels[i] adalah ambang tingkat i+1 (p80, p90, p98, p99,5).
	Levels [4]float64
}

// Compute menghitung ambang dari nilai klimatologi; paling sedikit minSamples
// nilai berisi. NaN dilewati; nilai negatif atau tak hingga ditolak.
func Compute(values []float64, minSamples int) (Set, error) {
	vs := make([]float64, 0, len(values))
	for _, v := range values {
		switch {
		case math.IsNaN(v):
			continue
		case math.IsInf(v, 0) || v < 0:
			return Set{}, fmt.Errorf("%w: nilai %v", ErrInvalid, v)
		}
		vs = append(vs, v)
	}
	if len(vs) < max(1, minSamples) {
		return Set{}, fmt.Errorf("%w: %d nilai, butuh %d", ErrTooFew, len(vs), minSamples)
	}
	slices.Sort(vs)
	s := Set{P50: Percentile(vs, 0.5)}
	for i, q := range Quantiles {
		s.Levels[i] = Percentile(vs, q)
	}
	return s, s.Validate()
}

// Percentile adalah kuantil q (0..1) dari nilai terurut naik, dengan
// interpolasi linear antara dua urutan terdekat (metode 7 Hyndman–Fan, bawaan
// NumPy dan R). Deret kosong menghasilkan NaN.
func Percentile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 || q < 0 || q > 1 {
		return math.NaN()
	}
	h := q * float64(n-1)
	lo := int(math.Floor(h))
	if lo >= n-1 {
		return sorted[n-1]
	}
	return sorted[lo] + (h-float64(lo))*(sorted[lo+1]-sorted[lo])
}

// Validate memeriksa ambang: hingga, positif, dan naik tegas. Ambang yang
// sama untuk dua tingkat membuat tingkat lebih rendah tidak pernah terjadi.
func (s Set) Validate() error {
	prev := 0.0
	for i, v := range s.Levels {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= prev {
			return fmt.Errorf("%w: ambang tingkat %d = %v, harus > %v", ErrInvalid, i+1, v, prev)
		}
		prev = v
	}
	if math.IsNaN(s.P50) || s.P50 < 0 || s.P50 > s.Levels[0] {
		return fmt.Errorf("%w: median %v", ErrInvalid, s.P50)
	}
	return nil
}

// Level mengembalikan tingkat nilai v: tingkat tertinggi yang ambangnya
// dicapai atau dilewati. NaN selalu normal.
func (s Set) Level(v float64) Level {
	l := Normal
	for i, t := range s.Levels {
		if v >= t {
			l = Level(i + 1)
		}
	}
	return l
}

// Peak adalah nilai terbesar satu periode beserta tanggalnya.
type Peak struct {
	Day   time.Time
	Value float64
}

// AnnualMax mengembalikan puncak tiap tahun kalender UTC dari deret harian
// yang dimulai pada start (pukul 00.00 UTC). Tahun dengan hari berisi kurang
// dari minDays dilewati.
func AnnualMax(start time.Time, daily []float64, minDays int) []Peak {
	var out []Peak
	cur, count := Peak{Value: math.NaN()}, 0
	year := -1
	flush := func() {
		if year >= 0 && count > 0 && count >= minDays {
			out = append(out, cur)
		}
	}
	for i, v := range daily {
		day := start.AddDate(0, 0, i)
		if day.Year() != year {
			flush()
			year, cur, count = day.Year(), Peak{Value: math.NaN()}, 0
		}
		if math.IsNaN(v) {
			continue
		}
		count++
		if math.IsNaN(cur.Value) || v > cur.Value {
			cur = Peak{Day: day, Value: v}
		}
	}
	flush()
	return out
}

// Median adalah median nilai (NaN bila kosong); urutan vs tidak diubah.
func Median(vs []float64) float64 {
	c := slices.Clone(vs)
	slices.Sort(c)
	return Percentile(c, 0.5)
}

// DailyMaxSum mengembalikan, untuk setiap hari UTC, akumulasi terbesar dari
// jendela `hours` jam berturut-turut yang berakhir di hari itu. hourly adalah
// deret per jam mulai start (pukul 00.00 UTC); jendela yang memuat NaN atau
// melewati awal deret tidak dihitung, dan hari tanpa jendela utuh bernilai
// NaN. Jumlah hari = ceil(len(hourly)/24).
func DailyMaxSum(hourly []float64, hours int) []float64 {
	days := (len(hourly) + 23) / 24
	out := make([]float64, days)
	for i := range out {
		out[i] = math.NaN()
	}
	if hours <= 0 {
		return out
	}
	sum, bad := 0.0, 0
	for i, v := range hourly {
		if math.IsNaN(v) {
			bad++
		} else {
			sum += v
		}
		if i >= hours {
			old := hourly[i-hours]
			if math.IsNaN(old) {
				bad--
			} else {
				sum -= old
			}
		}
		if i < hours-1 || bad > 0 {
			continue
		}
		// Pembulatan 1e-9 menghapus sisa galat floating point dari jendela geser.
		s := math.Round(sum*1e9) / 1e9
		if d := i / 24; math.IsNaN(out[d]) || s > out[d] {
			out[d] = s
		}
	}
	return out
}

// WeightedMean merata-rata beberapa deret sel dengan bobot (dinormalisasi
// supaya jumlahnya 1). Langkah yang salah satu selnya NaN bernilai NaN. Semua
// deret harus sama panjang.
func WeightedMean(cells [][]float64, weights []float64) ([]float64, error) {
	if len(cells) == 0 || len(cells) != len(weights) {
		return nil, fmt.Errorf("%w: %d deret untuk %d bobot", ErrInvalid, len(cells), len(weights))
	}
	total := 0.0
	for _, w := range weights {
		if !(w > 0) || math.IsInf(w, 0) {
			return nil, fmt.Errorf("%w: bobot %v", ErrInvalid, w)
		}
		total += w
	}
	n := len(cells[0])
	out := make([]float64, n)
	for k, c := range cells {
		if len(c) != n {
			return nil, fmt.Errorf("%w: panjang deret %d dan %d", ErrInvalid, len(c), n)
		}
		w := weights[k] / total
		for i, v := range c {
			out[i] += w * v
		}
	}
	return out, nil
}
