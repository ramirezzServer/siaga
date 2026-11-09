package quakelabel

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2023, 1, 18, 6, 0, 0, 0, time.UTC)

func origin(id, agency string, sec float64, lat, lon float64, mags ...Magnitude) Origin {
	return Origin{
		ID: id, Agency: agency, Time: t0.Add(time.Duration(sec * float64(time.Second))),
		Latitude: lat, Longitude: lon, DepthKm: 10, HasDepth: true, Magnitudes: mags,
	}
}

func mag(t string, v float64) Magnitude { return Magnitude{Type: t, Value: v} }

func TestRepresentative(t *testing.T) {
	e := Event{ID: "1", Origins: []Origin{
		origin("isc", "ISC", 0, 0, 0, mag("mb", 6.3)),
		origin("n1", AgencyUSGS, 0, 0, 0, mag("mb", 6.3)),
		origin("n2", AgencyUSGS, 0, 0, 0),
		origin("n3", AgencyUSGS, 0, 0, 0, mag("Mwc", 6.1)),
		origin("n4", AgencyUSGS, 0, 0, 0, mag("Mww", 6.0)),
		origin("n5", AgencyUSGS, 0, 0, 0, mag("Mww", 6.2)),
		origin("d1", AgencyBMKG, 0, 0, 0, mag("mB", 6.5), Magnitude{Type: "Mw", Value: 9, Author: "GCMT"}, mag("MLv", 6.8)),
	}}
	p, ok := Representative(e, AgencyUSGS, USGSPriority)
	if !ok || p.Origin.ID != "n4" || p.Magnitude.Value != 6.0 {
		t.Fatalf("NEIC wakil %+v %v (ingin Mww pertama)", p, ok)
	}
	p, ok = Representative(e, AgencyBMKG, BMKGPriority)
	if !ok || p.Origin.ID != "d1" || p.Magnitude.Type != "MLv" {
		t.Fatalf("DJA wakil %+v (Mw milik GCMT tidak dipakai)", p)
	}
	if _, ok := Representative(Event{Origins: []Origin{origin("x", AgencyBMKG, 0, 0, 0, mag("ML", 3))}}, AgencyBMKG, BMKGPriority); ok {
		t.Fatal("tipe tidak dikenal tidak boleh jadi wakil")
	}
}

// halmahera meniru kejadian susulan 18 Jan 2023 (testdata adapter quakeml):
// dua kejadian ISC berselang ±159 dtk, masing-masing dengan NEIC dan DJA.
func halmahera() []Event {
	return []Event{
		{ID: "625610809", Origins: []Origin{
			origin("620012961", AgencyUSGS, 1153.68, 2.8239, 127.0179, mag("mb", 5.1)),
			origin("631703691", AgencyBMKG, 1154.5, 2.82, 127.1, mag("mb", 5.37), mag("Mjma", 6.04), mag("MLv", 5.52)),
		}},
		{ID: "625610810", Origins: []Origin{
			origin("620012962", AgencyUSGS, 1313.43, 2.5854, 126.9129, mag("mb", 4.4)),
			origin("631703690", AgencyBMKG, 1312.9, 2.83, 127.11, mag("Mjma", 5.54), mag("MLv", 4.65)),
		}},
		// DJA jauh dari NEIC mana pun: tidak masuk set.
		{ID: "jauh", Origins: []Origin{origin("d-jauh", AgencyBMKG, 0, -8.25, 115.51, mag("MLv", 3.63))}},
		// NEIC kecil dilewati; DJA tanpa magnitudo dihitung NoMagnitude.
		{ID: "kecil", Origins: []Origin{
			origin("n-kecil", AgencyUSGS, 5000, -7, 107, mag("mb", 3.9)),
			origin("d-kecil", AgencyBMKG, 5002, -7.1, 107.1),
		}},
		// NEIC tanpa DJA di kejadian sama, DJA lain 400 km: bukan positif.
		{ID: "sendiri", Origins: []Origin{origin("n-sendiri", AgencyUSGS, 9000, -9, 110, mag("Mww", 5))}},
		{ID: "lain", Origins: []Origin{origin("d-lain", AgencyBMKG, 9010, -9, 113.7, mag("MLv", 5))}},
	}
}

func TestBuild(t *testing.T) {
	rows, st, err := Build(halmahera(), DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{Events: 6, USGS: 3, BMKG: 2, Positive: 2, Negative: 2, NoMagnitude: 1}
	if st != want {
		t.Fatalf("statistik %+v, ingin %+v", st, want)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
		if r.Basis != BasisISC {
			t.Fatalf("dasar %q", r.Basis)
		}
	}
	if !slices.Equal(ids, []string{"620012961", "631703691", "631703690", "620012962", "n-sendiri"}) {
		t.Fatalf("urutan %v", ids)
	}
	b := rows[1]
	if b.Source != SourceBMKG || b.Label != "625610809" || b.MagnitudeType != "MLv" || b.Magnitude != 5.52 ||
		b.Others != "Mjma 6.04; mb 5.37" || b.DepthKm != 10 {
		t.Fatalf("baris BMKG %+v", b)
	}
	if rows[0].Source != SourceUSGS || rows[0].Others != "" {
		t.Fatalf("baris USGS %+v", rows[0])
	}
}

func TestBuildDepthMissing(t *testing.T) {
	e := halmahera()[:1]
	e[0].Origins[0].HasDepth = false
	rows, _, err := Build(e, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(rows[0].DepthKm) {
		t.Fatalf("kedalaman %v, ingin NaN", rows[0].DepthKm)
	}
}

func TestBuildRejects(t *testing.T) {
	dup := append(halmahera(), halmahera()[0])
	if _, _, err := Build(dup, DefaultOptions); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kejadian ganda: %v", err)
	}
	if _, _, err := Build([]Event{{}}, DefaultOptions); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ID kosong: %v", err)
	}
	for name, mutate := range map[string]func(*Origin){
		"tanpa ID":    func(o *Origin) { o.ID = "" },
		"tanpa waktu": func(o *Origin) { o.Time = time.Time{} },
		"koordinat":   func(o *Origin) { o.Latitude = 95 },
	} {
		e := halmahera()[:1]
		mutate(&e[0].Origins[1])
		if _, _, err := Build(e, DefaultOptions); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, o := range []Options{
		{MinUSGSMagnitude: 4, Window: Window{Seconds: 0, Km: 300}},
		{MinUSGSMagnitude: 4, Window: Window{Seconds: 300, Km: 2000}},
		{MinUSGSMagnitude: math.NaN(), Window: DefaultOptions.Window},
		{MinUSGSMagnitude: -1, Window: DefaultOptions.Window},
	} {
		if _, _, err := Build(nil, o); !errors.Is(err, ErrInvalid) {
			t.Errorf("opsi %+v: %v", o, err)
		}
	}
}

func TestBuildMinMagnitude(t *testing.T) {
	opt := DefaultOptions
	opt.MinUSGSMagnitude = 4.5
	_, st, err := Build(halmahera(), opt)
	if err != nil {
		t.Fatal(err)
	}
	// 620012962 (mb 4,4) keluar; DJA 631703690 tetap masuk karena dekat 620012961.
	if st.USGS != 2 || st.Positive != 1 || st.BMKG != 2 || st.Negative != 1 {
		t.Fatalf("%+v", st)
	}
}

// Hasil Build tidak bergantung urutan kejadian maupun urutan origin di
// dalam kejadian (kecuali seri tipe magnitudo, yang memilih origin pertama).
func FuzzBuildOrderIndependent(f *testing.F) {
	f.Add(uint64(1))
	f.Add(uint64(2026))
	f.Fuzz(func(t *testing.T, seed uint64) {
		rng := rand.New(rand.NewPCG(seed, seed^0x5eed)) //nolint:gosec // data uji deterministik, bukan rahasia
		var events []Event
		for i := range 2 + rng.IntN(20) {
			e := Event{ID: string(rune('a'+i%26)) + string(rune('A'+i/26))}
			sec := float64(rng.IntN(3600))
			lat, lon := -10+rng.Float64()*15, 95+rng.Float64()*45
			if rng.IntN(4) > 0 {
				e.Origins = append(e.Origins, origin(e.ID+"n", AgencyUSGS, sec, lat, lon, mag("mb", 3.5+rng.Float64()*3)))
			}
			if rng.IntN(4) > 0 {
				e.Origins = append(e.Origins, origin(e.ID+"d", AgencyBMKG, sec+rng.Float64()*20-10, lat+rng.Float64()-0.5, lon, mag("MLv", 3.5+rng.Float64()*3)))
			}
			events = append(events, e)
		}
		want, wst, err := Build(events, DefaultOptions)
		if err != nil {
			t.Fatal(err)
		}
		rng.Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })
		got, gst, err := Build(events, DefaultOptions)
		if err != nil {
			t.Fatal(err)
		}
		if gst != wst || len(got) != len(want) {
			t.Fatalf("statistik %+v vs %+v", gst, wst)
		}
		for i := range got {
			if got[i].ID != want[i].ID || got[i].Label != want[i].Label {
				t.Fatalf("baris %d: %+v vs %+v", i, got[i], want[i])
			}
		}
		if wst.Positive > wst.USGS || wst.BMKG > len(events) {
			t.Fatalf("statistik tidak masuk akal %+v", wst)
		}
	})
}
