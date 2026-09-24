package natsx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
)

func TestEnsureStreamIdempotent(t *testing.T) {
	url := natstest.RunServer(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nc, err := natsx.Connect(url, "test", log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for range 2 {
		st, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest")
		if err != nil {
			t.Fatal(err)
		}
		cfg := st.CachedInfo().Config
		if cfg.MaxAge != streams.Raw.MaxAge || cfg.Duplicates != streams.Raw.DuplicateWindow || cfg.Subjects[0] != "raw.>" {
			t.Fatalf("konfigurasi stream tidak sesuai kontrak: %+v", cfg)
		}
	}
}

func TestEnsureStreamRejectsNonOwner(t *testing.T) {
	_, err := natsx.EnsureStream(context.Background(), nil, streams.Hazard, "ingest")
	if !errors.Is(err, natsx.ErrNotOwner) {
		t.Fatalf("err = %v, ingin ErrNotOwner", err)
	}
}

func TestEnsureStreamAllowsSharedStream(t *testing.T) {
	url := natstest.RunServer(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nc, err := natsx.Connect(url, "test", log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, svc := range []string{"geo-processor", "alert-engine"} {
		if _, err := natsx.EnsureStream(ctx, js, streams.DLQ, svc); err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
	}
}
