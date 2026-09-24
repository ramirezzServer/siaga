// Package warning berisi aturan murni untuk pesan peringatan dini cuaca
// berformat CAP 1.2 (Common Alerting Protocol): nilai yang dikenal, invarian,
// rujukan antar-pesan, dan tata letak ID pesan BMKG.
package warning

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid menandai pesan yang melanggar invarian domain.
var ErrInvalid = errors.New("peringatan CAP tidak valid")

// Batas yang masih masuk akal untuk satu pesan. Pesan BMKG terbesar yang
// terekam (Sumatera Utara, 2026-09-24) berisi 150 poligon dan 3.072 titik.
const (
	MaxClockSkew   = 10 * time.Minute
	MaxValidFor    = 7 * 24 * time.Hour
	MaxPolygons    = 5_000
	MaxPoints      = 200_000
	MinRingPoints  = 4
	MaxReferences  = 64
	maxIDLen       = 128
	maxShortText   = 512
	maxLongText    = 16 << 10
	maxLanguageLen = 16
)

// LanguagePrimary adalah bahasa teks yang wajib ada: teks asli BMKG.
const LanguagePrimary = "id"

// Status adalah alert/status CAP.
type Status int

// Nilai Status.
const (
	StatusUnknown Status = iota
	StatusActual
	StatusExercise
	StatusSystem
	StatusTest
	StatusDraft
)

// MsgType adalah alert/msgType CAP.
type MsgType int

// Nilai MsgType.
const (
	MsgUnknown MsgType = iota
	MsgAlert
	MsgUpdate
	MsgCancel
	MsgAck
	MsgError
)

// Severity adalah info/severity CAP.
type Severity int

// Nilai Severity.
const (
	SeverityUnspecified Severity = iota
	SeverityExtreme
	SeveritySevere
	SeverityModerate
	SeverityMinor
	SeverityUnknown
)

// Urgency adalah info/urgency CAP.
type Urgency int

// Nilai Urgency.
const (
	UrgencyUnspecified Urgency = iota
	UrgencyImmediate
	UrgencyExpected
	UrgencyFuture
	UrgencyPast
	UrgencyUnknown
)

// Certainty adalah info/certainty CAP.
type Certainty int

// Nilai Certainty.
const (
	CertaintyUnspecified Certainty = iota
	CertaintyObserved
	CertaintyLikely
	CertaintyPossible
	CertaintyUnlikely
	CertaintyUnknown
)

// ParseStatus membaca nilai status CAP (tidak peka huruf besar-kecil).
func ParseStatus(s string) Status {
	return lookup(s, map[string]Status{
		"actual": StatusActual, "exercise": StatusExercise, "system": StatusSystem,
		"test": StatusTest, "draft": StatusDraft,
	}, StatusUnknown)
}

// ParseMsgType membaca nilai msgType CAP.
func ParseMsgType(s string) MsgType {
	return lookup(s, map[string]MsgType{
		"alert": MsgAlert, "update": MsgUpdate, "cancel": MsgCancel, "ack": MsgAck, "error": MsgError,
	}, MsgUnknown)
}

// ParseSeverity membaca nilai severity CAP.
func ParseSeverity(s string) Severity {
	return lookup(s, map[string]Severity{
		"extreme": SeverityExtreme, "severe": SeveritySevere, "moderate": SeverityModerate,
		"minor": SeverityMinor, "unknown": SeverityUnknown,
	}, SeverityUnspecified)
}

// ParseUrgency membaca nilai urgency CAP.
func ParseUrgency(s string) Urgency {
	return lookup(s, map[string]Urgency{
		"immediate": UrgencyImmediate, "expected": UrgencyExpected, "future": UrgencyFuture,
		"past": UrgencyPast, "unknown": UrgencyUnknown,
	}, UrgencyUnspecified)
}

// ParseCertainty membaca nilai certainty CAP. "Very Likely" (CAP 1.0) dibaca Likely.
func ParseCertainty(s string) Certainty {
	return lookup(s, map[string]Certainty{
		"observed": CertaintyObserved, "likely": CertaintyLikely, "very likely": CertaintyLikely,
		"possible": CertaintyPossible, "unlikely": CertaintyUnlikely, "unknown": CertaintyUnknown,
	}, CertaintyUnspecified)
}

func lookup[T any](s string, table map[string]T, fallback T) T {
	if v, ok := table[strings.ToLower(strings.Join(strings.Fields(s), " "))]; ok {
		return v
	}
	return fallback
}

// Point adalah titik WGS84.
type Point struct {
	Lat, Lon float64
}

// Ring adalah cincin poligon tertutup (titik pertama = titik terakhir).
type Ring []Point

// Area adalah satu elemen info/area.
type Area struct {
	Desc     string
	Polygons []Ring
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

// Reference menunjuk pesan CAP lain (elemen references).
type Reference struct {
	Sender     string
	Identifier string
	Sent       time.Time
}

// Warning adalah satu pesan CAP. Waktu selalu UTC.
type Warning struct {
	Identifier string
	Sender     string
	Sent       time.Time
	Status     Status
	MsgType    MsgType
	References []Reference
	Category   string
	EventCode  string
	Urgency    Urgency
	Severity   Severity
	Certainty  Certainty
	Effective  time.Time
	Onset      time.Time // nol bila tidak diisi sumber
	Expires    time.Time
	// Texts urut kode bahasa dan selalu memuat LanguagePrimary.
	Texts     []Text
	Web       string
	Contact   string
	Areas     []Area
	SourceURL string
}

// Relevant melaporkan apakah pesan perlu diteruskan: hanya pesan sungguhan
// (Actual) yang membuat, memperbarui, atau membatalkan peringatan. Pesan
// latihan, uji, draft, Ack, dan Error dilewati.
func (w Warning) Relevant() bool {
	return w.Status == StatusActual && (w.MsgType == MsgAlert || w.MsgType == MsgUpdate || w.MsgType == MsgCancel)
}

// Text mengembalikan teks untuk bahasa lang.
func (w Warning) Text(lang string) (Text, bool) {
	i := slices.IndexFunc(w.Texts, func(t Text) bool { return t.Language == lang })
	if i < 0 {
		return Text{}, false
	}
	return w.Texts[i], true
}

// WithTranslation menambahkan teks dari dokumen bahasa lain untuk pesan yang
// sama. Dokumen itu harus punya identifier, sent, dan severity yang sama;
// yang diambil hanya teksnya. Teks bahasa yang sudah ada tidak ditimpa.
func (w Warning) WithTranslation(other Warning) (Warning, error) {
	switch {
	case other.Identifier != w.Identifier:
		return w, fmt.Errorf("%w: terjemahan untuk pesan lain (%q, bukan %q)", ErrInvalid, other.Identifier, w.Identifier)
	case !other.Sent.Equal(w.Sent) || other.Severity != w.Severity || other.MsgType != w.MsgType:
		return w, fmt.Errorf("%w: terjemahan %s tidak cocok dengan dokumen utama (sent/severity/msgType berbeda)", ErrInvalid, w.Identifier)
	}
	out := w
	out.Texts = slices.Clone(w.Texts)
	for _, t := range other.Texts {
		if _, ok := out.Text(t.Language); !ok {
			out.Texts = append(out.Texts, t)
		}
	}
	SortTexts(out.Texts)
	return out, nil
}

// SortTexts mengurutkan teks menurut kode bahasa.
func SortTexts(ts []Text) {
	slices.SortStableFunc(ts, func(a, b Text) int { return cmp.Compare(a.Language, b.Language) })
}

// ParseReferences membaca elemen references CAP: daftar "sender,identifier,sent"
// dipisah spasi. Rujukan ganda dibuang dan hasilnya urut (identifier, sent).
func ParseReferences(s string) ([]Reference, error) {
	var out []Reference
	for tok := range strings.FieldsSeq(s) {
		parts := strings.Split(tok, ",")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%w: rujukan %q harus berformat sender,identifier,sent", ErrInvalid, tok)
		}
		sent, err := time.Parse(time.RFC3339, parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: waktu rujukan %q: %w", ErrInvalid, parts[2], err)
		}
		// Zona waktu bisa menggeser tahun 0000 atau 9999 keluar dari rentang
		// RFC 3339 saat diubah ke UTC; waktu seperti itu bukan waktu kirim nyata.
		if y := sent.UTC().Year(); y < 1 || y > 9999 {
			return nil, fmt.Errorf("%w: waktu rujukan %q di luar rentang", ErrInvalid, parts[2])
		}
		out = append(out, Reference{Sender: parts[0], Identifier: parts[1], Sent: sent.UTC()})
	}
	SortReferences(out)
	return slices.CompactFunc(out, func(a, b Reference) bool {
		return a.Identifier == b.Identifier && a.Sender == b.Sender && a.Sent.Equal(b.Sent)
	}), nil
}

// SortReferences mengurutkan rujukan menurut (identifier, sent, sender).
func SortReferences(rs []Reference) {
	slices.SortFunc(rs, func(a, b Reference) int {
		return cmp.Or(cmp.Compare(a.Identifier, b.Identifier), a.Sent.Compare(b.Sent), cmp.Compare(a.Sender, b.Sender))
	})
}

// BMKGProvince membaca kode provinsi (kode BPS/Kemendagri 2 digit) dari ID
// pesan CAP BMKG, yang berupa OID WMO "2.49.0.1.360.0.<YYYY>.<MM>.<DD>.<xx>.<PP>.<urut>".
// ok false bila ID tidak mengikuti tata letak itu; pemanggil sebaiknya tidak
// menyaring pesan seperti itu supaya peringatan tidak hilang diam-diam.
func BMKGProvince(identifier string) (province string, ok bool) {
	const prefix = "2.49.0.1.360.0."
	rest, found := strings.CutPrefix(identifier, prefix)
	if !found {
		return "", false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 6 {
		return "", false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return "", false
		}
	}
	if len(parts[4]) != 2 {
		return "", false
	}
	return parts[4], true
}

// PointCount mengembalikan jumlah titik semua poligon.
func (w Warning) PointCount() int {
	n := 0
	for _, a := range w.Areas {
		for _, r := range a.Polygons {
			n += len(r)
		}
	}
	return n
}

// PolygonCount mengembalikan jumlah poligon semua area.
func (w Warning) PolygonCount() int {
	n := 0
	for _, a := range w.Areas {
		n += len(a.Polygons)
	}
	return n
}

// Validate memeriksa semua invarian. fetchedAt dipakai untuk menolak waktu
// kirim di masa depan. Semua pelanggaran dilaporkan sekaligus.
func (w Warning) Validate(fetchedAt time.Time) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if err := checkID(w.Identifier); err != nil {
		bad("identifier: %w", err)
	}
	if err := checkText(w.Sender, maxShortText); err != nil || w.Sender == "" {
		bad("sender kosong atau tidak valid")
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
	checkUTC("sent", w.Sent, true)
	checkUTC("effective", w.Effective, true)
	checkUTC("onset", w.Onset, false)
	checkUTC("expires", w.Expires, true)
	if !w.Sent.IsZero() && w.Sent.After(fetchedAt.Add(MaxClockSkew)) {
		bad("sent %s di masa depan (diambil %s)", w.Sent.Format(time.RFC3339), fetchedAt.UTC().Format(time.RFC3339))
	}
	if !w.Effective.IsZero() && !w.Expires.IsZero() {
		if !w.Expires.After(w.Effective) {
			bad("expires %s tidak setelah effective %s", w.Expires.Format(time.RFC3339), w.Effective.Format(time.RFC3339))
		} else if w.Expires.Sub(w.Effective) > MaxValidFor {
			bad("masa berlaku %v melebihi %v", w.Expires.Sub(w.Effective), MaxValidFor)
		}
	}
	if !w.Onset.IsZero() && !w.Expires.IsZero() && w.Onset.After(w.Expires) {
		bad("onset setelah expires")
	}
	if w.Status == StatusUnknown {
		bad("status tidak dikenal")
	}
	if w.MsgType == MsgUnknown {
		bad("msgType tidak dikenal")
	}
	if w.Severity == SeverityUnspecified {
		bad("severity tidak dikenal")
	}
	if w.Urgency == UrgencyUnspecified {
		bad("urgency tidak dikenal")
	}
	if w.Certainty == CertaintyUnspecified {
		bad("certainty tidak dikenal")
	}
	if (w.MsgType == MsgUpdate || w.MsgType == MsgCancel) && len(w.References) == 0 {
		bad("%s tanpa references", map[MsgType]string{MsgUpdate: "Update", MsgCancel: "Cancel"}[w.MsgType])
	}
	if len(w.References) > MaxReferences {
		bad("%d references melebihi %d", len(w.References), MaxReferences)
	}
	for _, r := range w.References {
		if err := checkID(r.Identifier); err != nil {
			bad("identifier rujukan: %w", err)
		} else if r.Identifier == w.Identifier {
			bad("pesan merujuk dirinya sendiri")
		}
		if r.Sent.IsZero() || r.Sent.Location() != time.UTC {
			bad("waktu rujukan %s kosong atau bukan UTC", r.Identifier)
		} else if !w.Sent.IsZero() && r.Sent.After(w.Sent) {
			bad("rujukan %s dikirim setelah pesan ini", r.Identifier)
		}
	}
	if !slices.IsSortedFunc(w.References, func(a, b Reference) int {
		return cmp.Or(cmp.Compare(a.Identifier, b.Identifier), a.Sent.Compare(b.Sent), cmp.Compare(a.Sender, b.Sender))
	}) {
		bad("references tidak urut")
	}
	errs = append(errs, w.validateTexts()...)
	errs = append(errs, w.validateAreas()...)
	for _, f := range []struct{ name, value string }{{"web", w.Web}, {"source_url", w.SourceURL}} {
		if err := checkURL(f.value); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	for _, f := range []struct{ name, value string }{
		{"category", w.Category}, {"event_code", w.EventCode}, {"contact", w.Contact},
	} {
		if err := checkText(f.value, maxShortText); err != nil {
			bad("%s: %w", f.name, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalid, w.Identifier, errors.Join(errs...))
}

func (w Warning) validateTexts() []error {
	var errs []error
	if _, ok := w.Text(LanguagePrimary); !ok {
		errs = append(errs, fmt.Errorf("teks bahasa %q tidak ada", LanguagePrimary))
	}
	seen := map[string]bool{}
	for i, t := range w.Texts {
		if t.Language == "" || len(t.Language) > maxLanguageLen || strings.TrimFunc(t.Language, isLangRune) != "" {
			errs = append(errs, fmt.Errorf("kode bahasa %q tidak valid", t.Language))
		}
		if seen[t.Language] {
			errs = append(errs, fmt.Errorf("teks bahasa %q ganda", t.Language))
		}
		seen[t.Language] = true
		if i > 0 && w.Texts[i-1].Language > t.Language {
			errs = append(errs, errors.New("teks tidak urut menurut bahasa"))
		}
		if strings.TrimSpace(t.Headline) == "" && strings.TrimSpace(t.Description) == "" {
			errs = append(errs, fmt.Errorf("teks %q tanpa headline dan description", t.Language))
		}
		for _, f := range []struct {
			name, value string
			max         int
		}{
			{"event", t.Event, maxShortText},
			{"headline", t.Headline, maxShortText},
			{"sender_name", t.SenderName, maxShortText},
			{"description", t.Description, maxLongText},
			{"instruction", t.Instruction, maxLongText},
		} {
			if err := checkText(f.value, f.max); err != nil {
				errs = append(errs, fmt.Errorf("teks %q %s: %w", t.Language, f.name, err))
			}
		}
	}
	return errs
}

func isLangRune(r rune) bool { return r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') }

func (w Warning) validateAreas() []error {
	var errs []error
	polygons, points := w.PolygonCount(), w.PointCount()
	if polygons == 0 && w.MsgType != MsgCancel {
		errs = append(errs, errors.New("tanpa poligon area"))
	}
	if polygons > MaxPolygons {
		errs = append(errs, fmt.Errorf("%d poligon melebihi %d", polygons, MaxPolygons))
	}
	if points > MaxPoints {
		errs = append(errs, fmt.Errorf("%d titik melebihi %d", points, MaxPoints))
	}
	for ai, a := range w.Areas {
		if err := checkText(a.Desc, maxShortText); err != nil {
			errs = append(errs, fmt.Errorf("area %d: %w", ai, err))
		}
		for pi, ring := range a.Polygons {
			if err := CheckRing(ring); err != nil {
				errs = append(errs, fmt.Errorf("area %d poligon %d: %w", ai, pi, err))
			}
		}
	}
	return errs
}

// CheckRing memeriksa satu cincin: minimal MinRingPoints titik, tertutup,
// koordinat berhingga di rentang WGS84, dan tidak semua titiknya sama.
// Validitas topologi (misal simpul yang memotong diri) diperbaiki di PostGIS.
func CheckRing(r Ring) error {
	if len(r) < MinRingPoints {
		return fmt.Errorf("%d titik, minimal %d", len(r), MinRingPoints)
	}
	for _, p := range r {
		if !inRange(p.Lat, -90, 90) || !inRange(p.Lon, -180, 180) {
			return fmt.Errorf("titik (%v, %v) di luar rentang WGS84", p.Lat, p.Lon)
		}
	}
	if r[0] != r[len(r)-1] {
		return errors.New("cincin tidak tertutup")
	}
	distinct := 1
	for _, p := range r[1:] {
		if p != r[0] {
			distinct++
			break
		}
	}
	if distinct < 2 {
		return errors.New("semua titik sama")
	}
	return nil
}

func inRange(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}

func checkID(id string) error {
	if id == "" || len(id) > maxIDLen {
		return fmt.Errorf("panjang %d harus 1..%d", len(id), maxIDLen)
	}
	for _, c := range id {
		// CAP melarang spasi, koma, dan karakter terlarang XML di identifier.
		if c > unicode.MaxASCII || !unicode.IsPrint(c) || unicode.IsSpace(c) || c == ',' || c == '<' || c == '&' {
			return fmt.Errorf("karakter %q tidak diizinkan", c)
		}
	}
	return nil
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
