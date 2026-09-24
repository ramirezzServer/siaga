// Command geo-processor mengonsumsi event raw.quake.* dari NATS JetStream,
// mengelompokkan laporan BMKG dan USGS menjadi kejadian gempa, menghitung
// wilayah terdampak, menyimpannya di schema hazard, dan menerbitkan
// hazard.quake.* lewat outbox transaksional.
//
// Konfigurasi lewat environment variable; lihat config() di bawah.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/hazardpb"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/httpstatus"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/natsjs"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/rawquake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/quakes"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/relay"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

const (
	service = "geo-processor"
	durable = "geo-processor-quake"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "geo-processor:", err)
		os.Exit(1)
	}
}

type settings struct {
	databaseURL string
	natsURL     string
	httpAddr    string
	logLevel    slog.Level
	rules       quake.Rules
}

func config(lookup envx.Lookup) (settings, error) {
	env := envx.NewReader(lookup)
	s := settings{
		databaseURL: env.Required("DATABASE_URL"),
		natsURL:     env.Default("NATS_URL", "nats://127.0.0.1:4222"),
		httpAddr:    env.Default("GEO_HTTP_ADDR", "127.0.0.1:8082"),
		rules:       quake.DefaultRules(),
	}
	var errs []error
	level, err := logx.ParseLevel(env.Default("LOG_LEVEL", "info"))
	errs = append(errs, err)
	s.logLevel = level
	if v := env.Default("QUAKE_DEDUP_CROSS_SOURCE", ""); v != "" {
		s.rules.CrossSource, err = parseRule(v)
		errs = append(errs, err)
	}
	if v := env.Default("QUAKE_DEDUP_REVISION", ""); v != "" {
		s.rules.Revision, err = parseRule(v)
		errs = append(errs, err)
	}
	errs = append(errs, env.Err())
	return s, errors.Join(errs...)
}

// parseRule membaca ambang "<selisih waktu>,<jarak km>,<selisih magnitudo>",
// misal "30s,100,1.0".
func parseRule(v string) (quake.Rule, error) {
	parts := strings.Split(v, ",")
	if len(parts) != 3 {
		return quake.Rule{}, fmt.Errorf("ambang %q harus berformat <durasi>,<km>,<magnitudo>", v)
	}
	dt, err1 := time.ParseDuration(strings.TrimSpace(parts[0]))
	km, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	dm, err3 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return quake.Rule{}, fmt.Errorf("ambang %q: %w", v, err)
	}
	r := quake.Rule{MaxTimeDelta: dt, MaxDistanceKm: km, MaxMagnitudeDelta: dm}
	return r, r.Validate()
}

func run() error {
	cfg, err := config(envx.OS)
	if err != nil {
		return err
	}
	log := logx.New(os.Stderr, service, cfg.logLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg, log)
}

// serve merakit dan menjalankan semua komponen sampai ctx selesai.
func serve(ctx context.Context, cfg settings, log *slog.Logger) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("konfigurasi database: %w", err)
	}
	defer pool.Close()

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
	_, err = natsx.EnsureStream(setupCtx, js, streams.Hazard, service)
	if err == nil {
		_, err = natsx.EnsureStream(setupCtx, js, streams.DLQ, service)
	}
	cancel()
	if err != nil {
		return err
	}

	store := postgres.NewQuakeStore(pool)
	svc, err := quakes.New(store, hazardpb.Encoder{}, cfg.rules, quake.DefaultPolicy(), time.Now)
	if err != nil {
		return err
	}
	outbox := relay.New(store, natsjs.NewPublisher(js, streams.Hazard.Name), log, time.Now, 100)
	handler, err := consume.New(rawquake.Decode, svc.Process, consume.DefaultOptions(), time.Now)
	if err != nil {
		return err
	}

	var consumerReady atomic.Bool
	checks := map[string]httpstatus.Check{
		"postgres": func(ctx context.Context) error { return pool.Ping(ctx) },
		"nats": func(context.Context) error {
			if nc.Status() != nats.CONNECTED {
				return fmt.Errorf("status %s", nc.Status())
			}
			return nil
		},
		"consumer": func(context.Context) error {
			if !consumerReady.Load() {
				return errors.New("menunggu stream RAW dari ingest")
			}
			return nil
		},
	}
	status := func() any {
		return map[string]any{"consumer": handler.Snapshot(), "outbox": outbox.Snapshot(), "rules": cfg.rules}
	}
	if cfg.httpAddr != "" {
		srv := &http.Server{
			Addr:              cfg.httpAddr,
			Handler:           httpstatus.Handler(checks, status, time.Now),
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

	var wg sync.WaitGroup
	wg.Go(func() { outbox.Run(ctx, time.Second) })
	wg.Go(func() {
		svc.RunExpiry(ctx, 30*time.Second, 100, func(n int, err error) {
			if err != nil {
				log.Warn("gagal mengakhiri kejadian kedaluwarsa", slog.Any("error", err))
				return
			}
			log.Info("kejadian kedaluwarsa diakhiri", slog.Int("count", n))
			outbox.Wake()
		})
	})
	wg.Go(func() {
		if err := consumeLoop(ctx, js, handler, outbox.Wake, log, func() { consumerReady.Store(true) }); err != nil {
			log.Error("consumer berhenti", slog.Any("error", err))
			stop()
		}
	})
	log.Info("geo-processor berjalan", slog.String("consumer", durable),
		slog.String("dedup_cross_source", fmt.Sprintf("%+v", cfg.rules.CrossSource)))
	<-ctx.Done()
	wg.Wait()
	log.Info("geo-processor berhenti")
	return nil
}

// consumeLoop menyiapkan consumer (menunggu stream RAW bila ingest belum
// pernah jalan) lalu memproses pesan sampai ctx selesai.
func consumeLoop(ctx context.Context, js jetstream.JetStream, h *consume.Handler, after func(), log *slog.Logger, ready func()) error {
	for {
		c, err := natsjs.NewConsumer(ctx, js, durable, service, h, log, after)
		if err == nil {
			ready()
			return c.Run(ctx)
		}
		if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return err
		}
		log.Warn("stream RAW belum ada; jalankan ingest. Dicoba lagi dalam 5 detik")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}
