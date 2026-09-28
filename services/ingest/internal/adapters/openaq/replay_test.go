package openaq

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
)

func TestReplayFeedRecorded(t *testing.T) {
	fetched := time.Date(2026, 9, 24, 17, 3, 20, 0, time.UTC)
	f := NewReplayFeed(stations.DefaultOptions().MaxAge)
	if f.Name() != "openaq-stasiun" || !f.Partial() || strings.Join(f.Archives(), ",") != "openaq-stasiun,openaq-stasiun-latest" {
		t.Fatal(f.Name(), f.Archives())
	}
	// Nilai terbaru sebelum ada daftar: tidak ada konteks stasiun.
	evs, rej, err := f.ParseFrom(ArchiveLatest, read(t, "latest-6539694-2026-09-24.json"), fetched)
	if err != nil || len(evs) != 0 || len(rej) != 1 || !errors.Is(rej[0].Reason, ErrNoContext) || rej[0].Key != "openaq:6539694" {
		t.Fatalf("%v %v %v", evs, rej, err)
	}
	evs, rej, err = f.Parse(read(t, "locations-2026-09-24.json"), fetched)
	if err != nil || len(evs) != 0 || len(rej) != 0 {
		t.Fatalf("%v %v %v", evs, rej, err)
	}
	evs, rej, err = f.ParseFrom(ArchiveLatest, read(t, "latest-6539694-2026-09-24.json"), fetched)
	if err != nil || len(rej) != 0 || len(evs) != 1 {
		t.Fatalf("%v %v %v", evs, rej, err)
	}

	// Isi event sama dengan jalur polling (stations.Observe dengan Source biasa).
	s := source(t)
	list, _, err := s.ParseList(read(t, "locations-2026-09-24.json"))
	if err != nil {
		t.Fatal(err)
	}
	var depok stations.Station
	for _, st := range list {
		if st.ID == "openaq:6539694" {
			depok = st
		}
	}
	live, _ := stations.Observe(s, depok, read(t, "latest-6539694-2026-09-24.json"), fetched, stations.DefaultOptions().MaxAge)
	a, _ := evs[0].Content()
	b, _ := live.Content()
	if evs[0].Key() != live.Key() || string(a) != string(b) {
		t.Fatal("replay berbeda dari polling")
	}

	// Stasiun lama: semua nilai basi, tanpa event dan tanpa penolakan.
	evs, rej, err = f.ParseFrom(ArchiveLatest, read(t, "latest-1563313-2026-09-24.json"), fetched)
	if err != nil || len(evs) != 0 || len(rej) != 0 {
		t.Fatalf("%v %v %v", evs, rej, err)
	}
}

func TestReplayFeedErrors(t *testing.T) {
	at := time.Date(2026, 9, 24, 17, 0, 0, 0, time.UTC)
	f := NewReplayFeed(24 * time.Hour)
	if _, _, err := f.ParseFrom("openaq-lain", []byte(`{}`), at); err == nil {
		t.Fatal("awalan asing diterima")
	}
	for name, body := range map[string]string{
		"bukan json":   "<html>",
		"tanpa lokasi": `{"meta":{},"results":[{"sensorsId":1,"value":1,"datetime":{"utc":"2026-09-24T16:00:00Z"}}]}`,
		"dua lokasi": `{"meta":{},"results":[{"sensorsId":1,"locationsId":5},` +
			`{"sensorsId":2,"locationsId":6}]}`,
	} {
		if _, _, err := f.ParseFrom(ArchiveLatest, []byte(body), at); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := f.ParseFrom(ArchiveList, []byte("<html>"), at); !errors.Is(err, ErrStructure) {
		t.Fatal(err)
	}
	// Payload nilai kosong: tanpa event, tanpa galat.
	if evs, rej, err := f.ParseFrom(ArchiveLatest, []byte(`{"meta":{},"results":[]}`), at); err != nil || len(evs)+len(rej) != 0 {
		t.Fatal(evs, rej, err)
	}
}
