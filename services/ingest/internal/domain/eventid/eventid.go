// Package eventid membentuk ID pesan deterministik untuk deduplikasi JetStream
// (header Nats-Msg-Id).
package eventid

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// MsgID mengembalikan 32 karakter heksadesimal (128 bit) dari hash konektor,
// kunci record di sumber, dan isi record tanpa metadata pengambilan.
// Isi yang sama menghasilkan ID yang sama, jadi pesan ulang ditolak JetStream;
// revisi dari sumber (misal magnitudo diperbarui) menghasilkan ID baru.
func MsgID(connector, key string, content []byte) string {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(connector), []byte(key), content} {
		// Panjang ditulis lebih dulu supaya ("ab","c") dan ("a","bc") berbeda.
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		h.Write(n[:])
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}
