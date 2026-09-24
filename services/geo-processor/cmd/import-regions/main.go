// Command import-regions memuat batas wilayah satu provinsi ke ref.region.
//
//	import-regions -source .cache/wilayah_boundaries/db -province 32
//	import-regions -dry-run -adm4-out adm4_32.txt   # daftar kelurahan/desa untuk sapuan prakiraan
//	import-regions -dry-run -grid-out grid025_32.txt # simpul grid 0,25° untuk Open-Meteo
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
	"strconv"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/cahyadsn"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/importregions"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
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
		adm4Out    = flag.String("adm4-out", "", "tulis daftar kode kelurahan/desa yang diterima ke file ini")
		gridOut    = flag.String("grid-out", "", "tulis simpul grid yang menyentuh kelurahan/desa ke file ini")
		gridStep   = flag.Float64("grid-step", 0.25, "jarak simpul grid dalam derajat untuk -grid-out")
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
		GridStep: gridStepFor(*gridOut, *gridStep),
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
	if err == nil && *adm4Out != "" {
		if err := writeVillages(*adm4Out, src, *province, rep.Villages); err != nil {
			return err
		}
		log.Info("daftar kelurahan/desa ditulis", slog.String("file", *adm4Out), slog.Int("codes", len(rep.Villages)))
	}
	if err == nil && *gridOut != "" {
		if err := writeGrid(*gridOut, src, *province, *gridStep, rep.Grid); err != nil {
			return err
		}
		log.Info("simpul grid ditulis", slog.String("file", *gridOut), slog.Int("nodes", len(rep.Grid)))
	}
	return err
}

// gridStepFor mengaktifkan perhitungan grid hanya bila -grid-out diisi.
func gridStepFor(out string, step float64) float64 {
	if out == "" {
		return 0
	}
	return step
}

// writeGrid menulis simpul grid untuk konektor Open-Meteo ingest, satu
// "lintang,bujur" per baris, diawali komentar asal data.
func writeGrid(path string, src *cahyadsn.DirSource, province string, step float64, nodes []region.GridNode) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Simpul grid %s° yang selnya menyentuh kelurahan/desa provinsi %s, untuk cuaca dan kualitas udara Open-Meteo.\n",
		strings.ReplaceAll(strconv.FormatFloat(step, 'f', -1, 64), ".", ","), province)
	fmt.Fprintf(&b, "# Dibuat `make grid-list` dari %s@%.12s; jangan diedit manual.\n", src.Name(), src.Version())
	fmt.Fprintf(&b, "# %d titik\n", len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&b, "%.2f,%.2f\n", n.Lat(), n.Lon())
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("menulis %s: %w", path, err)
	}
	return nil
}

// writeVillages menulis daftar kode adm4 untuk sapuan prakiraan ingest,
// diawali komentar asal data supaya perubahan terlihat jelas di diff.
func writeVillages(path string, src *cahyadsn.DirSource, province string, codes []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Kode kelurahan/desa (adm4) provinsi %s untuk sapuan prakiraan cuaca BMKG.\n", province)
	fmt.Fprintf(&b, "# Dibuat `make adm4-list` dari %s@%.12s; jangan diedit manual.\n", src.Name(), src.Version())
	fmt.Fprintf(&b, "# %d kode\n", len(codes))
	for _, c := range codes {
		b.WriteString(c)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("menulis %s: %w", path, err)
	}
	return nil
}
