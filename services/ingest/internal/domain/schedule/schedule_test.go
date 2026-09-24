package schedule

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	const s = time.Second
	cases := []struct {
		name                       string
		interval                   time.Duration
		failures                   int
		maxBackoff, retryAfter, ok time.Duration
	}{
		{"sukses", 30 * s, 0, 10 * time.Minute, 0, 30 * s},
		{"gagal sekali", 30 * s, 1, 10 * time.Minute, 0, 60 * s},
		{"gagal tiga kali", 30 * s, 3, 10 * time.Minute, 0, 240 * s},
		{"dibatasi maxBackoff", 30 * s, 20, 10 * time.Minute, 0, 10 * time.Minute},
		{"overflow tidak terjadi", 30 * s, 1_000_000, 10 * time.Minute, 0, 10 * time.Minute},
		{"maxBackoff lebih kecil dari interval", time.Hour, 2, time.Minute, 0, time.Hour},
		{"Retry-After lebih lama", 30 * s, 1, 10 * time.Minute, 5 * time.Minute, 5 * time.Minute},
		{"Retry-After lebih singkat diabaikan", 30 * s, 2, 10 * time.Minute, 10 * s, 120 * s},
		{"Retry-After ekstrem dibatasi", 30 * s, 0, 10 * time.Minute, 48 * time.Hour, MaxRetryAfter},
	}
	for _, c := range cases {
		if got := Next(c.interval, c.failures, c.maxBackoff, c.retryAfter); got != c.ok {
			t.Errorf("%s: Next = %v, ingin %v", c.name, got, c.ok)
		}
	}
}

func TestJitter(t *testing.T) {
	d := 100 * time.Second
	if got := Jitter(d, 0.1, 0); got != 90*time.Second {
		t.Errorf("u=0: %v", got)
	}
	if got := Jitter(d, 0.1, 0.5); got != d {
		t.Errorf("u=0.5: %v", got)
	}
	if got := Jitter(d, 0.9, 1); got != 150*time.Second {
		t.Errorf("frac dibatasi 0.5: %v", got)
	}
	if got := Jitter(d, -1, 0.99); got != d {
		t.Errorf("frac negatif jadi 0: %v", got)
	}
	if got := Jitter(d, 0.1, 7); got != 110*time.Second {
		t.Errorf("u dibatasi 1: %v", got)
	}
}

// Properti: jeda tidak pernah di bawah interval dan tidak pernah di atas
// max(interval, maxBackoff, MaxRetryAfter).
func FuzzNextBounds(f *testing.F) {
	f.Add(int64(30e9), 3, int64(600e9), int64(0))
	f.Fuzz(func(t *testing.T, interval int64, failures int, maxBackoff, retryAfter int64) {
		iv := time.Duration(interval%int64(24*time.Hour)+int64(24*time.Hour)) % (24 * time.Hour)
		iv = max(iv, time.Millisecond)
		mb := max(time.Duration(maxBackoff)%(24*time.Hour), 0)
		ra := time.Duration(retryAfter)
		got := Next(iv, failures, mb, ra)
		if got < iv {
			t.Fatalf("Next(%v, %d, %v, %v) = %v di bawah interval", iv, failures, mb, ra, got)
		}
		if upper := max(iv, mb, MaxRetryAfter); got > upper {
			t.Fatalf("Next(%v, %d, %v, %v) = %v di atas %v", iv, failures, mb, ra, got, upper)
		}
	})
}
