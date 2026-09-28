package openaq

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/aqbackfill"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
)

func TestMeasurementsRequest(t *testing.T) {
	s := source(t)
	from := time.Date(2026, 9, 26, 13, 0, 0, 0, time.FixedZone("WIB", 7*3600))
	to := time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)
	req := s.MeasurementsRequest(17620437, from, to, 2)
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	// Awal dilebarkan satu jam karena sumber menyaring dengan awal periode.
	if u.Path != "/v3/sensors/17620437/measurements" || q.Get("datetime_from") != "2026-09-26T05:00:00Z" ||
		q.Get("datetime_to") != "2026-09-26T19:00:00Z" || q.Get("page") != "2" || q.Get("limit") != "1000" {
		t.Fatal(req.URL)
	}
	if req.Header["X-API-Key"] != key || len(req.Secrets) != 1 || strings.Contains(req.Redacted(), key) {
		t.Fatalf("%+v", req)
	}
}

func TestParseMeasurementsRecorded(t *testing.T) {
	s := source(t)
	rows, err := s.ParseMeasurements(read(t, "measurements-17620437-2026-09-26.json"))
	if err != nil || len(rows) != 10 {
		t.Fatalf("%d %v", len(rows), err)
	}
	// Waktu ukur = akhir periode, sama dengan /latest dan data live
	// (ts.aq_observation 2026-09-26 12.00 UTC = 56,5 = periode 11.00–12.00).
	for _, r := range rows {
		if r.ObservedAt.Equal(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)) {
			if r.Value != 56.5 || r.Parameter != airquality.PM25 || r.Unit != airquality.UnitMicrogram {
				t.Fatalf("%+v", r)
			}
		}
	}
	last := rows[len(rows)-1]
	if !last.ObservedAt.Equal(time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)) || last.Value != 117.6 {
		t.Fatalf("%+v", last)
	}
	// /hours berbentuk sama (dipakai sebagai contoh "found" berupa teks).
	rows, err = s.ParseMeasurements(read(t, "hours-halaman-2026-09-26.json"))
	if err != nil || len(rows) != 3 {
		t.Fatalf("%d %v", len(rows), err)
	}
}

func TestParseMeasurementsErrors(t *testing.T) {
	s := source(t)
	for name, body := range map[string]string{
		"bukan json":   "<html>",
		"tanpa hasil":  `{"meta":{}}`,
		"nilai kosong": `{"meta":{},"results":[{"parameter":{"name":"pm25","units":"µg/m³"},"period":{"datetimeTo":{"utc":"2026-09-26T12:00:00Z"}}}]}`,
		"tanpa waktu":  `{"meta":{},"results":[{"value":1,"parameter":{"name":"pm25","units":"µg/m³"},"period":{}}]}`,
		"waktu rusak":  `{"meta":{},"results":[{"value":1,"parameter":{"name":"pm25","units":"µg/m³"},"period":{"datetimeTo":{"utc":"kemarin"}}}]}`,
	} {
		if _, err := s.ParseMeasurements([]byte(body)); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: %v", name, err)
		}
	}
	rows, err := s.ParseMeasurements([]byte(`{"meta":{},"results":[]}`))
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestSensors(t *testing.T) {
	s := source(t)
	list, _, err := s.ParseList(read(t, "locations-sintetis.json"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, st := range list {
		sensors := s.Sensors(st)
		for i, sn := range sensors {
			if !airquality.Known(sn.Parameter) || i > 0 && sn.ID <= sensors[i-1].ID {
				t.Fatalf("%s: %+v", st.ID, sensors)
			}
		}
		found += len(sensors)
	}
	if found == 0 {
		t.Fatal("tanpa sensor")
	}
	if got := s.Sensors(stations.Station{Station: airquality.Station{ID: "bukan-openaq"}}); got != nil {
		t.Fatal(got)
	}
	_ = aqbackfill.PageLimit // antarmuka dipenuhi (lihat var _ di measurements.go)
}
