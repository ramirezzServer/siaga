package riversnap

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

// Window adalah periode acuan reanalisis GloFAS yang dirata-rata untuk
// memilih sel. Debit rata-rata periode acuan hampir sebanding dengan luas
// daerah tangkapan di hulu sel, jadi pilihan sel tidak lagi ikut berubah
// dengan hujan beberapa hari terakhir seperti jendela prakiraan 14 hari
// (ADR 0019).
type Window struct {
	// Model adalah nilai parameter models Open-Meteo Flood API.
	Model string
	// From dan To adalah tanggal UTC (pukul 00.00), inklusif.
	From, To time.Time
}

// DefaultModel adalah reanalisis GloFAS v4 di Open-Meteo, data yang sama
// dengan dasar ambang persentil banjir.
const DefaultModel = "consolidated_v4"

// Periode acuan bawaan: dua tahun hidrologi penuh (Juli sampai Juni), jadi
// memuat dua musim hujan dan dua musim kemarau, di dalam periode reanalisis
// Open-Meteo. Rata-rata seluruh reanalisis untuk semua sel kandidat butuh
// puluhan ribu panggilan, jauh di atas kuota harian (ADR 0019).
//
// Reanalisis consolidated_v4 di Open-Meteo berisi 1997-01-01 sampai
// 2025-05-31 (dicek 2026-10-01 di beberapa sel Jawa Barat; 1984–1996 kosong
// semua walau dokumentasinya menyebut 1984 sampai Juli 2022), jadi
// ReanalysisStart 1997 (ADR 0020).
var (
	DefaultFrom     = time.Date(2020, 7, 1, 0, 0, 0, 0, time.UTC)
	DefaultTo       = time.Date(2022, 6, 30, 0, 0, 0, 0, time.UTC)
	ReanalysisStart = time.Date(1997, 1, 1, 0, 0, 0, 0, time.UTC)
)

// DefaultWindow adalah periode acuan bawaan.
func DefaultWindow() Window {
	return Window{Model: DefaultModel, From: DefaultFrom, To: DefaultTo}
}

var modelName = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// Validate memeriksa nama model dan tanggal.
func (w Window) Validate() error {
	switch {
	case !modelName.MatchString(w.Model):
		return fmt.Errorf("nama model %q tidak valid", w.Model)
	case !isDate(w.From) || !isDate(w.To):
		return errors.New("tanggal periode acuan harus pukul 00.00 UTC")
	case w.From.Before(ReanalysisStart):
		return fmt.Errorf("periode acuan dimulai %s, sebelum reanalisis GloFAS (%s)", date(w.From), date(ReanalysisStart))
	case w.To.Before(w.From):
		return fmt.Errorf("periode acuan %s..%s terbalik", date(w.From), date(w.To))
	}
	return nil
}

func isDate(t time.Time) bool {
	return t.Location() == time.UTC && t.Equal(t.Truncate(24*time.Hour))
}

// Days adalah jumlah hari periode acuan (inklusif).
func (w Window) Days() int {
	return int(w.To.Sub(w.From)/(24*time.Hour)) + 1
}

// Weight adalah bobot kuota Open-Meteo untuk satu lokasi dengan satu
// variabel harian: max(1, hari/14 × variabel/10) (calculateQueryWeight
// Open-Meteo, HANDOFF 1e-1).
func (w Window) Weight() float64 {
	return math.Max(1, float64(w.Days())/14/10)
}

// MinValid adalah jumlah hari berisi paling sedikit supaya debit rata-rata
// sel dipakai.
func (w Window) MinValid() int {
	return int(math.Ceil(MinCoverage * float64(w.Days())))
}

// MinCoverage adalah bagian hari periode acuan yang harus berisi; sel laut
// atau periode di luar data reanalisis menjawab nilai kosong.
const MinCoverage = 0.9

func (w Window) String() string {
	return fmt.Sprintf("%s %s..%s", w.Model, date(w.From), date(w.To))
}

func date(t time.Time) string { return t.Format(time.DateOnly) }
