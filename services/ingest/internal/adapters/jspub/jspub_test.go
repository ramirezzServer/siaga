package jspub

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func setup(t *testing.T) (jetstream.JetStream, context.Context) {
	t.Helper()
	nc, err := natsx.Connect(natstest.RunServer(t), "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}
	return js, ctx
}

func TestPublishDeduplicatesAndSetsHeaders(t *testing.T) {
	js, ctx := setup(t)
	p := New(js, streams.Raw.Name)
	msg := ports.Message{Subject: "raw.quake.bmkg", ID: "abc123", Data: []byte{1, 2, 3}}

	res, err := p.Publish(ctx, msg)
	if err != nil || res.Duplicate {
		t.Fatalf("pertama: %+v, %v", res, err)
	}
	res, err = p.Publish(ctx, msg)
	if err != nil || !res.Duplicate {
		t.Fatalf("ID sama harus terdeteksi ganda: %+v, %v", res, err)
	}

	st, err := js.Stream(ctx, streams.Raw.Name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetLastMsgForSubject(ctx, "raw.quake.bmkg")
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Get(jetstream.MsgIDHeader) != "abc123" || got.Header.Get("Content-Type") != ContentType || len(got.Data) != 3 {
		t.Fatalf("pesan tersimpan salah: %+v", got)
	}
	info, _ := st.Info(ctx)
	if info.State.Msgs != 1 {
		t.Fatalf("stream harus berisi 1 pesan, berisi %d", info.State.Msgs)
	}
}

func TestPublishOutsideStreamFails(t *testing.T) {
	js, ctx := setup(t)
	p := New(js, streams.Raw.Name)
	if _, err := p.Publish(ctx, ports.Message{Subject: "bukan.raw", ID: "x"}); err == nil {
		t.Fatal("subjek di luar stream harus gagal (tidak ada yang menyimpan)")
	}
}
