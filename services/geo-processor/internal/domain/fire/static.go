package fire

import (
	"fmt"
	"regexp"
	"time"
)

// Reason adalah alasan sumber dianggap tetap (kolom alasan
// sumber-panas-tetap-32.csv).
type Reason string

// Alasan sumber tetap.
const (
	ReasonPersistent Reason = "persisten"
	ReasonFIRMS      Reason = "type-firms"
	ReasonBoth       Reason = "keduanya"
)

// StaticSource adalah satu sumber panas tetap (industri, suar gas, PLTU,
// anjungan lepas pantai) yang deteksinya tidak menjadi kejadian.
type StaticSource struct {
	// ID adalah pusat lima desimal, misal "-6.37815,108.39848".
	ID          string
	Lat, Lon    float64
	RadiusKm    float64
	Reason      Reason
	Detections  int
	First, Last time.Time
}

var staticID = regexp.MustCompile(`^-?[0-9]{1,2}\.[0-9]{5},-?[0-9]{1,3}\.[0-9]{5}$`)

// Validate memeriksa invarian; sama dengan constraint ref.fire_static_source.
func (s StaticSource) Validate() error {
	switch {
	case !staticID.MatchString(s.ID) || s.ID != fmt.Sprintf("%.5f,%.5f", s.Lat, s.Lon):
		return fmt.Errorf("%w: ID sumber tetap %q tidak cocok dengan pusat (%v, %v)", ErrInvalid, s.ID, s.Lat, s.Lon)
	case !(s.Lat >= -12 && s.Lat <= 7 && s.Lon >= 94 && s.Lon <= 142):
		return fmt.Errorf("%w: sumber tetap %s di luar Indonesia", ErrInvalid, s.ID)
	case !(s.RadiusKm > 0 && s.RadiusKm <= 20):
		return fmt.Errorf("%w: jari-jari sumber tetap %s %v km", ErrInvalid, s.ID, s.RadiusKm)
	case s.Reason != ReasonPersistent && s.Reason != ReasonFIRMS && s.Reason != ReasonBoth:
		return fmt.Errorf("%w: alasan sumber tetap %s %q", ErrInvalid, s.ID, s.Reason)
	case s.Detections < 1 || s.First.IsZero() || s.Last.Before(s.First):
		return fmt.Errorf("%w: sumber tetap %s: %d deteksi, %s..%s", ErrInvalid, s.ID, s.Detections, s.First, s.Last)
	}
	return nil
}

// Mask adalah daftar sumber panas tetap.
type Mask []StaticSource

// Match mengembalikan sumber pertama yang jari-jarinya memuat (lat, lon),
// sama dengan firecluster.Mask.Match.
func (m Mask) Match(lat, lon float64) (StaticSource, bool) {
	for _, s := range m {
		if DistanceKm(lat, lon, s.Lat, s.Lon) <= s.RadiusKm {
			return s, true
		}
	}
	return StaticSource{}, false
}

// Period adalah rentang tanggal UTC arsip kalibrasi.
type Period struct{ From, To time.Time }

// Calibration adalah saringan sumber panas tetap yang disematkan ke binary
// (salinan persis docs/calibration/sumber-panas-tetap-32.csv).
type Calibration struct {
	Static Mask
	Period Period
	SHA256 [32]byte
}

// Validate memeriksa setiap sumber, ID unik, dan periode.
func (c Calibration) Validate() error {
	if c.Period.From.IsZero() || !c.Period.From.Before(c.Period.To) {
		return fmt.Errorf("%w: periode arsip %s..%s", ErrInvalid, c.Period.From, c.Period.To)
	}
	seen := map[string]bool{}
	for _, s := range c.Static {
		if err := s.Validate(); err != nil {
			return err
		}
		if seen[s.ID] {
			return fmt.Errorf("%w: sumber tetap %s ganda", ErrInvalid, s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}
