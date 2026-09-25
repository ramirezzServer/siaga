// Command ingest mengambil data dari sumber eksternal secara berkala,
// mengarsipkan payload mentah, dan menerbitkan event raw.* ke NATS JetStream.
//
//	ingest                         # jalan terus (mode layanan)
//	ingest -once -publish=false    # rekam payload sekali ke arsip, tanpa NATS
//
// Konektor: gempa BMKG dan USGS (raw.quake.*), peringatan dini cuaca BMKG
// (bmkg-cap, raw.weather.bmkg), sapuan prakiraan BMKG per kelurahan/desa
// (bmkg-prakiraan, raw.forecast.bmkg), dan Open-Meteo: cuaca grid 0,25°
// (openmeteo-cuaca, raw.forecast.openmeteo), kualitas udara CAMS
// (openmeteo-udara, raw.aq.openmeteo), debit sungai GloFAS
// (openmeteo-sungai, raw.flood.openmeteo); stasiun kualitas udara OpenAQ
// (openaq-stasiun, raw.aq.openaq) dan titik panas NASA FIRMS
// (firms-*, raw.fire.firms). Dua yang terakhir butuh key gratis
// (OPENAQ_API_KEY, FIRMS_MAP_KEY); tanpa key, konektornya tidak dijalankan.
//
// Konfigurasi lewat environment variable; lihat config() di bawah.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
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
	"go.opentelemetry.io/otel"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/otelx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archiveurl"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/firms"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpstatus"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/jspub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/nopub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openaq"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/regionlist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/telemetry"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/usgs"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/capfeed"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/poll"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/stations"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/sweep"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/forecast"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

const service = "ingest"

// version diisi saat build image (-ldflags "-X main.version=..."); dipakai
// sebagai service.version di telemetri.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ingest:", err)
		os.Exit(1)
	}
}

type settings struct {
	natsURL string
	// archiveURL: folder, file:///folder, atau s3://bucket/awalan?endpoint=...
	// (adapters/archiveurl). Kosong = payload mentah tidak diarsipkan.
	archiveURL   string
	archiveCreds archiveurl.Credentials
	httpAddr     string
	connectors   []string
	bmkgBaseURL  string
	usgsURL      string
	logLevel     slog.Level

	capBaseURL   string
	capProvinces []string
	capLanguages []string

	forecastURL       string
	forecastProvinces []string
	forecastFocus     []string
	forecastInterval  time.Duration
	forecastLimit     int

	openMeteoWeatherURL string
	openMeteoAirURL     string
	openMeteoFloodURL   string
	openMeteoProvinces  []string

	// Key gratis; kosong berarti konektornya tidak dijalankan.
	openAQKey     string
	openAQBaseURL string
	openAQBox     area.Box
	firmsKey      string
	firmsBaseURL  string
	firmsBox      area.Box
}

func config(lookup envx.Lookup) (settings, error) {
	env := envx.NewReader(lookup)
	s := settings{
		natsURL:    env.Default("NATS_URL", "nats://127.0.0.1:4222"),
		archiveURL: strings.TrimSpace(env.Default("INGEST_ARCHIVE_URL", "")),
		archiveCreds: archiveurl.Credentials{
			AccessKeyID:     strings.TrimSpace(env.Default("ARCHIVE_S3_ACCESS_KEY_ID", "")),
			SecretAccessKey: strings.TrimSpace(env.Default("ARCHIVE_S3_SECRET_ACCESS_KEY", "")),
		},
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
		forecastFocus:       list(env.Default("INGEST_FORECAST_FOCUS", "32.73,32.04,32.17,32.77")),
		openMeteoWeatherURL: env.Default("OPENMETEO_WEATHER_URL", openmeteo.DefaultWeatherURL),
		openMeteoAirURL:     env.Default("OPENMETEO_AIR_URL", openmeteo.DefaultAirURL),
		openMeteoFloodURL:   env.Default("OPENMETEO_FLOOD_URL", openmeteo.DefaultFloodURL),
		openMeteoProvinces:  list(env.Default("INGEST_OPENMETEO_PROVINCES", "32")),
		openAQKey:           strings.TrimSpace(env.Default("OPENAQ_API_KEY", "")),
		openAQBaseURL:       env.Default("OPENAQ_BASE_URL", openaq.DefaultBaseURL),
		firmsKey:            strings.TrimSpace(env.Default("FIRMS_MAP_KEY", "")),
		firmsBaseURL:        env.Default("FIRMS_BASE_URL", firms.DefaultBaseURL),
	}
	var errs []error
	// INGEST_ARCHIVE_DIR (fase 1a–1d) tetap diterima sebagai folder arsip.
	switch dir := strings.TrimSpace(env.Default("INGEST_ARCHIVE_DIR", "")); {
	case dir != "" && s.archiveURL != "":
		errs = append(errs, errors.New("isi salah satu saja: INGEST_ARCHIVE_URL atau INGEST_ARCHIVE_DIR"))
	case dir != "":
		s.archiveURL = dir
	}
	level, err := logx.ParseLevel(env.Default("LOG_LEVEL", "info"))
	errs = append(errs, err)
	s.logLevel = level
	if s.forecastInterval, err = time.ParseDuration(env.Default("INGEST_FORECAST_INTERVAL", "6h")); err != nil || s.forecastInterval < time.Minute {
		errs = append(errs, fmt.Errorf("INGEST_FORECAST_INTERVAL harus durasi >= 1m: %w", err))
	}
	if s.forecastLimit, err = strconv.Atoi(env.Default("INGEST_FORECAST_LIMIT", "0")); err != nil || s.forecastLimit < 0 {
		errs = append(errs, fmt.Errorf("INGEST_FORECAST_LIMIT harus bilangan >= 0: %w", err))
	}
	if s.openAQBox, err = area.ParseBox(env.Default("INGEST_OPENAQ_BBOX", area.JawaBarat.String())); err != nil {
		errs = append(errs, fmt.Errorf("INGEST_OPENAQ_BBOX: %w", err))
	}
	if s.firmsBox, err = area.ParseBox(env.Default("INGEST_FIRMS_BBOX", area.JawaBarat.String())); err != nil {
		errs = append(errs, fmt.Errorf("INGEST_FIRMS_BBOX: %w", err))
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
		// Per request HTTP; kuota Open-Meteo dihitung per titik (ADR 0011).
		{"openmeteo", 10, 3},
		// OpenAQ: 60/menit dan 2.000/jam per key; FIRMS: 5.000 transaksi per
		// 10 menit per MAP_KEY (ADR 0013). Jauh di bawah keduanya.
		{"openaq", 30, 3},
		{"firms", 10, 4},
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

// Interval Open-Meteo (dokumen arsitektur): grid cuaca dan udara tiap jam,
// titik pantau sungai tiap 6 jam (GloFAS diperbarui sekali sehari).
const (
	openMeteoGridInterval  = time.Hour
	openMeteoFloodInterval = 6 * time.Hour
)

// openMeteoConnectors membuat konektor Open-Meteo untuk provinsi yang
// dikonfigurasi. Satu konektor memuat semua titik semua provinsi.
func openMeteoConnectors(s settings, now func() time.Time) ([]connectorSpec, error) {
	if len(s.openMeteoProvinces) == 0 {
		return nil, nil
	}
	var grid, rivers []series.Site
	for _, p := range s.openMeteoProvinces {
		g, err := sitelist.Grid(p)
		if err != nil {
			return nil, err
		}
		r, err := sitelist.Rivers(p)
		if err != nil {
			return nil, err
		}
		grid, rivers = append(grid, g...), append(rivers, r...)
	}
	weather, err := openmeteo.NewWeatherConnector(s.openMeteoWeatherURL, grid, now)
	if err != nil {
		return nil, err
	}
	air, err := openmeteo.NewAirQualityConnector(s.openMeteoAirURL, grid, now)
	if err != nil {
		return nil, err
	}
	flood, err := openmeteo.NewDischargeConnector(s.openMeteoFloodURL, rivers, now)
	if err != nil {
		return nil, err
	}
	return []connectorSpec{
		{conn: weather, interval: openMeteoGridInterval, budget: "openmeteo"},
		{conn: air, interval: openMeteoGridInterval, budget: "openmeteo"},
		{conn: flood, interval: openMeteoFloodInterval, budget: "openmeteo"},
	}, nil
}

// Interval polling RSS peringatan dini (dokumen arsitektur: 2 menit).
const capInterval = 2 * time.Minute

// Interval OpenAQ dan FIRMS (dokumen arsitektur): stasiun udara tiap 15 menit,
// titik panas tiap 30 menit.
const (
	openAQInterval = 15 * time.Minute
	firmsInterval  = 30 * time.Minute
)

// keyedSources membuat konektor yang butuh key. Konektor tanpa key tidak
// dijalankan, kecuali diminta eksplisit lewat INGEST_CONNECTORS (galat).
func keyedSources(cfg settings, d deps, explicit func(string) bool) ([]runner.Job, error) {
	var jobs []runner.Job
	var missing []string
	switch {
	case cfg.firmsKey != "":
		conns, err := firms.NewConnectors(cfg.firmsBaseURL, cfg.firmsKey, cfg.firmsBox)
		if err != nil {
			return nil, err
		}
		for _, c := range conns {
			jobs = append(jobs, runner.Job{
				Poller:   poll.New(c, d.fetch, d.archive, d.pub, d.clock),
				Interval: firmsInterval,
				Limiters: []*runner.Limiter{d.limiters["firms"]},
			})
		}
	case explicit("firms"):
		missing = append(missing, "FIRMS_MAP_KEY")
	default:
		d.log.Warn("FIRMS_MAP_KEY kosong; konektor titik panas FIRMS tidak dijalankan")
	}
	switch {
	case cfg.openAQKey != "":
		src, err := openaq.New(cfg.openAQBaseURL, cfg.openAQKey, cfg.openAQBox)
		if err != nil {
			return nil, err
		}
		p, err := stations.New(src, d.fetch, d.archive, d.pub, d.clock,
			runner.NewGate(d.clock, d.limiters["openaq"]), stations.DefaultOptions())
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, runner.Job{Poller: p, Interval: openAQInterval, Limiters: []*runner.Limiter{d.limiters["openaq"]}})
	case explicit("openaq"):
		missing = append(missing, "OPENAQ_API_KEY")
	default:
		d.log.Warn("OPENAQ_API_KEY kosong; konektor stasiun OpenAQ tidak dijalankan")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("INGEST_CONNECTORS meminta konektor yang butuh key, tetapi %s kosong", strings.Join(missing, " dan "))
	}
	return jobs, nil
}

// openArchive membuka arsip payload mentah dan memastikan bisa ditulis.
// Arsip yang tidak bisa dibuka menggagalkan start (salah konfigurasi harus
// kelihatan); galat arsip saat berjalan hanya dicatat per polling.
func openArchive(ctx context.Context, cfg settings, log *slog.Logger) (ports.Archive, error) {
	if cfg.archiveURL == "" {
		log.Warn("INGEST_ARCHIVE_URL kosong; payload mentah tidak diarsipkan")
		return nil, nil
	}
	store, err := archiveurl.Open(cfg.archiveURL, cfg.archiveCreds, nil)
	if err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := store.Check(checkCtx); err != nil {
		return nil, err
	}
	log.Info("arsip payload aktif", slog.String("lokasi", store.Location()))
	return store, nil
}

// deps adalah adapter yang dipakai bersama semua konektor.
type deps struct {
	fetch    ports.Fetcher
	archive  ports.Archive
	pub      ports.Publisher
	clock    ports.Clock
	log      *slog.Logger
	limiters map[string]*runner.Limiter
	// sweepObserve membuat pengamat per konektor sapuan; boleh nil.
	sweepObserve func(connector string) sweep.ItemObserver
}

// plan merakit semua job polling dan sapuan sesuai konfigurasi.
func plan(cfg settings, d deps) ([]runner.Job, []*sweep.Sweeper, error) {
	enabled := func(name string) bool { return len(cfg.connectors) == 0 || slices.Contains(cfg.connectors, name) }
	om, err := openMeteoConnectors(cfg, d.clock.Now)
	if err != nil {
		return nil, nil, err
	}
	var jobs []runner.Job
	for _, spec := range append(connectors(cfg), om...) {
		if !enabled(spec.conn.Name()) {
			continue
		}
		jobs = append(jobs, runner.Job{
			Poller:   poll.New(spec.conn, d.fetch, d.archive, d.pub, d.clock),
			Interval: spec.interval,
			Limiters: []*runner.Limiter{d.limiters[spec.budget]},
		})
	}
	explicit := func(prefix string) bool {
		return slices.ContainsFunc(cfg.connectors, func(c string) bool { return strings.HasPrefix(c, prefix) })
	}
	keyed, err := keyedSources(cfg, d, explicit)
	if err != nil {
		return nil, nil, err
	}
	for _, j := range keyed {
		if enabled(j.Poller.Name()) {
			jobs = append(jobs, j)
		}
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
		if d.sweepObserve != nil {
			opts.Observe = d.sweepObserve(fcSrc.Name())
		}
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tel, err := otelx.Setup(ctx, otelx.Options{Service: service, Version: version, Log: logx.New(os.Stderr, service, cfg.logLevel)})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tel.Shutdown(shutdownCtx)
	}()
	log := logx.New(os.Stderr, service, cfg.logLevel, tel.LogHandler())
	inst, err := telemetry.New(otel.GetTracerProvider(), otel.GetMeterProvider())
	if err != nil {
		return err
	}

	archive, err := openArchive(ctx, cfg, log)
	if err != nil {
		return err
	}
	archive = inst.Archive(archive)
	if archive == nil && !*publish {
		return errors.New("mode rekam (-publish=false) butuh INGEST_ARCHIVE_URL")
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
		if _, err := natsx.ObserveStreams(otel.GetMeterProvider().Meter(telemetry.Scope), js, streams.Raw.Name); err != nil {
			return err
		}
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
		fetch: inst.Fetcher(httpfetch.New(20 * time.Second)), archive: archive, pub: pub, clock: clock, log: log,
		limiters: limiters, sweepObserve: inst.SweepItem,
	})
	if err != nil {
		return err
	}

	r := runner.New(clock, log, runner.Options{MaxBackoff: 10 * time.Minute, JitterFrac: 0.1, Observe: inst.Poll})
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
	budgetList := make([]*runner.Limiter, 0, len(limiters))
	for _, name := range slices.Sorted(maps.Keys(limiters)) {
		budgetList = append(budgetList, limiters[name])
	}
	if _, err := inst.Observe(telemetry.Sources{
		Connectors: r.Snapshot, Sweeps: sweepStatus, Limiters: budgetList, Now: clock.Now,
	}); err != nil {
		return err
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
	log.Info("ingest berjalan", slog.Any("connectors", names), slog.Bool("publish", *publish),
		slog.Bool("trace", tel.Enabled(otelx.Traces)), slog.Bool("metrics", tel.Enabled(otelx.Metrics)), slog.Bool("logs", tel.Enabled(otelx.Logs)))
	var wg sync.WaitGroup
	for _, sw := range sweepers {
		wg.Go(func() { sw.Run(ctx) })
	}
	r.Run(ctx, jobs)
	wg.Wait()
	log.Info("ingest berhenti")
	return nil
}
