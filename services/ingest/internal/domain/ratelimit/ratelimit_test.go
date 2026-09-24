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

func TestReserveLowKeepsHeadroom(t *testing.T) {
	b, _ := New(60, 5)
	// Burst 5, cadangan 2: prioritas rendah hanya boleh memakai 3 izin pertama.
	for i := range 3 {
		if ok, w := b.ReserveLow(t0, 2); !ok || w != 0 {
			t.Fatalf("request rendah %d: ok=%v tunggu=%v", i, ok, w)
		}
	}
	ok, w := b.ReserveLow(t0, 2)
	if ok || w != time.Second {
		t.Fatalf("request rendah keempat harus ditolak dengan jeda 1 dtk, dapat ok=%v tunggu=%v", ok, w)
	}
	// Cadangan tetap utuh untuk request biasa.
	for i := range 2 {
		if w := b.Reserve(t0); w != 0 {
			t.Fatalf("request biasa %d harus langsung jalan, tunggu %v", i, w)
		}
	}
	// Setelah jeda yang disebut, request rendah boleh lagi bila tidak ada pesanan lain.
	b2, _ := New(60, 5)
	for range 3 {
		_, _ = b2.ReserveLow(t0, 2)
	}
	_, w = b2.ReserveLow(t0, 2)
	if ok, _ := b2.ReserveLow(t0.Add(w), 2); !ok {
		t.Fatal("request rendah harus diizinkan setelah jeda retryAfter")
	}
}

func TestReserveLowClampsHeadroom(t *testing.T) {
	b, _ := New(60, 3)
	if b.MaxHeadroom() != 2 {
		t.Fatalf("MaxHeadroom = %d", b.MaxHeadroom())
	}
	// Cadangan berlebihan dipangkas ke burst-1: masih ada satu izin untuk prioritas rendah.
	if ok, _ := b.ReserveLow(t0, 99); !ok {
		t.Fatal("cadangan di atas burst-1 harus dipangkas, bukan menutup jalur rendah")
	}
	if ok, _ := b.ReserveLow(t0, 99); ok {
		t.Fatal("izin rendah kedua melanggar cadangan 2")
	}
	// Cadangan negatif dianggap nol.
	c, _ := New(60, 1)
	if ok, _ := c.ReserveLow(t0, -5); !ok {
		t.Fatal("cadangan negatif harus dianggap nol")
	}
}

// Properti campuran prioritas: (1) batas jendela 60 detik tetap berlaku untuk
// gabungan request biasa dan rendah; (2) setiap kali request rendah diizinkan,
// request biasa yang datang pada saat yang sama langsung jalan (cadangan >= 1);
// (3) request rendah tidak pernah diizinkan dengan jeda.
func FuzzPriorityHeadroom(f *testing.F) {
	f.Add(uint8(55), uint8(5), uint8(2), []byte{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0})
	f.Add(uint8(60), uint8(3), uint8(1), make([]byte, 300))
	f.Add(uint8(1), uint8(1), uint8(0), []byte{255, 255, 1, 3, 3, 0})
	f.Fuzz(func(t *testing.T, per, burst, head uint8, ops []byte) {
		p, bu := int(per%120)+1, int(burst)
		bu = bu%p + 1
		b, err := New(p, bu)
		if err != nil {
			t.Fatal(err)
		}
		h := int(head) % bu // 0..burst-1
		if len(ops) > 3000 {
			ops = ops[:3000]
		}
		now := t0
		var runs []time.Time
		for i := 0; i+2 < len(ops); i += 3 {
			now = now.Add(time.Duration(binary.BigEndian.Uint16(ops[i:i+2])) * time.Millisecond)
			if ops[i+2]%2 == 0 {
				runs = append(runs, now.Add(b.Reserve(now)))
				continue
			}
			before := *b
			ok, wait := b.ReserveLow(now, h)
			if !ok {
				if *b != before {
					t.Fatal("ReserveLow yang ditolak tidak boleh mengubah bucket")
				}
				if wait <= 0 {
					t.Fatalf("penolakan harus menyebut jeda positif, dapat %v", wait)
				}
				continue
			}
			if wait != 0 {
				t.Fatalf("izin rendah dengan jeda %v", wait)
			}
			runs = append(runs, now)
			if h >= 1 {
				probe := *b
				if w := probe.Reserve(now); w != 0 {
					t.Fatalf("request biasa menunggu %v setelah izin rendah (cadangan %d)", w, h)
				}
			}
		}
		limit := p + bu - 1
		for i := range runs {
			n := 0
			for j := range runs {
				if d := runs[j].Sub(runs[i]); d >= 0 && d < time.Minute {
					n++
				}
			}
			if n > limit {
				t.Fatalf("%d request dalam 60 detik sejak %v, batas %d", n, runs[i], limit)
			}
		}
	})
}
