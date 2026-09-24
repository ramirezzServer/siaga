// Command ingest mengambil data dari sumber eksternal secara berkala,
// mengarsipkan payload mentah, dan menerbitkan event raw.* ke NATS JetStream.
//
//	ingest                         # jalan terus (mode layanan)
//	ingest -once -publish=false    # rekam payload sekali ke arsip, tanpa NATS
//
// Konektor: gempa BMKG dan USGS (raw.quake.*), peringatan dini cuaca BMKG
// (bmkg-cap, raw.weather.bmkg), dan sapuan prakiraan BMKG per kelurahan/desa
// (bmkg-prakiraan, raw.forecast.bmkg).
//
// Konfigurasi lewat environment variable; lihat config() di bawah.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpstatus"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/jspub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/nopub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/regionlist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/usgs"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/capfeed"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/sweep"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const service = "ingest"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ingest:", err)
		os.Exit(1)
	}
}

type settings struct {
	natsURL     string
	archiveDir  string
	httpAddr    string
	connectors  []string
	bmkgBaseURL string
	usgsURL     string
	logLevel    slog.Level

	capBaseURL   string
	capProvinces []string
	capLanguages []string

	forecastURL       string
	forecastProvinces []string
	forecastFocus     []string
	forecastInterval  time.Duration
	forecastLimit     int
}

func config(lookup envx.Lookup) (settings, error) {
	env := envx.NewReader(lookup)
	s := settings{
		natsURL:           env.Default("NATS_URL", "nats://127.0.0.1:4222"),
		archiveDir:        env.Default("INGEST_ARCHIVE_DIR", ""),
		httpAddr:          env.Default("INGEST_HTTP_ADDR", "127.0.0.1:8081"),
		bmkgBaseURL:       env.Default("BMKG_TEWS_BASE_URL", bmkg.DefaultTEWSBaseURL),
		usgsURL:           env.Default("USGS_SUMMARY_URL", usgs.DefaultSummaryURL),
		connectors:        list(env.Default("INGEST_CONNECTORS", "")),
		capBaseURL:        env.Default("BMKG_CAP_BASE_URL", bmkg.DefaultCAPBaseURL),
		capProvinces:      list(env.Default("INGEST_CAP_PROVINCES", "32")),
		capLanguages:      list(env.Default("INGEST_CAP_LANGUAGES", "en")),
		forecastURL:       env.Default("BMKG_FORECAST_URL", bmkg.DefaultForecastURL),
		forecastProvinces: list(env.Default("INGEST_FORECAST_PROVINCES", "32")),
		// Zona fokus Bandung Raya lebih dulu: Kota Bandung, Kab. Bandung, Kab. Bandung Barat, Cimahi.
		forecastFocus: list(env.Default("INGEST_FORECAST_FOCUS", "32.73,32.04,32.17,32.77")),
	}
	var errs []error
	level, err := logx.ParseLevel(env.Default("LOG_LEVEL", "info"))
	errs = append(errs, err)
	s.logLevel = level
	if s.forecastInterval, err = time.ParseDuration(env.Default("INGEST_FORECAST_INTERVAL", "6h")); err != nil || s.forecastInterval < time.Minute {
		errs = append(errs, fmt.Errorf("INGEST_FORECAST_INTERVAL harus durasi >= 1m: %w", err))
	}
	if s.forecastLimit, err = strconv.Atoi(env.Default("INGEST_FORECAST_LIMIT", "0")); err != nil || s.forecastLimit < 0 {
		errs = append(errs, fmt.Errorf("INGEST_FORECAST_LIMIT harus bilangan >= 0: %w", err))
	}
	errs = append(errs, env.Err())
	return s, errors.Join(errs...)
}

// list memecah daftar dipisah koma, membuang spasi dan elemen kosong.
func list(v string) []string {
	var out []string
	for c := range strings.SplitSeq(v, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// Anggaran request per sumber. BMKG membatasi 60 request/menit per IP untuk
// semua endpoint sekaligus; 55/menit dengan burst 5 menjamin paling banyak 59
// request dalam jendela 60 detik mana pun (lihat domain/ratelimit). Sapuan
// prakiraan punya anggaran sendiri 50/menit dan hanya memakai sisa anggaran
// BMKG dengan cadangan forecastHeadroom izin untuk gempa dan peringatan dini
// (ADR 0009).
func budgets() (map[string]*runner.Limiter, error) {
	out := map[string]*runner.Limiter{}
	for _, b := range []struct {
		name             string
		perMinute, burst int
	}{
		{"bmkg", 55, 5},
		{"usgs", 6, 2},
		{"bmkg-prakiraan", 50, 1},
	} {
		l, err := runner.NewLimiter(b.name, b.perMinute, b.burst)
		if err != nil {
			return nil, err
		}
		out[b.name] = l
	}
	return out, nil
}

// forecastHeadroom adalah izin anggaran BMKG yang selalu disisakan sapuan
// prakiraan untuk request biasa (gempa, CAP).
const forecastHeadroom = 2

type connectorSpec struct {
	conn     ports.Connector
	interval time.Duration
	budget   string
}

func connectors(s settings) []connectorSpec {
	var out []connectorSpec
	for _, c := range bmkg.NewQuakeConnectors(s.bmkgBaseURL) {
		interval := time.Minute
		if c.Name() == "bmkg-autogempa" {
			interval = 30 * time.Second
		}
		out = append(out, connectorSpec{conn: c, interval: interval, budget: "bmkg"})
	}
	out = append(out, connectorSpec{conn: usgs.NewSummaryConnector(s.usgsURL), interval: time.Minute, budget: "usgs"})
	return out
}

// Interval polling RSS peringatan dini (dokumen arsitektur: 2 menit).
const capInterval = 2 * time.Minute

// deps adalah adapter yang dipakai bersama semua konektor.
type deps struct {
	fetch    ports.Fetcher
	archive  ports.Archive
	pub      ports.Publisher
	clock    ports.Clock
	log      *slog.Logger
	limiters map[string]*runner.Limiter
}

// plan merakit semua job polling dan sapuan sesuai konfigurasi.
func plan(cfg settings, d deps) ([]runner.Job, []*sweep.Sweeper, error) {
	enabled := func(name string) bool { return len(cfg.connectors) == 0 || slices.Contains(cfg.connectors, name) }
	var jobs []runner.Job
	for _, spec := range connectors(cfg) {
		if !enabled(spec.conn.Name()) {
			continue
		}
		jobs = append(jobs, runner.Job{
			Poller:   poll.New(spec.conn, d.fetch, d.archive, d.pub, d.clock),
			Interval: spec.interval,
			Limiters: []*runner.Limiter{d.limiters[spec.budget]},
		})
	}
	capSrc := bmkg.NewCAPFeed(cfg.capBaseURL)
	if enabled(capSrc.Name()) {
		jobs = append(jobs, runner.Job{
			Poller: capfeed.New(capSrc, d.fetch, d.archive, d.pub, d.clock,
				runner.NewGate(d.clock, d.limiters["bmkg"]),
				capfeed.Options{Provinces: cfg.capProvinces, Languages: cfg.capLanguages}),
			Interval: capInterval,
			Limiters: []*runner.Limiter{d.limiters["bmkg"]},
		})
	}

	var sweepers []*sweep.Sweeper
	fcSrc := bmkg.NewForecastSource(cfg.forecastURL)
	if enabled(fcSrc.Name()) && len(cfg.forecastProvinces) > 0 {
		var codes []string
		for _, p := range cfg.forecastProvinces {
			c, err := regionlist.Load(p)
			if err != nil {
				return nil, nil, err
			}
			codes = append(codes, c...)
		}
		gate, err := runner.NewGate(d.clock, d.limiters["bmkg-prakiraan"]).Low(d.limiters["bmkg"], forecastHeadroom)
		if err != nil {
			return nil, nil, err
		}
		opts := sweep.DefaultOptions()
		opts.Interval, opts.Limit = cfg.forecastInterval, cfg.forecastLimit
		sw, err := sweep.New(fcSrc, forecast.Order(codes, cfg.forecastFocus), d.fetch, d.archive, d.pub, d.clock, gate, d.log, opts)
		if err != nil {
			return nil, nil, err
		}
		sweepers = append(sweepers, sw)
	}
	if len(jobs) == 0 && len(sweepers) == 0 {
		return nil, nil, fmt.Errorf("tidak ada konektor yang cocok dengan INGEST_CONNECTORS=%v", cfg.connectors)
	}
	return jobs, sweepers, nil
}

func run() error {
	once := flag.Bool("once", false, "polling setiap konektor sekali lalu keluar")
	publish := flag.Bool("publish", true, "terbitkan event ke NATS; false untuk merekam payload saja")
	forecastSample := flag.Int("forecast-sample", 20, "jumlah kode prakiraan pada mode -once bila INGEST_FORECAST_LIMIT kosong")
	flag.Parse()

	cfg, err := config(envx.OS)
	if err != nil {
		return err
	}
	log := logx.New(os.Stderr, service, cfg.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var archive ports.Archive
	if cfg.archiveDir != "" {
		a, err := fsarchive.New(cfg.archiveDir)
		if err != nil {
			return err
		}
		archive = a
		log.Info("arsip payload aktif", slog.String("dir", a.Root()))
	} else if !*publish {
		return errors.New("mode rekam (-publish=false) butuh INGEST_ARCHIVE_DIR")
	} else {
		log.Warn("INGEST_ARCHIVE_DIR kosong; payload mentah tidak diarsipkan")
	}

	var pub ports.Publisher = &nopub.Publisher{}
	ready := func() bool { return true }
	if *publish {
		nc, err := natsx.Connect(cfg.natsURL, service, log)
		if err != nil {
			return err
		}
		defer func() { _ = nc.Drain() }()
		js, err := jetstream.New(nc)
		if err != nil {
			return fmt.Errorf("jetstream: %w", err)
		}
		setupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = natsx.EnsureStream(setupCtx, js, streams.Raw, service)
		cancel()
		if err != nil {
			return err
		}
		pub = jspub.New(js, streams.Raw.Name)
		ready = func() bool { return nc.Status() == nats.CONNECTED }
	}

	limiters, err := budgets()
	if err != nil {
		return err
	}
	if *once && cfg.forecastLimit == 0 {
		// Rekaman cukup contoh dari zona fokus; sapuan penuh butuh ±2 jam.
		cfg.forecastLimit = *forecastSample
	}
	clock := sysclock.Clock{}
	jobs, sweepers, err := plan(cfg, deps{
		fetch: httpfetch.New(20 * time.Second), archive: archive, pub: pub, clock: clock, log: log, limiters: limiters,
	})
	if err != nil {
		return err
	}

	r := runner.New(clock, log, runner.Options{MaxBackoff: 10 * time.Minute, JitterFrac: 0.1})
	if *once {
		errs := []error{r.RunOnce(ctx, jobs)}
		for _, sw := range sweepers {
			if _, err := sw.Sweep(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	sweepStatus := func() []sweep.Status {
		out := make([]sweep.Status, len(sweepers))
		for i, sw := range sweepers {
			out[i] = sw.Snapshot()
		}
		return out
	}
	if cfg.httpAddr != "" {
		srv := &http.Server{
			Addr:              cfg.httpAddr,
			Handler:           httpstatus.Handler(ready, r.Snapshot, sweepStatus, time.Now),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("server status berhenti", slog.Any("error", err))
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
		log.Info("endpoint status", slog.String("addr", "http://"+cfg.httpAddr+"/status"))
	}

	names := make([]string, 0, len(jobs)+len(sweepers))
	for _, j := range jobs {
		names = append(names, j.Poller.Name())
	}
	for _, sw := range sweepers {
		names = append(names, sw.Name())
	}
	log.Info("ingest berjalan", slog.Any("connectors", names), slog.Bool("publish", *publish))
	var wg sync.WaitGroup
	for _, sw := range sweepers {
		wg.Go(func() { sw.Run(ctx) })
	}
	r.Run(ctx, jobs)
	wg.Wait()
	log.Info("ingest berhenti")
	return nil
}
