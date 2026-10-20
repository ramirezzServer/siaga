package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const key = "KUNCIUJIKUNCIUJIKUNCIUJIKUNCIUJI"

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// firmsFake menjawab ketersediaan, status, dan Area API dengan deteksi
// sintetis: kilang tiap 10 hari (SP type 2) dan kebakaran Ciremai 21–25
// Okt 2019.
type firmsFake struct {
	mu   sync.Mutex
	area int
}

func (f *firmsFake) Fetch(_ context.Context, r ports.Request) (ports.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := r.URL
	switch {
	case strings.Contains(u, "/api/data_availability/"):
		if !strings.Contains(u, key) {
			return ports.Response{}, &httpfetch.StatusError{Status: 401, Body: "Invalid MAP_KEY."}
		}
		return ports.Response{Body: []byte("data_id,min_date,max_date\nVIIRS_SNPP_SP,2012-01-20,2019-12-31\nVIIRS_SNPP_NRT,2019-11-01,2026-10-19\n" +
			"MODIS_SP,2000-11-01,2019-12-31\nMODIS_NRT,2019-11-01,2026-10-19\n")}, nil
	case strings.Contains(u, "/mapserver/mapkey_status/"):
		return ports.Response{Body: []byte(`{"transaction_limit":5000,"current_transactions":7}`)}, nil
	}
	f.area++
	parts := strings.Split(u, "/")
	product, days, date := parts[len(parts)-4], parts[len(parts)-2], parts[len(parts)-1]
	start, _ := time.Parse(time.DateOnly, date)
	var n int
	_, _ = fmt.Sscan(days, &n)
	modis := strings.HasPrefix(product, "MODIS")
	var b strings.Builder
	if modis {
		b.WriteString("latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_t31,frp,daynight,type\n")
	} else {
		b.WriteString("latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_ti5,frp,daynight,type\n")
	}
	for d := range n {
		day := start.AddDate(0, 0, d)
		ds := day.Format(time.DateOnly)
		if !modis && day.Sub(time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)).Hours()/24 >= 0 && int(day.Sub(time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)).Hours()/24)%10 == 0 && day.Year() <= 2019 {
			fmt.Fprintf(&b, "-6.38510,108.37020,340,0.4,0.4,%s,1840,N,VIIRS,n,2,290,25,N,2\n", ds)
		}
		if day.After(time.Date(2019, 10, 20, 0, 0, 0, 0, time.UTC)) && day.Before(time.Date(2019, 10, 26, 0, 0, 0, 0, time.UTC)) {
			for j := range 4 {
				if modis {
					fmt.Fprintf(&b, "%.5f,108.38000,330,1.1,1.0,%s,0310,Aqua,MODIS,85,6.1,300,40,D,0\n", -6.875+0.003*float64(j), ds)
				} else {
					fmt.Fprintf(&b, "%.5f,108.38000,350,0.4,0.4,%s,0540,N,VIIRS,n,2,295,12,D,0\n", -6.875+0.003*float64(j), ds)
				}
			}
		}
	}
	return ports.Response{Body: []byte(b.String())}, nil
}

type sleepless struct{}

func (sleepless) Now() time.Time                                   { return now }
func (sleepless) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRun(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "kebakaran.csv")
	if err := os.WriteFile(events, []byte("id,mulai,selesai,lintang,bujur,radius_km,luas_ha,sumber,catatan\n"+
		"ciremai-2019-10,2019-10-21,2019-10-26,-6.880,108.385,7,190,https://contoh.test/a,lereng\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, static := filepath.Join(dir, "titik-api.md"), filepath.Join(dir, "sumber-panas-tetap-32.csv")
	args := []string{
		"-from", "2018-01-01", "-to", "2020-01-31", "-satellites", "S-NPP,MODIS", "-cache", filepath.Join(dir, "cache"),
		"-events", events, "-report", report, "-static", static,
	}
	f := &firmsFake{}
	d := deps{fetch: f, clock: sleepless{}, now: func() time.Time { return now }}
	var log bytes.Buffer
	if err := runWith(t.Context(), args, env(map[string]string{"FIRMS_MAP_KEY": key}), &log, d); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	// 2018-01-01..2019-12-31 SP + 2020-01 NRT untuk dua wahana.
	if f.area < 300 || f.area > 320 {
		t.Fatalf("%d request area\n%s", f.area, log.String())
	}
	if strings.Contains(log.String(), key) {
		t.Fatal("key tercetak di log")
	}
	rep, err := os.ReadFile(report) //nolint:gosec // path dari t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**1 sumber**", "| [ciremai-2019-10](https://contoh.test/a) | 2019-10-21 s.d. 10-26 | 190 ha | Siaga, 40 deteksi", "| S-NPP | `VIIRS_SNPP_NRT` | 2020-01-01 | 2020-01-31 |"} {
		if !strings.Contains(string(rep), want) {
			t.Errorf("laporan tanpa %q\n%s", want, rep)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sumber-panas-tetap-32.geojson")); err != nil {
		t.Fatal(err)
	}

	// Jalan kedua: semua potongan final dari cache, NRT terbaru tidak ada
	// (Januari 2020 sudah final).
	f.area = 0
	log.Reset()
	if err := runWith(t.Context(), args, env(map[string]string{"FIRMS_MAP_KEY": key}), &log, d); err != nil || f.area != 0 {
		t.Fatalf("jalan ulang %d request: %v\n%s", f.area, err, log.String())
	}
	again, _ := os.ReadFile(report) //nolint:gosec // path dari t.TempDir()
	if !bytes.Equal(rep, again) {
		t.Fatal("laporan jalan ulang berbeda")
	}
	// Offline tanpa key.
	log.Reset()
	if err := runWith(t.Context(), append(args, "-offline"), env(nil), &log, d); err != nil || f.area != 0 {
		t.Fatalf("offline: %v\n%s", err, log.String())
	}
	// Rencana saja.
	log.Reset()
	if err := runWith(t.Context(), append(args, "-plan"), env(map[string]string{"FIRMS_MAP_KEY": key}), &log, d); err != nil ||
		!strings.Contains(log.String(), "dari cache, 0 request ke FIRMS") {
		t.Fatalf("plan: %v\n%s", err, log.String())
	}
}

func TestRunErrors(t *testing.T) {
	d := deps{fetch: &firmsFake{}, clock: sleepless{}, now: func() time.Time { return now }}
	withKey := env(map[string]string{"FIRMS_MAP_KEY": key})
	cache := []string{"-cache", "CACHE"}
	for name, c := range map[string]struct {
		args []string
		env  func(string) string
		want string
	}{
		"tanpa key":      {cache, env(nil), "FIRMS_MAP_KEY"},
		"key ditolak":    {cache, env(map[string]string{"FIRMS_MAP_KEY": "SALAHSALAHSALAHSALAH"}), "MAP_KEY"},
		"kotak":          {append(cache, "-bbox", "1,2,3"), withKey, "kotak"},
		"from":           {append(cache, "-from", "kemarin"), withKey, "-from"},
		"to":             {append(cache, "-to", "besok"), withKey, "-to"},
		"terbalik":       {append(cache, "-from", "2020-01-02", "-to", "2020-01-01"), withKey, "sebelum"},
		"wahana":         {append(cache, "-satellites", "GOES"), withKey, "wahana"},
		"argumen":        {append(cache, "lebih"), withKey, "argumen"},
		"masa depan":     {append(cache, "-from", "2030-01-01", "-to", "2030-02-01"), withKey, "tidak ada produk"},
		"offline kosong": {append(cache, "-offline"), env(nil), "-offline butuh"},
		"uji hilang":     {append(cache, "-from", "2019-10-21", "-to", "2019-10-22", "-events", "/tidak/ada.csv"), withKey, "ada.csv"},
		"flag":           {append(cache, "-tidak-ada"), withKey, "tidak-ada"},
	} {
		t.Run(name, func(t *testing.T) {
			var log bytes.Buffer
			args := slices.Clone(c.args)
			args[1] = t.TempDir()
			err := runWith(t.Context(), args, c.env, &log, d)
			if err == nil || !strings.Contains(err.Error()+log.String(), c.want) {
				t.Fatalf("%v\n%s", err, log.String())
			}
		})
	}
}

func TestOfflineWithoutChunks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "availability.csv"), []byte("data_id,min_date,max_date\nMODIS_SP,2000-11-01,2019-12-31\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := deps{fetch: &firmsFake{}, clock: sleepless{}, now: func() time.Time { return now }}
	var log bytes.Buffer
	err := runWith(t.Context(), []string{"-offline", "-cache", dir, "-from", "2019-01-01"}, env(nil), &log, d)
	if err == nil || !strings.Contains(err.Error(), "jalankan dulu tanpa -offline") {
		t.Fatal(err)
	}
}
