// Command ingest mengambil data dari sumber eksternal secara berkala,
// mengarsipkan payload mentah, dan menerbitkan event raw.* ke NATS JetStream.
//
//	ingest                         # jalan terus (mode layanan)
//	ingest -once -publish=false    # rekam payload sekali ke arsip, tanpa NATS
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
	"strings"
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
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/usgs"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
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
}

func config(lookup envx.Lookup) (settings, error) {
	env := envx.NewReader(lookup)
	s := settings{
		natsURL:     env.Default("NATS_URL", "nats://127.0.0.1:4222"),
		archiveDir:  env.Default("INGEST_ARCHIVE_DIR", ""),
		httpAddr:    env.Default("INGEST_HTTP_ADDR", "127.0.0.1:8081"),
		bmkgBaseURL: env.Default("BMKG_TEWS_BASE_URL", bmkg.DefaultTEWSBaseURL),
		usgsURL:     env.Default("USGS_SUMMARY_URL", usgs.DefaultSummaryURL),
	}
	if v := env.Default("INGEST_CONNECTORS", ""); v != "" {
		for c := range strings.SplitSeq(v, ",") {
			if c = strings.TrimSpace(c); c != "" {
				s.connectors = append(s.connectors, c)
			}
		}
	}
	level, err := logx.ParseLevel(env.Default("LOG_LEVEL", "info"))
	if err != nil {
		return s, err
	}
	s.logLevel = level
	return s, env.Err()
}

// Anggaran request per sumber. BMKG membatasi 60 request/menit per IP untuk
// semua endpoint sekaligus; 55/menit dengan burst 5 menjamin paling banyak 59
// request dalam jendela 60 detik mana pun (lihat domain/ratelimit).
func budgets() (map[string]*runner.Limiter, error) {
	out := map[string]*runner.Limiter{}
	for _, b := range []struct {
		name             string
		perMinute, burst int
	}{
		{"bmkg", 55, 5},
		{"usgs", 6, 2},
	} {
		l, err := runner.NewLimiter(b.name, b.perMinute, b.burst)
		if err != nil {
			return nil, err
		}
		out[b.name] = l
	}
	return out, nil
}

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

func run() error {
	once := flag.Bool("once", false, "polling setiap konektor sekali lalu keluar")
	publish := flag.Bool("publish", true, "terbitkan event ke NATS; false untuk merekam payload saja")
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
	fetch := httpfetch.New(20 * time.Second)
	clock := sysclock.Clock{}
	var jobs []runner.Job
	for _, spec := range connectors(cfg) {
		if len(cfg.connectors) > 0 && !slices.Contains(cfg.connectors, spec.conn.Name()) {
			continue
		}
		jobs = append(jobs, runner.Job{
			Poller:   poll.New(spec.conn, fetch, archive, pub, clock),
			Interval: spec.interval,
			Limiters: []*runner.Limiter{limiters[spec.budget]},
		})
	}
	if len(jobs) == 0 {
		return fmt.Errorf("tidak ada konektor yang cocok dengan INGEST_CONNECTORS=%v", cfg.connectors)
	}

	r := runner.New(clock, log, runner.Options{MaxBackoff: 10 * time.Minute, JitterFrac: 0.1})
	if *once {
		return r.RunOnce(ctx, jobs)
	}

	if cfg.httpAddr != "" {
		srv := &http.Server{
			Addr:              cfg.httpAddr,
			Handler:           httpstatus.Handler(ready, r.Snapshot, time.Now),
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

	names := make([]string, len(jobs))
	for i, j := range jobs {
		names[i] = j.Poller.Name()
	}
	log.Info("ingest berjalan", slog.Any("connectors", names), slog.Bool("publish", *publish))
	r.Run(ctx, jobs)
	log.Info("ingest berhenti")
	return nil
}
