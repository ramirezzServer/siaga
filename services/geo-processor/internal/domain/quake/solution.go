package quake

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Solution adalah pandangan satu sumber tentang satu kejadian: gabungan semua
// feed sumber itu untuk ID yang sama. BMKG menerbitkan gempa yang sama di
// autogempa, gempaterkini, dan gempadirasakan dengan isi berbeda (misal hanya
// autogempa yang memuat shakemap), jadi tiap field diambil dari feed terbaru
// yang mengisinya.
type Solution struct {
	Key           IdentityKey
	OccurredAt    time.Time
	Latitude      float64
	Longitude     float64
	Magnitude     float64
	MagnitudeType string
	DepthKm       float64
	Place         string
	Felt          string
	Tsunami       Tsunami
	PotentialText string
	ShakemapURL   string
	SourceURL     string
	// AlternateIDs adalah ID lain untuk kejadian yang sama di sumber yang sama
	// (USGS "ids"). Solusi yang saling menyebut ID dianggap satu kejadian.
	AlternateIDs []string
	// Deleted true bila sumber menarik kejadian ini (USGS "deleted").
	Deleted bool
	// FirstSeenAt adalah waktu ambil paling awal di feed mana pun. Di antara dua
	// ID dari sumber yang sama dalam satu kejadian, yang terlihat paling akhir
	// adalah revisi terbaru.
	FirstSeenAt time.Time
}

// Origin mengembalikan parameter yang dipakai deduplikasi.
func (s Solution) Origin() Origin {
	return Origin{OccurredAt: s.OccurredAt, Latitude: s.Latitude, Longitude: s.Longitude, Magnitude: s.Magnitude}
}

// ErrNoReports dikembalikan Merge untuk daftar laporan kosong.
var ErrNoReports = errors.New("tidak ada laporan untuk digabung")

// Merge menggabungkan laporan semua feed milik satu kejadian di satu sumber.
// Parameter angka (waktu, lokasi, magnitudo, kedalaman) diambil utuh dari
// laporan terbaru supaya tidak tercampur antar-revisi; field teks diambil dari
// laporan terbaru yang mengisinya.
func Merge(reports []Stored) (Solution, error) {
	if len(reports) == 0 {
		return Solution{}, ErrNoReports
	}
	key := reports[0].Identity()
	for _, r := range reports[1:] {
		if r.Identity() != key {
			return Solution{}, fmt.Errorf("%w: laporan %s dan %s bukan kejadian yang sama", ErrInvalid, key, r.Identity())
		}
	}
	// Terbaru lebih dulu; newerThan total sehingga urutan masukan tidak berpengaruh.
	sorted := slices.Clone(reports)
	slices.SortFunc(sorted, func(a, b Stored) int {
		switch {
		case a.newerThan(b.Report):
			return -1
		case b.newerThan(a.Report):
			return 1
		default:
			return 0
		}
	})
	top := sorted[0]
	s := Solution{
		Key:           key,
		OccurredAt:    top.OccurredAt,
		Latitude:      top.Latitude,
		Longitude:     top.Longitude,
		Magnitude:     top.Magnitude,
		MagnitudeType: top.MagnitudeType,
		DepthKm:       top.DepthKm,
		AlternateIDs:  slices.Clone(top.AlternateIDs),
		Deleted:       top.Deleted(),
		FirstSeenAt:   top.FirstSeenAt,
	}
	firstText := func(get func(Report) string) string {
		for _, r := range sorted {
			if v := get(r.Report); v != "" {
				return v
			}
		}
		return ""
	}
	s.Place = firstText(func(r Report) string { return r.Place })
	s.Felt = firstText(func(r Report) string { return r.Felt })
	s.PotentialText = firstText(func(r Report) string { return r.PotentialText })
	s.ShakemapURL = firstText(func(r Report) string { return r.ShakemapURL })
	s.SourceURL = firstText(func(r Report) string { return r.SourceURL })
	for _, r := range sorted {
		if r.Tsunami != TsunamiUnknown {
			s.Tsunami = r.Tsunami
			break
		}
	}
	for _, r := range sorted[1:] {
		if r.FirstSeenAt.Before(s.FirstSeenAt) {
			s.FirstSeenAt = r.FirstSeenAt
		}
	}
	return s, nil
}

// linked melaporkan apakah dua solusi dari sumber yang sama saling menyebut
// sebagai ID alternatif.
func (s Solution) linked(o Solution) bool {
	return s.Key.Source == o.Key.Source &&
		(slices.Contains(s.AlternateIDs, o.Key.EventID) || slices.Contains(o.AlternateIDs, s.Key.EventID))
}
