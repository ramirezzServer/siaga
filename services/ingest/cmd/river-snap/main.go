// Command river-snap memilih sel GloFAS untuk setiap titik pantau sungai
// (make river-snap). Membaca daftar titik berkoordinat perkiraan, meminta
// debit Open-Meteo untuk sel di sekitarnya, lalu menulis daftar titik untuk
// ingest dan laporan pemeriksaan.
//
//	river-snap -src docs/calibration/titik-sungai-32.csv \
//	  -out services/ingest/internal/adapters/sitelist/data/rivers_32.csv \
//	  -report docs/calibration/titik-sungai.md
//
// Satu kali jalan untuk 38 titik memakai sekitar 800 "API call" Open-Meteo
// (kuota gratis 10.000/hari) dan butuh ±2 menit karena dibatasi 400 lokasi
// per menit.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/riversnap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "river-snap:", err)
		os.Exit(1)
	}
}

var province = regexp.MustCompile(`_([0-9]{2})\.csv$`)

func run() error {
	src := flag.String("src", "docs/calibration/titik-sungai-32.csv", "daftar titik berkoordinat perkiraan")
	out := flag.String("out", "services/ingest/internal/adapters/sitelist/data/rivers_32.csv", "daftar titik untuk ingest")
	report := flag.String("report", "docs/calibration/titik-sungai.md", "laporan kalibrasi (Markdown)")
	endpoint := flag.String("url", envOr("OPENMETEO_FLOOD_URL", openmeteo.DefaultFloodURL), "endpoint Flood API")
	flag.Parse()

	m := province.FindStringSubmatch(*out)
	if m == nil {
		return fmt.Errorf("nama file -out %q harus berakhiran _<kode provinsi>.csv", *out)
	}
	f, err := os.Open(*src)
	if err != nil {
		return err
	}
	stations, err := riversnap.ReadStations(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := &riversnap.Snapper{
		Endpoint: *endpoint, Fetch: httpfetch.New(60 * time.Second), Clock: sysclock.Clock{},
		Progress: func(done, total int) { fmt.Fprintf(os.Stderr, "request %d/%d\n", done, total) },
	}
	choices, err := s.Run(ctx, stations)
	if err != nil {
		return err
	}

	var sites, rep bytes.Buffer
	if err := riversnap.WriteSites(&sites, m[1], filepath.ToSlash(*src), choices); err != nil {
		return err
	}
	// Pastikan keluaran bisa dibaca ingest sebelum menimpa file lama.
	if _, err := sitelist.ParseRivers(sites.Bytes()); err != nil {
		return fmt.Errorf("keluaran tidak valid: %w", err)
	}
	if err := riversnap.WriteReport(&rep, filepath.ToSlash(*src), time.Now(), choices); err != nil {
		return err
	}
	if err := os.WriteFile(*out, sites.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*report, rep.Bytes(), 0o600); err != nil {
		return err
	}
	suspects := 0
	for _, c := range choices {
		if c.Suspect() {
			suspects++
		}
	}
	fmt.Fprintf(os.Stderr, "%d titik ditulis ke %s; %d perlu diperiksa manual (lihat %s)\n", len(choices), *out, suspects, *report)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
