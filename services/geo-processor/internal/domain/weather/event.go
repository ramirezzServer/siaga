package weather

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
)

// ErrNoArea berarti rantai pesan belum punya pesan berarea (misal baru
// menerima Cancel untuk peringatan yang tidak pernah diterima), jadi belum
// ada kejadian yang bisa dibentuk.
var ErrNoArea = errors.New("rantai peringatan tanpa area")

// Node adalah satu pesan dalam rantai, baik yang sudah diterima maupun yang
// hanya dikenal dari references pesan lain.
type Node struct {
	Key
	Sent time.Time
}

// Root mengembalikan pendiri rantai: node dengan (sent, identifier) terkecil
// di antara pesan dan semua rujukannya. Karena rujukan membawa waktu kirim
// pesan yang dirujuk, pendiri sudah benar walau pesan awalnya belum atau
// tidak pernah diterima; hasilnya hanya bergantung pada himpunan pesan.
func Root(msgs []Message) (Node, bool) {
	var nodes []Node
	for _, m := range msgs {
		nodes = append(nodes, Node{m.Key, m.Sent})
		for _, r := range m.References {
			nodes = append(nodes, Node{r.Key, r.Sent})
		}
	}
	if len(nodes) == 0 {
		return Node{}, false
	}
	return slices.MinFunc(nodes, func(a, b Node) int { return cmp.Or(a.Sent.Compare(b.Sent), a.Compare(b.Key)) }), true
}

// EventIDFor membentuk ID kejadian cuaca dari pendiri rantai.
func EventIDFor(root Key) hazard.EventID {
	return hazard.NewEventID("siaga/hazard/weather", 0, string(root.Source), root.Identifier)
}

// latest mengurutkan pesan menurut (sent, identifier).
func latest(a, b Message) int { return cmp.Or(a.Sent.Compare(b.Sent), a.Compare(b.Key)) }

// View adalah keadaan kejadian yang diturunkan dari rantai pesannya. Semua
// field adalah fungsi murni dari himpunan pesan.
type View struct {
	ID   hazard.EventID
	Root Node
	// Current adalah pesan terbaru (bisa Cancel).
	Current Message
	// Content adalah pesan terbaru yang berarea: sumber teks, area, dan
	// severity. Sama dengan Current kecuali Current adalah Cancel tanpa area.
	Content    Message
	Messages   int
	Cancelled  bool
	OccurredAt time.Time
	DetectedAt time.Time
	ExpiresAt  time.Time
	Level      hazard.Level
	Title      string
	Summary    string
}

// Derive menghitung View dari semua pesan dalam satu rantai.
func Derive(msgs []Message) (View, error) {
	if len(msgs) == 0 {
		return View{}, ErrNoArea
	}
	root, _ := Root(msgs)
	v := View{ID: EventIDFor(root.Key), Root: root, Messages: len(msgs)}
	v.Current = slices.MaxFunc(msgs, latest)
	withArea := slices.DeleteFunc(slices.Clone(msgs), func(m Message) bool { return !m.HasArea })
	if len(withArea) == 0 {
		return View{}, fmt.Errorf("%w: %s", ErrNoArea, root.Key)
	}
	v.Content = slices.MaxFunc(withArea, latest)
	v.Cancelled = v.Current.MsgType == MsgCancel
	v.OccurredAt = v.Content.Start()
	for _, m := range withArea {
		if m.Start().Before(v.OccurredAt) {
			v.OccurredAt = m.Start()
		}
	}
	v.DetectedAt = v.Current.FirstSeenAt
	for _, m := range msgs {
		if m.FirstSeenAt.Before(v.DetectedAt) {
			v.DetectedAt = m.FirstSeenAt
		}
	}
	v.ExpiresAt = v.Content.Expires
	v.Level = v.Content.Severity.Level()
	v.Title = title(v.Content)
	v.Summary = summary(v)
	return v, nil
}

// Status menentukan status kejadian pada waktu now.
func (v View) Status(now time.Time) Status {
	switch {
	case v.Cancelled:
		return StatusRetracted
	case !v.ExpiresAt.After(now):
		return StatusExpired
	default:
		return StatusActive
	}
}

// Status adalah status kejadian; nilainya sama dengan hazard.event.status.
type Status string

// Nilai Status yang bisa diturunkan dari rantai pesan.
const (
	StatusActive    Status = "active"
	StatusExpired   Status = "expired"
	StatusRetracted Status = "retracted"
)

var wib = time.FixedZone("WIB", 7*3600)

var months = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// FormatWIB menulis waktu gaya Indonesia, misal "24 Sep 2026 15.27 WIB".
func FormatWIB(t time.Time) string {
	w := t.In(wib)
	return fmt.Sprintf("%d %s %d %02d.%02d WIB", w.Day(), months[w.Month()-1], w.Year(), w.Hour(), w.Minute())
}

func formatRange(from, to time.Time) string {
	f, t := from.In(wib), to.In(wib)
	if f.Year() == t.Year() && f.YearDay() == t.YearDay() {
		return fmt.Sprintf("%d %s %d %02d.%02d–%02d.%02d WIB", f.Day(), months[f.Month()-1], f.Year(), f.Hour(), f.Minute(), t.Hour(), t.Minute())
	}
	return strings.TrimSuffix(FormatWIB(from), " WIB") + " – " + FormatWIB(to)
}

var severityLabel = map[Severity]string{
	SeverityMinor: "Info", SeverityModerate: "Waspada", SeveritySevere: "Siaga", SeverityExtreme: "Bahaya", SeverityUnknown: "Info",
}

func title(m Message) string {
	t, _ := m.Text(LanguagePrimary)
	if h := strings.TrimSpace(t.Headline); h != "" {
		return h
	}
	if e := strings.TrimSpace(t.Event); e != "" {
		return "Peringatan dini cuaca: " + e
	}
	return "Peringatan dini cuaca"
}

func summary(v View) string {
	t, _ := v.Content.Text(LanguagePrimary)
	event := strings.TrimSpace(t.Event)
	if event == "" {
		event = "Peringatan dini cuaca"
	}
	parts := []string{fmt.Sprintf("%s, tingkat %s (CAP %s).", event, severityLabel[v.Content.Severity], v.Content.Severity.CAP())}
	if v.Cancelled {
		parts = append(parts, "Dibatalkan BMKG pada "+FormatWIB(v.Current.Sent)+".")
	} else {
		parts = append(parts, "Berlaku "+formatRange(v.Content.Start(), v.Content.Expires)+".")
	}
	if v.Messages > 1 {
		parts = append(parts, fmt.Sprintf("Diperbarui %d kali.", v.Messages-1))
	}
	parts = append(parts, "Sumber: BMKG.")
	return strings.Join(parts, " ")
}

// RegionCoverage adalah satu kelurahan/desa yang beririsan dengan area
// peringatan, dengan bagian luasnya yang tercakup (0..1].
type RegionCoverage struct {
	Code     string
	Name     string
	Coverage float64
}

// Footprint adalah area peringatan setelah poligon digabung dan diperbaiki
// di PostGIS, beserta semua kelurahan/desa yang beririsan.
type Footprint struct {
	AreaGeoJSON string
	// Titik yang dijamin berada di dalam area (ST_PointOnSurface).
	Latitude, Longitude float64
	AreaKm2             float64
	Regions             []RegionCoverage
}

// Impact adalah kelurahan/desa terdampak.
type Impact struct {
	Code     string
	Name     string
	Coverage float64
	Level    hazard.Level
}

// Policy adalah aturan wilayah terdampak. Angkanya nilai awal yang
// dikalibrasi dengan arsip CAP Jawa Barat (ADR 0010).
type Policy struct {
	// MinCoverage adalah bagian luas desa minimum yang harus tercakup
	// supaya desa dihitung terdampak. BMKG menggambar poligon per kecamatan
	// dengan batas yang berbeda tipis dari ref.region; tanpa ambang, desa
	// tetangga ikut terhitung karena irisan tipis di perbatasan.
	MinCoverage float64
}

// DefaultPolicy mengembalikan nilai awal.
func DefaultPolicy() Policy { return Policy{MinCoverage: 0.1} }

// Validate memastikan aturan masuk akal.
func (p Policy) Validate() error {
	if !(p.MinCoverage > 0 && p.MinCoverage <= 1) {
		return fmt.Errorf("MinCoverage %v harus di (0, 1]", p.MinCoverage)
	}
	return nil
}

// Assessment adalah View beserta area dan dampaknya, bentuk lengkap yang
// disimpan dan diterbitkan.
type Assessment struct {
	View
	Footprint
	Impacts []Impact
	// Relevant false berarti area tidak beririsan dengan wilayah pantauan
	// (ref.region) sama sekali.
	Relevant bool
}

// Assess menghitung dampak. Hanya tingkat Waspada ke atas yang punya daftar
// wilayah terdampak (sama dengan gempa); urut cakupan terbesar lalu kode.
func (p Policy) Assess(v View, fp Footprint) Assessment {
	a := Assessment{View: v, Footprint: fp, Relevant: len(fp.Regions) > 0}
	a.Regions = nil // daftar mentah tidak ikut disimpan atau di-hash
	if v.Level < hazard.LevelWaspada {
		return a
	}
	for _, r := range fp.Regions {
		if r.Coverage >= p.MinCoverage && !math.IsNaN(r.Coverage) {
			a.Impacts = append(a.Impacts, Impact{Code: r.Code, Name: r.Name, Coverage: min(r.Coverage, 1), Level: v.Level})
		}
	}
	slices.SortFunc(a.Impacts, func(x, y Impact) int {
		return cmp.Or(cmp.Compare(y.Coverage, x.Coverage), cmp.Compare(x.Code, y.Code))
	})
	return a
}

// Digest adalah SHA-256 semua isi yang diterbitkan; kejadian hanya
// diterbitkan ulang bila digest berubah.
func (a Assessment) Digest() [32]byte {
	d := hazard.NewDigester("siaga/weather-assessment/v1")
	d.Str(a.ID.String())
	d.Str(a.Root.Identifier)
	d.Str(a.Current.Identifier)
	d.Str(string(a.Current.MsgType))
	d.Str(a.Content.Identifier)
	d.Int(int64(a.Messages))
	d.Bool(a.Cancelled)
	d.Time(a.OccurredAt)
	d.Time(a.DetectedAt)
	d.Time(a.ExpiresAt)
	d.Int(int64(a.Level))
	d.Str(a.Title)
	d.Str(a.Summary)
	c := a.Content
	for _, s := range []string{string(c.Severity), c.Urgency, c.Certainty, c.EventCode, c.Web, c.SourceURL, c.AreaDesc} {
		d.Str(s)
	}
	d.Int(int64(len(c.Texts)))
	for _, t := range c.Texts {
		for _, s := range []string{t.Language, t.Event, t.Headline, t.Description, t.Instruction} {
			d.Str(s)
		}
	}
	d.Str(a.AreaGeoJSON)
	d.F64(a.Latitude)
	d.F64(a.Longitude)
	d.F64(a.AreaKm2)
	d.Int(int64(len(a.Impacts)))
	for _, im := range a.Impacts {
		d.Str(im.Code)
		d.Str(im.Name)
		d.F64(im.Coverage)
		d.Int(int64(im.Level))
	}
	return d.Sum()
}
