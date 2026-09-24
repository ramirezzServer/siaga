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
