// Command river-snap memilih sel GloFAS untuk setiap titik pantau sungai
// (make river-snap). Membaca daftar titik berkoordinat perkiraan, meminta
// debit reanalisis GloFAS periode acuan untuk sel di sekitarnya, lalu menulis
// daftar titik untuk ingest dan laporan pemeriksaan (ADR 0019).
//
//	river-snap -src docs/calibration/titik-sungai-32.csv \
//	  -out services/ingest/internal/adapters/sitelist/data/rivers_32.csv \
//	  -report docs/calibration/titik-sungai.md
//
// Jalan pertama untuk 38 titik dengan periode acuan bawaan (730 hari, bobot
// ±5,2 per lokasi) memakai ±4.000 "API call" Open-Meteo (kuota gratis
// 10.000/hari, ingest memakai ±3.500) dan butuh ±10 menit karena dibatasi 400
// panggilan per menit. Jawaban disimpan di -cache, jadi jalan ulang (misal
// setelah titik diverifikasi manual) tidak memakai kuota.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
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
	geo := flag.String("geojson", "", "peta pemeriksaan GeoJSON untuk geojson.io (kosong = tidak ditulis)")
	endpoint := flag.String("url", envOr("OPENMETEO_FLOOD_URL", openmeteo.DefaultFloodURL), "endpoint Flood API")
	def := riversnap.DefaultWindow()
	model := flag.String("model", def.Model, "model reanalisis Open-Meteo Flood API")
	from := flag.String("from", def.From.Format(time.DateOnly), "awal periode acuan (UTC, inklusif)")
	to := flag.String("to", def.To.Format(time.DateOnly), "akhir periode acuan (UTC, inklusif)")
	cachePath := flag.String("cache", "", "file cache debit per sel (kosong = .cache/river-snap/glofas-<model>-<from>-<to>.json)")
	maxCalls := flag.Float64("max-calls", 4500, "batas panggilan Open-Meteo baru dalam satu kali jalan (0 = tanpa batas)")
	flag.Parse()

	win, err := window(*model, *from, *to)
	if err != nil {
		return err
	}
	if *cachePath == "" {
		*cachePath = filepath.Join(".cache", "river-snap", fmt.Sprintf("glofas-%s-%s-%s.json", win.Model, *from, *to))
	}

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
	cache, err := loadCache(*cachePath, win)
	if err != nil {
		return err
	}
	s := &riversnap.Snapper{
		Endpoint: *endpoint, Fetch: httpfetch.New(120 * time.Second), Clock: sysclock.Clock{},
		Window: win, Cache: cache, MaxCalls: *maxCalls,
		Progress:   func(done, total int) { fmt.Fprintf(os.Stderr, "request %d/%d\n", done, total) },
		Checkpoint: func(c *riversnap.Cache) error { return saveCache(*cachePath, c) },
	}
	plan := s.Plan(stations)
	fmt.Fprintf(os.Stderr, "periode acuan %s: %d titik permintaan, %d dari cache %s, perlu ±%.0f panggilan Open-Meteo\n",
		win, plan.Points, plan.Cached, *cachePath, math.Ceil(plan.Calls))
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
	if err := riversnap.WriteReport(&rep, filepath.ToSlash(*src), time.Now(), win, choices); err != nil {
		return err
	}
	if *geo != "" {
		var g bytes.Buffer
		if err := riversnap.WriteGeoJSON(&g, choices); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*geo), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(*geo, g.Bytes(), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "peta pemeriksaan: %s (buka di https://geojson.io)\n", *geo)
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

func window(model, from, to string) (riversnap.Window, error) {
	f, err1 := time.Parse(time.DateOnly, from)
	t, err2 := time.Parse(time.DateOnly, to)
	if err := errors.Join(err1, err2); err != nil {
		return riversnap.Window{}, fmt.Errorf("periode acuan: %w", err)
	}
	w := riversnap.Window{Model: model, From: f, To: t}
	return w, w.Validate()
}

// loadCache membaca cache untuk win; file yang belum ada berarti cache kosong.
func loadCache(path string, win riversnap.Window) (*riversnap.Cache, error) {
	f, err := os.Open(path) //nolint:gosec // path dari flag -cache operator
	if errors.Is(err, fs.ErrNotExist) {
		return riversnap.NewCache(win), nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return riversnap.ReadCache(f, win)
}

// saveCache menulis cache lewat file sementara lalu rename, jadi file tetap
// utuh bila proses berhenti di tengah.
func saveCache(path string, c *riversnap.Cache) error {
	var b bytes.Buffer
	if err := c.Write(&b); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
