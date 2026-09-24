// Package streams mendefinisikan subjek dan stream NATS JetStream SIAGA.
// Ini bagian dari kontrak event, sama seperti proto: layanan penerbit dan
// konsumen memakai definisi yang sama. Tabel lengkapnya ada di docs/events.md.
package streams

import (
	"fmt"
	"regexp"
	"time"
)

// Spec adalah definisi satu stream JetStream, lepas dari library klien NATS.
type Spec struct {
	Name        string
	Description string
	Subjects    []string
	// MaxAge adalah retensi pesan.
	MaxAge time.Duration
	// DuplicateWindow adalah rentang JetStream mengingat Nats-Msg-Id untuk
	// menolak pesan ganda. Tidak boleh melebihi MaxAge.
	DuplicateWindow time.Duration
	// MaxBytes membatasi ukuran stream supaya disk lokal tidak penuh.
	MaxBytes int64
	// Owner adalah satu-satunya layanan yang membuat dan memperbarui stream ini,
	// sama seperti satu role pemilik per schema database.
	Owner string
}

const day = 24 * time.Hour

// Raw menampung payload sumber yang sudah divalidasi ingest (subjek raw.<jenis>.<sumber>).
var Raw = Spec{
	Name:            "RAW",
	Description:     "Data sumber tervalidasi dari ingest, belum dinormalisasi",
	Subjects:        []string{"raw.>"},
	MaxAge:          7 * day,
	DuplicateWindow: day,
	MaxBytes:        2 << 30,
	Owner:           "ingest",
}

// Hazard menampung kejadian bahaya ternormalisasi dari geo-processor.
var Hazard = Spec{
	Name:            "HAZARD",
	Description:     "Kejadian bahaya ternormalisasi dan terdeduplikasi",
	Subjects:        []string{"hazard.>"},
	MaxAge:          30 * day,
	DuplicateWindow: day,
	MaxBytes:        1 << 30,
	Owner:           "geo-processor",
}

// Kind adalah potongan subjek untuk jenis data.
type Kind string

// Jenis data. Nilainya sama dengan subjects.ts di @siaga/contracts.
const (
	KindQuake   Kind = "quake"
	KindWeather Kind = "weather"
	KindFlood   Kind = "flood"
	KindFire    Kind = "fire"
	KindAQ      Kind = "aq"
)

// Source adalah potongan subjek untuk sumber data raw.
type Source string

// Sumber data raw.
const (
	SourceBMKG      Source = "bmkg"
	SourceUSGS      Source = "usgs"
	SourceOpenMeteo Source = "openmeteo"
	SourceOpenAQ    Source = "openaq"
	SourceFIRMS     Source = "firms"
)

// Transition adalah perubahan status kejadian bahaya.
type Transition string

// Transisi kejadian bahaya.
const (
	Created Transition = "created"
	Updated Transition = "updated"
	Expired Transition = "expired"
)

var token = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// RawSubject membentuk subjek raw.<jenis>.<sumber>.
func RawSubject(k Kind, s Source) (string, error) {
	if !token.MatchString(string(k)) || !token.MatchString(string(s)) {
		return "", fmt.Errorf("streams: token subjek tidak valid %q.%q", k, s)
	}
	return "raw." + string(k) + "." + string(s), nil
}

// HazardSubject membentuk subjek hazard.<jenis>.<transisi>.
func HazardSubject(k Kind, t Transition) (string, error) {
	switch t {
	case Created, Updated, Expired:
	default:
		return "", fmt.Errorf("streams: transisi tidak dikenal %q", t)
	}
	if !token.MatchString(string(k)) {
		return "", fmt.Errorf("streams: jenis tidak valid %q", k)
	}
	return "hazard." + string(k) + "." + string(t), nil
}
