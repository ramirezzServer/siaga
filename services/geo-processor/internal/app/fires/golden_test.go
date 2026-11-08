package fires

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/calibration"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hazard"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/hotspot"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Berkas acuan ditulis test firecluster ingest
// (internal/app/firethreshold, TestGoldenForGeoProcessor) dari deteksi yang
// sama; lihat ADR 0023.
type goldenEvent struct {
	Members   []string `json:"members"`
	Level     string   `json:"level"`
	Peak      int      `json:"peak"`
	ReachedAt string   `json:"reached_at"`
}

type golden struct {
	Rule     string        `json:"rule"`
	Excluded int           `json:"excluded"`
	Masked   int           `json:"masked"`
	Events   []goldenEvent `json:"events"`
}

var satelliteCode = map[string]string{"VIIRS_SNPP_NRT": "N", "VIIRS_NOAA20_NRT": "N20", "VIIRS_NOAA21_NRT": "N21", "MODIS_NRT": "Aqua"}

// latency adalah jeda antara lintasan dan diterimanya data di simulasi.
const latency = 3 * time.Hour

// fixture membaca deteksi asli sebagai pesan raw.fire.firms, urut waktu
// deteksi lalu ID, diterima latency setelah lintasan.
func fixture(t *testing.T) []hotspot.Detection {
	t.Helper()
	b, err := os.ReadFile("testdata/firms-2023-08.csv")
	if err != nil {
		t.Fatal(err)
	}
	cr := csv.NewReader(bytes.NewReader(b))
	cr.Comment = '#'
	recs, err := cr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var out []hotspot.Detection
	for _, r := range recs[1:] {
		lat, _ := strconv.ParseFloat(r[2], 64)
		lon, _ := strconv.ParseFloat(r[3], 64)
		at, _ := time.Parse(time.RFC3339, r[4])
		frp, _ := strconv.ParseFloat(r[6], 64)
		d := hotspot.Detection{
			ID: r[0], Product: r[1], Satellite: satelliteCode[r[1]], Instrument: "VIIRS", Lat: lat, Lon: lon, DetectedAt: at,
			Confidence: hotspot.Confidence(r[5]), BrightnessK: 340, FRPMW: &frp, ScanKm: 0.4, TrackKm: 0.4, Daytime: r[7] == "true",
			Version: "2.0NRT", FetchedAt: at.Add(latency),
		}
		if r[1] == "MODIS_NRT" {
			pct := map[hotspot.Confidence]int{hotspot.Low: 20, hotspot.Nominal: 60, hotspot.High: 90}[d.Confidence]
			d.Instrument, d.ConfidencePct, d.ScanKm, d.TrackKm = "MODIS", &pct, 1.1, 1.0
		}
		out = append(out, d)
	}
	return out
}

// TestMatchesFirecluster memutar deteksi asli Agustus 2023 satu per satu
// (seperti pesan yang tiba latency setelah lintasan) dan memastikan
// kejadian, tingkat, puncak, dan waktu capai tingkatnya sama persis dengan
// firecluster ingest (Cluster + Evaluate dengan aturan terpilih).
func TestMatchesFirecluster(t *testing.T) {
	var want golden
	b, err := os.ReadFile("testdata/firms-2023-08.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	cal, err := calibration.LoadFire()
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	clock := time.Time{}
	svc, err := New(store, fakeEncoder{}, fire.DefaultPolicy(), cal, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	var excluded, masked, merged int
	for _, d := range fixture(t) {
		clock = d.FetchedAt
		res, err := svc.Process(t.Context(), d)
		if err != nil {
			t.Fatal(err)
		}
		merged += res.Merged
		switch res.Skipped {
		case SkipProduct:
			excluded++
		case SkipStatic:
			masked++
		case SkipSeen:
			t.Fatalf("%s ulangan", d.ID)
		}
	}
	// Deteksi yang tiba urut waktu tetap menyambung dua kejadian aktif
	// (lintasan berikutnya menjembatani dua kelompok piksel).
	if merged == 0 {
		t.Fatal("tidak ada penggabungan; jalur merge tidak teruji")
	}
	if excluded != want.Excluded || masked != want.Masked {
		t.Fatalf("dibuang %d (acuan %d), tersaring %d (acuan %d)", excluded, want.Excluded, masked, want.Masked)
	}
	// Kejadian yang tidak digabung: anggota menurut kejadian aktif atau
	// kedaluwarsa (bukan merged).
	got := map[string]goldenEvent{}
	members := map[hazard.EventID][]string{}
	for id, m := range store.state.members {
		members[m.EventID] = append(members[m.EventID], id)
	}
	for id, e := range store.state.events {
		if e.status == ports.StatusMerged {
			if len(members[id]) != 0 {
				t.Fatalf("kejadian gabungan %s masih punya anggota", id)
			}
			continue
		}
		a := e.rec.Assessment
		ids := make([]string, len(a.Members))
		for i, m := range a.Members {
			ids[i] = m.ID
		}
		stored := slices.Clone(members[id])
		slices.Sort(stored)
		sortedIDs := slices.Sorted(slices.Values(ids))
		if !slices.Equal(stored, sortedIDs) {
			t.Fatalf("anggota tersimpan %s berbeda dari isi kejadian", id)
		}
		got[ids[0]] = goldenEvent{Members: ids, Level: fire.Label(a.Level), Peak: a.PeakDetections, ReachedAt: a.ReachedAt.UTC().Format(time.RFC3339)}
	}
	if len(got) != len(want.Events) {
		t.Fatalf("%d kejadian, acuan %d", len(got), len(want.Events))
	}
	for _, w := range want.Events {
		g, ok := got[w.Members[0]]
		if !ok || !slices.Equal(g.Members, w.Members) || g.Level != w.Level || g.Peak != w.Peak || g.ReachedAt != w.ReachedAt {
			t.Fatalf("kejadian %s:\n  dapat %+v\n  acuan %+v", w.Members[0], g, w)
		}
	}
	if want.Rule != "SIAGA" {
		t.Fatalf("aturan acuan %q", want.Rule)
	}
	// Setiap kejadian punya created; yang lebih tua dari 24 jam setelah data
	// terakhir sudah diakhiri putaran kedaluwarsa.
	n, err := svc.Expire(t.Context(), clock.Add(48*time.Hour), 10000)
	if err != nil || len(store.active()) != 0 || n == 0 {
		t.Fatalf("%d diakhiri, %d masih aktif, %v", n, len(store.active()), err)
	}
	created := 0
	for _, m := range store.state.outbox {
		if strings.HasSuffix(m.Subject, ".created") {
			created++
		}
	}
	if created != len(store.state.events) {
		t.Fatalf("%d created untuk %d kejadian", created, len(store.state.events))
	}
}
