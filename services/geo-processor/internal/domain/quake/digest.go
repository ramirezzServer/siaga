package quake

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"time"
)

// digester menyusun hash SHA-256 dari field bertipe dengan pembatas panjang,
// sehingga dua isi berbeda tidak pernah menghasilkan urutan byte yang sama.
type digester struct {
	h   hash.Hash
	buf []byte
}

func newDigester(domain string) *digester {
	d := &digester{h: sha256.New()}
	d.str(domain)
	return d
}

func (d *digester) int(v int64) {
	d.buf = binary.AppendVarint(d.buf[:0], v)
	d.h.Write(d.buf)
}

func (d *digester) str(s string) {
	d.int(int64(len(s)))
	d.h.Write([]byte(s))
}

func (d *digester) f64(f float64) {
	d.buf = binary.BigEndian.AppendUint64(d.buf[:0], math.Float64bits(f))
	d.h.Write(d.buf)
}

func (d *digester) time(t time.Time) {
	if t.IsZero() {
		d.int(0)
		return
	}
	d.int(1)
	d.int(t.UnixNano())
}

func (d *digester) sum() [32]byte {
	var out [32]byte
	copy(out[:], d.h.Sum(nil))
	return out
}
