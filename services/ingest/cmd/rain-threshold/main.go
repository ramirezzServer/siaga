// Command rain-threshold menghitung ambang indeks hujan sub-DAS Citarum Hulu
// (make rain-threshold): persentil puncak harian akumulasi hujan 3, 6, dan
// 24 jam rata-rata wilayah setiap sub-DAS dari arsip ECMWF IFS (ADR 0020).
//
//	rain-threshold -basins docs/calibration/sub-das-citarum-hulu.csv \
//	  -out docs/calibration/ambang-hujan-sub-das.csv -report docs/calibration/ambang-hujan-sub-das.md
//
// Jalan pertama untuk 47 sel dengan periode bawaan 2017–2024 (2.922 hari,
// bobot ±21 per sel) memakai ±1.000 "API call" Open-Meteo dan butuh ±3
// menit. Deret disimpan di -cache, jadi jalan ulang tidak memakai kuota.
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
	"syscall"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/reanalysiscache"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/rainthreshold"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/catchment"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rain-threshold:", err)
		os.Exit(1)
	}
}

func run() error {
	basinsPath := flag.String("basins", "docs/calibration/sub-das-citarum-hulu.csv", "sel dan bobot luas per sub-DAS")
	out := flag.String("out", "docs/calibration/ambang-hujan-sub-das.csv", "daftar ambang per sub-DAS dan jendela")
	report := flag.String("report", "docs/calibration/ambang-hujan-sub-das.md", "laporan kalibrasi (Markdown)")
	endpoint := flag.String("url", envOr("OPENMETEO_ARCHIVE_URL", rainthreshold.DefaultArchiveURL), "endpoint Historical Weather API")
	model := flag.String("model", rainthreshold.DefaultModel, "model arsip Open-Meteo")
	from := flag.String("from", reanalysis.Date(rainthreshold.DefaultFrom), "awal periode klimatologi (UTC, inklusif)")
	to := flag.String("to", reanalysis.Date(rainthreshold.DefaultTo), "akhir periode klimatologi (UTC, inklusif)")
	cacheDir := flag.String("cache", ".cache/rain-threshold", "folder cache deret per sel")
	maxCalls := flag.Float64("max-calls", 2000, "batas panggilan Open-Meteo baru dalam satu kali jalan (0 = tanpa batas)")
	flag.Parse()

	f, err1 := time.Parse(time.DateOnly, *from)
	t, err2 := time.Parse(time.DateOnly, *to)
	if err := errors.Join(err1, err2); err != nil {
		return fmt.Errorf("periode klimatologi: %w", err)
	}
	src := rainthreshold.Source(*endpoint, *model, f, t)
	if err := src.Validate(); err != nil {
		return err
	}
	bf, err := os.Open(*basinsPath)
	if err != nil {
		return err
	}
	basins, err := catchment.Parse(bf)
	_ = bf.Close()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	path := reanalysiscache.Path(*cacheDir, src)
	cache, err := reanalysiscache.Load(path, src)
	if err != nil {
		return err
	}
	loader := &reanalysis.Loader{
		Fetch: httpfetch.New(300 * time.Second), Clock: sysclock.Clock{}, Cache: cache, MaxCalls: *maxCalls,
		Progress:   func(done, total int) { fmt.Fprintf(os.Stderr, "  request %d/%d\n", done, total) },
		Checkpoint: func(c *reanalysis.Cache) error { return reanalysiscache.Save(path, c) },
	}
	cells := catchment.Cells(basins)
	plan := loader.Plan(cells)
	fmt.Fprintf(os.Stderr, "%s: %d sub-DAS, %d sel, %d dari cache %s, perlu ±%.0f panggilan Open-Meteo\n",
		src, len(basins), plan.Cells, plan.Cached, path, math.Ceil(plan.Calls))
	data, err := loader.Load(ctx, cells)
	if err != nil {
		return err
	}
	results, err := rainthreshold.Compute(src, basins, data)
	if err != nil {
		return err
	}
	var csvOut, rep bytes.Buffer
	if err := rainthreshold.WriteThresholds(&csvOut, src, results); err != nil {
		return err
	}
	if err := rainthreshold.WriteReport(&rep, time.Now(), src, results); err != nil {
		return err
	}
	if err := os.WriteFile(*out, csvOut.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*report, rep.Bytes(), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ambang %d sub-DAS × %d jendela ditulis ke %s\n", len(basins), len(rainthreshold.Windows), *out)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
