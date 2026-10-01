// Command flood-threshold menghitung ambang banjir setiap titik pantau
// sungai (make flood-threshold): persentil debit harian reanalisis GloFAS
// periode klimatologi di sel titik itu, lalu menguji ambang terhadap kejadian
// banjir tercatat (ADR 0020).
//
//	flood-threshold -sites services/ingest/internal/adapters/sitelist/data/rivers_32.csv \
//	  -events docs/calibration/banjir-tercatat.csv \
//	  -out docs/calibration/ambang-banjir-32.csv -report docs/calibration/ambang-banjir.md
//
// Jalan pertama untuk 38 titik dengan periode bawaan 1997–2024 (10.227 hari,
// bobot ±73 per sel) memakai ±2.800 "API call" Open-Meteo, ditambah ±240
// untuk pembanding seamless_v4 2022-08..2024-12 dan beberapa untuk kejadian
// di luar sampel, dan butuh ±8 menit karena dibatasi 400 panggilan per menit.
// Deret disimpan di -cache, jadi jalan ulang tidak memakai kuota.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/reanalysiscache"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/floodthreshold"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "flood-threshold:", err)
		os.Exit(1)
	}
}

var province = regexp.MustCompile(`[_-]([0-9]{2})\.csv$`)

func run() error {
	sitesPath := flag.String("sites", "services/ingest/internal/adapters/sitelist/data/rivers_32.csv", "daftar titik pantau sungai (keluaran river-snap)")
	eventsPath := flag.String("events", "docs/calibration/banjir-tercatat.csv", "kejadian banjir tercatat untuk uji (kosong = tanpa uji)")
	out := flag.String("out", "docs/calibration/ambang-banjir-32.csv", "daftar ambang per titik")
	report := flag.String("report", "docs/calibration/ambang-banjir.md", "laporan kalibrasi (Markdown)")
	endpoint := flag.String("url", envOr("OPENMETEO_FLOOD_URL", floodthreshold.DefaultFloodURL), "endpoint Flood API")
	model := flag.String("model", floodthreshold.ModelReanalysis, "model reanalisis Open-Meteo Flood API")
	from := flag.String("from", reanalysis.Date(floodthreshold.DefaultFrom), "awal periode klimatologi (UTC, inklusif)")
	to := flag.String("to", reanalysis.Date(floodthreshold.DefaultTo), "akhir periode klimatologi (UTC, inklusif)")
	overlapFrom := flag.String("overlap-from", reanalysis.Date(floodthreshold.DefaultOverlapFrom), "awal periode pembanding seamless_v4 (kosong = tanpa pembanding)")
	overlapTo := flag.String("overlap-to", reanalysis.Date(floodthreshold.DefaultOverlapTo), "akhir periode pembanding seamless_v4")
	cacheDir := flag.String("cache", ".cache/flood-threshold", "folder cache deret per sel")
	maxCalls := flag.Float64("max-calls", 4000, "batas panggilan Open-Meteo baru per sumber dalam satu kali jalan (0 = tanpa batas)")
	flag.Parse()

	m := province.FindStringSubmatch(*out)
	if m == nil {
		return fmt.Errorf("nama file -out %q harus berakhiran -<kode provinsi>.csv", *out)
	}
	f, err1 := time.Parse(time.DateOnly, *from)
	t, err2 := time.Parse(time.DateOnly, *to)
	if err := errors.Join(err1, err2); err != nil {
		return fmt.Errorf("periode klimatologi: %w", err)
	}
	var of, ot time.Time
	if *overlapFrom != "" {
		var err1, err2 error
		of, err1 = time.Parse(time.DateOnly, *overlapFrom)
		ot, err2 = time.Parse(time.DateOnly, *overlapTo)
		if err := errors.Join(err1, err2); err != nil {
			return fmt.Errorf("periode pembanding: %w", err)
		}
	}
	src := floodthreshold.Source(*endpoint, *model, f, t)
	if err := src.Validate(); err != nil {
		return err
	}
	b, err := os.ReadFile(*sitesPath)
	if err != nil {
		return err
	}
	sites, err := sitelist.ParseRivers(b)
	if err != nil {
		return err
	}
	var events []floodthreshold.Event
	if *eventsPath != "" {
		ef, err := os.Open(*eventsPath)
		if err != nil {
			return err
		}
		events, err = floodthreshold.ReadEvents(ef)
		_ = ef.Close()
		if err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fetch, clock := httpfetch.New(180*time.Second), sysclock.Clock{}
	cal := &floodthreshold.Calibrator{
		Endpoint: *endpoint, Source: src, OverlapFrom: of, OverlapTo: ot,
		Open: func(s reanalysis.Source) (*reanalysis.Loader, error) {
			path := reanalysiscache.Path(*cacheDir, s)
			c, err := reanalysiscache.Load(path, s)
			if err != nil {
				return nil, err
			}
			l := &reanalysis.Loader{
				Fetch: fetch, Clock: clock, Cache: c, MaxCalls: *maxCalls,
				Progress:   func(done, total int) { fmt.Fprintf(os.Stderr, "  request %d/%d\n", done, total) },
				Checkpoint: func(c *reanalysis.Cache) error { return reanalysiscache.Save(path, c) },
			}
			return l, nil
		},
	}
	loader, err := cal.Open(src)
	if err != nil {
		return err
	}
	cells := make([]series.LatLon, 0, len(sites))
	for _, s := range sites {
		cells = append(cells, s.Requested)
	}
	plan := loader.Plan(cells)
	fmt.Fprintf(os.Stderr, "%s: %d sel, %d dari cache %s, perlu ±%.0f panggilan Open-Meteo\n",
		src, plan.Cells, plan.Cached, reanalysiscache.Path(*cacheDir, src), math.Ceil(plan.Calls))

	results, checks, err := cal.Run(ctx, sites, events)
	if err != nil {
		return err
	}
	var csvOut, rep bytes.Buffer
	if err := floodthreshold.WriteThresholds(&csvOut, m[1], src, results); err != nil {
		return err
	}
	if err := floodthreshold.WriteReport(&rep, time.Now(), src, results, checks); err != nil {
		return err
	}
	if err := os.WriteFile(*out, csvOut.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*report, rep.Bytes(), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ambang %d titik ditulis ke %s; %d uji kejadian di %s\n", len(results), *out, len(checks), *report)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
