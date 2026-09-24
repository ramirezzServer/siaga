// Package quake berisi aturan murni untuk laporan gempa dari sumber mana pun:
// invarian nilai, identitas kejadian, dan tafsir teks potensi tsunami BMKG.
package quake

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid menandai laporan yang melanggar invarian domain.
var ErrInvalid = errors.New("laporan gempa tidak valid")

// Source adalah penerbit data gempa.
type Source string

// Sumber gempa yang dikenal.
const (
	SourceBMKG Source = "bmkg"
	SourceUSGS Source = "usgs"
)

// Feed adalah feed asal laporan.
type Feed string

// Feed yang dikenal beserta sumbernya.
const (
	FeedBMKGLatest  Feed = "bmkg-latest"
	FeedBMKGRecent  Feed = "bmkg-recent"
	FeedBMKGFelt    Feed = "bmkg-felt"
	FeedUSGSSummary Feed = "usgs-summary"
)

// Source mengembalikan sumber pemilik feed, atau "" bila feed tidak dikenal.
func (f Feed) Source() Source {
	switch f {
	case FeedBMKGLatest, FeedBMKGRecent, FeedBMKGFelt:
		return SourceBMKG
	case FeedUSGSSummary:
		return SourceUSGS
	default:
		return ""
	}
}

// Tsunami adalah pernyataan sumber tentang potensi tsunami.
type Tsunami int

// Nilai Tsunami.
const (
	// TsunamiUnknown: sumber tidak menyatakan apa pun tentang tsunami.
	TsunamiUnknown Tsunami = iota
	TsunamiNone
	TsunamiPotential
)

// Batas nilai fisik yang masih masuk akal. Di luar ini hampir pasti data rusak.
const (
	MinMagnitude = -2.0
	MaxMagnitude = 10.0
	// USGS memakai kedalaman negatif untuk hiposenter di atas muka laut.
	MinDepthKm = -10.0
	MaxDepthKm = 800.0
	// MaxClockSkew adalah toleransi waktu kejadian yang tampak di masa depan
	// dibanding waktu ambil, untuk menampung jam server yang sedikit berbeda.
	MaxClockSkew = 10 * time.Minute
	maxIDLen     = 128
	maxTextLen   = 1024
)

var minTime = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// Report adalah satu laporan gempa dari satu feed. Waktu selalu UTC.
type Report struct {
	Source          Source
	Feed            Feed
	EventID         string
	OccurredAt      time.Time
	Latitude        float64
	Longitude       float64
	Magnitude       float64
	MagnitudeType   string
	DepthKm         float64
	Place           string
	Felt            string
	Tsunami         Tsunami
	PotentialText   string
	ShakemapURL     string
	SourceUpdatedAt time.Time
	ReviewStatus    string
	AlternateIDs    []string
	SourceURL       string
}

// Validate memeriksa semua invarian. fetchedAt dipakai untuk menolak waktu
// kejadian di masa depan. Semua pelanggaran dilaporkan sekaligus.
func (r Report) Validate(fetchedAt time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if r.Feed.Source() == "" {
		bad("feed tidak dikenal %q", r.Feed)
	} else if r.Feed.Source() != r.Source {
		bad("feed %s bukan milik sumber %q", r.Feed, r.Source)
	}
	if err := checkID(r.EventID); err != nil {
		bad("ID kejadian: %w", err)
	}
	for _, id := range r.AlternateIDs {
		if err := checkID(id); err != nil {
			bad("ID alternatif: %w", err)
		}
	}
	switch {
	case r.OccurredAt.IsZero():
		bad("waktu kejadian kosong")
	case r.OccurredAt.Location() != time.UTC:
		bad("waktu kejadian harus UTC, dapat %s", r.OccurredAt.Location())
	case r.OccurredAt.Before(minTime):
		bad("waktu kejadian %s terlalu lampau", r.OccurredAt.Format(time.RFC3339))
	case r.OccurredAt.After(fetchedAt.Add(MaxClockSkew)):
		bad("waktu kejadian %s di masa depan (diambil %s)", r.OccurredAt.Format(time.RFC3339), fetchedAt.UTC().Format(time.RFC3339))
	}
	if !r.SourceUpdatedAt.IsZero() && r.SourceUpdatedAt.Location() != time.UTC {
		bad("waktu revisi harus UTC")
	}
	if !inRange(r.Latitude, -90, 90) {
		bad("lintang %v di luar -90..90", r.Latitude)
	}
	if !inRange(r.Longitude, -180, 180) {
		bad("bujur %v di luar -180..180", r.Longitude)
	}
	if !inRange(r.Magnitude, MinMagnitude, MaxMagnitude) {
		bad("magnitudo %v di luar %v..%v", r.Magnitude, MinMagnitude, MaxMagnitude)
	}
	if !inRange(r.DepthKm, MinDepthKm, MaxDepthKm) {
		bad("kedalaman %v km di luar %v..%v", r.DepthKm, MinDepthKm, MaxDepthKm)
	}
	switch r.Tsunami {
	case TsunamiUnknown, TsunamiNone, TsunamiPotential:
	default:
		bad("nilai tsunami tidak dikenal %d", r.Tsunami)
	}
	for _, f := range []field{
		{"magnitude_type", r.MagnitudeType},
		{"place", r.Place},
		{"felt", r.Felt},
		{"potential_text", r.PotentialText},
		{"review_status", r.ReviewStatus},
	} {
		if err := checkText(f.value); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	for _, f := range []field{{"shakemap_url", r.ShakemapURL}, {"source_url", r.SourceURL}} {
		if err := checkURL(f.value); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
}

type field struct{ name, value string }

func inRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && v >= lo && v <= hi
}

func checkID(id string) error {
	if id == "" || len(id) > maxIDLen {
		return fmt.Errorf("panjang %d harus 1..%d", len(id), maxIDLen)
	}
	for _, c := range id {
		if c > unicode.MaxASCII || !unicode.IsPrint(c) || unicode.IsSpace(c) {
			return fmt.Errorf("karakter %q tidak diizinkan", c)
		}
	}
	return nil
}

func checkText(s string) error {
	if len(s) > maxTextLen {
		return fmt.Errorf("panjang %d melebihi %d", len(s), maxTextLen)
	}
	if !utf8.ValidString(s) {
		return errors.New("bukan UTF-8 valid")
	}
	return nil
}

func checkURL(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > maxTextLen {
		return fmt.Errorf("panjang %d melebihi %d", len(s), maxTextLen)
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("URL rusak: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("URL harus https absolut, dapat %q", s)
	}
	return nil
}

// BMKGEventID membentuk ID kejadian BMKG dari waktu kejadian. BMKG tidak
// menerbitkan ID, jadi waktu UTC sampai detik dipakai; nilainya sama di
// autogempa, gempaterkini, dan gempadirasakan sehingga geo-processor bisa
// menggabungkan ketiganya.
func BMKGEventID(occurredAt time.Time) string {
	return occurredAt.UTC().Format("20060102150405")
}

// ParseTsunamiText menafsirkan teks "Potensi" BMKG. Teks yang bukan pernyataan
// tsunami (misal "Gempa ini dirasakan untuk diteruskan pada masyarakat")
// menghasilkan TsunamiUnknown, bukan TsunamiNone.
func ParseTsunamiText(s string) Tsunami {
	t := strings.Join(strings.Fields(strings.ToLower(s)), " ")
	switch {
	case !strings.Contains(t, "tsunami"):
		return TsunamiUnknown
	case strings.Contains(t, "tidak berpotensi tsunami"), strings.Contains(t, "tidak berpotensi menimbulkan tsunami"):
		return TsunamiNone
	case strings.Contains(t, "berpotensi tsunami"), strings.Contains(t, "berpotensi menimbulkan tsunami"),
		strings.Contains(t, "peringatan dini tsunami"):
		return TsunamiPotential
	default:
		return TsunamiUnknown
	}
}

// Kotak Indonesia untuk menyaring feed global (USGS). Sengaja lebih lebar
// dari wilayah darat supaya gempa laut di sekitar perbatasan tetap masuk.
const (
	indonesiaMinLat = -12.0
	indonesiaMaxLat = 7.0
	indonesiaMinLon = 94.0
	indonesiaMaxLon = 142.0
)

// InIndonesiaBox melaporkan apakah titik berada di kotak pantauan Indonesia.
func InIndonesiaBox(lat, lon float64) bool {
	return inRange(lat, indonesiaMinLat, indonesiaMaxLat) && inRange(lon, indonesiaMinLon, indonesiaMaxLon)
}
