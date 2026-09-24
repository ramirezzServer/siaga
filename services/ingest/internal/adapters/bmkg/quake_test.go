package bmkg

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var fetchedAt = time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)

func connector(t *testing.T, name string) *QuakeConnector {
	t.Helper()
	for _, c := range NewQuakeConnectors("http://replay.test/TEWS") {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("konektor %s tidak ada", name)
	return nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func reports(t *testing.T, evs []ports.Event) []*rawv1.QuakeReport {
	t.Helper()
	out := make([]*rawv1.QuakeReport, len(evs))
	for i, e := range evs {
		out[i] = e.(*rawpb.QuakeEvent).Report()
		if e.Subject() != "raw.quake.bmkg" {
			t.Fatalf("subjek %q", e.Subject())
		}
	}
	return out
}

func TestAutogempa(t *testing.T) {
	c := connector(t, "bmkg-autogempa")
	req := c.Request()
	if req.URL != "http://replay.test/TEWS/autogempa.json" || c.ArchiveExt() != "json" || req.MaxBytes == 0 {
		t.Fatalf("request %+v", req)
	}
	evs, rej, err := c.Parse(fixture(t, "autogempa.json"), fetchedAt)
	if err != nil || len(rej) != 0 || len(evs) != 1 {
		t.Fatalf("%d event, %v, %v", len(evs), rej, err)
	}
	got := reports(t, evs)[0]
	want := &rawv1.QuakeReport{
		Source:           hazardv1.Source_SOURCE_BMKG,
		Feed:             rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST,
		SourceEventId:    "20260923121656",
		OccurredAt:       got.GetOccurredAt(),
		Epicenter:        &commonv1.Point{Latitude: -8.51, Longitude: 123.27},
		Magnitude:        1.8,
		DepthKm:          12,
		Place:            "Pusat gempa berada di darat 24 km barat daya Lembata",
		FeltDescription:  "II-III Kab. Lembata",
		TsunamiPotential: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_UNSPECIFIED,
		PotentialText:    "Gempa ini dirasakan untuk diteruskan pada masyarakat",
		ShakemapUrl:      "https://data.bmkg.go.id/DataMKG/TEWS/20260923191656.mmi.jpg",
	}
	if !proto.Equal(got, want) {
		t.Fatalf("hasil:\n%v\ningin:\n%v", got, want)
	}
	if !got.GetOccurredAt().AsTime().Equal(time.Date(2026, 9, 23, 12, 16, 56, 0, time.UTC)) {
		t.Fatalf("waktu %v", got.GetOccurredAt().AsTime())
	}
	if evs[0].Key() != "20260923121656" {
		t.Fatalf("kunci %q", evs[0].Key())
	}
}

func TestGempaterkiniAndDirasakan(t *testing.T) {
	evs, rej, err := connector(t, "bmkg-gempaterkini").Parse(fixture(t, "gempaterkini.json"), fetchedAt)
	if err != nil || len(rej) != 0 || len(evs) != 2 {
		t.Fatalf("%d event, %v, %v", len(evs), rej, err)
	}
	r := reports(t, evs)
	if r[0].GetEpicenter().GetLatitude() != 4.74 || r[0].GetTsunamiPotential() != rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE ||
		r[0].GetFeed() != rawv1.QuakeFeed_QUAKE_FEED_BMKG_RECENT || r[1].GetMagnitude() != 5.3 {
		t.Fatalf("gempaterkini: %v", r)
	}

	evs, rej, err = connector(t, "bmkg-gempadirasakan").Parse(fixture(t, "gempadirasakan.json"), fetchedAt)
	if err != nil || len(rej) != 0 || len(evs) != 2 {
		t.Fatalf("%d event, %v, %v", len(evs), rej, err)
	}
	r = reports(t, evs)
	if r[1].GetFeltDescription() != "II - III Sumur" || r[1].GetSourceEventId() != "20260922222742" ||
		r[1].GetFeed() != rawv1.QuakeFeed_QUAKE_FEED_BMKG_FELT || r[1].GetShakemapUrl() != "" {
		t.Fatalf("gempadirasakan: %v", r[1])
	}
}

// Gempa yang sama di dua feed harus punya ID sumber yang sama agar bisa digabung
// geo-processor, tetapi isi (dan ID pesan) tetap berbeda karena feed-nya berbeda.
func TestSameQuakeAcrossFeedsSharesSourceID(t *testing.T) {
	body := []byte(`{"Infogempa":{"gempa":[` + strings.TrimSuffix(strings.TrimPrefix(string(fixture(t, "autogempa.json")), `{"Infogempa":{"gempa":`), "}}\n") + `]}}`)
	felt, _, err := connector(t, "bmkg-gempadirasakan").Parse(body, fetchedAt)
	if err != nil || len(felt) != 1 {
		t.Fatalf("%v", err)
	}
	latest, _, _ := connector(t, "bmkg-autogempa").Parse(fixture(t, "autogempa.json"), fetchedAt)
	if felt[0].Key() != latest[0].Key() {
		t.Fatalf("ID sumber berbeda: %s vs %s", felt[0].Key(), latest[0].Key())
	}
	a, _ := felt[0].Content()
	b, _ := latest[0].Content()
	if string(a) == string(b) {
		t.Fatal("isi dari feed berbeda harus berbeda")
	}
}

func TestFallbacksAndLenientTypes(t *testing.T) {
	body := `{"Infogempa":{"gempa":[
	  {"Tanggal":"23 Agu 2026","Jam":"19:16:56 WIB","Lintang":"6,91 LS","Bujur":"107,61 BT","Magnitude":4.2,"Kedalaman":"10km","Wilayah":"  Pusat   gempa di darat ","Potensi":"Tidak berpotensi tsunami","Shakemap":"../../etc/passwd"},
	  {"Tanggal":"1 Mei 2026","Jam":"01:00:00 WITA","DateTime":null,"Coordinates":"-8.1,115.2","Magnitude":"5,0","Kedalaman":"30 Km","Potensi":"Berpotensi tsunami"},
	  {"Tanggal":"2 Jan 2026","Jam":"00:30:00 WIB","DateTime":"bukan-waktu","Coordinates":"-6.9,107.6","Magnitude":"3","Kedalaman":"5 km"}
	]}}`
	evs, rej, err := connector(t, "bmkg-gempaterkini").Parse([]byte(body), fetchedAt)
	if err != nil || len(rej) != 0 || len(evs) != 3 {
		t.Fatalf("%d event, %v, %v", len(evs), rej, err)
	}
	r := reports(t, evs)
	if got := r[0].GetOccurredAt().AsTime(); !got.Equal(time.Date(2026, 8, 23, 12, 16, 56, 0, time.UTC)) {
		t.Errorf("WIB ke UTC: %v", got)
	}
	if r[0].GetEpicenter().GetLatitude() != -6.91 || r[0].GetEpicenter().GetLongitude() != 107.61 || r[0].GetMagnitude() != 4.2 {
		t.Errorf("fallback Lintang/Bujur dan magnitudo angka: %v", r[0])
	}
	if r[0].GetPlace() != "Pusat gempa di darat" || r[0].GetShakemapUrl() != "" {
		t.Errorf("spasi harus dirapikan dan shakemap mencurigakan diabaikan: %v", r[0])
	}
	if got := r[1].GetOccurredAt().AsTime(); !got.Equal(time.Date(2026, 4, 30, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("WITA ke UTC melewati tengah malam: %v", got)
	}
	if r[1].GetMagnitude() != 5.0 || r[1].GetTsunamiPotential() != rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_POTENTIAL || r[1].GetDepthKm() != 30 {
		t.Errorf("baris kedua: %v", r[1])
	}
	if got := r[2].GetOccurredAt().AsTime(); !got.Equal(time.Date(2026, 1, 1, 17, 30, 0, 0, time.UTC)) {
		t.Errorf("DateTime rusak harus jatuh ke Tanggal+Jam: %v", got)
	}
}

func TestRejectsBadRecordsButKeepsOthers(t *testing.T) {
	good := `{"DateTime":"2026-09-23T12:16:56+00:00","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`
	bad := []string{
		`{"DateTime":"kemarin","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"Tanggal":"31 Feb 2026","Jam":"10:00:00 WIB","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"Tanggal":"3 Xyz 2026","Jam":"10:00:00 WIB","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"Tanggal":"3 Sep 2026","Jam":"25:00:00 WIB","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"Tanggal":"3 Sep 2026","Jam":"10:00:00 UTC","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:00Z","Coordinates":"-8.51","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:01Z","Coordinates":"a,b","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:02Z","Lintang":"8.5","Bujur":"123 BT","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:03Z","Lintang":"x LS","Bujur":"123 BT","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:04Z","Lintang":"8.5 LS","Bujur":"123 BX","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:05Z","Coordinates":"-8.51,123.27","Magnitude":"besar","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-23T12:00:06Z","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"dalam"}`,
		`{"DateTime":"2026-09-23T12:00:07Z","Coordinates":"-98.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		`{"DateTime":"2026-09-25T12:00:00Z","Coordinates":"-8.51,123.27","Magnitude":"1.8","Kedalaman":"12 km"}`,
		good, // ID ganda dalam payload yang sama
	}
	body := `{"Infogempa":{"gempa":[` + good + "," + strings.Join(bad, ",") + `]}}`
	evs, rej, err := connector(t, "bmkg-gempaterkini").Parse([]byte(body), fetchedAt)
	if err != nil || len(evs) != 1 || len(rej) != len(bad) {
		t.Fatalf("%d event, %d tolak (ingin 1 dan %d), %v", len(evs), len(rej), len(bad), err)
	}
	for _, r := range rej {
		if r.Key == "" || r.Reason == nil {
			t.Fatalf("penolakan tanpa keterangan: %+v", r)
		}
	}
}

func TestStructureErrors(t *testing.T) {
	c := connector(t, "bmkg-autogempa")
	for _, body := range []string{
		``, `[]`, `{}`, `{"Infogempa":{}}`, `{"Infogempa":{"gempa":"teks"}}`,
		`{"Infogempa":{"gempa":[1,2]}}`, `{"Infogempa":{"gempa":{"Magnitude":{}}}}`,
		`{"Infogempa":{"gempa":{"Magnitude":"\ud800"}}`,
	} {
		if _, _, err := c.Parse([]byte(body), fetchedAt); !errors.Is(err, ErrStructure) {
			t.Errorf("Parse(%q) = %v, ingin ErrStructure", body, err)
		}
	}
}

// Properti: masukan apa pun tidak membuat panik, dan setiap event yang lolos
// bisa diserialisasi dengan kunci tidak kosong.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"autogempa.json", "gempaterkini.json", "gempadirasakan.json"} {
		b, err := fs.ReadFile(os.DirFS("testdata"), name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"Infogempa":{"gempa":{"Tanggal":"1 Mei 2026","Jam":"01:00:00 WITA","Lintang":"1 LU","Bujur":"1 BB","Magnitude":"1","Kedalaman":"1 km"}}}`))
	c := NewQuakeConnectors(DefaultTEWSBaseURL)[0]
	f.Fuzz(func(t *testing.T, body []byte) {
		evs, _, err := c.Parse(body, fetchedAt)
		if err != nil && len(evs) > 0 {
			t.Fatal("galat struktur tidak boleh disertai event")
		}
		for _, e := range evs {
			if e.Key() == "" {
				t.Fatal("kunci kosong")
			}
			if _, err := e.Content(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
