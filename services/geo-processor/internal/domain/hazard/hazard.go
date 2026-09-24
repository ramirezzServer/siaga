// Package hazard berisi konsep domain yang sama untuk semua jenis bahaya:
// ID kejadian deterministik, tingkat peringatan, dan hash isi yang diterbitkan.
package hazard

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"slices"
	"strconv"
	"time"
)

// ErrInvalidID menandai teks yang bukan ID kejadian.
var ErrInvalidID = errors.New("ID kejadian tidak valid")

// EventID adalah ID kejadian bahaya: UUID versi 8 (RFC 9562) dari hash
// SHA-256 atas ruang nama jenis bahaya dan identitas pendiri kejadian.
// Memproses ulang data yang sama menghasilkan ID yang sama.
type EventID [16]byte

// NewEventID membentuk ID dari ruang nama (misal "siaga/hazard/quake") dan
// bagian identitas. Setiap bagian didahului byte nol, jadi ("ab","c") dan
// ("a","bc") menghasilkan ID berbeda. generation > 0 ditambahkan sebagai
// bagian terakhir bila ID generasi sebelumnya sudah terpakai.
func NewEventID(namespace string, generation int, parts ...string) EventID {
	h := sha256.New()
	h.Write([]byte(namespace))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	if generation > 0 {
		h.Write([]byte{0})
		h.Write([]byte(strconv.Itoa(generation)))
	}
	var id EventID
	copy(id[:], h.Sum(nil))
	id[6] = id[6]&0x0f | 0x80 // versi 8
	id[8] = id[8]&0x3f | 0x80 // varian RFC 9562
	return id
}

// IsZero melaporkan apakah ID kosong.
func (id EventID) IsZero() bool { return id == EventID{} }

// Compare mengurutkan ID secara byte.
func (id EventID) Compare(o EventID) int { return slices.Compare(id[:], o[:]) }

// String memformat ID sebagai UUID huruf kecil.
func (id EventID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], id[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], id[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], id[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], id[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], id[10:16])
	return string(b[:])
}

// ParseEventID membaca UUID berformat 8-4-4-4-12.
func ParseEventID(s string) (EventID, error) {
	var id EventID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return id, fmt.Errorf("%w: %q bukan UUID", ErrInvalidID, s)
	}
	raw := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(id[:], []byte(raw)); err != nil {
		return EventID{}, fmt.Errorf("%w: %q bukan UUID: %w", ErrInvalidID, s, err)
	}
	return id, nil
}

// Level adalah tingkat peringatan, sama urutan dan nilainya dengan
// siaga.hazard.v1.AlertLevel dan constraint database.
type Level int

// Tingkat peringatan dari PRD, urut dari paling ringan.
const (
	LevelInfo    Level = 1
	LevelWaspada Level = 2
	LevelSiaga   Level = 3
	LevelBahaya  Level = 4
)

func (l Level) String() string {
	switch l {
	case LevelInfo:
		return "info"
	case LevelWaspada:
		return "waspada"
	case LevelSiaga:
		return "siaga"
	case LevelBahaya:
		return "bahaya"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// Digester menyusun hash SHA-256 dari field bertipe dengan pembatas panjang,
// sehingga dua isi berbeda tidak pernah menghasilkan urutan byte yang sama.
type Digester struct {
	h   hash.Hash
	buf []byte
}

// NewDigester memulai hash dengan ruang nama, misal "siaga/quake-assessment/v1".
func NewDigester(domain string) *Digester {
	d := &Digester{h: sha256.New()}
	d.Str(domain)
	return d
}

// Int menambahkan bilangan bulat.
func (d *Digester) Int(v int64) {
	d.buf = binary.AppendVarint(d.buf[:0], v)
	d.h.Write(d.buf)
}

// Bool menambahkan nilai benar/salah.
func (d *Digester) Bool(v bool) {
	if v {
		d.Int(1)
	} else {
		d.Int(0)
	}
}

// Str menambahkan teks.
func (d *Digester) Str(s string) {
	d.Int(int64(len(s)))
	d.h.Write([]byte(s))
}

// F64 menambahkan bilangan pecahan apa adanya (bit IEEE 754).
func (d *Digester) F64(f float64) {
	d.buf = binary.BigEndian.AppendUint64(d.buf[:0], math.Float64bits(f))
	d.h.Write(d.buf)
}

// Time menambahkan waktu dengan presisi nanodetik; waktu nol dibedakan.
func (d *Digester) Time(t time.Time) {
	if t.IsZero() {
		d.Int(0)
		return
	}
	d.Int(1)
	d.Int(t.UnixNano())
}

// Sum mengembalikan hash.
func (d *Digester) Sum() [32]byte {
	var out [32]byte
	copy(out[:], d.h.Sum(nil))
	return out
}
