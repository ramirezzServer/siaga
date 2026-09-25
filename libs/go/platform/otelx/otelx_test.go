package otelx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPlan(t *testing.T) {
	t.Parallel()
	all := map[Signal]bool{Traces: true, Metrics: true, Logs: true}
	cases := []struct {
		name string
		env  map[string]string
		want map[Signal]bool
	}{
		{"tanpa endpoint", nil, map[Signal]bool{}},
		{"endpoint umum", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318"}, all},
		{"per sinyal", map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://x/v1/traces"}, map[Signal]bool{Traces: true}},
		{"satu sinyal dimatikan", map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_LOGS_EXPORTER": "none",
		}, map[Signal]bool{Traces: true, Metrics: true}},
		{"SDK dimatikan", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_SDK_DISABLED": "TRUE"}, map[Signal]bool{}},
		{"protokol http eksplisit", map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf", "OTEL_TRACES_EXPORTER": "otlp",
		}, all},
	}
	for _, c := range cases {
		got, err := Plan(env(c.env))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, ingin %v", c.name, got, c.want)
		}
		for s := range c.want {
			if !got[s] {
				t.Errorf("%s: sinyal %s tidak aktif", c.name, s)
			}
		}
	}
}

func TestPlanRejectsUnsupported(t *testing.T) {
	t.Parallel()
	_, err := Plan(env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}))
	if !errors.Is(err, ErrProtocol) {
		t.Errorf("grpc: err = %v, ingin ErrProtocol", err)
	}
	_, err = Plan(env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/json"}))
	if !errors.Is(err, ErrProtocol) {
		t.Errorf("http/json: err = %v, ingin ErrProtocol", err)
	}
	if _, err := Plan(env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_TRACES_EXPORTER": "zipkin"})); err == nil {
		t.Error("exporter zipkin harus ditolak")
	}
}

func TestResource(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "k8s.pod.name=ingest-0")
	res, err := Resource(context.Background(), Options{Service: "ingest", Version: "1.2.3"}, env(map[string]string{"SIAGA_ENV": "produksi"}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[attribute.Key]string{}
	for _, kv := range res.Attributes() {
		got[kv.Key] = kv.Value.String()
	}
	want := map[attribute.Key]string{
		semconv.ServiceNameKey: "ingest", semconv.ServiceNamespaceKey: Namespace, semconv.ServiceVersionKey: "1.2.3",
		semconv.DeploymentEnvironmentNameKey: "produksi", "k8s.pod.name": "ingest-0", semconv.TelemetrySDKLanguageKey: "go",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, ingin %q", k, got[k], v)
		}
	}
	if got[semconv.ServiceInstanceIDKey] == "" {
		t.Error("service.instance.id kosong")
	}

	t.Setenv("OTEL_SERVICE_NAME", "ingest-uji")
	res, err = Resource(context.Background(), Options{Service: "ingest"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range res.Attributes() {
		switch kv.Key {
		case semconv.ServiceNameKey:
			if kv.Value.AsString() != "ingest-uji" {
				t.Errorf("OTEL_SERVICE_NAME harus menang, dapat %q", kv.Value.AsString())
			}
		case semconv.DeploymentEnvironmentNameKey:
			if kv.Value.AsString() != "lokal" {
				t.Errorf("lingkungan bawaan %q, ingin lokal", kv.Value.AsString())
			}
		case semconv.ServiceVersionKey:
			if kv.Value.AsString() != "dev" {
				t.Errorf("versi bawaan %q, ingin dev", kv.Value.AsString())
			}
		}
	}
}

// resetGlobals mengembalikan provider global ke noop setelah test.
func resetGlobals(t *testing.T) {
	t.Cleanup(func() {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
}

func TestSetupDisabled(t *testing.T) {
	resetGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	tel, err := Setup(context.Background(), Options{Service: "ingest"})
	if err != nil {
		t.Fatal(err)
	}
	if tel.Enabled(Traces) || tel.Enabled(Metrics) || tel.Enabled(Logs) || tel.LogHandler() != nil {
		t.Error("tanpa endpoint semua sinyal harus mati")
	}
	// Propagator tetap aktif supaya traceparent diteruskan.
	fields := otel.GetTextMapPropagator().Fields()
	if !strings.Contains(strings.Join(fields, ","), "traceparent") {
		t.Errorf("propagator W3C tidak terpasang: %v", fields)
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var nilTel *Telemetry
	if nilTel.Enabled(Traces) || nilTel.LogHandler() != nil || nilTel.Shutdown(context.Background()) != nil {
		t.Error("Telemetry nil harus aman dipakai")
	}
	if _, err := Setup(context.Background(), Options{}); err == nil {
		t.Error("Service kosong harus ditolak")
	}
}

// collector adalah penerima OTLP/HTTP tiruan yang mencatat path yang diterima.
type collector struct {
	mu    sync.Mutex
	paths map[string]int
	auth  []string
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.paths[r.URL.Path]++
	c.auth = append(c.auth, r.Header.Get("Authorization"))
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func TestSetupExportsAllSignals(t *testing.T) {
	resetGlobals(t)
	col := &collector{paths: map[string]int{}}
	srv := httptest.NewServer(col)
	t.Cleanup(srv.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Basic%20dWppOnVqaQ==")
	ctx := context.Background()

	tel, err := Setup(ctx, Options{Service: "ingest", Version: "uji"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []Signal{Traces, Metrics, Logs} {
		if !tel.Enabled(s) {
			t.Fatalf("sinyal %s mati", s)
		}
	}
	_, span := otel.Tracer("uji").Start(ctx, "polling")
	span.End()
	counter, err := otel.Meter("uji").Int64Counter("siaga.uji")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)
	slog.New(tel.LogHandler()).InfoContext(ctx, "halo")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tel.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	col.mu.Lock()
	defer col.mu.Unlock()
	for _, p := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		if col.paths[p] == 0 {
			t.Errorf("collector tidak menerima %s (diterima: %v)", p, col.paths)
		}
	}
	for _, a := range col.auth {
		if a != "Basic dWppOnVqaQ==" {
			t.Errorf("header OTEL_EXPORTER_OTLP_HEADERS tidak dikirim: %q", a)
		}
	}
}

func TestSetupRejectsGRPC(t *testing.T) {
	resetGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	if _, err := Setup(context.Background(), Options{Service: "ingest"}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, ingin ErrProtocol", err)
	}
}

func TestErrorHandlerThrottles(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	h := newErrorHandler(slog.New(slog.NewTextHandler(&buf, nil)), func() time.Time { return now }, time.Minute)
	h.Handle(nil)
	for range 5 {
		h.Handle(errors.New("collector mati"))
	}
	h.Handle(errors.New("galat lain"))
	if n := strings.Count(buf.String(), "collector mati"); n != 1 {
		t.Fatalf("pesan sama dicatat %d kali dalam satu menit, ingin 1", n)
	}
	now = now.Add(time.Minute)
	h.Handle(errors.New("collector mati"))
	if !strings.Contains(buf.String(), "ditahan=4") {
		t.Errorf("jumlah pesan yang ditahan tidak dilaporkan: %s", buf.String())
	}
	if strings.Count(buf.String(), "galat lain") != 1 {
		t.Error("pesan berbeda harus tetap dicatat")
	}
	for i := range 300 {
		h.Handle(errors.New(strings.Repeat("x", i)))
	}
	if len(h.last) > 257 {
		t.Errorf("pesan yang diingat tidak dibatasi: %d", len(h.last))
	}
}
