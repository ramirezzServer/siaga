package airquality

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

var fetched = time.Date(2026, 9, 24, 8, 10, 0, 0, time.UTC)

func station() Station {
	return Station{
		ID: StationID(2178), Name: "Bandung Dago", Locality: "Bandung", Lat: -6.88, Lon: 107.61,
		Provider: "AirGradient", Owner: "Warga", Timezone: "Asia/Jakarta",
	}
}

func reading(id int64, p Parameter, unit string, v float64, age time.Duration) Reading {
	return Reading{SensorID: id, Parameter: p, Unit: unit, Value: v, ObservedAt: fetched.Add(-age)}
}

func TestCleanAndValidate(t *testing.T) {
	in := []Reading{
		reading(30, NO2, UnitPPB, 21, time.Hour),
		reading(10, PM25, UnitMicrogram, 41.5, time.Hour),
		reading(20, PM10, UnitMicrogram, 60, 3*24*time.Hour), // basi
		reading(40, PM25, UnitPPM, 1, time.Hour),             // satuan salah
		reading(50, "temperature", "c", 25, time.Hour),       // parameter lain
		reading(60, CO, UnitPPM, -0.1, time.Hour),            // negatif
		reading(70, O3, UnitMicrogram, 10, 0),
		reading(70, O3, UnitMicrogram, 11, 0), // ganda
	}
	kept, dropped := Clean(in, fetched, 24*time.Hour)
	ids := make([]int64, len(kept))
	for i, r := range kept {
		ids[i] = r.SensorID
	}
	if !slices.Equal(ids, []int64{10, 30}) || len(dropped) != 6 {
		t.Fatalf("kept %v, dropped %v", ids, dropped)
	}
	stale := 0
	for _, e := range dropped {
		if errors.Is(e, ErrStale) {
			stale++
		}
	}
	if stale != 1 {
		t.Fatalf("basi %d", stale)
	}
	o := Observation{Station: station(), Readings: kept}
	if err := o.Validate(fetched, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if !o.Newest().Equal(fetched.Add(-time.Hour)) || !(Observation{}).Newest().IsZero() {
		t.Fatal("Newest")
	}
}

func TestObservationInvalid(t *testing.T) {
	ok := []Reading{reading(1, PM25, UnitMicrogram, 12, time.Hour)}
	cases := map[string]func(*Observation){
		"ID":           func(o *Observation) { o.Station.ID = "openaq:0" },
		"lokasi":       func(o *Observation) { o.Station.Lat = 40 },
		"nama":         func(o *Observation) { o.Station.Name = "" },
		"penyedia":     func(o *Observation) { o.Station.Provider = " spasi" },
		"kontrol":      func(o *Observation) { o.Station.Owner = "a\tb" },
		"panjang":      func(o *Observation) { o.Station.Locality = string(make([]byte, 101)) },
		"utf8":         func(o *Observation) { o.Station.Name = "\xff" },
		"zona":         func(o *Observation) { o.Station.Timezone = "Asia Jakarta" },
		"kosong":       func(o *Observation) { o.Readings = nil },
		"urutan":       func(o *Observation) { o.Readings = append(o.Readings, reading(1, PM10, UnitMicrogram, 3, 0)) },
		"masa depan":   func(o *Observation) { o.Readings[0].ObservedAt = fetched.Add(time.Hour) },
		"bukan UTC":    func(o *Observation) { o.Readings[0].ObservedAt = fetched.In(time.FixedZone("WIB", 7*3600)) },
		"NaN":          func(o *Observation) { o.Readings[0].Value = math.NaN() },
		"ID sensor":    func(o *Observation) { o.Readings[0].SensorID = 0 },
		"batas":        func(o *Observation) { o.Readings[0].Value = 6000 },
		"banyak nilai": func(o *Observation) { o.Readings = make([]Reading, MaxReadings+1) },
	}
	for name, mut := range cases {
		o := Observation{Station: station(), Readings: slices.Clone(ok)}
		mut(&o)
		if err := o.Validate(fetched, 24*time.Hour); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLimits(t *testing.T) {
	for _, p := range []Parameter{PM25, PM10, NO2, O3, SO2, CO} {
		hi, ok := Limit(p, UnitMicrogram)
		if !Known(p) || !ok || hi <= 0 {
			t.Errorf("%s", p)
		}
		ppm, okPPM := Limit(p, UnitPPM)
		ppb, okPPB := Limit(p, UnitPPB)
		if okPPM != okPPB || (okPPM && math.Abs(ppb-ppm*1000) > 1e-9) {
			t.Errorf("%s: ppm %v ppb %v", p, ppm, ppb)
		}
	}
	if Known("pm1") {
		t.Error("pm1 tidak dipakai")
	}
}

// Keluaran Clean selalu lolos Validate bila stasiunnya valid, urut per ID
// sensor, dan tidak pernah memuat nilai yang ditolak.
func FuzzClean(f *testing.F) {
	f.Add(int64(1), "pm25", "µg/m³", 12.5, int64(3600), int64(2), "no2", "ppb", 30.0, int64(60))
	f.Add(int64(5), "co", "ppm", 0.4, int64(-60), int64(5), "co", "ppm", 0.5, int64(90000))
	f.Fuzz(func(t *testing.T, id1 int64, p1, u1 string, v1 float64, age1 int64, id2 int64, p2, u2 string, v2 float64, age2 int64) {
		in := []Reading{
			{SensorID: id1, Parameter: Parameter(p1), Unit: u1, Value: v1, ObservedAt: fetched.Add(-time.Duration(age1) * time.Second)},
			{SensorID: id2, Parameter: Parameter(p2), Unit: u2, Value: v2, ObservedAt: fetched.Add(-time.Duration(age2) * time.Second)},
		}
		kept, dropped := Clean(in, fetched, 24*time.Hour)
		if len(kept)+len(dropped) != len(in) {
			t.Fatalf("%d + %d != %d", len(kept), len(dropped), len(in))
		}
		if len(kept) == 0 {
			return
		}
		if err := (Observation{Station: station(), Readings: kept}).Validate(fetched, 24*time.Hour); err != nil {
			t.Fatalf("keluaran Clean tidak valid: %v", err)
		}
	})
}
