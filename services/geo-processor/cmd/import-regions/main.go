// Command import-regions memuat batas wilayah satu provinsi ke ref.region.
//
//	import-regions -source .cache/wilayah_boundaries/db -province 32
//
// Koneksi database dibaca dari DATABASE_URL (role siaga_geo).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/cahyadsn"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/importregions"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "import-regions:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		source     = flag.String("source", ".cache/wilayah_boundaries/db", "folder db/ dari repo wilayah_boundaries")
		province   = flag.String("province", "32", "kode provinsi dua digit")
		version    = flag.String("source-version", "", "versi data (default: isi file .source-version di atas folder sumber)")
		maxSkipped = flag.Int("max-skipped", 0, "jumlah baris bermasalah yang ditoleransi")
		dryRun     = flag.Bool("dry-run", false, "validasi saja tanpa menulis ke database")
	)
	flag.Parse()

	env := envx.NewReader(envx.OS)
	level, err := logx.ParseLevel(env.Default("LOG_LEVEL", "info"))
	if err != nil {
		return err
	}
	dbURL := ""
	if !*dryRun {
		dbURL = env.Required("DATABASE_URL")
	}
	if err := env.Err(); err != nil {
		return err
	}
	log := logx.New(os.Stderr, "import-regions", level)

	if *version == "" {
		b, err := os.ReadFile(filepath.Join(*source, "..", ".source-version"))
		if err != nil {
			return fmt.Errorf("isi -source-version atau jalankan scripts/fetch-region-data.sh: %w", err)
		}
		*version = strings.TrimSpace(string(b))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	src, err := cahyadsn.NewDirSource(*source, *province, *version)
	if err != nil {
		return err
	}

	// Tetap nil pada dry run; Run tidak menyentuh store dalam mode itu.
	var store ports.RegionStore
	if !*dryRun {
		conn, err := pgx.Connect(ctx, dbURL)
		if err != nil {
			return fmt.Errorf("koneksi database: %w", err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		store = postgres.NewRegionStore(conn)
	}

	rep, err := importregions.Run(ctx, src, store, importregions.Options{
		Province: *province, MaxSkipped: *maxSkipped, DryRun: *dryRun,
	})
	log.Info("ringkasan import",
		slog.String("source", src.Name()), slog.String("version", src.Version()),
		slog.Int("read", rep.Read), slog.Int("accepted", rep.Accepted),
		slog.Any("per_kind", rep.PerKind), slog.Any("skipped", rep.Skipped),
		slog.Any("repairs", rep.Repairs), slog.Any("result", rep.Result),
		slog.Bool("dry_run", *dryRun))
	for _, ex := range rep.SkipExamples {
		log.Warn("baris dilewati", slog.String("detail", ex))
	}
	if len(rep.Stale) > 0 {
		log.Warn("kode di database tidak ada di sumber; tinjau manual", slog.Any("codes", rep.Stale))
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("dibatalkan")
	}
	return err
}
