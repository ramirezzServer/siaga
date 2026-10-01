// Package openmeteotest berisi sumber Open-Meteo tiruan untuk menguji alat
// kalibrasi tanpa jaringan.
package openmeteotest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Value menghasilkan nilai langkah ke-step (0 = awal start_date) di titik
// lat,lon; NaN ditulis sebagai null.
type Value func(lat, lon float64, day time.Time, step int) float64

// Fake menjawab seperti Open-Meteo Flood API atau Historical Weather API:
// satu objek per lokasi, urut sama dengan request, sel = titik yang diminta
// (dengan pembulatan float32 seperti sumber asli).
type Fake struct {
	Value Value
	// Unit menimpa satuan bawaan (m³/s untuk river_discharge, mm lainnya).
	Unit string
	// Shift menggeser sel yang dijawab (uji sel berbeda).
	Shift float64
	// DropStep menghapus langkah terakhir (uji jumlah langkah).
	DropStep bool
	// Fail membuat setiap request galat.
	Fail error

	mu sync.Mutex
	// Requests dan Locations menghitung request dan lokasi yang diminta.
	Requests, Locations int
	// Queries menyimpan query setiap request.
	Queries []url.Values
}

// Fetch memenuhi ports.Fetcher.
func (f *Fake) Fetch(_ context.Context, req ports.Request) (ports.Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return ports.Response{}, err
	}
	q := u.Query()
	f.mu.Lock()
	f.Requests++
	f.Queries = append(f.Queries, q)
	f.mu.Unlock()
	if f.Fail != nil {
		return ports.Response{}, f.Fail
	}
	lats, lons := strings.Split(q.Get("latitude"), ","), strings.Split(q.Get("longitude"), ",")
	if len(lats) != len(lons) {
		return ports.Response{}, errors.New("jumlah lintang dan bujur berbeda")
	}
	from, err1 := time.Parse(time.DateOnly, q.Get("start_date"))
	to, err2 := time.Parse(time.DateOnly, q.Get("end_date"))
	if err := errors.Join(err1, err2); err != nil {
		return ports.Response{}, err
	}
	res, variable := "daily", q.Get("daily")
	step := 24 * time.Hour
	if variable == "" {
		res, variable, step = "hourly", q.Get("hourly"), time.Hour
	}
	unit := f.Unit
	if unit == "" {
		unit = "mm"
		if variable == "river_discharge" {
			unit = "m³/s"
		}
	}
	n := int(to.Add(24*time.Hour).Sub(from) / step)
	if f.DropStep {
		n--
	}
	out := make([]map[string]any, len(lats))
	for i := range lats {
		lat, err1 := strconv.ParseFloat(lats[i], 64)
		lon, err2 := strconv.ParseFloat(lons[i], 64)
		if err := errors.Join(err1, err2); err != nil {
			return ports.Response{}, err
		}
		times := make([]int64, n)
		vals := make([]any, n)
		for k := range n {
			at := from.Add(time.Duration(k) * step)
			times[k] = at.Unix()
			if v := f.Value(lat, lon, at.Truncate(24*time.Hour), k); !math.IsNaN(v) {
				vals[k] = v
			}
		}
		out[i] = map[string]any{
			"latitude": float64(float32(lat + f.Shift)), "longitude": float64(float32(lon)),
			res + "_units": map[string]string{"time": "unixtime", variable: unit},
			res:            map[string]any{"time": times, variable: vals},
		}
	}
	f.mu.Lock()
	f.Locations += len(lats)
	f.mu.Unlock()
	var body []byte
	if len(out) == 1 {
		body, err = json.Marshal(out[0])
	} else {
		body, err = json.Marshal(out)
	}
	if err != nil {
		return ports.Response{}, fmt.Errorf("encode: %w", err)
	}
	return ports.Response{Body: body}, nil
}

// Clock adalah jam tiruan yang mencatat total waktu tidur.
type Clock struct {
	mu    sync.Mutex
	Slept time.Duration
}

// Now memenuhi ports.Clock.
func (c *Clock) Now() time.Time { return time.Unix(0, 0).UTC() }

// Sleep memenuhi ports.Clock.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.Slept += d
	c.mu.Unlock()
	return ctx.Err()
}
