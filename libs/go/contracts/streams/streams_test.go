package streams

import (
	"strings"
	"testing"
)

func TestRawSubject(t *testing.T) {
	got, err := RawSubject(KindQuake, SourceBMKG)
	if err != nil || got != "raw.quake.bmkg" {
		t.Fatalf("RawSubject = %q, %v", got, err)
	}
	for _, bad := range []struct {
		k Kind
		s Source
	}{{"", SourceBMKG}, {KindQuake, ""}, {"Quake", SourceBMKG}, {KindQuake, "bmkg.x"}, {KindQuake, "bm kg"}, {"*", SourceBMKG}, {KindQuake, ">"}} {
		if _, err := RawSubject(bad.k, bad.s); err == nil {
			t.Errorf("RawSubject(%q, %q) seharusnya gagal", bad.k, bad.s)
		}
	}
}

func TestHazardSubject(t *testing.T) {
	got, err := HazardSubject(KindAQ, Expired)
	if err != nil || got != "hazard.aq.expired" {
		t.Fatalf("HazardSubject = %q, %v", got, err)
	}
	if _, err := HazardSubject(KindQuake, "deleted"); err == nil {
		t.Error("transisi tidak dikenal seharusnya ditolak")
	}
	if _, err := HazardSubject("quake.x", Created); err == nil {
		t.Error("jenis dengan titik seharusnya ditolak")
	}
}

// Invarian yang juga ditegakkan server JetStream; dicek di sini supaya
// kesalahan ketahuan saat test, bukan saat layanan start.
func TestSpecsValid(t *testing.T) {
	names := map[string]bool{}
	for _, s := range []Spec{Raw, Hazard} {
		if names[s.Name] {
			t.Errorf("nama stream ganda %s", s.Name)
		}
		names[s.Name] = true
		if s.DuplicateWindow <= 0 || s.DuplicateWindow > s.MaxAge {
			t.Errorf("%s: DuplicateWindow %v harus di (0, MaxAge %v]", s.Name, s.DuplicateWindow, s.MaxAge)
		}
		if s.MaxBytes <= 0 || s.Owner == "" || len(s.Subjects) == 0 {
			t.Errorf("%s: MaxBytes, Owner, dan Subjects wajib diisi", s.Name)
		}
		for _, sub := range s.Subjects {
			if !strings.HasSuffix(sub, ".>") {
				t.Errorf("%s: subjek %q seharusnya berupa prefix wildcard", s.Name, sub)
			}
		}
	}
}
