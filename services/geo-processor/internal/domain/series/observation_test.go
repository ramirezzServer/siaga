package series

import (
	"errors"
	"math"
	"testing"
	"time"
)

func stationObs() StationObservation {
	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	loc := Point{Lat: -6.88, Lon: 107.61}
	return StationObservation{
		Run: Run{
			Site:    Site{ID: "openaq:2178", Kind: SiteStation, Name: "Dago", Location: loc},
			Dataset: AirQualityObs, Source: SourceOpenAQ, Model: ModelSensor, Cell: loc,
			IssuedAt: now.Add(-time.Hour), FetchedAt: now, ArchiveKey: "openaq-stasiun-latest/k.json.gz",
		},
		Station: Station{Provider: "AirGradient", Owner: "Warga", Locality: "Bandung", Timezone: "Asia/Jakarta"},
		Readings: []Reading{
			{SensorID: 1, Parameter: "pm25", Unit: UnitMicrogram, Value: 40, ObservedAt: now.Add(-time.Hour)},
			{SensorID: 2, Parameter: "o3", Unit: UnitPPB, Value: 31, ObservedAt: now.Add(-2 * time.Hour)},
		},
	}
}

func TestStationObservationValid(t *testing.T) {
	o := stationObs()
	if err := o.Validate(o.FetchedAt); err != nil {
		t.Fatal(err)
	}
	if k, ok := KindOf("openaq:2178"); !ok || k != SiteStation {
		t.Fatal(k)
	}
}

func TestStationObservationInvalid(t *testing.T) {
	cases := map[string]func(*StationObservation){
		"jenis data":   func(o *StationObservation) { o.Dataset = AirQuality },
		"sumber":       func(o *StationObservation) { o.Source = SourceOpenMeteo },
		"model":        func(o *StationObservation) { o.Model = "cams_global" },
		"jenis titik":  func(o *StationObservation) { o.Site.Kind = SiteGrid },
		"kode wilayah": func(o *StationObservation) { o.Site.RegionCode = "32.73.01.1001" },
		"penyedia":     func(o *StationObservation) { o.Station.Provider = "" },
		"nama":         func(o *StationObservation) { o.Site.Name = "" },
		"pemilik":      func(o *StationObservation) { o.Station.Owner = " x" },
		"zona":         func(o *StationObservation) { o.Station.Timezone = "WIB 7" },
		"kosong":       func(o *StationObservation) { o.Readings = nil },
		"urutan":       func(o *StationObservation) { o.Readings[1].SensorID = 1 },
		"ID sensor":    func(o *StationObservation) { o.Readings[0].SensorID = -1 },
		"satuan":       func(o *StationObservation) { o.Readings[0].Unit = UnitPPM },
		"parameter":    func(o *StationObservation) { o.Readings[0].Parameter = "pm1" },
		"batas":        func(o *StationObservation) { o.Readings[1].Value = 3000 },
		"NaN":          func(o *StationObservation) { o.Readings[0].Value = math.NaN() },
		"bukan UTC": func(o *StationObservation) {
			o.Readings[1].ObservedAt = o.Readings[1].ObservedAt.In(time.FixedZone("WIB", 7*3600))
		},
		"terlalu tua":  func(o *StationObservation) { o.Readings[1].ObservedAt = o.FetchedAt.Add(-8 * 24 * time.Hour) },
		"masa depan":   func(o *StationObservation) { o.Readings[1].ObservedAt = o.FetchedAt.Add(2 * time.Hour) },
		"waktu terbit": func(o *StationObservation) { o.IssuedAt = o.FetchedAt },
		"OpenMeteo stasiun": func(o *StationObservation) {
			o.Source, o.Model = SourceOpenMeteo, "x"
		},
	}
	for name, mut := range cases {
		o := stationObs()
		o.Readings = append([]Reading(nil), o.Readings...)
		mut(&o)
		if err := o.Validate(o.FetchedAt); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, ok := ObservationLimit("pm10", UnitPPB); ok {
		t.Error("PM10 dalam ppb diterima")
	}
}
