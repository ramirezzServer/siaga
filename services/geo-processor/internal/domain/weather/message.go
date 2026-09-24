// Package weather berisi aturan murni peringatan dini cuaca (CAP) di
// geo-processor: invarian pesan, rantai pembaruan antar-pesan (references),
// identitas kejadian, tingkat peringatan dari severity, dan wilayah terdampak.
package weather

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

// ErrInvalid menandai pesan yang melanggar invarian domain. Pesan seperti ini
// tidak akan berhasil bila diulang.
var ErrInvalid = errors.New("pesan peringatan cuaca tidak valid")

// Batas yang sama dengan validasi ingest (domain/warning) dan constraint database.
const (
	MaxClockSkew  = 10 * time.Minute
	MaxValidFor   = 7 * 24 * time.Hour
	MinRingPoints = 4
	MaxPolygons   = 5_000
	MaxPoints     = 200_000
	MaxReferences = 64
	maxShortText  = 512
	maxLongText   = 16 << 10
)

// LanguagePrimary adalah bahasa teks yang wajib ada.
const LanguagePrimary = "id"

// Source adalah penerbit peringatan.
type Source string

// SourceBMKG adalah satu-satunya penerbit CAP yang dipakai saat ini.
const SourceBMKG Source = "bmkg"

// MsgType adalah jenis pesan CAP yang diteruskan ingest.
type MsgType string

// Nilai MsgType; sama dengan constraint hazard.cap_message.msg_type.
const (
	MsgAlert  MsgType = "alert"
	MsgUpdate MsgType = "update"
	MsgCancel MsgType = "cancel"
)

// Severity adalah keparahan CAP; sama dengan constraint hazard.cap_message.severity.
type Severity string

// Nilai Severity.
const (
	SeverityMinor    Severity = "minor"
	SeverityModerate Severity = "moderate"
	SeveritySevere   Severity = "severe"
	SeverityExtreme  Severity = "extreme"
	SeverityUnknown  Severity = "unknown"
)

// Level memetakan severity ke tingkat peringatan (PRD, Aturan bisnis):
// Minor = Info, Moderate = Waspada, Severe = Siaga, Extreme = Bahaya.
// Unknown dianggap Info supaya tidak memicu push tanpa dasar.
func (s Severity) Level() hazard.Level {
	switch s {
	case SeverityModerate:
		return hazard.LevelWaspada
	case SeveritySevere:
		return hazard.LevelSiaga
	case SeverityExtreme:
		return hazard.LevelBahaya
	case SeverityMinor, SeverityUnknown:
		return hazard.LevelInfo
	default:
		return hazard.LevelInfo
	}
}

// CAP mengembalikan nilai severity seperti ditulis CAP, misal "Moderate".
func (s Severity) CAP() string {
	if s == "" {
		return ""
	}
	return string(s[0]-'a'+'A') + string(s[1:])
}

func (s Severity) valid() bool {
	switch s {
	case SeverityMinor, SeverityModerate, SeveritySevere, SeverityExtreme, SeverityUnknown:
		return true
	default:
		return false
	}
}

// Key adalah identitas pesan CAP: penerbit dan identifier.
type Key struct {
	Source     Source
	Identifier string
}

func (k Key) String() string { return string(k.Source) + ":" + k.Identifier }

// Compare mengurutkan kunci.
func (k Key) Compare(o Key) int {
	return cmp.Or(cmp.Compare(k.Source, o.Source), cmp.Compare(k.Identifier, o.Identifier))
}

// Reference menunjuk pesan lain yang digantikan atau dibatalkan.
type Reference struct {
	Key
	Sender string
	Sent   time.Time
}

// Text adalah teks peringatan dalam satu bahasa.
type Text struct {
	Language    string
	Event       string
	Headline    string
	Description string
	Instruction string
	SenderName  string
}

// Point adalah titik WGS84.
type Point struct {
	Lat, Lon float64
}

// Ring adalah cincin poligon tertutup.
type Ring []Point

// Message adalah satu pesan CAP yang diterima dari raw.weather.*. Waktu UTC.
type Message struct {
	Key
	Sender     string
	Sent       time.Time
	MsgType    MsgType
	References []Reference
	Category   string
	EventCode  string
	// Urgency dan Certainty apa adanya dari CAP, misal "Immediate", "Observed".
	Urgency   string
	Certainty string
	Severity  Severity
	Effective time.Time
	Onset     time.Time // nol bila tidak diisi sumber
	Expires   time.Time
	Texts     []Text
	Web       string
	Contact   string
	SourceURL string
	AreaDesc  string
	// Polygons ada pada pesan dari raw.*; pesan dari penyimpanan hanya
	// membawa HasArea (area sudah digabung dan diperbaiki di PostGIS).
	Polygons []Ring
	HasArea  bool

	FetchedAt   time.Time
	FirstSeenAt time.Time
	ArchiveKey  string
	// Digest adalah ContentDigest saat pesan diterima; pesan dari penyimpanan
	// membawa nilai yang tersimpan (poligonnya tidak dimuat ulang).
	Digest [32]byte
	// Event adalah kejadian tempat pesan tergabung (kosong bila belum).
	Event hazard.EventID
}

var identifier = regexp.MustCompile(`^[!-~]{1,128}$`)

// Start adalah awal berlakunya pesan: onset bila ada, selain itu effective.
func (m Message) Start() time.Time {
	if !m.Onset.IsZero() {
		return m.Onset
	}
	return m.Effective
}

// Text mengembalikan teks untuk bahasa lang.
func (m Message) Text(lang string) (Text, bool) {
	i := slices.IndexFunc(m.Texts, func(t Text) bool { return t.Language == lang })
	if i < 0 {
		return Text{}, false
	}
	return m.Texts[i], true
}

// Validate memeriksa semua invarian pesan yang baru diterima. Semua
// pelanggaran dilaporkan sekaligus.
func (m Message) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if m.Source != SourceBMKG {
		bad("sumber %q tidak dikenal", m.Source)
	}
	if !identifier.MatchString(m.Identifier) {
		bad("identifier %q tidak valid", m.Identifier)
	}
	if m.Sender == "" || checkText(m.Sender, maxShortText) != nil {
		bad("sender kosong atau tidak valid")
	}
	for _, f := range []struct {
		name     string
		t        time.Time
		required bool
	}{
		{"sent", m.Sent, true},
		{"effective", m.Effective, true},
		{"onset", m.Onset, false},
		{"expires", m.Expires, true},
		{"fetched_at", m.FetchedAt, true},
		{"first_seen_at", m.FirstSeenAt, true},
	} {
		switch {
		case f.t.IsZero():
			if f.required {
				bad("%s kosong", f.name)
			}
		case f.t.Location() != time.UTC:
			bad("%s harus UTC", f.name)
		}
	}
	if !m.Sent.IsZero() && !m.FetchedAt.IsZero() && m.Sent.After(m.FetchedAt.Add(MaxClockSkew)) {
		bad("sent %s setelah diambil %s", m.Sent.Format(time.RFC3339), m.FetchedAt.Format(time.RFC3339))
	}
	if m.FirstSeenAt.After(m.FetchedAt) {
		bad("first_seen_at setelah fetched_at")
	}
	if !m.Expires.After(m.Effective) {
		bad("expires tidak setelah effective")
	} else if m.Expires.Sub(m.Effective) > MaxValidFor {
		bad("masa berlaku melebihi %v", MaxValidFor)
	}
	if !m.Onset.IsZero() && !m.Onset.Before(m.Expires) {
		bad("onset tidak sebelum expires")
	}
	switch m.MsgType {
	case MsgAlert:
	case MsgUpdate, MsgCancel:
		if len(m.References) == 0 {
			bad("%s tanpa references", m.MsgType)
		}
	default:
		bad("msgType %q tidak dikenal", m.MsgType)
	}
	if !m.Severity.valid() {
		bad("severity %q tidak dikenal", m.Severity)
	}
	errs = append(errs, m.validateReferences()...)
	errs = append(errs, m.validateTexts()...)
	errs = append(errs, m.validateArea()...)
	for _, f := range []struct{ name, value string }{{"web", m.Web}, {"source_url", m.SourceURL}} {
		if err := checkURL(f.value); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	for _, f := range []struct{ name, value string }{
		{"category", m.Category},
		{"event_code", m.EventCode},
		{"urgency", m.Urgency},
		{"certainty", m.Certainty},
		{"contact", m.Contact},
		{"area_desc", m.AreaDesc},
	} {
		if err := checkText(f.value, maxShortText); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, m.Key, errors.Join(errs...))
}

func (m Message) validateReferences() []error {
	var errs []error
	if len(m.References) > MaxReferences {
		errs = append(errs, fmt.Errorf("%d references melebihi %d", len(m.References), MaxReferences))
	}
	for i, r := range m.References {
		switch {
		case r.Source != m.Source || !identifier.MatchString(r.Identifier):
			errs = append(errs, fmt.Errorf("rujukan %s tidak valid", r.Key))
		case r.Key == m.Key:
			errs = append(errs, errors.New("pesan merujuk dirinya sendiri"))
		case r.Sent.IsZero() || r.Sent.Location() != time.UTC:
			errs = append(errs, fmt.Errorf("waktu rujukan %s kosong atau bukan UTC", r.Key))
		case r.Sent.After(m.Sent):
			errs = append(errs, fmt.Errorf("rujukan %s dikirim setelah pesan ini", r.Key))
		}
		if i > 0 && m.References[i-1].Compare(r.Key) >= 0 {
			errs = append(errs, errors.New("references tidak urut atau ganda"))
		}
	}
	return errs
}

func (m Message) validateTexts() []error {
	var errs []error
	if _, ok := m.Text(LanguagePrimary); !ok {
		errs = append(errs, fmt.Errorf("teks bahasa %q tidak ada", LanguagePrimary))
	}
	for i, t := range m.Texts {
		if t.Language == "" || (i > 0 && m.Texts[i-1].Language >= t.Language) {
			errs = append(errs, fmt.Errorf("bahasa teks %q kosong, ganda, atau tidak urut", t.Language))
		}
		for _, f := range []struct {
			v   string
			max int
		}{
			{t.Language, 16},
			{t.Event, maxShortText},
			{t.Headline, maxShortText},
			{t.SenderName, maxShortText},
			{t.Description, maxLongText},
			{t.Instruction, maxLongText},
		} {
			if err := checkText(f.v, f.max); err != nil {
				errs = append(errs, fmt.Errorf("teks %q: %w", t.Language, err))
			}
		}
	}
	return errs
}

func (m Message) validateArea() []error {
	var errs []error
	if len(m.Polygons) == 0 && m.MsgType != MsgCancel {
		errs = append(errs, errors.New("tanpa poligon area"))
	}
	if len(m.Polygons) > MaxPolygons {
		errs = append(errs, fmt.Errorf("%d poligon melebihi %d", len(m.Polygons), MaxPolygons))
	}
	points := 0
	for i, r := range m.Polygons {
		points += len(r)
		if err := CheckRing(r); err != nil {
			errs = append(errs, fmt.Errorf("poligon %d: %w", i, err))
		}
	}
	if points > MaxPoints {
		errs = append(errs, fmt.Errorf("%d titik melebihi %d", points, MaxPoints))
	}
	if m.HasArea != (len(m.Polygons) > 0) {
		errs = append(errs, errors.New("HasArea tidak sesuai poligon"))
	}
	return errs
}

// CheckRing memeriksa satu cincin: minimal MinRingPoints titik, tertutup,
// koordinat berhingga di rentang WGS84, dan tidak semua titiknya sama.
func CheckRing(r Ring) error {
	if len(r) < MinRingPoints {
		return fmt.Errorf("%d titik, minimal %d", len(r), MinRingPoints)
	}
	distinct := false
	for _, p := range r {
		if !finiteIn(p.Lat, -90, 90) || !finiteIn(p.Lon, -180, 180) {
			return fmt.Errorf("titik (%v, %v) di luar rentang WGS84", p.Lat, p.Lon)
		}
		distinct = distinct || p != r[0]
	}
	if r[0] != r[len(r)-1] {
		return errors.New("cincin tidak tertutup")
	}
	if !distinct {
		return errors.New("semua titik sama")
	}
	return nil
}

// ContentDigest adalah SHA-256 isi pesan tanpa keterangan pengambilan
// (fetched_at, first_seen_at, archive_key, kejadian). Pesan yang dikirim
// ulang dengan isi sama menghasilkan digest yang sama.
func (m Message) ContentDigest() [32]byte {
	d := hazard.NewDigester("siaga/cap-message/v1")
	d.Str(string(m.Source))
	d.Str(m.Identifier)
	d.Str(m.Sender)
	d.Time(m.Sent)
	d.Str(string(m.MsgType))
	d.Int(int64(len(m.References)))
	for _, r := range m.References {
		d.Str(string(r.Source))
		d.Str(r.Identifier)
		d.Str(r.Sender)
		d.Time(r.Sent)
	}
	for _, s := range []string{m.Category, m.EventCode, m.Urgency, m.Certainty, string(m.Severity)} {
		d.Str(s)
	}
	d.Time(m.Effective)
	d.Time(m.Onset)
	d.Time(m.Expires)
	d.Int(int64(len(m.Texts)))
	for _, t := range m.Texts {
		for _, s := range []string{t.Language, t.Event, t.Headline, t.Description, t.Instruction, t.SenderName} {
			d.Str(s)
		}
	}
	for _, s := range []string{m.Web, m.Contact, m.SourceURL, m.AreaDesc} {
		d.Str(s)
	}
	d.Int(int64(len(m.Polygons)))
	for _, r := range m.Polygons {
		d.Int(int64(len(r)))
		for _, p := range r {
			d.F64(p.Lat)
			d.F64(p.Lon)
		}
	}
	return d.Sum()
}

// Supersedes melaporkan apakah pesan m perlu menggantikan pesan tersimpan
// prev dengan identifier yang sama (keduanya dengan Digest terisi).
// Identifier CAP unik per pesan, jadi isi berbeda berarti sumber mengoreksi
// dokumennya: yang sent-nya lebih baru menang, lalu digest yang lebih besar,
// supaya hasilnya tidak bergantung urutan kedatangan.
func Supersedes(m, prev Message) bool {
	if m.Sent.Equal(prev.Sent) {
		return slices.Compare(m.Digest[:], prev.Digest[:]) > 0
	}
	return m.Sent.After(prev.Sent)
}

func finiteIn(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}

func checkText(s string, maxLen int) error {
	if len(s) > maxLen {
		return fmt.Errorf("panjang %d melebihi %d", len(s), maxLen)
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
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("URL %q harus https", s)
	}
	return nil
}
