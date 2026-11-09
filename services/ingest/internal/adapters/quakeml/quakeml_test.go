package quakeml

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quakelabel"
)

func sample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/isc-sampel.xml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSample(t *testing.T) {
	events, err := Parse(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("%d kejadian, ingin 6", len(events))
	}
	e := events[0]
	if e.ID != "625597758" || e.Type != "earthquake" || len(e.Origins) != 4 {
		t.Fatalf("kejadian pertama %+v", e)
	}
	dja := e.Origins[3]
	want := quakelabel.Origin{
		ID: "631703347", Agency: "DJA", Time: time.Date(2023, 1, 8, 20, 2, 5, 100_000_000, time.UTC),
		Latitude: 1.6, Longitude: 128.29, DepthKm: 63, HasDepth: true,
		Magnitudes: []quakelabel.Magnitude{{Type: "Mjma", Value: 4.31, Author: "DJA"}, {Type: "MLv", Value: 4.38, Author: "DJA"}},
	}
	if dja.ID != want.ID || dja.Agency != want.Agency || !dja.Time.Equal(want.Time) || dja.Latitude != want.Latitude ||
		dja.Longitude != want.Longitude || dja.DepthKm != want.DepthKm || !dja.HasDepth || len(dja.Magnitudes) != 2 ||
		dja.Magnitudes[0] != want.Magnitudes[0] || dja.Magnitudes[1] != want.Magnitudes[1] {
		t.Fatalf("origin DJA\n dapat %+v\n ingin %+v", dja, want)
	}
	if got := e.Origins[0].Magnitudes; len(got) != 1 || got[0].Author != "ISC" {
		t.Fatalf("magnitudo origin ISC %+v", got)
	}
	// Origin tanpa kedalaman (NEIC 625554142 terakhir).
	last := events[1].Origins[6]
	if last.HasDepth || last.Agency != "NEIC" || len(last.Magnitudes) != 0 {
		t.Fatalf("origin tanpa kedalaman %+v", last)
	}
	if events[5].ID != "uji-kecil" {
		t.Fatalf("ID buatan %q", events[5].ID)
	}
}

func TestParseEmpty(t *testing.T) {
	events, err := Parse([]byte(Empty))
	if err != nil || len(events) != 0 {
		t.Fatalf("%v %v", events, err)
	}
}

func TestParseAgencyFallsBackToAuthor(t *testing.T) {
	doc := strings.Replace(Empty, "</eventParameters>", `<event publicID="smi:ISC/evid=1"><origin publicID="smi:ISC/origid=2">
<time><value>2023-01-01T00:00:00</value></time><latitude><value>-7</value></latitude><longitude><value>107</value></longitude>
<creationInfo><author>DJA</author></creationInfo></origin></event></eventParameters>`, 1)
	events, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	o := events[0].Origins[0]
	if o.Agency != "DJA" || !o.Time.Equal(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)) || o.Time.Location() != time.UTC {
		t.Fatalf("%+v", o)
	}
}

func TestParseRejects(t *testing.T) {
	ev := func(inner string) string {
		return strings.Replace(Empty, "</eventParameters>", `<event publicID="smi:ISC/evid=1">`+inner+`</event></eventParameters>`, 1)
	}
	okOrigin := `<origin publicID="o"><time><value>2023-01-01T00:00:00Z</value></time><latitude><value>-7</value></latitude><longitude><value>107</value></longitude>%s</origin>`
	cases := map[string]string{
		"html":           "<html><body>Service unavailable</body></html>",
		"kosong":         "",
		"terpotong":      Empty[:len(Empty)-40],
		"event di luar":  `<q:quakeml xmlns:q="x"><event publicID="e"></event></q:quakeml>`,
		"tanpa publicID": strings.Replace(ev(""), `publicID="smi:ISC/evid=1"`, "", 1),
		"waktu":          ev(`<origin publicID="o"><time><value>kemarin</value></time><latitude><value>1</value></latitude><longitude><value>1</value></longitude></origin>`),
		"lintang":        ev(`<origin publicID="o"><time><value>2023-01-01T00:00:00Z</value></time><latitude><value>x</value></latitude><longitude><value>1</value></longitude></origin>`),
		"bujur":          ev(`<origin publicID="o"><time><value>2023-01-01T00:00:00Z</value></time><latitude><value>1</value></latitude><longitude><value></value></longitude></origin>`),
		"kedalaman":      ev(strings.Replace(okOrigin, "%s", "<depth><value>dalam</value></depth>", 1)),
		"magnitudo":      ev(`<magnitude><mag><value>besar</value></mag><type>mb</type><originID>o</originID></magnitude>`),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(doc)); !errors.Is(err, ErrFormat) {
				t.Fatalf("galat %v, ingin ErrFormat", err)
			}
		})
	}
}

func TestEventID(t *testing.T) {
	for in, want := range map[string]string{"smi:ISC/evid=637056312": "637056312", "tanpa-sama-dengan": "tanpa-sama-dengan"} {
		if got := EventID(in); got != want {
			t.Errorf("EventID(%q) = %q", in, got)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(Empty))
	f.Add(sampleBytes(f))
	f.Fuzz(func(t *testing.T, b []byte) {
		events, err := Parse(b)
		if err != nil {
			if !errors.Is(err, ErrFormat) {
				t.Fatalf("galat tanpa ErrFormat: %v", err)
			}
			return
		}
		for _, e := range events {
			if e.ID == "" {
				t.Fatal("kejadian tanpa ID diterima")
			}
			for _, o := range e.Origins {
				if o.Time.Location() != time.UTC {
					t.Fatal("waktu bukan UTC")
				}
			}
		}
	})
}

func sampleBytes(f *testing.F) []byte {
	b, err := os.ReadFile("testdata/isc-sampel.xml")
	if err != nil {
		f.Fatal(err)
	}
	return b
}
