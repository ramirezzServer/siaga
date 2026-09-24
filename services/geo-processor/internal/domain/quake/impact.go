package quake

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// Level adalah tingkat peringatan, sama urutan dan nilainya dengan
// siaga.hazard.v1.AlertLevel dan constraint database.
type Level int

// Tingkat peringatan dari PRD, urut dari paling ringan.
const (
	LevelInfo    Level = 1
	LevelWaspada Level = 2
	LevelSiaga   Level = 3
	LevelBahaya  Level = 4
)

func (l Level) String() string {
	switch l {
	case LevelInfo:
		return "info"
	case LevelWaspada:
		return "waspada"
	case LevelSiaga:
		return "siaga"
	case LevelBahaya:
		return "bahaya"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// Policy adalah aturan bisnis gempa dari PRD (bagian Aturan bisnis). Semua
// angka adalah nilai awal yang dikalibrasi sebelum R2, jadi disimpan sebagai
// konfigurasi, bukan konstanta di tengah kode.
type Policy struct {
	// Radius dirasakan R = 10^(FeltSlope·M − FeltIntercept) km.
	FeltSlope     float64
	FeltIntercept float64
	// Bahaya: M ≥ BahayaMinMagnitude dan episenter ≤ BahayaMaxDistanceKm.
	BahayaMinMagnitude  float64
	BahayaMaxDistanceKm float64
	// Siaga: di dalam radius dirasakan dan M ≥ SiagaFeltMinMagnitude, atau
	// episenter ≤ SiagaNearMaxDistanceKm dan M ≥ SiagaNearMinMagnitude.
	SiagaFeltMinMagnitude  float64
	SiagaNearMinMagnitude  float64
	SiagaNearMaxDistanceKm float64
	// ActiveFor adalah masa aktif kejadian gempa sejak waktu kejadian.
	ActiveFor time.Duration
	// MaxQueryRadiusKm membatasi pencarian wilayah terdampak (gempa M8+).
	MaxQueryRadiusKm float64
}

// DefaultPolicy mengembalikan angka PRD.
func DefaultPolicy() Policy {
	return Policy{
		FeltSlope:              0.5,
		FeltIntercept:          1,
		BahayaMinMagnitude:     6,
		BahayaMaxDistanceKm:    100,
		SiagaFeltMinMagnitude:  5,
		SiagaNearMinMagnitude:  4.5,
		SiagaNearMaxDistanceKm: 50,
		ActiveFor:              6 * time.Hour,
		MaxQueryRadiusKm:       1000,
	}
}

// Validate memastikan aturan konsisten.
func (p Policy) Validate() error {
	var errs []error
	if !(p.FeltSlope > 0 && p.FeltSlope <= 2) || math.IsNaN(p.FeltIntercept) || math.IsInf(p.FeltIntercept, 0) {
		errs = append(errs, errors.New("koefisien radius dirasakan tidak masuk akal"))
	}
	if !(p.BahayaMaxDistanceKm > 0) || !(p.SiagaNearMaxDistanceKm > 0) {
		errs = append(errs, errors.New("ambang jarak harus positif"))
	}
	if !(p.SiagaNearMinMagnitude <= p.SiagaFeltMinMagnitude && p.SiagaFeltMinMagnitude <= p.BahayaMinMagnitude) {
		errs = append(errs, errors.New("ambang magnitudo harus Siaga-dekat ≤ Siaga-dirasakan ≤ Bahaya"))
	}
	if p.ActiveFor <= 0 {
		errs = append(errs, errors.New("masa aktif harus positif"))
	}
	if !(p.MaxQueryRadiusKm >= p.BahayaMaxDistanceKm && p.MaxQueryRadiusKm >= p.SiagaNearMaxDistanceKm) {
		errs = append(errs, errors.New("radius pencarian harus mencakup ambang jarak"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("aturan gempa tidak valid: %w", errors.Join(errs...))
	}
	return nil
}

// FeltRadius menghitung estimasi radius dirasakan (hiposenter) dan radius di
// permukaan: R = 10^(0,5M − 1) km, R_permukaan = √(R² − h²). Bila kedalaman h
// melebihi R, gempa dianggap tidak dirasakan di permukaan (R_permukaan = 0).
// Kedalaman negatif (di atas muka laut, USGS) dihitung nol.
func (p Policy) FeltRadius(magnitude, depthKm float64) (hypocentralKm, surfaceKm float64) {
	r := math.Pow(10, p.FeltSlope*magnitude-p.FeltIntercept)
	h := math.Max(depthKm, 0)
	if h >= r {
		return r, 0
	}
	return r, math.Sqrt(r*r - h*h)
}

// LevelAt menghitung tingkat peringatan di satu lokasi yang berjarak
// distanceKm dari episenter, menurut tabel PRD. Tabel PRD tidak menyebut
// lokasi di dalam radius dirasakan untuk M ≥ 6 yang lebih dari 100 km; di sini
// diberi Siaga supaya tingkat tidak pernah turun saat magnitudo naik.
func (p Policy) LevelAt(magnitude, depthKm, distanceKm float64) Level {
	_, surface := p.FeltRadius(magnitude, depthKm)
	inside := surface > 0 && distanceKm <= surface+epsilon
	atLeast := func(m float64) bool { return magnitude >= m-epsilon }
	within := func(km float64) bool { return distanceKm <= km+epsilon }
	switch {
	case atLeast(p.BahayaMinMagnitude) && within(p.BahayaMaxDistanceKm):
		return LevelBahaya
	case inside && atLeast(p.SiagaFeltMinMagnitude),
		atLeast(p.SiagaNearMinMagnitude) && within(p.SiagaNearMaxDistanceKm):
		return LevelSiaga
	case inside:
		return LevelWaspada
	default:
		return LevelInfo
	}
}

// QueryRadiusKm adalah jarak terjauh dari episenter yang bisa bertingkat di
// atas Info, untuk membatasi pencarian wilayah.
func (p Policy) QueryRadiusKm(magnitude, depthKm float64) float64 {
	_, r := p.FeltRadius(magnitude, depthKm)
	if magnitude >= p.BahayaMinMagnitude-epsilon {
		r = math.Max(r, p.BahayaMaxDistanceKm)
	}
	if magnitude >= p.SiagaNearMinMagnitude-epsilon {
		r = math.Max(r, p.SiagaNearMaxDistanceKm)
	}
	return math.Min(r, p.MaxQueryRadiusKm)
}

// RegionDistance adalah jarak terdekat dari episenter ke batas satu
// kelurahan/desa (0 bila episenter di dalamnya).
type RegionDistance struct {
	Code       string
	Name       string
	DistanceKm float64
}

// Impact adalah perkiraan dampak di satu kelurahan/desa.
type Impact struct {
	RegionDistance
	Level      Level
	WithinFelt bool
}

// Assess menghitung dampak per wilayah dan tingkat kejadian. Hanya wilayah
// bertingkat Waspada ke atas yang dikembalikan, terdekat lebih dulu. Tingkat
// kejadian adalah tingkat tertinggi di wilayah pantauan (Info bila tidak ada),
// dinaikkan ke Bahaya bila BMKG menyatakan potensi tsunami dan ada wilayah
// pantauan yang terdampak. Aturan tsunami per lokasi pesisir ada di
// alert-engine (fase 3) karena butuh data garis pantai.
func (p Policy) Assess(magnitude, depthKm float64, tsunami Tsunami, regions []RegionDistance) (Level, []Impact) {
	_, surface := p.FeltRadius(magnitude, depthKm)
	level := LevelInfo
	var out []Impact
	for _, r := range regions {
		l := p.LevelAt(magnitude, depthKm, r.DistanceKm)
		if l < LevelWaspada {
			continue
		}
		out = append(out, Impact{RegionDistance: r, Level: l, WithinFelt: surface > 0 && r.DistanceKm <= surface+epsilon})
		level = max(level, l)
	}
	if tsunami == TsunamiPotential && len(out) > 0 {
		level = LevelBahaya
	}
	slices.SortFunc(out, func(a, b Impact) int {
		if c := cmp.Compare(a.DistanceKm, b.DistanceKm); c != 0 {
			return c
		}
		return cmp.Compare(a.Code, b.Code)
	})
	return level, out
}
