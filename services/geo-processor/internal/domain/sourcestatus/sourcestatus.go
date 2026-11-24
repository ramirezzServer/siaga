// Package sourcestatus berisi aturan murni keadaan konektor ingest di
// ts.source_status: hasil setiap polling dari stream STATUS dilipat menjadi
// keadaan terakhir per konektor (kapan terakhir dicoba, berhasil, dan membawa
// isi baru, serta kegagalan beruntun). Keadaan ini tahan restart ingest,
// karena disimpan di database dan bukan di memori proses (ADR 0030).
// Invarian di sini sama dengan constraint migrasi 00009_source_status.sql.
package sourcestatus

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalid menandai hasil polling yang melanggar invarian. Pesan dengan
// galat ini tidak akan berhasil bila dicoba ulang.
var ErrInvalid = errors.New("hasil polling konektor tidak valid")

// Batas nilai.
const (
	// MaxConnectorLen adalah panjang nama konektor terbesar.
	MaxConnectorLen = 60
	// MinInterval dan MaxInterval membatasi jarak polling normal.
	MinInterval = time.Second
	MaxInterval = 7 * 24 * time.Hour
	// MaxDuration adalah lama percobaan terbesar (sapuan prakiraan ±2 jam).
	MaxDuration = 24 * time.Hour
	// MaxClockSkew adalah selisih jam ingest–geo-processor yang ditoleransi.
	MaxClockSkew = 10 * time.Minute
)

var (
	// Sama dengan nama konektor ingest, misal "usgs-2.5-day".
	connectorFormat = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)
	// Subjek raw yang dikenal: jenis dan sumber sama dengan
	// libs/go/contracts/streams (dicek di test).
	subjectFormat = regexp.MustCompile(`^raw\.(quake|weather|forecast|aq|flood|rain|fire)\.(bmkg|usgs|openmeteo|openaq|firms)$`)
)

// Poll adalah hasil satu percobaan konektor: satu pesan
// status.connector.<konektor>.
type Poll struct {
	Connector string
	// Subject adalah subjek raw yang diisi konektor, misal "raw.quake.bmkg".
	Subject  string
	Interval time.Duration
	// StartedAt dan FinishedAt adalah awal dan akhir percobaan (UTC).
	StartedAt  time.Time
	FinishedAt time.Time
	OK         bool
	// Published adalah pesan raw baru yang diterima broker; lebih dari nol
	// berarti percobaan membawa data baru.
	Published int
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func validName(connector string) error {
	if len(connector) > MaxConnectorLen || !connectorFormat.MatchString(connector) {
		return invalid("nama konektor %q", connector)
	}
	return nil
}

// Validate memeriksa invarian hasil polling terhadap jam now.
func (p Poll) Validate(now time.Time) error {
	if err := validName(p.Connector); err != nil {
		return err
	}
	switch {
	case !subjectFormat.MatchString(p.Subject):
		return invalid("%s: subjek raw %q tidak dikenal", p.Connector, p.Subject)
	case p.Interval < MinInterval || p.Interval > MaxInterval:
		return invalid("%s: interval %v di luar %v..%v", p.Connector, p.Interval, MinInterval, MaxInterval)
	case p.StartedAt.IsZero() || p.FinishedAt.Before(p.StartedAt):
		return invalid("%s: waktu percobaan %v..%v", p.Connector, p.StartedAt, p.FinishedAt)
	case p.FinishedAt.Sub(p.StartedAt) > MaxDuration:
		return invalid("%s: percobaan lebih lama dari %v", p.Connector, MaxDuration)
	case p.FinishedAt.After(now.Add(MaxClockSkew)):
		return invalid("%s: waktu selesai %v di masa depan", p.Connector, p.FinishedAt)
	case p.Published < 0 || (p.Published > 0 && !p.OK):
		return invalid("%s: jumlah terbit %d untuk percobaan ok=%v", p.Connector, p.Published, p.OK)
	}
	return nil
}

// State adalah keadaan terakhir satu konektor (satu baris ts.source_status).
// Waktu nol berarti belum pernah terjadi.
type State struct {
	Connector string
	Subject   string
	Interval  time.Duration
	// LastAttemptAt adalah akhir percobaan terbaru, berhasil atau gagal.
	LastAttemptAt time.Time
	// LastSuccessAt adalah akhir percobaan berhasil terbaru.
	LastSuccessAt time.Time
	// LastChangeAt adalah akhir percobaan terbaru yang membawa data baru
	// (ada pesan raw yang terbit).
	LastChangeAt time.Time
	// FailingSince adalah akhir percobaan gagal pertama sejak percobaan
	// berhasil terakhir; nol bila percobaan terbaru berhasil.
	FailingSince        time.Time
	ConsecutiveFailures int
}

// Validate memeriksa invarian keadaan.
func (s State) Validate() error {
	if err := validName(s.Connector); err != nil {
		return err
	}
	switch {
	case !subjectFormat.MatchString(s.Subject):
		return invalid("%s: subjek raw %q tidak dikenal", s.Connector, s.Subject)
	case s.Interval < MinInterval || s.Interval > MaxInterval:
		return invalid("%s: interval %v", s.Connector, s.Interval)
	case s.LastAttemptAt.IsZero():
		return invalid("%s: belum pernah dicoba", s.Connector)
	case s.LastSuccessAt.After(s.LastAttemptAt) || s.LastChangeAt.After(s.LastSuccessAt):
		return invalid("%s: urutan waktu coba/berhasil/berubah", s.Connector)
	case !s.LastChangeAt.IsZero() && s.LastSuccessAt.IsZero():
		return invalid("%s: berubah tanpa pernah berhasil", s.Connector)
	case s.ConsecutiveFailures < 0 || (s.ConsecutiveFailures == 0) != s.FailingSince.IsZero():
		return invalid("%s: kegagalan beruntun %d dengan gagal sejak %v", s.Connector, s.ConsecutiveFailures, s.FailingSince)
	case s.ConsecutiveFailures == 0 && !s.LastSuccessAt.Equal(s.LastAttemptAt):
		return invalid("%s: percobaan terbaru berhasil tetapi waktunya berbeda", s.Connector)
	case !s.FailingSince.IsZero() && (s.FailingSince.After(s.LastAttemptAt) || !s.FailingSince.After(s.LastSuccessAt)):
		return invalid("%s: gagal sejak %v di luar (berhasil, coba]", s.Connector, s.FailingSince)
	}
	return nil
}

// Apply melipat hasil polling p ke keadaan sebelumnya (prev nil untuk
// konektor yang belum pernah tercatat). Hasil yang tidak lebih baru dari
// percobaan terakhir diabaikan (applied false): pesan ulangan, atau pesan
// yang datang terlambat setelah percobaan yang lebih baru tercatat.
// p harus sudah lolos Validate.
func Apply(prev *State, p Poll) (next State, applied bool) {
	if prev != nil && !p.FinishedAt.After(prev.LastAttemptAt) {
		return *prev, false
	}
	next = State{Connector: p.Connector, Subject: p.Subject, Interval: p.Interval, LastAttemptAt: p.FinishedAt}
	if prev != nil {
		next.LastSuccessAt, next.LastChangeAt = prev.LastSuccessAt, prev.LastChangeAt
		next.FailingSince, next.ConsecutiveFailures = prev.FailingSince, prev.ConsecutiveFailures
	}
	if p.OK {
		next.LastSuccessAt = p.FinishedAt
		next.FailingSince, next.ConsecutiveFailures = time.Time{}, 0
		if p.Published > 0 {
			next.LastChangeAt = p.FinishedAt
		}
		return next, true
	}
	if next.ConsecutiveFailures == 0 {
		next.FailingSince = p.FinishedAt
	}
	next.ConsecutiveFailures++
	return next, true
}
