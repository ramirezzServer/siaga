// Package retention adalah aturan berapa lama payload mentah satu konektor
// disimpan di arsip panas (Garage) sebelum boleh dihapus (ADR 0022).
//
// Satuannya hari kalender UTC, sama dengan komponen tanggal kunci arsip:
// objek bertanggal D kedaluwarsa bila D lebih awal dari hari ini dikurangi
// jumlah hari aturannya. Konektor tanpa aturan disimpan selamanya, jadi
// konektor baru tidak pernah terhapus sebelum diputuskan. Kedaluwarsa hanya
// berarti boleh dihapus; penghapusan sendiri masih mensyaratkan objek sudah
// ada di cadangan yang terverifikasi (app/archivebackup).
package retention

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MinDays adalah batas bawah aturan: lebih panjang dari retensi stream RAW
// JetStream (7 hari), supaya payload yang pesannya masih bisa diputar ulang
// dari NATS juga masih ada di arsip panas.
const MinDays = 8

// ErrInvalid menandai spesifikasi aturan yang tidak bisa dipakai.
var ErrInvalid = errors.New("aturan retensi tidak valid")

var connectorRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Policy memetakan konektor ke jumlah hari simpan. Konektor yang tidak ada
// di peta disimpan selamanya.
type Policy struct {
	days map[string]int
}

// Default adalah aturan yang diputuskan di ADR 0022 dari ukuran arsip
// 25 Sep–2 Okt 2026: prakiraan BMKG (±94% objek, ±72% byte) dan feed yang
// isinya berulang tiap polling dibatasi; gempa, CAP, FIRMS, dan OpenAQ
// (kecil, dibutuhkan replay T3/T4 dan kalibrasi) disimpan selamanya.
func Default() Policy {
	return Policy{days: map[string]int{
		"bmkg-prakiraan":   14,
		"usgs-2.5-day":     30,
		"openmeteo-cuaca":  30,
		"openmeteo-udara":  30,
		"openmeteo-sungai": 90,
		"openmeteo-hujan":  90,
	}}
}

// Parse membaca "konektor=hari,konektor=hari". String kosong berarti tanpa
// aturan (semua disimpan selamanya).
func Parse(spec string) (Policy, error) {
	p := Policy{days: map[string]int{}}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return p, nil
	}
	for item := range strings.SplitSeq(spec, ",") {
		name, val, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			return Policy{}, fmt.Errorf("%w: %q harus berbentuk konektor=hari", ErrInvalid, item)
		}
		name = strings.TrimSpace(name)
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), "d")))
		if err != nil {
			return Policy{}, fmt.Errorf("%w: jumlah hari %q", ErrInvalid, val)
		}
		if _, dup := p.days[name]; dup {
			return Policy{}, fmt.Errorf("%w: konektor %q disebut dua kali", ErrInvalid, name)
		}
		p.days[name] = n
	}
	return p, p.Validate()
}

// Validate memastikan nama konektor baku dan setiap aturan >= MinDays.
func (p Policy) Validate() error {
	var errs []error
	for _, name := range p.Connectors() {
		if !connectorRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("%w: nama konektor %q", ErrInvalid, name))
		}
		if d := p.days[name]; d < MinDays {
			errs = append(errs, fmt.Errorf("%w: %s %d hari, minimal %d", ErrInvalid, name, d, MinDays))
		}
	}
	return errors.Join(errs...)
}

// Days mengembalikan jumlah hari simpan konektor; ok false berarti selamanya.
func (p Policy) Days(connector string) (days int, ok bool) {
	days, ok = p.days[connector]
	return days, ok
}

// Connectors mengembalikan konektor yang punya aturan, urut nama.
func (p Policy) Connectors() []string {
	return slices.Sorted(maps.Keys(p.days))
}

// String adalah bentuk yang bisa dibaca kembali oleh Parse.
func (p Policy) String() string {
	parts := make([]string, 0, len(p.days))
	for _, name := range p.Connectors() {
		parts = append(parts, fmt.Sprintf("%s=%d", name, p.days[name]))
	}
	return strings.Join(parts, ",")
}

// Cutoff adalah hari UTC pertama yang masih disimpan untuk konektor pada
// waktu now; objek bertanggal sebelum Cutoff kedaluwarsa. ok false berarti
// konektor disimpan selamanya.
func (p Policy) Cutoff(connector string, now time.Time) (cutoff time.Time, ok bool) {
	days, ok := p.days[connector]
	if !ok {
		return time.Time{}, false
	}
	// Aturan yang lolos Validate tidak pernah di bawah MinDays; batas ini
	// menjaga Policy yang dibentuk tanpa Validate.
	days = max(days, MinDays)
	return Day(now).AddDate(0, 0, -days), true
}

// Expired melaporkan apakah objek konektor bertanggal day boleh dihapus pada
// waktu now.
func (p Policy) Expired(connector string, day, now time.Time) bool {
	cutoff, ok := p.Cutoff(connector, now)
	return ok && Day(day).Before(cutoff)
}

// Day adalah awal hari UTC dari t.
func Day(t time.Time) time.Time {
	return t.UTC().Truncate(24 * time.Hour)
}
