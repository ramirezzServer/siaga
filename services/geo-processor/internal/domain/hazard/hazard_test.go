package hazard

import (
	"errors"
	"testing"
	"time"
)

func TestEventIDRoundTripAndLayout(t *testing.T) {
	id := NewEventID("siaga/hazard/uji", 0, "bmkg", "20260924")
	if id.IsZero() || id[6]>>4 != 8 || id[8]>>6 != 2 {
		t.Fatalf("bukan UUIDv8 RFC 9562: %s", id)
	}
	back, err := ParseEventID(id.String())
	if err != nil || back != id || back.Compare(id) != 0 {
		t.Fatalf("%s → %s, %v", id, back, err)
	}
	if NewEventID("siaga/hazard/uji", 0, "ab", "c") == NewEventID("siaga/hazard/uji", 0, "a", "bc") {
		t.Fatal("pembatas bagian tidak bekerja")
	}
	if NewEventID("siaga/hazard/uji", 1, "x") == NewEventID("siaga/hazard/uji", 0, "x") || NewEventID("a", 0, "x") == NewEventID("b", 0, "x") {
		t.Fatal("generasi atau ruang nama tidak membedakan ID")
	}
	if (EventID{}).Compare(id) >= 0 || !(EventID{}).IsZero() {
		t.Fatal("urutan ID nol")
	}
	for _, bad := range []string{"", "0192-", "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz", "01234567x89ab-cdef-0123-456789abcdef"} {
		if _, err := ParseEventID(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("ParseEventID(%q) = %v", bad, err)
		}
	}
}

func TestLevelString(t *testing.T) {
	for l, s := range map[Level]string{LevelInfo: "info", LevelWaspada: "waspada", LevelSiaga: "siaga", LevelBahaya: "bahaya", 9: "Level(9)"} {
		if l.String() != s {
			t.Errorf("%d → %s", int(l), l)
		}
	}
}

func TestDigesterSeparatesFields(t *testing.T) {
	sum := func(fn func(d *Digester)) [32]byte {
		d := NewDigester("uji")
		fn(d)
		return d.Sum()
	}
	a := sum(func(d *Digester) { d.Str("ab"); d.Str("c") })
	b := sum(func(d *Digester) { d.Str("a"); d.Str("bc") })
	if a == b {
		t.Fatal("teks tidak dipisah")
	}
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	if sum(func(d *Digester) { d.Time(time.Time{}) }) == sum(func(d *Digester) { d.Time(at) }) ||
		sum(func(d *Digester) { d.Bool(true) }) == sum(func(d *Digester) { d.Bool(false) }) ||
		sum(func(d *Digester) { d.F64(0.1) }) == sum(func(d *Digester) { d.F64(0.2) }) {
		t.Fatal("nilai berbeda menghasilkan hash sama")
	}
	five := func(d *Digester) { d.Int(5) }
	if first, second := sum(five), sum(five); first != second {
		t.Fatal("hash tidak deterministik")
	}
}
