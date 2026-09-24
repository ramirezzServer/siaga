// Package quake berisi aturan murni geo-processor untuk gempa: menyimpan
// laporan per feed tanpa bergantung urutan pesan, menggabungkan feed menjadi
// satu solusi per sumber, mengelompokkan solusi BMKG dan USGS menjadi satu
// kejadian (deduplikasi), serta menghitung radius dirasakan dan tingkat
// peringatan. Tidak ada I/O di paket ini; jam dan data disuntikkan pemanggil.
package quake

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid menandai laporan yang melanggar invarian domain. Laporan seperti
// ini tidak akan pernah berhasil diproses, jadi konsumen memindahkannya ke DLQ
// tanpa mencoba ulang.
var ErrInvalid = errors.New("laporan gempa tidak valid")

// Source adalah penerbit data gempa.
type Source string

// Sumber gempa yang dikenal.
const (
	SourceBMKG Source = "bmkg"
	SourceUSGS Source = "usgs"
)

// rank menentukan sumber utama sebuah kejadian: angka kecil menang. BMKG
// adalah sumber resmi Indonesia (PRD, aturan deduplikasi), USGS pembanding.
func (s Source) rank() int {
	switch s {
	case SourceBMKG:
		return 0
	case SourceUSGS:
		return 1
	default:
		return math.MaxInt
	}
}

// Feed adalah feed asal laporan.
type Feed string

// Feed yang dikenal. Nilainya sama dengan ingest dan constraint database.
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
	TsunamiUnknown Tsunami = iota
	TsunamiNone
	TsunamiPotential
)

// String mengembalikan nilai yang disimpan di database.
func (t Tsunami) String() string {
	switch t {
	case TsunamiNone:
		return "none"
	case TsunamiPotential:
		return "potential"
	case TsunamiUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("Tsunami(%d)", int(t))
	}
}

// ParseTsunami kebalikan dari String.
func ParseTsunami(s string) (Tsunami, error) {
	switch s {
	case "unknown":
		return TsunamiUnknown, nil
	case "none":
		return TsunamiNone, nil
	case "potential":
		return TsunamiPotential, nil
	default:
		return 0, fmt.Errorf("%w: nilai tsunami tidak dikenal %q", ErrInvalid, s)
	}
}

// ReviewDeleted adalah status USGS untuk kejadian yang ditarik (bukan gempa).
const ReviewDeleted = "deleted"

// Batas nilai fisik; sama dengan validasi ingest dan constraint database.
const (
	MinMagnitude = -2.0
	MaxMagnitude = 10.0
	MinDepthKm   = -10.0
	MaxDepthKm   = 800.0
	// MaxClockSkew adalah toleransi waktu kejadian yang tampak di masa depan
	// dibanding waktu ambil.
	MaxClockSkew = 10 * time.Minute
	maxIDLen     = 128
	maxTextLen   = 1024
	maxAltIDs    = 32
)

// IdentityKey mengenali satu kejadian di satu sumber.
type IdentityKey struct {
	Source  Source
	EventID string
}

func (k IdentityKey) String() string { return string(k.Source) + ":" + k.EventID }

// Compare mengurutkan kunci menurut sumber lalu ID.
func (k IdentityKey) Compare(o IdentityKey) int {
	if c := cmp.Compare(k.Source, o.Source); c != 0 {
		return c
	}
	return cmp.Compare(k.EventID, o.EventID)
}

// ReportKey mengenali satu laporan: satu kejadian di satu feed.
type ReportKey struct {
	IdentityKey
	Feed Feed
}

// Report adalah laporan gempa dari satu feed, hasil dekode event raw.quake.*.
// Waktu selalu UTC.
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
	// FetchedAt dan ArchiveKey berasal dari FetchMeta ingest. Keduanya bukan
	// bagian isi laporan: tidak ikut ContentDigest.
	FetchedAt  time.Time
	ArchiveKey string
}

// Identity mengembalikan kunci kejadian di sumbernya.
func (r Report) Identity() IdentityKey { return IdentityKey{Source: r.Source, EventID: r.EventID} }

// Key mengembalikan kunci laporan.
func (r Report) Key() ReportKey { return ReportKey{IdentityKey: r.Identity(), Feed: r.Feed} }

// Deleted melaporkan apakah sumber menarik kejadian ini.
func (r Report) Deleted() bool { return r.ReviewStatus == ReviewDeleted }

// Validate memeriksa semua invarian. Semua pelanggaran dilaporkan sekaligus.
func (r Report) Validate() error {
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
	if len(r.AlternateIDs) > maxAltIDs {
		bad("ID alternatif %d melebihi %d", len(r.AlternateIDs), maxAltIDs)
	}
	for _, id := range r.AlternateIDs {
		if err := checkID(id); err != nil {
			bad("ID alternatif: %w", err)
		}
	}
	checkUTC := func(name string, t time.Time, required bool) {
		switch {
		case t.IsZero():
			if required {
				bad("%s kosong", name)
			}
		case t.Location() != time.UTC:
			bad("%s harus UTC, dapat %s", name, t.Location())
		}
	}
	checkUTC("waktu kejadian", r.OccurredAt, true)
	checkUTC("waktu ambil", r.FetchedAt, true)
	checkUTC("waktu revisi", r.SourceUpdatedAt, false)
	if !r.OccurredAt.IsZero() && !r.FetchedAt.IsZero() && r.OccurredAt.After(r.FetchedAt.Add(MaxClockSkew)) {
		bad("waktu kejadian %s di masa depan (diambil %s)", r.OccurredAt.Format(time.RFC3339), r.FetchedAt.Format(time.RFC3339))
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
	for _, f := range []struct{ name, value string }{
		{"magnitude_type", r.MagnitudeType},
		{"place", r.Place},
		{"felt", r.Felt},
		{"potential_text", r.PotentialText},
		{"review_status", r.ReviewStatus},
		{"archive_key", r.ArchiveKey},
	} {
		if err := checkText(f.value); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	for _, f := range []struct{ name, value string }{{"shakemap_url", r.ShakemapURL}, {"source_url", r.SourceURL}} {
		if err := checkURL(f.value); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, r.Key().IdentityKey, errors.Join(errs...))
}

// ContentDigest adalah SHA-256 isi laporan tanpa FetchedAt dan ArchiveKey.
// Dua pengambilan yang isinya sama menghasilkan digest yang sama.
func (r Report) ContentDigest() [32]byte {
	d := newDigester("siaga/quake-report/v1")
	d.Str(string(r.Source))
	d.Str(string(r.Feed))
	d.Str(r.EventID)
	d.Time(r.OccurredAt)
	d.F64(r.Latitude)
	d.F64(r.Longitude)
	d.F64(r.Magnitude)
	d.Str(r.MagnitudeType)
	d.F64(r.DepthKm)
	d.Str(r.Place)
	d.Str(r.Felt)
	d.Int(int64(r.Tsunami))
	d.Str(r.PotentialText)
	d.Str(r.ShakemapURL)
	d.Time(r.SourceUpdatedAt)
	d.Str(r.ReviewStatus)
	d.Int(int64(len(r.AlternateIDs)))
	for _, id := range r.AlternateIDs {
		d.Str(id)
	}
	d.Str(r.SourceURL)
	return d.Sum()
}

// newerThan mengurutkan dua laporan dari feed yang sama: revisi sumber lebih
// dulu (USGS "updated"), lalu waktu ambil, lalu digest sebagai penentu akhir.
// Urutan ini total, jadi hasil penyimpanan tidak bergantung urutan pesan.
func (r Report) newerThan(o Report) bool {
	if c := r.SourceUpdatedAt.Compare(o.SourceUpdatedAt); c != 0 {
		return c > 0
	}
	if c := r.FetchedAt.Compare(o.FetchedAt); c != 0 {
		return c > 0
	}
	a, b := r.ContentDigest(), o.ContentDigest()
	return bytes.Compare(a[:], b[:]) > 0
}

// Stored adalah laporan yang tersimpan beserta kapan kejadian ini pertama
// kali terlihat di feed tersebut.
type Stored struct {
	Report
	FirstSeenAt time.Time
}

// Apply menerapkan laporan masuk ke laporan tersimpan (nil bila belum ada).
//
// Hasil akhirnya hanya bergantung pada himpunan laporan yang pernah diterima,
// bukan urutannya: laporan tersimpan selalu yang terbaru menurut newerThan,
// dan FirstSeenAt selalu waktu ambil paling awal. write berarti baris harus
// ditulis ulang; changed berarti isi atau urutan kejadian ikut berubah sehingga
// pengelompokan perlu dihitung ulang.
func Apply(stored *Stored, in Report) (next Stored, write, changed bool) {
	if stored == nil {
		return Stored{Report: in, FirstSeenAt: in.FetchedAt}, true, true
	}
	next = *stored
	if in.FetchedAt.Before(next.FirstSeenAt) {
		next.FirstSeenAt = in.FetchedAt
		write, changed = true, true
	}
	if in.newerThan(stored.Report) {
		if in.ContentDigest() != stored.ContentDigest() {
			changed = true
		}
		next.Report = in
		write = true
	}
	return next, write, changed
}

func inRange(v, lo, hi float64) bool { return !math.IsNaN(v) && v >= lo && v <= hi }

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
	if slices.Contains([]byte(s), 0) {
		return errors.New("mengandung byte NUL")
	}
	return nil
}

func checkURL(s string) error {
	if s == "" {
		return nil
	}
	if err := checkText(s); err != nil {
		return err
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
