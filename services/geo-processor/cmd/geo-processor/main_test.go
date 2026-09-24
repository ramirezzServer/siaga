package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestConfigDefaultsAndOverrides(t *testing.T) {
	cfg, err := config(lookup(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.natsURL != "nats://127.0.0.1:4222" || cfg.httpAddr != "127.0.0.1:8082" || cfg.rules != quake.DefaultRules() {
		t.Fatalf("bawaan %+v", cfg)
	}
	cfg, err = config(lookup(map[string]string{
		"DATABASE_URL": "postgres://x", "QUAKE_DEDUP_CROSS_SOURCE": "90s, 75, 0.7", "QUAKE_DEDUP_REVISION": "5s,20,0.5",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.rules.CrossSource != quake.PRDInitialRule || cfg.rules.Revision.MaxTimeDelta != 5*time.Second {
		t.Fatalf("override %+v", cfg.rules)
	}
}

func TestConfigReportsAllErrors(t *testing.T) {
	_, err := config(lookup(map[string]string{"LOG_LEVEL": "berisik", "QUAKE_DEDUP_CROSS_SOURCE": "x", "QUAKE_DEDUP_REVISION": "1h,1,1"}))
	if err == nil {
		t.Fatal("harus gagal")
	}
	for _, want := range []string{"DATABASE_URL", "berisik", `"x"`, "10m"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("galat %q tidak menyebut %s", err, want)
		}
	}
	if _, err := parseRule("1s,a,1"); err == nil {
		t.Error("angka rusak harus ditolak")
	}
}
