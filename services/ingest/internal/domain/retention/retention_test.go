package retention

import (
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDefault(t *testing.T) {
	p := Default()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if d, ok := p.Days("bmkg-prakiraan"); !ok || d != 14 {
		t.Fatalf("prakiraan %d %v", d, ok)
	}
	for _, c := range []string{"bmkg-autogempa", "bmkg-cap", "firms-viirs-snpp-nrt", "openaq-stasiun", "konektor-baru"} {
		if _, ok := p.Days(c); ok {
			t.Errorf("%s harus disimpan selamanya", c)
		}
	}
	back, err := Parse(p.String())
	if err != nil || back.String() != p.String() {
		t.Fatalf("Parse(String) = %v, %v", back, err)
	}
}

func TestParse(t *testing.T) {
	p, err := Parse(" usgs-2.5-day = 30d , bmkg-prakiraan=8")
	if err != nil {
		t.Fatal(err)
	}
	if p.String() != "bmkg-prakiraan=8,usgs-2.5-day=30" {
		t.Fatal(p.String())
	}
	if p, err := Parse("  "); err != nil || len(p.Connectors()) != 0 {
		t.Fatalf("kosong: %v %v", p, err)
	}
	for _, bad := range []string{"x", "x=", "x=abc", "x=7", "x=-3", "X=30", "a=30,a=40", "a/b=30", "=30"} {
		if _, err := Parse(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %v, ingin ErrInvalid", bad, err)
		}
	}
}

func TestCutoffAndExpired(t *testing.T) {
	p, err := Parse("c=14")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 0, 30, 0, 0, time.FixedZone("WIB", 7*3600)) // 15 Okt 17.30 UTC
	cutoff, ok := p.Cutoff("c", now)
	if !ok || !cutoff.Equal(day("2026-10-01")) {
		t.Fatalf("cutoff %v %v", cutoff, ok)
	}
	cases := map[string]bool{"2026-09-30": true, "2026-10-01": false, "2026-10-15": false}
	for d, want := range cases {
		// Jam di dalam hari tidak berpengaruh.
		if got := p.Expired("c", day(d).Add(23*time.Hour+59*time.Minute), now); got != want {
			t.Errorf("Expired(%s) = %v, ingin %v", d, got, want)
		}
	}
	if p.Expired("lain", day("2000-01-01"), now) {
		t.Fatal("konektor tanpa aturan tidak boleh kedaluwarsa")
	}
	if _, ok := p.Cutoff("lain", now); ok {
		t.Fatal("cutoff konektor tanpa aturan")
	}
	// Policy yang dibentuk tanpa Validate tetap tidak memangkas di bawah MinDays.
	raw := Policy{days: map[string]int{"c": 1}}
	if raw.Expired("c", now.AddDate(0, 0, -MinDays+1), now) {
		t.Fatal("MinDays dilanggar")
	}
}

// FuzzExpiredMonotone: bila hari d kedaluwarsa, setiap hari sebelumnya juga,
// dan hari dalam MinDays terakhir tidak pernah kedaluwarsa.
func FuzzExpiredMonotone(f *testing.F) {
	f.Add(int64(1790900000), uint16(14), uint16(3), uint16(20))
	f.Add(int64(0), uint16(0), uint16(0), uint16(0))
	f.Fuzz(func(t *testing.T, nowSec int64, keep, back, earlier uint16) {
		now := time.Unix(nowSec%(1<<40), 0).UTC()
		p := Policy{days: map[string]int{"c": int(keep)}}
		d := now.AddDate(0, 0, -int(back))
		if p.Expired("c", d, now) {
			if !p.Expired("c", d.AddDate(0, 0, -int(earlier)), now) {
				t.Fatalf("tidak monoton: keep=%d back=%d earlier=%d", keep, back, earlier)
			}
			if int(back) < MinDays {
				t.Fatalf("hari ke-%d dari now kedaluwarsa (MinDays %d)", back, MinDays)
			}
		}
	})
}
