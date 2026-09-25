package telemetry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/relay"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func collect(t *testing.T, r *sdkmetric.ManualReader) map[string]map[string]float64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]float64{}
	add := func(name string, attrs attribute.Set, v float64) {
		if out[name] == nil {
			out[name] = map[string]float64{}
		}
		key := ""
		for _, kv := range attrs.ToSlice() {
			key += fmt.Sprintf("%s=%s ", kv.Key, kv.Value.String())
		}
		out[name][key] += v
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					add(m.Name, dp.Attributes, float64(dp.Value))
				}
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					add(m.Name, dp.Attributes, float64(dp.Value))
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					add(m.Name, dp.Attributes, dp.Value)
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					add(m.Name, dp.Attributes, float64(dp.Count))
				}
			default:
				t.Fatalf("%s: %T", m.Name, m.Data)
			}
		}
	}
	return out
}

func TestObserve(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	m := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("uji")
	last := time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC)
	backlogErr := error(nil)
	if _, err := Observe(m, Sources{
		Consumers: map[string]func() consume.Stats{
			"geo-processor-quake": func() consume.Stats {
				return consume.Stats{Received: 10, Applied: 6, Unchanged: 2, Retried: 1, DeadLettered: 1, Created: 3, Updated: 2, Ended: 1, LastSuccess: last}
			},
			"geo-processor-fire-firms": func() consume.Stats { return consume.Stats{Received: 4, Rows: 160} },
		},
		Relay: func() relay.Stats { return relay.Stats{Published: 6} },
		Backlog: func(context.Context) (Backlog, error) {
			return Backlog{Pending: 2, Oldest: time.Now().Add(-90 * time.Second)}, backlogErr
		},
	}); err != nil {
		t.Fatal(err)
	}
	got := collect(t, reader)
	for name, want := range map[string]map[string]float64{
		"siaga.geo.messages": {
			"consumer=geo-processor-quake result=received ": 10, "consumer=geo-processor-quake result=dead_lettered ": 1,
			"consumer=geo-processor-quake result=unchanged ": 2, "consumer=geo-processor-fire-firms result=received ": 4,
		},
		"siaga.geo.hazard.changes":        {"change=created consumer=geo-processor-quake ": 3, "change=ended consumer=geo-processor-quake ": 1},
		"siaga.geo.series.rows":           {"consumer=geo-processor-fire-firms ": 160},
		"siaga.geo.consumer.last_success": {"consumer=geo-processor-quake ": float64(last.Unix())},
		"siaga.geo.outbox.published":      {"": 6},
		"siaga.geo.outbox.pending":        {"": 2},
	} {
		for attrs, v := range want {
			if got[name][attrs] != v {
				t.Errorf("%s{%s} = %v, ingin %v", name, attrs, got[name][attrs], v)
			}
		}
	}
	if age := got["siaga.geo.outbox.oldest_age"][""]; age < 89 || age > 120 {
		t.Errorf("umur outbox tertua %v", age)
	}
	if _, ok := got["siaga.geo.consumer.last_success"]["consumer=geo-processor-fire-firms "]; ok {
		t.Error("consumer tanpa sukses tidak boleh punya last_success")
	}

	// Outbox kosong: umur 0; galat database: gauge outbox dilewati.
	reader2 := sdkmetric.NewManualReader()
	m2 := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader2)).Meter("uji")
	calls := 0
	if _, err := Observe(m2, Sources{Backlog: func(context.Context) (Backlog, error) {
		calls++
		if calls > 1 {
			return Backlog{}, errors.New("database mati")
		}
		return Backlog{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if got := collect(t, reader2); got["siaga.geo.outbox.oldest_age"][""] != 0 || got["siaga.geo.outbox.pending"][""] != 0 {
		t.Errorf("outbox kosong %v", got)
	}
	if got := collect(t, reader2); len(got["siaga.geo.outbox.pending"]) != 0 {
		t.Errorf("galat database harus melewatkan gauge outbox: %v", got)
	}
}

func TestOutboxObserver(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	obs, err := OutboxObserver(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("uji"))
	if err != nil {
		t.Fatal(err)
	}
	obs(context.Background(), ports.OutboxMessage{Subject: "hazard.quake.created", CreatedAt: time.Now().Add(-time.Second)}, nil)
	obs(context.Background(), ports.OutboxMessage{Subject: "hazard.quake.created", CreatedAt: time.Now()}, errors.New("broker mati"))
	obs(context.Background(), ports.OutboxMessage{Subject: "hazard.quake.created"}, nil) // tanpa waktu tulis: dilewati
	got := collect(t, reader)["siaga.geo.outbox.wait"]
	if got["result=ok subject=hazard.quake.created "] != 1 || got["result=error subject=hazard.quake.created "] != 1 {
		t.Errorf("outbox.wait %v", got)
	}
}
