// Command geo-processor mengonsumsi event raw.* dari NATS JetStream,
// mengelompokkan laporan BMKG dan USGS menjadi kejadian gempa, menyusun pesan
// CAP BMKG menjadi kejadian cuaca, menghitung wilayah terdampak, menyimpannya
// di schema hazard, dan menerbitkan hazard.* lewat outbox transaksional.
// Prakiraan cuaca, kualitas udara, dan debit sungai disimpan ke hypertable
// schema ts.
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
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/rawseries"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/rawweather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/quakes"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/relay"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/timeseries"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/warnings"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
)

const service = "geo-processor"

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
	weather     weather.Policy
}

func config(lookup envx.Lookup) (settings, error) {
	env := envx.NewReader(lookup)
	s := settings{
		databaseURL: env.Required("DATABASE_URL"),
		natsURL:     env.Default("NATS_URL", "nats://127.0.0.1:4222"),
		httpAddr:    env.Default("GEO_HTTP_ADDR", "127.0.0.1:8082"),
		rules:       quake.DefaultRules(),
		weather:     weather.DefaultPolicy(),
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
	if v := env.Default("WEATHER_MIN_COVERAGE", ""); v != "" {
		s.weather.MinCoverage, err = strconv.ParseFloat(v, 64)
		errs = append(errs, err, s.weather.Validate())
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
	wsvc, err := warnings.New(postgres.NewWeatherStore(pool), hazardpb.WeatherEncoder{}, cfg.weather, time.Now)
	if err != nil {
		return err
	}
	// Outbox dipakai bersama semua jenis bahaya.
	outbox := relay.New(store, natsjs.NewPublisher(js, streams.Hazard.Name), log, time.Now, 100)
	quakeHandler, err := consume.New(rawquake.Decode, func(ctx context.Context, r quake.Report) (consume.Outcome, error) {
		res, err := svc.Process(ctx, r)
		return consume.Outcome{Changed: res.Changed, Created: res.Created, Updated: res.Updated, Ended: res.Ended}, err
	}, []error{quake.ErrInvalid}, consume.DefaultOptions(), time.Now)
	if err != nil {
		return err
	}
	weatherHandler, err := consume.New(rawweather.Decode, func(ctx context.Context, m weather.Message) (consume.Outcome, error) {
		res, err := wsvc.Process(ctx, m)
		if res.Ignored {
			log.Info("peringatan di luar wilayah pantauan diabaikan", slog.String("cap", m.Identifier))
		}
		return consume.Outcome{Changed: res.Changed, Created: res.Created, Updated: res.Updated, Ended: res.Ended}, err
	}, []error{weather.ErrInvalid}, consume.DefaultOptions(), time.Now)
	if err != nil {
		return err
	}
	tsvc := timeseries.New(postgres.NewSeriesStore(pool), time.Now)
	forecastBMKG, err := seriesHandler(rawseries.DecodeRegionForecast, tsvc.Weather)
	if err != nil {
		return err
	}
	forecastGrid, err := seriesHandler(rawseries.DecodeGridWeather, tsvc.Weather)
	if err != nil {
		return err
	}
	airQuality, err := seriesHandler(rawseries.DecodeAirQuality, tsvc.AirQuality)
	if err != nil {
		return err
	}
	discharge, err := seriesHandler(rawseries.DecodeDischarge, tsvc.Discharge)
	if err != nil {
		return err
	}
	consumers := []consumerJob{
		{spec: natsjs.QuakeConsumer, handler: quakeHandler},
		{spec: natsjs.WeatherConsumer, handler: weatherHandler},
		{spec: natsjs.ForecastBMKGConsumer, handler: forecastBMKG},
		{spec: natsjs.ForecastOpenMeteoConsumer, handler: forecastGrid},
		{spec: natsjs.AirQualityOpenMeteoConsumer, handler: airQuality},
		{spec: natsjs.FloodOpenMeteoConsumer, handler: discharge},
	}

	var consumersReady atomic.Int32
	checks := map[string]httpstatus.Check{
		"postgres": func(ctx context.Context) error { return pool.Ping(ctx) },
		"nats": func(context.Context) error {
			if nc.Status() != nats.CONNECTED {
				return fmt.Errorf("status %s", nc.Status())
			}
			return nil
		},
		"consumer": func(context.Context) error {
			if int(consumersReady.Load()) < len(consumers) {
				return errors.New("menunggu stream RAW dari ingest")
			}
			return nil
		},
	}
	status := func() any {
		return map[string]any{
			"consumer": quakeHandler.Snapshot(), "weather_consumer": weatherHandler.Snapshot(),
			"series_consumers": map[string]consume.Stats{
				natsjs.ForecastBMKGConsumer.Durable:        forecastBMKG.Snapshot(),
				natsjs.ForecastOpenMeteoConsumer.Durable:   forecastGrid.Snapshot(),
				natsjs.AirQualityOpenMeteoConsumer.Durable: airQuality.Snapshot(),
				natsjs.FloodOpenMeteoConsumer.Durable:      discharge.Snapshot(),
			},
			"outbox": outbox.Snapshot(), "rules": cfg.rules, "weather_policy": cfg.weather,
		}
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
	expired := func(kind string) func(n int, err error) {
		return func(n int, err error) {
			if err != nil {
				log.Warn("gagal mengakhiri kejadian kedaluwarsa", slog.String("kind", kind), slog.Any("error", err))
				return
			}
			log.Info("kejadian kedaluwarsa diakhiri", slog.String("kind", kind), slog.Int("count", n))
			outbox.Wake()
		}
	}
	wg.Go(func() { svc.RunExpiry(ctx, 30*time.Second, 100, expired("quake")) })
	wg.Go(func() { wsvc.RunExpiry(ctx, 30*time.Second, 100, expired("weather")) })
	for _, c := range consumers {
		wg.Go(func() {
			if err := consumeLoop(ctx, js, c, outbox.Wake, log, func() { consumersReady.Add(1) }); err != nil {
				log.Error("consumer berhenti", slog.String("consumer", c.spec.Durable), slog.Any("error", err))
				stop()
			}
		})
	}
	names := make([]string, len(consumers))
	for i, c := range consumers {
		names[i] = c.spec.Durable
	}
	log.Info("geo-processor berjalan", slog.Any("consumers", names),
		slog.String("dedup_cross_source", fmt.Sprintf("%+v", cfg.rules.CrossSource)),
		slog.Float64("weather_min_coverage", cfg.weather.MinCoverage))
	<-ctx.Done()
	wg.Wait()
	log.Info("geo-processor berhenti")
	return nil
}

// seriesHandler membuat handler consumer deret waktu: pesan yang melanggar
// invarian series langsung ke DLQ, galat database dicoba ulang.
func seriesHandler[R any](decode consume.Decoder[R], process func(context.Context, R) (timeseries.Result, error)) (*consume.Handler[R], error) {
	return consume.New(decode, func(ctx context.Context, r R) (consume.Outcome, error) {
		res, err := process(ctx, r)
		return consume.Outcome{Changed: res.Changed, Rows: res.Rows}, err
	}, []error{series.ErrInvalid}, consume.DefaultOptions(), time.Now)
}

// consumerJob adalah satu durable consumer beserta handler-nya.
type consumerJob struct {
	spec    natsjs.ConsumerSpec
	handler natsjs.Handler
}

// consumeLoop menyiapkan consumer (menunggu stream RAW bila ingest belum
// pernah jalan) lalu memproses pesan sampai ctx selesai.
func consumeLoop(ctx context.Context, js jetstream.JetStream, c consumerJob, after func(), log *slog.Logger, ready func()) error {
	for {
		cons, err := natsjs.NewConsumer(ctx, js, c.spec, service, c.handler, log, after)
		if err == nil {
			ready()
			return cons.Run(ctx)
		}
		if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return err
		}
		log.Warn("stream RAW belum ada; jalankan ingest. Dicoba lagi dalam 5 detik", slog.String("consumer", c.spec.Durable))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}
