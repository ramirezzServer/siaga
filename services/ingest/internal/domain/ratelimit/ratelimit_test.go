package ratelimit

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

func TestNewRejectsInvalid(t *testing.T) {
	for _, c := range []struct{ per, burst int }{{0, 1}, {-1, 1}, {60_001, 1}, {10, 0}, {10, 11}} {
		if _, err := New(c.per, c.burst); !errors.Is(err, ErrInvalidLimit) {
			t.Errorf("New(%d, %d) err = %v, ingin ErrInvalidLimit", c.per, c.burst, err)
		}
	}
	b, err := New(55, 5)
	if err != nil || b.PerMinute() != 55 || b.Burst() != 5 {
		t.Fatalf("New(55, 5) = %+v, %v", b, err)
	}
}

func TestBurstThenSteadyRate(t *testing.T) {
	b, _ := New(60, 3)
	for i := range 3 {
		if w := b.Reserve(t0); w != 0 {
			t.Fatalf("request %d dalam burst harus langsung jalan, tunggu %v", i, w)
		}
	}
	if w := b.Reserve(t0); w != time.Second {
		t.Fatalf("request keempat harus menunggu 1 detik, dapat %v", w)
	}
	if w := b.Reserve(t0); w != 2*time.Second {
		t.Fatalf("request kelima harus menunggu 2 detik, dapat %v", w)
	}
	// Setelah lama diam, burst pulih penuh tetapi tidak lebih.
	later := t0.Add(time.Hour)
	for i := range 3 {
		if w := b.Reserve(later); w != 0 {
			t.Fatalf("burst setelah diam, request %d menunggu %v", i, w)
		}
	}
	if w := b.Reserve(later); w == 0 {
		t.Fatal("burst tidak boleh menumpuk melebihi batas")
	}
}

func TestClockGoingBackwardsIsConservative(t *testing.T) {
	b, _ := New(60, 1)
	_ = b.Reserve(t0)
	if w := b.Reserve(t0.Add(-time.Minute)); w <= 0 {
		t.Fatalf("jam mundur tidak boleh memberi izin gratis, tunggu %v", w)
	}
}

func TestIntervalRoundsUp(t *testing.T) {
	b, _ := New(7, 1) // 60s/7 tidak habis dibagi
	if b.interval*7 < time.Minute {
		t.Fatalf("interval %v membuat laju melebihi 7/menit", b.interval)
	}
}

// Properti: dalam jendela 60 detik mana pun, request yang dijalankan tepat pada
// waktu izinnya tidak melebihi PerMinute + Burst - 1, apa pun pola kedatangannya.
func FuzzWindowBound(f *testing.F) {
	f.Add(uint8(55), uint8(5), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(uint8(1), uint8(1), []byte{255, 1, 2, 3})
	f.Add(uint8(60), uint8(60), make([]byte, 400))
	f.Fuzz(func(t *testing.T, per, burst uint8, gaps []byte) {
		p, bu := int(per%120)+1, int(burst)
		bu = bu%p + 1
		b, err := New(p, bu)
		if err != nil {
			t.Fatal(err)
		}
		if len(gaps) > 2000 {
			gaps = gaps[:2000]
		}
		now := t0
		var runs []time.Time
		for i := 0; i+1 < len(gaps); i += 2 {
			// Jeda kedatangan 0..65535 ms, termasuk banyak request bersamaan.
			now = now.Add(time.Duration(binary.BigEndian.Uint16(gaps[i:i+2])) * time.Millisecond)
			runs = append(runs, now.Add(b.Reserve(now)))
		}
		limit := p + bu - 1
		for i := range runs {
			n := 0
			for j := i; j < len(runs) && runs[j].Sub(runs[i]) < time.Minute; j++ {
				n++
			}
			if n > limit {
				t.Fatalf("%d request dalam 60 detik sejak %v, batas %d (per=%d burst=%d)", n, runs[i], limit, p, bu)
			}
		}
	})
}
