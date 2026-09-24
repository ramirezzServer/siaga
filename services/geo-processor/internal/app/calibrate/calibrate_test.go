package calibrate

import (
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

var t0 = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func ev(id string, dt time.Duration, lat, lon, mag float64) Event {
	return Event{ID: id, Origin: quake.Origin{OccurredAt: t0.Add(dt), Latitude: lat, Longitude: lon, Magnitude: mag}}
}

func TestFilterBoxPeriodAndDuplicates(t *testing.T) {
	box := Box{MinLat: -9, MaxLat: -5.5, MinLon: 105, MaxLon: 109.5}
	in := []Event{
		ev("b", time.Hour, -7, 107, 4),
		ev("a", time.Hour, -7, 107, 4), // solusi kembar
		ev("luar", 0, 0, 120, 4),
		ev("lama", -time.Hour, -7, 107, 4),
		ev("c", 0, -7, 106, 3),
	}
	got := Filter(in, box, t0, t0.Add(2*time.Hour))
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Fatalf("Filter = %+v", got)
	}
}

func TestEvaluateCountsRecallFalseMatchesAndPairs(t *testing.T) {
	bmkg := []Event{
		ev("b1", 0, -7, 107, 5.0),
		ev("b2", 10*time.Minute, -7.5, 107, 4.0),
		ev("b2dup", 10*time.Minute+2*time.Second, -7.52, 107, 4.1),
		ev("b3", 3*time.Hour, -6, 106, 4.5),
	}
	usgs := []Event{
		ev("u1", 2*time.Second, -7.1, 107, 5.2),                // cocok dengan b1
		ev("u2", 10*time.Minute+5*time.Second, -8.3, 107, 4.1), // ±90 km dari b2: hanya ambang longgar
		ev("u3", 30*time.Hour, -7, 107, 4.0),                   // tanpa kandidat BMKG
	}
	rules := []quake.Rule{quake.PRDInitialRule, quake.DefaultCrossSourceRule, quake.DefaultRevisionRule}
	rep := Evaluate(bmkg, usgs, rules, 5)
	if rep.BMKG != 4 || rep.USGS != 3 || rep.WithCandidate != 2 {
		t.Fatalf("hitungan %+v", rep)
	}
	if rep.Rules[0].Matched != 1 || rep.Rules[1].Matched != 2 || rep.Rules[1].Recall != 1 {
		t.Fatalf("recall %+v", rep.Rules)
	}
	if len(rep.Misses) != 1 || rep.Misses[0].USGS.ID != "u2" || rep.Misses[0].Km < 85 {
		t.Fatalf("contoh terlewat %+v", rep.Misses)
	}
	if rep.Rules[2].SameSourceBMKG != 1 || rep.Rules[2].SameSourceUSGS != 0 {
		t.Fatalf("pasangan sumber sama %+v", rep.Rules[2])
	}
	// u1 digeser 3 jam jatuh tepat di b3? Tidak: lokasi berbeda jauh, jadi nol.
	if rep.Rules[0].FalsePerThousandHours != 0 {
		t.Errorf("gabungan salah jam %+v", rep.Rules[0])
	}
	// u2 digeser +2 menit masih jauh dari semua; u1 −10 menit? b1 di 0, u1 di 2 dtk → tidak.
	if rep.Offsets.Seconds.Max <= 0 || rep.Offsets.Km.P50 <= 0 {
		t.Errorf("selisih %+v", rep.Offsets)
	}
	md := Markdown(rep, Meta{Box: Box{-9, -5.5, 105, 109.5}, From: t0, To: t0.Add(24 * time.Hour), BMKGSources: []string{"a"}, USGSSources: []string{"b"}, Command: "make x"})
	for _, want := range []string{"| Gempa BMKG | 4 |", "1m30s, 75 km, 0,7 | 50,0% (1/2)", "| u2 |", "`make x`"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown tanpa %q:\n%s", want, md)
		}
	}
}

func TestFalseRateDetectsShiftedCoincidence(t *testing.T) {
	// Gempa BMKG tepat 2 menit setelah gempa USGS di lokasi sama: geseran +2
	// menit menjadikannya gabungan salah.
	bmkg := []Event{ev("b", 2*time.Minute, -7, 107, 4)}
	usgs := []Event{ev("u", 0, -7, 107, 4)}
	rep := Evaluate(bmkg, usgs, []quake.Rule{quake.DefaultCrossSourceRule}, 0)
	if rep.Rules[0].FalsePerThousandMinutes != 100 { // 1 dari 10 geseran
		t.Fatalf("gabungan salah menit = %v", rep.Rules[0].FalsePerThousandMinutes)
	}
	if empty := Evaluate(nil, nil, []quake.Rule{quake.DefaultCrossSourceRule}, 0); empty.Rules[0].Recall != 0 {
		t.Error("katalog kosong")
	}
	if p := percentiles([]float64{3, 1, 2}); p.P50 != 2 || p.Max != 3 || p.P99 != 3 {
		t.Errorf("percentiles %+v", p)
	}
}
