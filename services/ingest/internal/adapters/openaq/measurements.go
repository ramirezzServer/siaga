package openaq

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/aqbackfill"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/airquality"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

var _ aqbackfill.Source = (*Source)(nil)

// Sensors mengembalikan sensor parameter SIAGA milik st menurut daftar
// lokasi terakhir (ParseList), urut ID.
func (s *Source) Sensors(st stations.Station) []aqbackfill.Sensor {
	id, err := strconv.ParseInt(strings.TrimPrefix(st.ID, "openaq:"), 10, 64)
	if err != nil {
		return nil
	}
	var out []aqbackfill.Sensor
	for sid, info := range s.sensors {
		if info.location == id {
			out = append(out, aqbackfill.Sensor{ID: sid, Parameter: info.parameter, Unit: info.unit})
		}
	}
	slices.SortFunc(out, func(a, b aqbackfill.Sensor) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// MeasurementsRequest meminta nilai mentah satu sensor dengan akhir periode
// di sekitar [from, to]. Nilai mentah dipakai, bukan /hours: nilai terbaru
// yang dibaca polling adalah nilai mentah, dan agregat per jam bisa sedikit
// berbeda (rekaman 2026-09-26: 118,0 di /hours, 117,6 di /measurements dan
// /latest). Sumber menyaring dengan awal periode, jadi batasnya dilebarkan
// satu jam dan pemanggil menyaring ulang dengan akhir periode.
func (s *Source) MeasurementsRequest(sensorID int64, from, to time.Time, page int) ports.Request {
	q := url.Values{
		"datetime_from": {from.Add(-time.Hour).UTC().Format(time.RFC3339)},
		"datetime_to":   {to.UTC().Format(time.RFC3339)},
		"limit":         {strconv.Itoa(aqbackfill.PageLimit)},
		"page":          {strconv.Itoa(page)},
	}
	return s.request(s.base + "/sensors/" + strconv.FormatInt(sensorID, 10) + "/measurements?" + q.Encode())
}

type measurement struct {
	Value     *float64 `json:"value"`
	Parameter *struct {
		Name  string `json:"name"`
		Units string `json:"units"`
	} `json:"parameter"`
	Period *struct {
		DatetimeTo *utcTime `json:"datetimeTo"`
	} `json:"period"`
}

// ParseMeasurements membaca satu halaman nilai mentah. Waktu ukur adalah akhir
// periode, sama dengan waktu di nilai terbaru (rekaman 2026-09-26: nilai
// periode 11.00–12.00 UTC muncul di /latest sebagai 12.00). Baris yang rusak
// membuat seluruh halaman ditolak karena berarti format sumber berubah.
func (s *Source) ParseMeasurements(body []byte) ([]aqbackfill.Measurement, error) {
	rows, err := decode[measurement](body)
	if err != nil {
		return nil, err
	}
	out := make([]aqbackfill.Measurement, 0, len(rows))
	var errs []error
	for i, r := range rows {
		if r.Value == nil || r.Parameter == nil || r.Period == nil || r.Period.DatetimeTo == nil {
			errs = append(errs, fmt.Errorf("baris %d: nilai, parameter, atau periode kosong", i))
			continue
		}
		t, err := time.Parse(time.RFC3339, r.Period.DatetimeTo.UTC)
		if err != nil {
			errs = append(errs, fmt.Errorf("baris %d: waktu %q: %w", i, r.Period.DatetimeTo.UTC, err))
			continue
		}
		out = append(out, aqbackfill.Measurement{
			Parameter: airquality.Parameter(strings.ToLower(strings.TrimSpace(r.Parameter.Name))),
			Unit:      normalizeUnit(r.Parameter.Units), Value: *r.Value, ObservedAt: t.UTC(),
		})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStructure, err)
	}
	return out, nil
}
