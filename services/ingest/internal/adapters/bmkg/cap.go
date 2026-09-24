package bmkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/warning"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultCAPBaseURL adalah folder peringatan dini cuaca (nowcast) BMKG. Di
// bawahnya ada <bahasa>/rss.xml dan <bahasa>/<kode>_alert.xml. Bisa diganti
// lewat konfigurasi untuk uji replay.
const DefaultCAPBaseURL = "https://www.bmkg.go.id/alerts/nowcast/"

// PublicCAPBaseURL dipakai untuk source_url yang dibuka pengguna, jadi selalu
// menunjuk ke BMKG walau DefaultCAPBaseURL diarahkan ke server replay.
const PublicCAPBaseURL = "https://www.bmkg.go.id/alerts/nowcast/"

// Batas ukuran. RSS nasional sekitar 14 KB, CAP terbesar yang terekam 47 KB.
const (
	maxRSS = 2 << 20
	maxCAP = 4 << 20
)

// Namespace CAP yang diterima.
var capNamespaces = map[string]bool{
	"urn:oasis:names:tc:emergency:cap:1.2": true,
	"urn:oasis:names:tc:emergency:cap:1.1": true,
}

var capFileName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}\.xml$`)

// CAPFeed membaca peringatan dini cuaca BMKG: daftar RSS nowcast dan dokumen
// CAP per peringatan, dalam bahasa Indonesia (utama) dan bahasa lain.
type CAPFeed struct {
	base string
}

// NewCAPFeed membuat pembaca untuk folder nowcast di baseURL.
func NewCAPFeed(baseURL string) *CAPFeed {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return &CAPFeed{base: baseURL}
}

// Name adalah nama konektor, dipakai di log, arsip, dan ID pesan.
func (c *CAPFeed) Name() string { return "bmkg-cap" }

// ArchiveExt mengembalikan "xml".
func (c *CAPFeed) ArchiveExt() string { return "xml" }

// FeedRequest adalah permintaan RSS nowcast bahasa Indonesia.
func (c *CAPFeed) FeedRequest() ports.Request {
	return ports.Request{URL: c.base + warning.LanguagePrimary + "/rss.xml", Accept: "application/rss+xml, application/xml", MaxBytes: maxRSS}
}

// DetailRequest adalah permintaan dokumen CAP satu peringatan dalam bahasa lang.
func (c *CAPFeed) DetailRequest(it ports.FeedItem, lang string) ports.Request {
	return ports.Request{URL: c.base + url.PathEscape(lang) + "/" + it.File, Accept: "application/xml", MaxBytes: maxCAP}
}

// SourceURL adalah URL publik dokumen CAP bahasa Indonesia.
func SourceURL(file string) string { return PublicCAPBaseURL + warning.LanguagePrimary + "/" + file }

// ParseFeed membaca RSS nowcast. Entri tanpa guid atau tautan yang bukan
// dokumen CAP dilewati; RSS yang strukturnya berubah menjadi galat.
func (c *CAPFeed) ParseFeed(body []byte) ([]ports.FeedItem, []ports.Rejection, error) {
	var doc struct {
		XMLName xml.Name `xml:"rss"`
		Channel *struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				GUID        string `xml:"guid"`
				PubDate     string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := decodeXML(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("%w: RSS: %w", ErrStructure, err)
	}
	if doc.Channel == nil {
		return nil, nil, fmt.Errorf("%w: RSS tanpa channel", ErrStructure)
	}
	var items []ports.FeedItem
	var rejected []ports.Rejection
	seen := map[string]bool{}
	for i, it := range doc.Channel.Items {
		guid := strings.TrimSpace(it.GUID)
		key := fmt.Sprintf("#%d %s", i, guid)
		file, err := capFile(it.Link)
		switch {
		case guid == "":
			err = fmt.Errorf("entri RSS tanpa guid (link %q)", it.Link)
		case seen[guid]:
			err = fmt.Errorf("guid %s muncul dua kali", guid)
		}
		if err != nil {
			rejected = append(rejected, ports.Rejection{Key: key, Reason: err})
			continue
		}
		seen[guid] = true
		h := sha256.New()
		for _, part := range []string{guid, it.Link, it.Title, it.Description, it.PubDate} {
			h.Write([]byte(strconv.Itoa(len(part))))
			h.Write([]byte{':'})
			h.Write([]byte(part))
		}
		items = append(items, ports.FeedItem{
			Key: guid, File: file, Title: squash(it.Title), Digest: hex.EncodeToString(h.Sum(nil)),
		})
	}
	return items, rejected, nil
}

// capFile mengambil nama file CAP dari tautan RSS, misal
// https://www.bmkg.go.id/alerts/nowcast/id/CJB20260924001_alert.xml.
func capFile(link string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Path == "" {
		return "", fmt.Errorf("tautan %q tidak valid", link)
	}
	name := path.Base(u.Path)
	if !capFileName.MatchString(name) {
		return "", fmt.Errorf("tautan %q bukan dokumen CAP", link)
	}
	return name, nil
}

type capInfo struct {
	Language  string `xml:"language"`
	Category  string `xml:"category"`
	Event     string `xml:"event"`
	Urgency   string `xml:"urgency"`
	Severity  string `xml:"severity"`
	Certainty string `xml:"certainty"`
	EventCode []struct {
		ValueName string `xml:"valueName"`
		Value     string `xml:"value"`
	} `xml:"eventCode"`
	Effective   string `xml:"effective"`
	Onset       string `xml:"onset"`
	Expires     string `xml:"expires"`
	SenderName  string `xml:"senderName"`
	Headline    string `xml:"headline"`
	Description string `xml:"description"`
	Instruction string `xml:"instruction"`
	Web         string `xml:"web"`
	Contact     string `xml:"contact"`
	Areas       []struct {
		Desc     string   `xml:"areaDesc"`
		Polygons []string `xml:"polygon"`
	} `xml:"area"`
}

// ParseDetail membaca satu dokumen CAP. Hasilnya belum divalidasi; pemanggil
// menggabungkan terjemahan lalu memanggil Validate. lang adalah bahasa yang
// diminta; info berbahasa lain di dokumen yang sama tetap dibaca.
func (c *CAPFeed) ParseDetail(it ports.FeedItem, lang string, body []byte) (warning.Warning, error) {
	var doc struct {
		XMLName    xml.Name
		Identifier string    `xml:"identifier"`
		Sender     string    `xml:"sender"`
		Sent       string    `xml:"sent"`
		Status     string    `xml:"status"`
		MsgType    string    `xml:"msgType"`
		References string    `xml:"references"`
		Infos      []capInfo `xml:"info"`
	}
	if err := decodeXML(body, &doc); err != nil {
		return warning.Warning{}, fmt.Errorf("%w: CAP: %w", ErrStructure, err)
	}
	if doc.XMLName.Local != "alert" || !capNamespaces[doc.XMLName.Space] {
		return warning.Warning{}, fmt.Errorf("%w: akar dokumen %s %s bukan alert CAP", ErrStructure, doc.XMLName.Space, doc.XMLName.Local)
	}
	if len(doc.Infos) == 0 {
		return warning.Warning{}, fmt.Errorf("%w: CAP %s tanpa info", ErrStructure, doc.Identifier)
	}
	sent, err := capTime(doc.Sent)
	if err != nil {
		return warning.Warning{}, fmt.Errorf("sent: %w", err)
	}
	refs, err := warning.ParseReferences(doc.References)
	if err != nil {
		return warning.Warning{}, err
	}
	// Info utama: yang berbahasa sesuai permintaan, atau yang pertama.
	main := doc.Infos[0]
	for _, in := range doc.Infos {
		if strings.EqualFold(strings.TrimSpace(in.Language), lang) {
			main = in
			break
		}
	}
	w := warning.Warning{
		Identifier: strings.TrimSpace(doc.Identifier),
		Sender:     strings.TrimSpace(doc.Sender),
		Sent:       sent,
		Status:     warning.ParseStatus(doc.Status),
		MsgType:    warning.ParseMsgType(doc.MsgType),
		References: refs,
		Category:   squash(main.Category),
		Urgency:    warning.ParseUrgency(main.Urgency),
		Severity:   warning.ParseSeverity(main.Severity),
		Certainty:  warning.ParseCertainty(main.Certainty),
		Contact:    squash(main.Contact),
		Web:        httpsOnly(main.Web),
		SourceURL:  SourceURL(it.File),
	}
	for _, ec := range main.EventCode {
		if v := squash(ec.Value); v != "" {
			w.EventCode = v
			break
		}
	}
	// CAP: effective kosong berarti sama dengan sent.
	if w.Effective, err = capTime(main.Effective); err != nil {
		if strings.TrimSpace(main.Effective) != "" {
			return warning.Warning{}, fmt.Errorf("effective: %w", err)
		}
		w.Effective = sent
	}
	if strings.TrimSpace(main.Onset) != "" {
		if w.Onset, err = capTime(main.Onset); err != nil {
			return warning.Warning{}, fmt.Errorf("onset: %w", err)
		}
	}
	if w.Expires, err = capTime(main.Expires); err != nil {
		return warning.Warning{}, fmt.Errorf("expires: %w", err)
	}
	seen := map[string]bool{}
	for _, in := range doc.Infos {
		l := strings.ToLower(strings.TrimSpace(in.Language))
		if l == "" {
			l = lang
		}
		if seen[l] {
			continue
		}
		seen[l] = true
		w.Texts = append(w.Texts, warning.Text{
			Language:    l,
			Event:       squash(in.Event),
			Headline:    squash(in.Headline),
			Description: paragraph(in.Description),
			Instruction: paragraph(in.Instruction),
			SenderName:  squash(in.SenderName),
		})
	}
	warning.SortTexts(w.Texts)
	for _, a := range main.Areas {
		area := warning.Area{Desc: squash(a.Desc)}
		for i, p := range a.Polygons {
			ring, err := parsePolygon(p)
			if err != nil {
				return warning.Warning{}, fmt.Errorf("area %q poligon %d: %w", area.Desc, i, err)
			}
			area.Polygons = append(area.Polygons, ring)
		}
		w.Areas = append(w.Areas, area)
	}
	return w, nil
}

// Event membungkus peringatan yang sudah valid sebagai event raw.weather.bmkg.
func (c *CAPFeed) Event(w warning.Warning) (ports.Event, error) {
	return rawpb.NewWeatherEvent(w)
}

// parsePolygon membaca "lat,lon lat,lon ..." (CAP 1.2, WGS84).
func parsePolygon(s string) (warning.Ring, error) {
	var ring warning.Ring
	for tok := range strings.FieldsSeq(s) {
		latS, lonS, ok := strings.Cut(tok, ",")
		if !ok {
			return nil, fmt.Errorf("titik %q bukan lat,lon", tok)
		}
		lat, err1 := strconv.ParseFloat(latS, 64)
		lon, err2 := strconv.ParseFloat(lonS, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("titik %q bukan angka", tok)
		}
		ring = append(ring, warning.Point{Lat: lat, Lon: lon})
	}
	return ring, nil
}

// capTime membaca waktu CAP (ISO 8601 dengan zona, misal 2026-09-24T15:07:00+08:00) ke UTC.
func capTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("waktu CAP %q: %w", s, err)
	}
	return t.UTC(), nil
}

// decodeXML membaca XML UTF-8. encoding/xml tidak memuat entitas eksternal
// (aman dari XXE) dan membatasi kedalaman elemen; ukuran dibatasi Request.MaxBytes.
func decodeXML(body []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(body))
	d.CharsetReader = func(charset string, r io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "us-ascii") {
			return r, nil
		}
		return nil, fmt.Errorf("charset %q tidak didukung", charset)
	}
	return d.Decode(v)
}

// squash merapikan spasi menjadi satu baris.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// paragraph merapikan spasi per baris tetapi mempertahankan pemisah baris,
// karena deskripsi BMKG ditulis per kalimat.
func paragraph(s string) string {
	var lines []string
	for line := range strings.SplitSeq(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l := squash(line); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// httpsOnly membuang URL yang bukan https alih-alih menolak seluruh peringatan.
func httpsOnly(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Scheme == "https" && u.Host != "" {
		return s
	}
	return ""
}
