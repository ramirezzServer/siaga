package openaq

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
)

const key = "KUNCIUJIKUNCIUJIKUNCIUJIKUNCIUJI"

func read(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func source(t *testing.T) *Source {
	t.Helper()
	s, err := New(DefaultBaseURL+"/", key, area.JawaBarat)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRequests(t *testing.T) {
	s := source(t)
	req := s.ListRequest()
	if req.URL != DefaultBaseURL+"/locations?bbox=106.3%2C-7.9%2C108.9%2C-5.7&limit=1000" || req.Header["X-API-Key"] != key ||
		len(req.Secrets) != 1 || strings.Contains(req.Redacted(), key) {
		t.Fatalf("%+v", req)
	}
	d := s.DetailRequest(stations.Station{Station: airquality.Station{ID: "openaq:2178"}})
	if d.URL != DefaultBaseURL+"/locations/2178/latest?limit=100" || d.Header["X-API-Key"] != key {
		t.Fatalf("%+v", d)
	}
	if s.Name() != "openaq-stasiun" {
		t.Fatal(s.Name())
	}
	if _, err := New(DefaultBaseURL, "", area.JawaBarat); err == nil {
		t.Error("key kosong diterima")
	}
	if _, err := New(DefaultBaseURL, key, area.Box{}); err == nil {
		t.Error("kotak kosong diterima")
	}
}

func TestParseListAndDetail(t *testing.T) {
	s := source(t)
	list, rejected, err := s.ParseList(read(t, "locations-sintetis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || len(rejected) != 1 || rejected[0].Key != "openaq:99003" {
		t.Fatalf("%d stasiun %v, penolakan %v", len(list), list, rejected)
	}
	dago := list[0]
	if dago.ID != "openaq:2178" || dago.Provider != "AirGradient" || dago.Owner != "Warga Dago" || dago.IsMonitor ||
		!dago.LastReport.Equal(time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)) || dago.Lat != -6.8841 {
		t.Fatalf("%+v", dago)
	}
	if list[1].Locality != "" || !list[1].IsMonitor {
		t.Fatalf("%+v", list[1])
	}
	readings, err := s.ParseDetail(dago, read(t, "latest-2178-sintetis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 2 || readings[0].Parameter != airquality.PM25 || readings[0].Unit != airquality.UnitMicrogram ||
		readings[0].Value != 41.5 || readings[1].SensorID != 3917 {
		t.Fatalf("%+v", readings)
	}
	obs := airquality.Observation{Station: dago.Station, Readings: readings[:1]}
	ev, err := s.Event(obs)
	if err != nil {
		t.Fatal(err)
	}
	msg := ev.(*rawpb.ModelEvent).Message().(*rawv1.AirQualityObservation)
	if ev.Subject() != "raw.aq.openaq" || ev.Key() != "openaq:2178" || msg.GetStation().GetProvider() != "AirGradient" ||
		msg.GetReadings()[0].GetSensorId() != 3916 {
		t.Fatalf("%v", msg)
	}
}

func TestParseDetailErrors(t *testing.T) {
	s := source(t)
	list, _, _ := s.ParseList(read(t, "locations-sintetis.json"))
	dago := list[0]
	body := `{"meta":{},"results":[
		{"datetime":{"utc":"kemarin"},"value":1,"sensorsId":3916,"locationsId":2178},
		{"datetime":null,"value":1,"sensorsId":3917,"locationsId":2178},
		{"datetime":{"utc":"2026-09-24T07:00:00Z"},"value":1,"sensorsId":3916,"locationsId":8840},
		{"datetime":{"utc":"2026-09-24T07:00:00Z"},"value":1,"sensorsId":22160,"locationsId":8840}]}`
	readings, err := s.ParseDetail(dago, []byte(body))
	if len(readings) != 0 || err == nil || strings.Count(err.Error(), "sensor") != 3 {
		t.Fatalf("%v %v", readings, err)
	}
	if _, err := s.ParseDetail(stations.Station{Station: airquality.Station{ID: "openaq:x"}}, []byte(`{"meta":{},"results":[]}`)); err == nil {
		t.Error("ID stasiun rusak diterima")
	}
	for name, b := range map[string][]byte{
		"tanpa key":  read(t, "tanpa-key.json"),
		"bukan json": []byte("<html>"),
		"kosong":     nil,
	} {
		if _, _, err := s.ParseList(b); !errors.Is(err, ErrStructure) {
			t.Errorf("list %s: %v", name, err)
		}
		if _, err := s.ParseDetail(dago, b); !errors.Is(err, ErrStructure) {
			t.Errorf("detail %s: %v", name, err)
		}
	}
}

func TestParseListTruncatedAndBadTime(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"meta":{},"results":[`)
	for i := range ListLimit {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":1,"name":"x","isMobile":true}`)
	}
	b.WriteString(`,{"id":7,"name":"Waktu rusak","provider":{"name":"P"},"sensors":[{"id":70,"parameter":{"name":"PM25","units":"ug/m3"}}],
		"coordinates":{"latitude":-6.9,"longitude":107.6},"datetimeLast":{"utc":"kemarin"}}]}`)
	s := source(t)
	list, rejected, err := s.ParseList([]byte(b.String()))
	if err != nil || len(list) != 0 || len(rejected) != 2 || rejected[0].Key != "daftar" {
		t.Fatalf("%v %v %v", list, rejected, err)
	}
	if s.sensors[70].unit != airquality.UnitMicrogram || s.sensors[70].parameter != airquality.PM25 {
		t.Fatalf("%+v", s.sensors[70])
	}
}

func TestClipAndUnits(t *testing.T) {
	if got := clip("  a\t b\n  c  "); got != "a b c" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("é", 60)
	if got := clip(long); len(got) > 100 || !strings.HasPrefix(long, got) {
		t.Fatalf("%q", got)
	}
	for in, want := range map[string]string{"μg/m³": "µg/m³", "UG/M3": "µg/m³", " ppm ": "ppm", "ppb": "ppb", "c": "c"} {
		if got := normalizeUnit(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}

// ParseList dan ParseDetail tidak pernah panik, galat struktur selalu
// dibungkus ErrStructure, dan setiap stasiun yang keluar punya ID unik.
func FuzzParseOpenAQ(f *testing.F) {
	f.Add(read(f, "locations-sintetis.json"), read(f, "latest-2178-sintetis.json"))
	f.Add(read(f, "tanpa-key.json"), []byte(`{"meta":{},"results":[]}`))
	f.Add(read(f, "locations-2026-09-24.json"), read(f, "latest-6539694-2026-09-24.json"))
	f.Fuzz(func(t *testing.T, list, detail []byte) {
		s := &Source{base: DefaultBaseURL, key: key, box: area.JawaBarat, sensors: map[int64]sensorInfo{}}
		stationsOut, _, err := s.ParseList(list)
		if err != nil {
			if !errors.Is(err, ErrStructure) {
				t.Fatalf("galat tanpa ErrStructure: %v", err)
			}
			return
		}
		for _, st := range stationsOut {
			readings, _ := s.ParseDetail(st, detail)
			for _, r := range readings {
				if !airquality.Known(r.Parameter) {
					t.Fatalf("parameter %q lolos", r.Parameter)
				}
			}
		}
	})
}

// Rekaman asli 2026-09-24 17.03 UTC: 31 lokasi di kotak Jawa Barat, hanya
// dua yang melapor dalam 48 jam (AirGradient "BMKG 1" di Jakarta dan "Griya
// Tugu Asri" di Depok). 4 lokasi tanpa sensor parameter SIAGA (hanya karbon
// hitam, suhu, dsb.) dilewati; 7 dari sisanya tanpa datetimeLast.
func TestRecordedPayloads(t *testing.T) {
	fetched := time.Date(2026, 9, 24, 17, 3, 20, 0, time.UTC)
	s := source(t)
	list, rejected, err := s.ParseList(read(t, "locations-2026-09-24.json"))
	if err != nil || len(rejected) != 0 {
		t.Fatalf("%v %v", rejected, err)
	}
	active := map[string]stations.Station{}
	noReport := 0
	for _, st := range list {
		switch {
		case st.LastReport.IsZero():
			noReport++
		case st.LastReport.After(fetched.Add(-48 * time.Hour)):
			active[st.ID] = st
		}
	}
	if len(list) != 27 || len(active) != 2 || noReport != 7 {
		t.Fatalf("%d stasiun, %d aktif, %d tanpa laporan", len(list), len(active), noReport)
	}
	depok := active["openaq:6539694"]
	if depok.Name != "Griya Tugu Asri" || depok.Provider != "AirGradient" || depok.IsMonitor {
		t.Fatalf("%+v", depok)
	}
	readings, err := s.ParseDetail(depok, read(t, "latest-6539694-2026-09-24.json"))
	if err != nil {
		t.Fatal(err)
	}
	// pm1, suhu, kelembapan, dan um003 dilewati; hanya PM2,5.
	if len(readings) != 1 || readings[0].SensorID != 17620437 || readings[0].Parameter != airquality.PM25 || readings[0].Value != 57.5 {
		t.Fatalf("%+v", readings)
	}
	kept, dropped := airquality.Clean(readings, fetched, 24*time.Hour)
	obs := airquality.Observation{Station: depok.Station, Readings: kept}
	if len(dropped) != 0 || obs.Validate(fetched, 24*time.Hour) != nil {
		t.Fatalf("%v %v", dropped, obs.Validate(fetched, 24*time.Hour))
	}
	// Stasiun lama: nilai 2024–2025 semuanya basi.
	var old stations.Station
	for _, st := range list {
		if st.ID == "openaq:1563313" {
			old = st
		}
	}
	readings, err = s.ParseDetail(old, read(t, "latest-1563313-2026-09-24.json"))
	if err != nil || len(readings) == 0 {
		t.Fatalf("%v %v", readings, err)
	}
	if kept, _ := airquality.Clean(readings, fetched, 24*time.Hour); len(kept) != 0 {
		t.Fatalf("nilai basi lolos: %+v", kept)
	}
}
