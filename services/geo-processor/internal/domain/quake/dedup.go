package quake

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// EarthRadiusKm adalah jari-jari rata-rata bumi (IUGG), dipakai untuk jarak
// lingkaran besar. Selisih dengan jarak elipsoid PostGIS di bawah 0,5%.
const EarthRadiusKm = 6371.0088

// DistanceKm menghitung jarak lingkaran besar (haversine) antara dua titik.
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	p1, p2 := lat1*rad, lat2*rad
	dp := p2 - p1
	dl := (lon2 - lon1) * rad
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * EarthRadiusKm * math.Asin(math.Sqrt(math.Min(1, h)))
}

// Origin adalah parameter pusat gempa yang dibandingkan saat deduplikasi.
type Origin struct {
	OccurredAt time.Time
	Latitude   float64
	Longitude  float64
	Magnitude  float64
}

// Rule adalah ambang deduplikasi: dua origin dianggap kejadian yang sama bila
// ketiga selisihnya berada di dalam ambang.
type Rule struct {
	MaxTimeDelta      time.Duration
	MaxDistanceKm     float64
	MaxMagnitudeDelta float64
	// LargeMagnitude dan LargeTimeDelta melebarkan jendela waktu bila salah
	// satu origin bermagnitudo ≥ LargeMagnitude: waktu kejadian gempa besar
	// paling berbeda antar-agensi (set berlabel ISC: sampai 22 dtk untuk
	// M 7,1). Keduanya nol berarti tidak ada jendela khusus.
	LargeMagnitude float64
	LargeTimeDelta time.Duration
}

// TimeDelta adalah jendela waktu yang berlaku untuk pasangan a dan b.
func (r Rule) TimeDelta(a, b Origin) time.Duration {
	if r.LargeTimeDelta > r.MaxTimeDelta && math.Max(a.Magnitude, b.Magnitude) >= r.LargeMagnitude-epsilon {
		return r.LargeTimeDelta
	}
	return r.MaxTimeDelta
}

// Window adalah jendela waktu terlebar aturan, untuk mencari kandidat.
func (r Rule) Window() time.Duration { return max(r.MaxTimeDelta, r.LargeTimeDelta) }

// Toleransi pembanding bilangan desimal: 5,4 − 4,7 di float64 adalah
// 0,7000000000000002, dan itu harus tetap lolos ambang 0,7.
const epsilon = 1e-9

// Ambang bawaan. Angka PRD (90 dtk, 75 km, 0,7) adalah nilai awal. Ambang
// lintas sumber diukur terhadap set berlabel ISC Bulletin 2021–Okt 2024
// (docs/calibration/dedup-berlabel.md, ADR 0025) dan katalog BMKG × USGS
// Jawa Barat 2008–2023 (docs/calibration/dedup-gempa.md, ADR 0008).
var (
	// PRDInitialRule adalah ambang awal dari PRD, disimpan untuk kalibrasi.
	PRDInitialRule = Rule{MaxTimeDelta: 90 * time.Second, MaxDistanceKm: 75, MaxMagnitudeDelta: 0.7}
	// ADR0008CrossSourceRule adalah ambang lintas sumber hasil kalibrasi
	// katalog (ADR 0008), bawaan sampai fase 1e-3c-2c; disimpan untuk
	// pembanding.
	ADR0008CrossSourceRule = Rule{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 1.0}
	// DefaultCrossSourceRule menggabungkan laporan BMKG dan USGS (ADR 0025).
	// Jendela 20 detik memisahkan gempa kembar yang berselang 20–30 detik,
	// dilebarkan ke 60 detik untuk M ≥ 6; 150 km dan 1,3 menangkap gempa
	// laut jauh yang lokasi dan magnitudo kedua agensinya paling berbeda.
	DefaultCrossSourceRule = Rule{
		MaxTimeDelta: 20 * time.Second, MaxDistanceKm: 150, MaxMagnitudeDelta: 1.3,
		LargeMagnitude: 6, LargeTimeDelta: 60 * time.Second,
	}
	// DefaultRevisionRule menggabungkan dua ID berbeda dari sumber yang sama,
	// yaitu revisi parameter (BMKG memakai waktu kejadian sebagai ID, jadi
	// revisi waktu menghasilkan ID baru) atau solusi ganda di katalog sumber.
	DefaultRevisionRule = Rule{MaxTimeDelta: 10 * time.Second, MaxDistanceKm: 50, MaxMagnitudeDelta: 1.0}
)

// Validate memastikan ambang masuk akal.
func (r Rule) Validate() error {
	var errs []error
	if r.MaxTimeDelta <= 0 || r.MaxTimeDelta > 10*time.Minute {
		errs = append(errs, fmt.Errorf("selisih waktu %v harus di (0, 10m]", r.MaxTimeDelta))
	}
	if !(r.MaxDistanceKm > 0 && r.MaxDistanceKm <= 500) {
		errs = append(errs, fmt.Errorf("jarak %v km harus di (0, 500]", r.MaxDistanceKm))
	}
	if !(r.MaxMagnitudeDelta >= 0 && r.MaxMagnitudeDelta <= 3) {
		errs = append(errs, fmt.Errorf("selisih magnitudo %v harus di [0, 3]", r.MaxMagnitudeDelta))
	}
	if r.LargeMagnitude != 0 || r.LargeTimeDelta != 0 {
		if !(r.LargeMagnitude > 0 && r.LargeMagnitude <= 10) {
			errs = append(errs, fmt.Errorf("magnitudo gempa besar %v harus di (0, 10]", r.LargeMagnitude))
		}
		if r.LargeTimeDelta <= r.MaxTimeDelta || r.LargeTimeDelta > 10*time.Minute {
			errs = append(errs, fmt.Errorf("selisih waktu gempa besar %v harus di (%v, 10m]", r.LargeTimeDelta, r.MaxTimeDelta))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("aturan deduplikasi tidak valid: %w", errors.Join(errs...))
	}
	return nil
}

// Match melaporkan apakah a dan b memenuhi ambang, beserta skor kemiripan:
// jumlah tiap selisih dibagi ambangnya (0 = identik, ≤ 3 bila cocok). Skor
// dipakai memilih kandidat terbaik bila lebih dari satu yang cocok. Selisih
// waktu dibagi jendela yang berlaku untuk pasangan itu (TimeDelta).
func (r Rule) Match(a, b Origin) (ok bool, score float64) {
	dt := a.OccurredAt.Sub(b.OccurredAt).Abs()
	w := r.TimeDelta(a, b)
	if dt > w {
		return false, math.Inf(1)
	}
	dm := math.Abs(a.Magnitude - b.Magnitude)
	if dm > r.MaxMagnitudeDelta+epsilon {
		return false, math.Inf(1)
	}
	d := DistanceKm(a.Latitude, a.Longitude, b.Latitude, b.Longitude)
	if d > r.MaxDistanceKm+epsilon {
		return false, math.Inf(1)
	}
	score = float64(dt)/float64(w) + d/r.MaxDistanceKm
	if r.MaxMagnitudeDelta > 0 {
		score += dm / r.MaxMagnitudeDelta
	}
	return true, score
}
