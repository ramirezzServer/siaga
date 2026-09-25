// Package ratelimit membatasi laju request ke sumber data dengan algoritma
// GCRA (generic cell rate algorithm), setara token bucket tetapi memakai
// aritmetika bilangan bulat sehingga batasnya bisa dibuktikan persis.
package ratelimit

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidLimit menandai konfigurasi batas yang tidak masuk akal.
var ErrInvalidLimit = errors.New("batas laju tidak valid")

// Bucket membatasi laju ke PerMinute request per menit dengan lonjakan awal
// sebesar Burst. Jaminannya: dalam jendela 60 detik mana pun, jumlah request
// yang dijalankan tepat pada waktu izinnya tidak melebihi PerMinute + Burst - 1.
// Tidak aman dipakai bersamaan dari beberapa goroutine; pembungkusnya ada di app.
type Bucket struct {
	interval  time.Duration // jarak antar-request pada laju tetap
	tolerance time.Duration // kelonggaran lonjakan: (Burst-1) * interval
	tat       time.Time     // theoretical arrival time
	perMinute int
	burst     int
}

// New membuat Bucket. Interval dibulatkan ke atas supaya laju efektif tidak
// pernah melebihi perMinute.
func New(perMinute, burst int) (*Bucket, error) {
	if perMinute < 1 || perMinute > 60_000 {
		return nil, fmt.Errorf("%w: %d per menit harus 1..60000", ErrInvalidLimit, perMinute)
	}
	if burst < 1 || burst > perMinute {
		return nil, fmt.Errorf("%w: burst %d harus 1..%d", ErrInvalidLimit, burst, perMinute)
	}
	interval := (time.Minute + time.Duration(perMinute) - 1) / time.Duration(perMinute)
	return &Bucket{
		interval:  interval,
		tolerance: time.Duration(burst-1) * interval,
		perMinute: perMinute,
		burst:     burst,
	}, nil
}

// PerMinute mengembalikan laju yang dikonfigurasi.
func (b *Bucket) PerMinute() int { return b.perMinute }

// Burst mengembalikan lonjakan yang dikonfigurasi.
func (b *Bucket) Burst() int { return b.burst }

// Reserve memesan satu request pada waktu now dan mengembalikan berapa lama
// pemanggil harus menunggu sebelum menjalankannya. Pesanan tidak bisa dibatalkan;
// pemanggil yang batal menunggu hanya membuat bucket lebih konservatif.
func (b *Bucket) Reserve(now time.Time) time.Duration {
	if b.tat.Before(now) {
		b.tat = now
	}
	allowAt := b.tat.Add(-b.tolerance)
	b.tat = b.tat.Add(b.interval)
	if wait := allowAt.Sub(now); wait > 0 {
		return wait
	}
	return 0
}

// Available mengembalikan jumlah request yang bisa dipesan pada now tanpa
// menunggu (0..Burst), tanpa memesan apa pun. Dipakai untuk metrik sisa
// anggaran; Reserve tetap satu-satunya cara memperoleh izin.
func (b *Bucket) Available(now time.Time) int {
	tat := b.tat
	if tat.Before(now) {
		tat = now
	}
	// Request ke-k (k>=1) boleh jalan bila tat + (k-1)*interval - tolerance <= now.
	slack := now.Sub(tat) + b.tolerance
	if slack < 0 {
		return 0
	}
	return min(int(slack/b.interval)+1, b.burst)
}

// MaxHeadroom adalah cadangan terbesar yang bisa diminta ReserveLow: satu
// kurang dari Burst, supaya request prioritas rendah tetap mungkin jalan.
func (b *Bucket) MaxHeadroom() int { return b.burst - 1 }

// ReserveLow memesan satu request prioritas rendah pada waktu now, tetapi
// hanya bila request itu bisa jalan sekarang dan setelahnya masih tersisa
// headroom izin langsung untuk request biasa (Reserve). Bila syarat itu tidak
// terpenuhi, tidak ada yang dipesan dan retryAfter adalah jeda paling cepat
// syarat itu bisa terpenuhi, dengan anggapan tidak ada pesanan lain.
//
// Dengan begitu lalu lintas prioritas rendah (misal sapuan prakiraan) hanya
// memakai sisa anggaran dan tidak pernah membuat request biasa (misal gempa)
// menunggu di belakangnya. headroom dibatasi ke 0..MaxHeadroom.
func (b *Bucket) ReserveLow(now time.Time, headroom int) (ok bool, retryAfter time.Duration) {
	headroom = min(max(headroom, 0), b.MaxHeadroom())
	tat := b.tat
	if tat.Before(now) {
		tat = now
	}
	// Request ke-(headroom+1) yang dipesan sekarang boleh jalan pada allowAt;
	// bila itu belum sekarang, sisa izin langsung kurang dari headroom+1.
	allowAt := tat.Add(time.Duration(headroom)*b.interval - b.tolerance)
	if wait := allowAt.Sub(now); wait > 0 {
		return false, wait
	}
	b.tat = tat.Add(b.interval)
	return true, 0
}
