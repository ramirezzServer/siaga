package main

import (
	"log/slog"
	"testing"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestConfig(t *testing.T) {
	s, err := config(lookup(map[string]string{"INGEST_CONNECTORS": " bmkg-autogempa, ,usgs-2.5-day ", "LOG_LEVEL": "debug"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.connectors) != 2 || s.connectors[1] != "usgs-2.5-day" || s.logLevel != slog.LevelDebug || s.natsURL == "" {
		t.Fatalf("%+v", s)
	}
	if _, err := config(lookup(map[string]string{"LOG_LEVEL": "cerewet"})); err == nil {
		t.Fatal("level log salah harus ditolak")
	}
}

func TestConnectorsAndBudgets(t *testing.T) {
	s, _ := config(lookup(nil))
	lim, err := budgets()
	if err != nil {
		t.Fatal(err)
	}
	specs := connectors(s)
	names := map[string]bool{}
	for _, c := range specs {
		if names[c.conn.Name()] {
			t.Fatalf("nama konektor ganda %s", c.conn.Name())
		}
		names[c.conn.Name()] = true
		if lim[c.budget] == nil || c.interval <= 0 {
			t.Fatalf("%s tanpa anggaran atau interval", c.conn.Name())
		}
	}
	if len(specs) != 4 {
		t.Fatalf("ingin 4 konektor, dapat %d", len(specs))
	}
	// Anggaran BMKG harus cukup untuk semua konektor BMKG pada intervalnya.
	var perMinute float64
	for _, c := range specs {
		if c.budget == "bmkg" {
			perMinute += float64(60e9) / float64(c.interval)
		}
	}
	if perMinute > 55 {
		t.Fatalf("konektor BMKG butuh %.1f req/menit, melebihi anggaran", perMinute)
	}
}
