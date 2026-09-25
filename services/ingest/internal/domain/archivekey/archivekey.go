// Package archivekey adalah format baku kunci arsip payload mentah:
//
//	<konektor>/<YYYY>/<MM>/<DD>/<hhmmss>Z-<sha256[:12]>.<ext>.gz
//
// Waktu adalah saat payload diambil (UTC, presisi detik). Karena komponen
// waktu tertulis dengan lebar tetap, urutan leksikografis kunci satu konektor
// sama dengan urutan waktu pengambilan; sifat ini dipakai replay dan
// penyalinan arsip. Kunci yang sama di filesystem lokal dan di object storage.
package archivekey

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SumLen adalah panjang potongan SHA-256 di nama file.
const SumLen = 12

// ErrInvalid menandai kunci yang tidak mengikuti format baku.
var ErrInvalid = errors.New("kunci arsip tidak sesuai format")

// Key adalah isi satu kunci arsip.
type Key struct {
	Connector string
	// FetchedAt dalam UTC, presisi detik.
	FetchedAt time.Time
	// Sum adalah awal SHA-256 heksadesimal payload sebelum dikompres.
	Sum string
	// Ext adalah ekstensi payload tanpa titik dan tanpa ".gz", misal "json".
	Ext string
}

var (
	connectorRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	extRe       = regexp.MustCompile(`^[a-z0-9]+$`)
	sumRe       = regexp.MustCompile(`^[0-9a-f]+$`)
)

// Build membentuk kunci dari konektor, ekstensi, SHA-256 payload (heksadesimal
// penuh atau potongannya), dan waktu pengambilan.
func Build(connector, ext, sum string, at time.Time) string {
	return fmt.Sprintf("%s/%s-%s.%s.gz", connector, at.UTC().Format("2006/01/02/150405Z"), sum[:min(SumLen, len(sum))], ext)
}

// String sama dengan Build untuk isi k.
func (k Key) String() string { return Build(k.Connector, k.Ext, k.Sum, k.FetchedAt) }

// Parse membaca kunci yang dibentuk Build. Konektor boleh berisi titik dan
// tanda hubung, tetapi tidak garis miring.
func Parse(key string) (Key, error) {
	fail := func(why string) (Key, error) { return Key{}, fmt.Errorf("%w: %q (%s)", ErrInvalid, key, why) }
	parts := strings.Split(key, "/")
	if len(parts) != 5 {
		return fail("harus lima bagian dipisah /")
	}
	conn, file := parts[0], parts[4]
	if !connectorRe.MatchString(conn) {
		return fail("nama konektor")
	}
	name, ok := strings.CutSuffix(file, ".gz")
	if !ok {
		return fail("harus berakhiran .gz")
	}
	stamp, rest, ok := strings.Cut(name, "Z-")
	if !ok || len(stamp) != len("150405") {
		return fail("jam hhmmssZ")
	}
	sum, ext, ok := strings.Cut(rest, ".")
	if !ok || len(sum) == 0 || len(sum) > SumLen || !sumRe.MatchString(sum) || !extRe.MatchString(ext) {
		return fail("potongan SHA-256 atau ekstensi")
	}
	at, err := time.Parse("2006/01/02/150405", strings.Join([]string{parts[1], parts[2], parts[3], stamp}, "/"))
	if err != nil {
		return fail("tanggal atau jam")
	}
	k := Key{Connector: conn, FetchedAt: at, Sum: sum, Ext: ext}
	// Menolak bentuk yang lolos time.Parse tetapi tidak kanonik (misal
	// digit tambahan), supaya satu payload tidak punya dua kunci.
	if k.String() != key {
		return fail("bukan bentuk kanonik")
	}
	return k, nil
}

// DayPrefix adalah awalan semua kunci satu konektor pada tanggal UTC t,
// misal "bmkg-autogempa/2026/09/24/".
func DayPrefix(connector string, t time.Time) string {
	return connector + "/" + t.UTC().Format("2006/01/02") + "/"
}

// Lower adalah batas bawah leksikografis kunci konektor yang diambil pada
// atau setelah t. Semua kunci dengan FetchedAt >= t (presisi detik) lebih besar
// dari Lower(t).
func Lower(connector string, t time.Time) string {
	// "-" < angka, jadi "<jam>Z-" + apa pun > "<jam>Z" dan kunci detik
	// sebelumnya ("<jam-1>Z-...") < "<jam>Z".
	return connector + "/" + t.UTC().Truncate(time.Second).Format("2006/01/02/150405Z")
}

// Matches melaporkan apakah SHA-256 penuh payload cocok dengan potongan di kunci.
func (k Key) Matches(fullSum string) bool {
	return k.Sum != "" && strings.HasPrefix(fullSum, k.Sum)
}
