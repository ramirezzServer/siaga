// Package envx membaca konfigurasi dari environment variable (prinsip 12-factor).
// Fungsi menerima lookup agar bisa diuji tanpa mengubah environment proses.
package envx

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Lookup adalah tanda tangan os.LookupEnv.
type Lookup func(key string) (string, bool)

// OS memakai environment proses.
var OS Lookup = os.LookupEnv

// Reader mengumpulkan semua kesalahan konfigurasi sekaligus, supaya operator
// melihat seluruh variabel yang kurang dalam satu kali jalan.
type Reader struct {
	lookup Lookup
	errs   []error
}

// NewReader membuat Reader di atas fungsi lookup.
func NewReader(lookup Lookup) *Reader { return &Reader{lookup: lookup} }

// Required mengembalikan nilai variabel; kosong atau tidak ada dicatat sebagai kesalahan.
func (r *Reader) Required(key string) string {
	v, ok := r.lookup(key)
	if !ok || strings.TrimSpace(v) == "" {
		r.errs = append(r.errs, fmt.Errorf("variabel %s wajib diisi", key))
		return ""
	}
	return v
}

// Default mengembalikan nilai variabel, atau fallback bila tidak ada atau kosong.
func (r *Reader) Default(key, fallback string) string {
	if v, ok := r.lookup(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// Err mengembalikan gabungan semua kesalahan, atau nil.
func (r *Reader) Err() error { return errors.Join(r.errs...) }
