// Package flood berisi aturan murni potensi banjir (ADR 0020–0021): ambang
// persentil per titik pantau sungai dan per sub-DAS, koreksi bias data
// prakiraan, penilaian keluaran model per hari (median dan P75 ensemble
// debit, indeks hujan 3/6/24 jam), serta bentuk kejadian hazard.flood.*
// yang disimpan dan diterbitkan. Invarian ambang sama dengan constraint
// migrasi 00007_flood.sql.
package flood

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
)

// ErrInvalid menandai ambang atau keluaran model yang melanggar invarian.
// Pesan dengan galat ini tidak akan berhasil bila dicoba ulang.
var ErrInvalid = errors.New("data banjir tidak valid")

// ErrNoThreshold berarti titik tidak punya ambang: daftar titik ingest dan
// ambang geo-processor tidak sejalan (kalibrasi perlu diperbarui).
var ErrNoThreshold = errors.New("titik tanpa ambang banjir")

// Indicator adalah dasar penilaian potensi banjir.
type Indicator string

// Indikator (kolom hazard.flood.indicator).
const (
	// Discharge: debit model GloFAS di titik pantau sungai.
	Discharge Indicator = "discharge"
	// Rainfall: indeks hujan sub-DAS. Bukan pengukuran debit, jadi tingkatnya
	// dibatasi dan berlabel "indikasi potensi banjir".
	Rainfall Indicator = "rainfall"
)

// None adalah tingkat di bawah ambang Info (atau tanpa data). Tingkat 1..4
// sama dengan hazard.Level.
const None hazard.Level = 0

// Windows adalah panjang jendela akumulasi indeks hujan (jam), urutan kolom
// RainfallThreshold.Windows.
var Windows = [3]int{3, 6, 24}

// Set adalah ambang tingkat Info, Waspada, Siaga, dan Bahaya (persentil 80,
// 90, 98, 99,5).
type Set [4]float64

// Validate memastikan ambang hingga, positif, dan naik tegas, sama dengan
// constraint *_order di database.
func (s Set) Validate(limit float64) error {
	prev := 0.0
	for i, v := range s {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= prev || v >= limit {
			return fmt.Errorf("%w: ambang tingkat %d = %v, harus > %v dan < %v", ErrInvalid, i+1, v, prev, limit)
		}
		prev = v
	}
	return nil
}

// Level mengembalikan tingkat tertinggi yang ambangnya dicapai atau dilewati
// v (PRD: Info ≥ p80, dst.). NaN selalu None.
func (s Set) Level(v float64) hazard.Level {
	l := None
	for i, t := range s {
		if v >= t {
			l = hazard.Level(i + 1)
		}
	}
	return l
}

// Scale mengalikan semua ambang dengan f.
func (s Set) Scale(f float64) Set {
	for i := range s {
		s[i] *= f
	}
	return s
}

// Batas atas nilai, sama dengan constraint database.
const (
	maxDischarge = 200000 // m³/s
	maxRainfall  = 1000   // mm per 24 jam
	maxTextLen   = 100
)

var (
	riverID = regexp.MustCompile(`^river:[a-z0-9]+(-[a-z0-9]+)*$`)
	basinID = regexp.MustCompile(`^catchment:[a-z0-9]+(-[a-z0-9]+)*$`)
)

// Period adalah periode klimatologi ambang (tanggal UTC, inklusif).
type Period struct {
	From, To time.Time
}

// DischargeThreshold adalah ambang debit satu titik pantau sungai.
type DischargeThreshold struct {
	SiteID      string
	River, Name string
	// Cell adalah pusat sel GloFAS tempat ambang dihitung.
	Cell series.Point
	P50  float64
	// Climatology adalah persentil debit harian reanalisis (apa adanya dari
	// make flood-threshold).
	Climatology Set
	// SeamlessRatio adalah p98 data prakiraan (seamless_v4) dibagi p98
	// reanalisis di periode tumpang tindih.
	SeamlessRatio float64
	// Correction adalah faktor pengali ambang (1 = tanpa koreksi).
	Correction float64
	// MaxLevel adalah tingkat tertinggi titik ini.
	MaxLevel hazard.Level
}

// Effective adalah ambang yang dipakai menilai keluaran prakiraan.
func (t DischargeThreshold) Effective() Set { return t.Climatology.Scale(t.Correction) }

// RainfallThreshold adalah ambang indeks hujan satu sub-DAS.
type RainfallThreshold struct {
	SiteID string
	Name   string
	// P50 dan Windows sejajar dengan flood.Windows (3, 6, 24 jam).
	P50      [3]float64
	Windows  [3]Set
	MaxLevel hazard.Level
}

// Policy adalah aturan potensi banjir (ADR 0020 butir 5 dan 9, ADR 0021).
type Policy struct {
	// Horizon adalah jumlah hari setelah hari ini yang dinilai (hari ini
	// sampai +Horizon, tanggal UTC).
	Horizon int
	// Linger adalah lama kejadian tetap aktif sejak keluaran terakhir yang
	// mencapai ambang Info (PRD: 24 jam).
	Linger time.Duration
	// BiasCutoff: ambang titik dengan rasio p98 seamless/reanalisis di bawah
	// nilai ini dikalikan rasionya.
	BiasCutoff float64
	// RainfallMax adalah tingkat tertinggi indeks hujan.
	RainfallMax hazard.Level
	// Regulated adalah batas tingkat titik yang debit modelnya diatur waduk.
	Regulated map[string]hazard.Level
}

// DefaultPolicy mengembalikan aturan ADR 0021.
func DefaultPolicy() Policy {
	return Policy{
		Horizon: 3, Linger: 24 * time.Hour, BiasCutoff: 0.9, RainfallMax: hazard.LevelSiaga,
		Regulated: map[string]hazard.Level{
			"river:citarum-hilir-jatiluhur": hazard.LevelSiaga,
			"river:citarum-karawang":        hazard.LevelSiaga,
			"river:citarum-muara":           hazard.LevelSiaga,
		},
	}
}

// Validate memastikan aturan masuk akal.
func (p Policy) Validate() error {
	var errs []error
	if p.Horizon < 0 || p.Horizon > 9 {
		errs = append(errs, fmt.Errorf("horizon %d hari di luar 0..9", p.Horizon))
	}
	if p.Linger < time.Hour || p.Linger > 7*24*time.Hour {
		errs = append(errs, fmt.Errorf("lama aktif %v di luar 1 jam..7 hari", p.Linger))
	}
	if !(p.BiasCutoff > 0 && p.BiasCutoff <= 1) {
		errs = append(errs, fmt.Errorf("batas koreksi bias %v di luar (0, 1]", p.BiasCutoff))
	}
	if p.RainfallMax < hazard.LevelInfo || p.RainfallMax > hazard.LevelSiaga {
		errs = append(errs, fmt.Errorf("tingkat tertinggi indeks hujan %d di luar Info..Siaga", p.RainfallMax))
	}
	for id, l := range p.Regulated {
		if !riverID.MatchString(id) || l < hazard.LevelInfo || l > hazard.LevelBahaya {
			errs = append(errs, fmt.Errorf("batas tingkat %s = %d tidak valid", id, l))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: aturan banjir: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
}

// DischargeRow adalah satu baris ambang debit hasil make flood-threshold.
type DischargeRow struct {
	SiteID        string
	River, Name   string
	Cell          series.Point
	P50           float64
	Climatology   Set
	SeamlessRatio float64
}

// Discharge memvalidasi baris dan menerapkan koreksi bias serta batas tingkat.
func (p Policy) Discharge(r DischargeRow) (DischargeThreshold, error) {
	var errs []error
	if !riverID.MatchString(r.SiteID) || len(r.SiteID) > 64 {
		errs = append(errs, fmt.Errorf("ID titik %q bukan river:<slug>", r.SiteID))
	}
	for _, t := range []struct{ name, v string }{{"sungai", r.River}, {"nama", r.Name}} {
		if err := checkText(t.v); err != nil || t.v == "" {
			errs = append(errs, fmt.Errorf("%s %q kosong atau tidak valid", t.name, t.v))
		}
	}
	if !r.Cell.InIndonesia() {
		errs = append(errs, fmt.Errorf("sel (%v, %v) di luar Indonesia", r.Cell.Lat, r.Cell.Lon))
	}
	if err := r.Climatology.Validate(maxDischarge); err != nil {
		errs = append(errs, err)
	}
	if math.IsNaN(r.P50) || r.P50 < 0 || r.P50 > r.Climatology[0] {
		errs = append(errs, fmt.Errorf("median %v di luar 0..p80", r.P50))
	}
	if !(r.SeamlessRatio > 0 && r.SeamlessRatio < 10) {
		errs = append(errs, fmt.Errorf("rasio p98 seamless %v di luar (0, 10)", r.SeamlessRatio))
	}
	if len(errs) > 0 {
		return DischargeThreshold{}, fmt.Errorf("%w: ambang debit %s: %w", ErrInvalid, r.SiteID, errors.Join(errs...))
	}
	t := DischargeThreshold{
		SiteID: r.SiteID, River: r.River, Name: r.Name, Cell: r.Cell, P50: r.P50,
		Climatology: r.Climatology, SeamlessRatio: r.SeamlessRatio, Correction: 1, MaxLevel: hazard.LevelBahaya,
	}
	if r.SeamlessRatio < p.BiasCutoff {
		t.Correction = r.SeamlessRatio
	}
	if l, ok := p.Regulated[r.SiteID]; ok {
		t.MaxLevel = l
	}
	return t, nil
}

// RainfallRow adalah satu baris ambang indeks hujan hasil make rain-threshold.
type RainfallRow struct {
	SiteID string
	Name   string
	Hours  int
	P50    float64
	Levels Set
}

// Rainfall menyusun ambang per sub-DAS dari baris per jendela. Setiap
// sub-DAS wajib punya tepat satu baris untuk setiap jendela di Windows.
func (p Policy) Rainfall(rows []RainfallRow) (map[string]RainfallThreshold, error) {
	out := map[string]RainfallThreshold{}
	seen := map[string]map[int]bool{}
	var errs []error
	for _, r := range rows {
		w := slices.Index(Windows[:], r.Hours)
		switch {
		case !basinID.MatchString(r.SiteID) || len(r.SiteID) > 64:
			errs = append(errs, fmt.Errorf("ID sub-DAS %q bukan catchment:<slug>", r.SiteID))
			continue
		case w < 0:
			errs = append(errs, fmt.Errorf("%s: jendela %d jam tidak dikenal", r.SiteID, r.Hours))
			continue
		case seen[r.SiteID][r.Hours]:
			errs = append(errs, fmt.Errorf("%s: jendela %d jam ganda", r.SiteID, r.Hours))
			continue
		}
		if err := checkText(r.Name); err != nil || r.Name == "" {
			errs = append(errs, fmt.Errorf("%s: nama %q tidak valid", r.SiteID, r.Name))
		}
		if err := r.Levels.Validate(maxRainfall); err != nil {
			errs = append(errs, fmt.Errorf("%s, %d jam: %w", r.SiteID, r.Hours, err))
		}
		if math.IsNaN(r.P50) || r.P50 < 0 || r.P50 > r.Levels[0] {
			errs = append(errs, fmt.Errorf("%s, %d jam: median %v di luar 0..p80", r.SiteID, r.Hours, r.P50))
		}
		t, ok := out[r.SiteID]
		if !ok {
			t = RainfallThreshold{SiteID: r.SiteID, Name: r.Name, MaxLevel: p.RainfallMax}
			seen[r.SiteID] = map[int]bool{}
		} else if t.Name != r.Name {
			errs = append(errs, fmt.Errorf("%s: nama berbeda antarbaris", r.SiteID))
		}
		seen[r.SiteID][r.Hours] = true
		t.P50[w], t.Windows[w] = r.P50, r.Levels
		out[r.SiteID] = t
	}
	for _, id := range slices.Sorted(maps.Keys(seen)) {
		if len(seen[id]) != len(Windows) {
			errs = append(errs, fmt.Errorf("%s: %d dari %d jendela", id, len(seen[id]), len(Windows)))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: ambang indeks hujan: %w", ErrInvalid, errors.Join(errs...))
	}
	return out, nil
}

// Source adalah asal satu daftar ambang: periode klimatologi dan SHA-256
// isi file, supaya baris di ref.* bisa dilacak ke versi kalibrasinya.
type Source struct {
	Period Period
	SHA256 [32]byte
}

// Calibration adalah semua ambang yang dipakai geo-processor.
type Calibration struct {
	Discharge       map[string]DischargeThreshold
	Rainfall        map[string]RainfallThreshold
	DischargeSource Source
	RainfallSource  Source
}

// Validate memastikan kalibrasi lengkap.
func (c Calibration) Validate() error {
	var errs []error
	if len(c.Discharge) == 0 || len(c.Rainfall) == 0 {
		errs = append(errs, fmt.Errorf("%d titik debit dan %d sub-DAS", len(c.Discharge), len(c.Rainfall)))
	}
	for _, s := range []struct {
		name string
		src  Source
	}{{"debit", c.DischargeSource}, {"indeks hujan", c.RainfallSource}} {
		if s.src.Period.From.IsZero() || !s.src.Period.To.After(s.src.Period.From) || s.src.SHA256 == ([32]byte{}) {
			errs = append(errs, fmt.Errorf("asal ambang %s tidak lengkap", s.name))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: kalibrasi: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
}

func checkText(s string) error {
	switch {
	case len(s) > maxTextLen:
		return fmt.Errorf("panjang %d melebihi %d", len(s), maxTextLen)
	case !utf8.ValidString(s):
		return errors.New("bukan UTF-8 valid")
	case s != strings.TrimSpace(s):
		return errors.New("diawali atau diakhiri spasi")
	}
	return nil
}
