// Package bmkg membaca feed gempa BMKG (data.bmkg.go.id/DataMKG/TEWS):
// autogempa.json, gempaterkini.json, dan gempadirasakan.json.
// Semua waktu dikonversi ke UTC di sini; BMKG menulis jam dalam WIB.
package bmkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultTEWSBaseURL adalah folder feed gempa BMKG. Bisa diganti lewat konfigurasi
// untuk uji replay atau uji chaos (misal lewat Toxiproxy).
const DefaultTEWSBaseURL = "https://data.bmkg.go.id/DataMKG/TEWS/"

// ShakemapBaseURL adalah lokasi publik gambar shakemap. Sengaja tidak mengikuti
// DefaultTEWSBaseURL yang bisa diarahkan ke server replay: URL ini untuk
// dibuka pengguna, jadi selalu menunjuk ke BMKG.
const ShakemapBaseURL = "https://data.bmkg.go.id/DataMKG/TEWS/"

// ErrStructure menandai payload yang strukturnya tidak lagi sesuai format BMKG.
var ErrStructure = errors.New("struktur payload BMKG berubah")

// maxPayload cukup untuk 15 gempa dengan cadangan besar; feed asli sekitar 5 KB.
const maxPayload = 1 << 20

// QuakeConnector adalah satu feed gempa BMKG sebagai ports.Connector.
type QuakeConnector struct {
	name string
	feed quake.Feed
	url  string
}

// NewQuakeConnectors membuat konektor untuk ketiga feed gempa BMKG.
func NewQuakeConnectors(baseURL string) []*QuakeConnector {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	mk := func(name, file string, feed quake.Feed) *QuakeConnector {
		return &QuakeConnector{name: name, feed: feed, url: baseURL + file}
	}
	return []*QuakeConnector{
		mk("bmkg-autogempa", "autogempa.json", quake.FeedBMKGLatest),
		mk("bmkg-gempaterkini", "gempaterkini.json", quake.FeedBMKGRecent),
		mk("bmkg-gempadirasakan", "gempadirasakan.json", quake.FeedBMKGFelt),
	}
}

// Name mengembalikan nama konektor.
func (c *QuakeConnector) Name() string { return c.name }

// ArchiveExt mengembalikan "json".
func (c *QuakeConnector) ArchiveExt() string { return "json" }

// Request mengembalikan permintaan ke feed.
func (c *QuakeConnector) Request() ports.Request {
	return ports.Request{URL: c.url, Accept: "application/json", MaxBytes: maxPayload}
}

// Parse membaca payload. Satu gempa yang rusak menjadi Rejection; payload yang
// strukturnya berubah menjadi galat supaya kegagalan sumber cepat terlihat.
func (c *QuakeConnector) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	var doc struct {
		Infogempa *struct {
			Gempa json.RawMessage `json:"gempa"`
		} `json:"Infogempa"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrStructure, err)
	}
	if doc.Infogempa == nil || len(doc.Infogempa.Gempa) == 0 {
		return nil, nil, fmt.Errorf("%w: Infogempa.gempa tidak ada", ErrStructure)
	}
	var records []record
	switch raw := bytes.TrimSpace(doc.Infogempa.Gempa); {
	case bytes.HasPrefix(raw, []byte("[")):
		if err := json.Unmarshal(raw, &records); err != nil {
			return nil, nil, fmt.Errorf("%w: daftar gempa: %w", ErrStructure, err)
		}
	case bytes.HasPrefix(raw, []byte("{")):
		var r record
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, nil, fmt.Errorf("%w: gempa: %w", ErrStructure, err)
		}
		records = []record{r}
	default:
		return nil, nil, fmt.Errorf("%w: Infogempa.gempa bukan objek atau daftar", ErrStructure)
	}

	var events []ports.Event
	var rejected []ports.Rejection
	seen := map[string]bool{}
	for i, r := range records {
		key := fmt.Sprintf("#%d %s", i, r.DateTime)
		rep, err := r.toReport(c.feed)
		if err == nil {
			err = rep.Validate(fetchedAt)
		}
		if err == nil && seen[rep.EventID] {
			err = fmt.Errorf("ID %s muncul dua kali dalam satu payload", rep.EventID)
		}
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		seen[rep.EventID] = true
		ev, err := rawpb.NewQuakeEvent(rep)
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		events = append(events, ev)
	}
	return events, rejected, nil
}

// text menerima string, angka, atau null dari JSON, supaya perubahan kecil
// tipe data di sisi BMKG tidak menjatuhkan seluruh payload.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		*t = ""
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("teks: %w", err)
		}
		*t = text(s)
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return fmt.Errorf("nilai bukan teks atau angka: %s", b)
		}
		*t = text(n.String())
	}
	return nil
}

func (t text) String() string { return strings.Join(strings.Fields(string(t)), " ") }

type record struct {
	Tanggal     text `json:"Tanggal"`
	Jam         text `json:"Jam"`
	DateTime    text `json:"DateTime"`
	Coordinates text `json:"Coordinates"`
	Lintang     text `json:"Lintang"`
	Bujur       text `json:"Bujur"`
	Magnitude   text `json:"Magnitude"`
	Kedalaman   text `json:"Kedalaman"`
	Wilayah     text `json:"Wilayah"`
	Potensi     text `json:"Potensi"`
	Dirasakan   text `json:"Dirasakan"`
	Shakemap    text `json:"Shakemap"`
}

var shakemapName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}\.(?:jpg|jpeg|png)$`)

func (r record) toReport(feed quake.Feed) (quake.Report, error) {
	at, err := r.occurredAt()
	if err != nil {
		return quake.Report{}, err
	}
	lat, lon, err := r.position()
	if err != nil {
		return quake.Report{}, err
	}
	mag, err := parseNumber(r.Magnitude.String())
	if err != nil {
		return quake.Report{}, fmt.Errorf("magnitudo: %w", err)
	}
	depth, err := parseDepth(r.Kedalaman.String())
	if err != nil {
		return quake.Report{}, fmt.Errorf("kedalaman: %w", err)
	}
	rep := quake.Report{
		Source:        quake.SourceBMKG,
		Feed:          feed,
		EventID:       quake.BMKGEventID(at),
		OccurredAt:    at,
		Latitude:      lat,
		Longitude:     lon,
		Magnitude:     mag,
		DepthKm:       depth,
		Place:         r.Wilayah.String(),
		Felt:          r.Dirasakan.String(),
		PotentialText: r.Potensi.String(),
		Tsunami:       quake.ParseTsunamiText(r.Potensi.String()),
	}
	// Nama file shakemap yang aneh diabaikan, bukan menolak gempanya.
	if name := r.Shakemap.String(); shakemapName.MatchString(name) {
		rep.ShakemapURL = ShakemapBaseURL + name
	}
	return rep, nil
}

// occurredAt memakai DateTime (ISO 8601, UTC) bila ada; bila tidak, Tanggal+Jam WIB.
func (r record) occurredAt() (time.Time, error) {
	if s := r.DateTime.String(); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err == nil {
			return t.UTC(), nil
		}
		if r.Tanggal == "" {
			return time.Time{}, fmt.Errorf("DateTime %q: %w", s, err)
		}
	}
	return parseLocal(r.Tanggal.String(), r.Jam.String())
}

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"mei": time.May, "may": time.May, "jun": time.June, "jul": time.July,
	"agu": time.August, "agt": time.August, "ags": time.August, "aug": time.August,
	"sep": time.September, "okt": time.October, "oct": time.October,
	"nov": time.November, "des": time.December, "dec": time.December,
}

var zones = map[string]*time.Location{
	"WIB":  time.FixedZone("WIB", 7*3600),
	"WITA": time.FixedZone("WITA", 8*3600),
	"WIT":  time.FixedZone("WIT", 9*3600),
}

// parseLocal membaca "23 Sep 2026" + "19:16:56 WIB" lalu mengubahnya ke UTC.
func parseLocal(tanggal, jam string) (time.Time, error) {
	d := strings.Fields(tanggal)
	j := strings.Fields(jam)
	if len(d) != 3 || len(j) != 2 {
		return time.Time{}, fmt.Errorf("tanggal/jam tidak dikenali: %q %q", tanggal, jam)
	}
	day, err1 := strconv.Atoi(d[0])
	year, err2 := strconv.Atoi(d[2])
	mon, ok := months[strings.ToLower(d[1][:min(3, len(d[1]))])]
	loc, zok := zones[strings.ToUpper(j[1])]
	if err1 != nil || err2 != nil || !ok || !zok {
		return time.Time{}, fmt.Errorf("tanggal/jam tidak dikenali: %q %q", tanggal, jam)
	}
	clock, err := time.Parse("15:04:05", j[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("jam %q: %w", jam, err)
	}
	t := time.Date(year, mon, day, clock.Hour(), clock.Minute(), clock.Second(), 0, loc)
	if t.Day() != day || t.Month() != mon {
		return time.Time{}, fmt.Errorf("tanggal tidak ada di kalender: %q", tanggal)
	}
	return t.UTC(), nil
}

// position memakai Coordinates "lat,lon" bila ada; bila tidak, Lintang/Bujur.
func (r record) position() (lat, lon float64, err error) {
	if s := r.Coordinates.String(); s != "" {
		a, b, ok := strings.Cut(s, ",")
		if !ok {
			return 0, 0, fmt.Errorf("koordinat %q tidak berformat lat,lon", s)
		}
		lat, err1 := parseNumber(a)
		lon, err2 := parseNumber(b)
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("koordinat %q tidak berformat lat,lon", s)
		}
		return lat, lon, nil
	}
	lat, err = parseHemisphere(r.Lintang.String(), "LU", "LS")
	if err != nil {
		return 0, 0, fmt.Errorf("lintang: %w", err)
	}
	lon, err = parseHemisphere(r.Bujur.String(), "BT", "BB")
	if err != nil {
		return 0, 0, fmt.Errorf("bujur: %w", err)
	}
	return lat, lon, nil
}

// parseHemisphere membaca "8.51 LS" menjadi -8.51.
func parseHemisphere(s, positive, negative string) (float64, error) {
	f := strings.Fields(strings.ToUpper(s))
	if len(f) != 2 {
		return 0, fmt.Errorf("%q tidak berformat \"<angka> %s|%s\"", s, positive, negative)
	}
	v, err := parseNumber(f[0])
	if err != nil {
		return 0, err
	}
	switch f[1] {
	case positive:
		return v, nil
	case negative:
		return -v, nil
	default:
		return 0, fmt.Errorf("arah %q bukan %s atau %s", f[1], positive, negative)
	}
}

// parseDepth membaca "12 km" atau "12km".
func parseDepth(s string) (float64, error) {
	v := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(s), "km"))
	return parseNumber(v)
}

// parseNumber menerima titik atau koma sebagai pemisah desimal.
func parseNumber(s string) (float64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("angka %q tidak valid", s)
	}
	return v, nil
}
