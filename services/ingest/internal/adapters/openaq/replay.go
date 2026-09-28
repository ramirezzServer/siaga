package openaq

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Nama konektor di kunci arsip: daftar lokasi dan nilai terbaru per lokasi.
const (
	ArchiveList   = "openaq-stasiun"
	ArchiveLatest = "openaq-stasiun-latest"
)

// ErrNoContext menandai payload nilai terbaru untuk stasiun yang belum ada di
// daftar lokasi sebelumnya (misal replay dimulai dengan -from setelah daftar
// terakhir diarsipkan).
var ErrNoContext = errors.New("stasiun tidak ada di daftar lokasi sebelumnya")

// ReplayFeed memutar ulang arsip OpenAQ (dipenuhi replay.MultiFeed dan
// replay.PartialFeed). Kunci arsip tidak menyebut stasiun yang diminta dan
// nilai terbaru hanya menyebut ID sensor, jadi konteksnya diambil dari
// payload daftar lokasi terakhir sebelum payload nilai itu, persis seperti
// yang dipakai polling saat itu (daftar diarsipkan setiap kali berubah dan
// setiap ingest start).
type ReplayFeed struct {
	src      *Source
	stations map[int64]stations.Station
	maxAge   time.Duration
}

// NewReplayFeed membuat feed replay; maxAge sama dengan polling
// (stations.Options.MaxAge).
func NewReplayFeed(maxAge time.Duration) *ReplayFeed {
	return &ReplayFeed{src: &Source{sensors: map[int64]sensorInfo{}}, stations: map[int64]stations.Station{}, maxAge: maxAge}
}

// Name sama dengan konektor polling, supaya ID pesan sama.
func (f *ReplayFeed) Name() string { return ArchiveList }

// Archives adalah daftar lokasi dan nilai terbaru.
func (f *ReplayFeed) Archives() []string { return []string{ArchiveList, ArchiveLatest} }

// Partial selalu true: satu payload nilai terbaru hanya satu stasiun.
func (f *ReplayFeed) Partial() bool { return true }

// Parse sama dengan ParseFrom untuk daftar lokasi.
func (f *ReplayFeed) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	return f.ParseFrom(ArchiveList, body, fetchedAt)
}

// ParseFrom membaca daftar lokasi (memperbarui konteks, tanpa event) atau
// nilai terbaru satu lokasi (satu event bila ada nilai yang belum basi).
func (f *ReplayFeed) ParseFrom(archive string, body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	switch archive {
	case ArchiveList:
		list, rejected, err := f.src.ParseList(body)
		if err != nil {
			return nil, nil, err
		}
		f.stations = make(map[int64]stations.Station, len(list))
		for _, st := range list {
			id, err := strconv.ParseInt(strings.TrimPrefix(st.ID, "openaq:"), 10, 64)
			if err != nil {
				rejected = append(rejected, ports.Rejection{Key: st.ID, Reason: err})
				continue
			}
			f.stations[id] = st
		}
		return nil, rejected, nil
	case ArchiveLatest:
		loc, err := latestLocation(body)
		if err != nil || loc == 0 {
			return nil, nil, err
		}
		st, ok := f.stations[loc]
		if !ok {
			return nil, []ports.Rejection{{Key: airquality.StationID(loc), Reason: ErrNoContext}}, nil
		}
		ev, rejected := stations.Observe(f.src, st, body, fetchedAt, f.maxAge)
		if ev == nil {
			return nil, rejected, nil
		}
		return []ports.Event{ev}, rejected, nil
	}
	return nil, nil, fmt.Errorf("awalan arsip %q bukan milik %s", archive, f.Name())
}

// latestLocation membaca ID lokasi dari payload nilai terbaru; nol bila
// payload tidak berisi nilai. Semua baris harus dari lokasi yang sama.
func latestLocation(body []byte) (int64, error) {
	rows, err := decode[latest](body)
	if err != nil {
		return 0, err
	}
	var loc int64
	for _, r := range rows {
		switch {
		case r.LocationsID <= 0:
			return 0, fmt.Errorf("%w: sensor %d tanpa ID lokasi", ErrStructure, r.SensorsID)
		case loc != 0 && r.LocationsID != loc:
			return 0, fmt.Errorf("%w: nilai dari lokasi %d dan %d dalam satu payload", ErrStructure, loc, r.LocationsID)
		}
		loc = r.LocationsID
	}
	return loc, nil
}
