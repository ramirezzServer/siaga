// Package otelx menyiapkan OpenTelemetry (trace, metrik, log) yang seragam
// untuk semua layanan Go SIAGA (ADR 0015).
//
// Konfigurasi memakai environment variable standar OpenTelemetry, jadi
// tujuan ekspor bisa diganti tanpa mengubah kode: Grafana lokal
// (`make obs-up`), OTel Collector di cluster, atau Grafana Cloud.
//
//	OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318   # semua sinyal
//	OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_ENDPOINT   # per sinyal
//	OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic%20…  # Grafana Cloud
//	OTEL_{TRACES,METRICS,LOGS}_EXPORTER=none            # matikan satu sinyal
//	OTEL_SDK_DISABLED=true                              # matikan semuanya
//	OTEL_RESOURCE_ATTRIBUTES, OTEL_SERVICE_NAME, OTEL_TRACES_SAMPLER,
//	OTEL_METRIC_EXPORT_INTERVAL                         # dibaca SDK langsung
//
// Tanpa endpoint, SDK tidak dipasang: tracer dan meter global tetap noop
// (tanpa biaya), tetapi propagator W3C tetap aktif sehingga traceparent dari
// pesan masuk tetap diteruskan ke pesan keluar.
package otelx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Namespace adalah service.namespace semua layanan SIAGA. Di Prometheus/Mimir
// label job menjadi "<namespace>/<service>", misal "siaga/ingest".
const Namespace = "siaga"

// Signal adalah satu jenis data telemetri.
type Signal string

// Sinyal yang didukung.
const (
	Traces  Signal = "traces"
	Metrics Signal = "metrics"
	Logs    Signal = "logs"
)

// Options mengatur Setup.
type Options struct {
	// Service adalah service.name, misal "ingest".
	Service string
	// Version adalah service.version; kosong = "dev".
	Version string
	// Environment adalah deployment.environment.name; kosong = SIAGA_ENV
	// atau "lokal".
	Environment string
	// Log menerima galat ekspor (misal collector mati). Nil = slog.Default().
	Log *slog.Logger
	// Getenv menggantikan os.Getenv di test. Exporter tetap membaca
	// environment proses secara langsung.
	Getenv func(string) string
}

// Telemetry adalah SDK yang terpasang. Nilai nol berarti telemetri mati.
type Telemetry struct {
	signals  map[Signal]bool
	logs     slog.Handler
	shutdown []func(context.Context) error
}

// Enabled melaporkan apakah sinyal diekspor.
func (t *Telemetry) Enabled(s Signal) bool { return t != nil && t.signals[s] }

// LogHandler mengembalikan handler slog yang mengirim log lewat OTLP, atau
// nil bila sinyal log mati. Dipakai sebagai handler tambahan logx.New.
func (t *Telemetry) LogHandler() slog.Handler {
	if t == nil {
		return nil
	}
	return t.logs
}

// Shutdown mengirim sisa data lalu mematikan SDK. Aman dipanggil pada nil.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	for i := len(t.shutdown) - 1; i >= 0; i-- {
		errs = append(errs, t.shutdown[i](ctx))
	}
	t.shutdown = nil
	return errors.Join(errs...)
}

// ErrProtocol dikembalikan bila OTEL_EXPORTER_OTLP_PROTOCOL bukan http/protobuf.
var ErrProtocol = errors.New("otelx: hanya OTLP http/protobuf yang didukung")

// Plan menentukan sinyal yang diekspor dari environment, tanpa memasang apa pun.
func Plan(getenv func(string) string) (map[Signal]bool, error) {
	out := map[Signal]bool{}
	if strings.EqualFold(strings.TrimSpace(getenv("OTEL_SDK_DISABLED")), "true") {
		return out, nil
	}
	generic := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != ""
	for _, s := range []Signal{Traces, Metrics, Logs} {
		up := strings.ToUpper(string(s))
		endpoint := generic || strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_"+up+"_ENDPOINT")) != ""
		exporter := strings.ToLower(strings.TrimSpace(getenv("OTEL_" + up + "_EXPORTER")))
		if !endpoint || exporter == "none" {
			continue
		}
		if exporter != "" && exporter != "otlp" {
			return nil, fmt.Errorf("otelx: OTEL_%s_EXPORTER=%q tidak didukung (otlp atau none)", up, exporter)
		}
		proto := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_" + up + "_PROTOCOL"))
		if proto == "" {
			proto = strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
		}
		if proto != "" && proto != "http/protobuf" {
			return nil, fmt.Errorf("%w (%s: %q)", ErrProtocol, s, proto)
		}
		out[s] = true
	}
	return out, nil
}

// Setup memasang propagator W3C, lalu tracer, meter, dan logger provider
// OTLP/HTTP untuk sinyal yang dikonfigurasi. Selalu panggil Shutdown saat
// layanan berhenti supaya data terakhir terkirim.
func Setup(ctx context.Context, opt Options) (*Telemetry, error) {
	if opt.Service == "" {
		return nil, errors.New("otelx: Service wajib diisi")
	}
	getenv := opt.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	log := opt.Log
	if log == nil {
		log = slog.Default()
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	signals, err := Plan(getenv)
	if err != nil {
		return nil, err
	}
	t := &Telemetry{signals: signals}
	if len(signals) == 0 {
		return t, nil
	}
	otel.SetErrorHandler(newErrorHandler(log, time.Now, time.Minute))

	res, err := Resource(ctx, opt, getenv)
	if err != nil {
		return nil, err
	}
	if err := t.install(ctx, opt.Service, res); err != nil {
		return nil, errors.Join(err, t.Shutdown(ctx))
	}
	return t, nil
}

func (t *Telemetry) install(ctx context.Context, service string, res *resource.Resource) error {
	if t.signals[Traces] {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return fmt.Errorf("otelx: exporter trace: %w", err)
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		otel.SetTracerProvider(tp)
		t.shutdown = append(t.shutdown, tp.Shutdown)
	}
	if t.signals[Metrics] {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return fmt.Errorf("otelx: exporter metrik: %w", err)
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)), sdkmetric.WithResource(res))
		otel.SetMeterProvider(mp)
		t.shutdown = append(t.shutdown, mp.Shutdown)
		// Metrik runtime Go: memori, goroutine, GC.
		if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
			return fmt.Errorf("otelx: metrik runtime: %w", err)
		}
	}
	if t.signals[Logs] {
		exp, err := otlploghttp.New(ctx)
		if err != nil {
			return fmt.Errorf("otelx: exporter log: %w", err)
		}
		lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)), sdklog.WithResource(res))
		t.shutdown = append(t.shutdown, lp.Shutdown)
		t.logs = otelslog.NewHandler(service, otelslog.WithLoggerProvider(lp))
	}
	return nil
}

// Resource menyusun atribut layanan: bawaan SDK, lalu atribut SIAGA, lalu
// OTEL_RESOURCE_ATTRIBUTES dan OTEL_SERVICE_NAME (yang terakhir menang).
func Resource(ctx context.Context, opt Options, getenv func(string) string) (*resource.Resource, error) {
	version := opt.Version
	if version == "" {
		version = "dev"
	}
	env := opt.Environment
	if env == "" {
		env = strings.TrimSpace(getenv("SIAGA_ENV"))
	}
	if env == "" {
		env = "lokal"
	}
	host, _ := os.Hostname()
	instance := opt.Service + "-" + strconv.Itoa(os.Getpid())
	if host != "" {
		instance = host + "-" + strconv.Itoa(os.Getpid())
	}
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(opt.Service),
			semconv.ServiceNamespace(Namespace),
			semconv.ServiceVersion(version),
			semconv.ServiceInstanceID(instance),
			semconv.DeploymentEnvironmentNameKey.String(env),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("otelx: resource: %w", err)
	}
	return res, nil
}

// errorHandler mencatat galat SDK (misal collector tidak terjangkau) ke log,
// paling sering sekali per interval untuk pesan yang sama supaya log tidak
// banjir saat collector mati lama.
type errorHandler struct {
	log      *slog.Logger
	now      func() time.Time
	interval time.Duration

	mu   sync.Mutex
	last map[string]time.Time
	// dropped menghitung pesan yang ditahan sejak terakhir dicatat.
	dropped map[string]int
}

func newErrorHandler(log *slog.Logger, now func() time.Time, interval time.Duration) *errorHandler {
	return &errorHandler{log: log, now: now, interval: interval, last: map[string]time.Time{}, dropped: map[string]int{}}
}

// Handle memenuhi otel.ErrorHandler.
func (h *errorHandler) Handle(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	now := h.now()
	h.mu.Lock()
	last, seen := h.last[msg]
	if seen && now.Sub(last) < h.interval {
		h.dropped[msg]++
		h.mu.Unlock()
		return
	}
	dropped := h.dropped[msg]
	h.last[msg], h.dropped[msg] = now, 0
	// Batasi jumlah pesan berbeda yang diingat.
	if len(h.last) > 256 {
		clear(h.last)
		clear(h.dropped)
	}
	h.mu.Unlock()
	h.log.Warn("galat OpenTelemetry; telemetri mungkin tidak terkirim", slog.String("error", msg), slog.Int("ditahan", dropped))
}
