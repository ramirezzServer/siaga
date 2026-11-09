// Package quakeml membaca jawaban layanan FDSN event ISC Bulletin (QuakeML
// 1.2 dengan includeallorigins=true) menjadi kejadian quakelabel: semua
// origin agensi beserta magnitudo yang menempel di masing-masing origin.
package quakeml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quakelabel"
)

// ErrFormat berarti isi bukan dokumen QuakeML yang dikenali (misal halaman
// galat HTML atau jawaban terpotong).
var ErrFormat = errors.New("bukan dokumen QuakeML")

// Empty adalah dokumen QuakeML tanpa kejadian, disimpan di cache untuk
// minggu yang dijawab ISC tanpa data (HTTP 204).
const Empty = `<?xml version="1.0" encoding="UTF-8"?>
<q:quakeml xmlns:q="http://quakeml.org/xmlns/quakeml/1.2" xmlns="http://quakeml.org/xmlns/bed/1.2"><eventParameters publicID="smi:SIAGA/empty"></eventParameters></q:quakeml>
`

type value struct {
	Value string `xml:"value"`
}

type creation struct {
	Author   string `xml:"author"`
	AgencyID string `xml:"agencyID"`
}

type xmlOrigin struct {
	PublicID  string   `xml:"publicID,attr"`
	Time      value    `xml:"time"`
	Latitude  value    `xml:"latitude"`
	Longitude value    `xml:"longitude"`
	Depth     *value   `xml:"depth"`
	Creation  creation `xml:"creationInfo"`
}

type xmlMagnitude struct {
	Mag      value    `xml:"mag"`
	Type     string   `xml:"type"`
	OriginID string   `xml:"originID"`
	Creation creation `xml:"creationInfo"`
}

type xmlEvent struct {
	PublicID   string         `xml:"publicID,attr"`
	Type       string         `xml:"type"`
	Origins    []xmlOrigin    `xml:"origin"`
	Magnitudes []xmlMagnitude `xml:"magnitude"`
}

// Parse membaca seluruh dokumen. Origin yang tidak bisa dibaca membuat
// seluruh dokumen ditolak: bulletin ISC konsisten, jadi galat berarti
// jawaban rusak dan harus diambil ulang, bukan baris yang dilewati.
func Parse(body []byte) ([]quakelabel.Event, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var events []quakelabel.Event
	root, params := false, false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFormat, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "quakeml":
			root = true
		case "eventParameters":
			params = true
		case "event":
			if !params {
				return nil, fmt.Errorf("%w: <event> di luar <eventParameters>", ErrFormat)
			}
			var x xmlEvent
			if err := dec.DecodeElement(&x, &se); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrFormat, err)
			}
			e, err := convert(x)
			if err != nil {
				return nil, err
			}
			events = append(events, e)
		}
	}
	if !root || !params {
		return nil, fmt.Errorf("%w: tidak ada <quakeml>/<eventParameters>", ErrFormat)
	}
	return events, nil
}

// EventID mengambil angka evid dari publicID "smi:ISC/evid=637056312".
func EventID(publicID string) string { return afterEq(publicID) }

func afterEq(s string) string {
	if i := strings.LastIndexByte(s, '='); i >= 0 {
		return s[i+1:]
	}
	return s
}

func convert(x xmlEvent) (quakelabel.Event, error) {
	e := quakelabel.Event{ID: EventID(x.PublicID), Type: strings.TrimSpace(x.Type)}
	if e.ID == "" {
		return e, fmt.Errorf("%w: kejadian tanpa publicID", ErrFormat)
	}
	mags := map[string][]quakelabel.Magnitude{}
	for _, m := range x.Magnitudes {
		v, err := strconv.ParseFloat(strings.TrimSpace(m.Mag.Value), 64)
		if err != nil {
			return e, fmt.Errorf("%w: kejadian %s: magnitudo %q: %w", ErrFormat, e.ID, m.Mag.Value, err)
		}
		id := strings.TrimSpace(m.OriginID)
		mags[id] = append(mags[id], quakelabel.Magnitude{
			Type: strings.TrimSpace(m.Type), Value: v, Author: strings.TrimSpace(m.Creation.Author),
		})
	}
	for _, o := range x.Origins {
		out, err := origin(o)
		if err != nil {
			return e, fmt.Errorf("kejadian %s: %w", e.ID, err)
		}
		out.Magnitudes = mags[strings.TrimSpace(o.PublicID)]
		e.Origins = append(e.Origins, out)
	}
	return e, nil
}

func origin(o xmlOrigin) (quakelabel.Origin, error) {
	out := quakelabel.Origin{ID: afterEq(strings.TrimSpace(o.PublicID))}
	out.Agency = strings.TrimSpace(o.Creation.AgencyID)
	if out.Agency == "" {
		out.Agency = strings.TrimSpace(o.Creation.Author)
	}
	t, err := parseTime(o.Time.Value)
	if err != nil {
		return out, fmt.Errorf("%w: origin %s: %w", ErrFormat, out.ID, err)
	}
	out.Time = t
	if out.Latitude, err = strconv.ParseFloat(strings.TrimSpace(o.Latitude.Value), 64); err != nil {
		return out, fmt.Errorf("%w: origin %s lintang: %w", ErrFormat, out.ID, err)
	}
	if out.Longitude, err = strconv.ParseFloat(strings.TrimSpace(o.Longitude.Value), 64); err != nil {
		return out, fmt.Errorf("%w: origin %s bujur: %w", ErrFormat, out.ID, err)
	}
	if o.Depth != nil && strings.TrimSpace(o.Depth.Value) != "" {
		m, err := strconv.ParseFloat(strings.TrimSpace(o.Depth.Value), 64)
		if err != nil {
			return out, fmt.Errorf("%w: origin %s kedalaman: %w", ErrFormat, out.ID, err)
		}
		out.DepthKm, out.HasDepth = m/1000, true // QuakeML: meter
	}
	return out, nil
}

// parseTime menerima RFC 3339 dan bentuk tanpa zona (dianggap UTC).
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse("2006-01-02T15:04:05.999999999", s)
}
