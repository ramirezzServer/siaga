package natsjs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/consume"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/quakes"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func setup(t *testing.T) (jetstream.JetStream, context.Context) {
	t.Helper()
	nc, err := natsx.Connect(natstest.RunServer(t), "test", quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	for _, s := range []struct {
		spec  streams.Spec
		owner string
	}{{streams.Raw, "ingest"}, {streams.Hazard, "geo-processor"}, {streams.DLQ, "geo-processor"}} {
		if _, err := natsx.EnsureStream(ctx, js, s.spec, s.owner); err != nil {
			t.Fatal(err)
		}
	}
	return js, ctx
}

func TestPublisherDeduplicatesAndChecksStream(t *testing.T) {
	js, ctx := setup(t)
	pub := NewPublisher(js, streams.Hazard.Name)
	for range 2 {
		if err := pub.Publish(ctx, "hazard.quake.created", "id:r1", []byte("x"), map[string]string{"Content-Type": "application/protobuf"}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := js.Stream(ctx, streams.Hazard.Name)
	if err != nil {
		t.Fatal(err)
	}
	if n := info.CachedInfo().State.Msgs; n != 1 {
		t.Fatalf("pesan dengan ID sama harus satu, ada %d", n)
	}
	wrong := NewPublisher(js, streams.DLQ.Name)
	if err := wrong.Publish(ctx, "hazard.quake.created", "id:r2", nil, nil); err == nil {
		t.Fatal("subjek di stream lain harus ditolak")
	}
}

// Consumer menjalankan ketiga keputusan Handler terhadap server sungguhan:
// pesan berhasil di-ack, pesan rusak masuk DLQ dengan header asal, galat
// sementara dikirim ulang lalu masuk DLQ setelah batas percobaan.
func TestConsumerActions(t *testing.T) {
	js, ctx := setup(t)
	var calls atomic.Int32
	process := func(_ context.Context, r quake.Report) (quakes.Result, error) {
		calls.Add(1)
		if r.EventID == "sementara" {
			return quakes.Result{}, errors.New("database\nputus")
		}
		return quakes.Result{Changed: true}, nil
	}
	decode := func(b []byte) (quake.Report, error) {
		if string(b) == "rusak" {
			return quake.Report{}, quake.ErrInvalid
		}
		return quake.Report{EventID: string(b)}, nil
	}
	opts := consume.Options{MaxDeliveries: 3, Backoff: []time.Duration{10 * time.Millisecond}}
	h, err := consume.New(decode, process, opts, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var acked atomic.Int32
	c, err := NewConsumer(ctx, js, "test-quake", "geo-processor", h, quiet, func() { acked.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx) }()

	for _, body := range []string{"baik", "rusak", "sementara"} {
		m := nats.NewMsg("raw.quake.bmkg")
		m.Data = []byte(body)
		m.Header.Set(jetstream.MsgIDHeader, body)
		m.Header.Set("Content-Type", "application/protobuf")
		if _, err := js.PublishMsg(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	dlq, err := js.Stream(ctx, streams.DLQ.Name)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err := dlq.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.State.Msgs == 2 && acked.Load() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DLQ=%d ack=%d setelah 10 detik", info.State.Msgs, acked.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	// baik 1× + sementara 3× (rusak tidak sampai ke process).
	if n := calls.Load(); n != 4 {
		t.Errorf("process dipanggil %d kali, ingin 4", n)
	}

	var reasons []string
	for seq := uint64(1); seq <= 2; seq++ {
		msg, err := dlq.GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		if msg.Subject != "dlq.geo-processor" || msg.Header.Get(HeaderDLQSubject) != "raw.quake.bmkg" ||
			msg.Header.Get(HeaderDLQStream) != "RAW" || msg.Header.Get(HeaderDLQConsumer) != "test-quake" ||
			msg.Header.Get("Content-Type") != "application/protobuf" {
			t.Errorf("header DLQ %v", msg.Header)
		}
		reasons = append(reasons, string(msg.Data)+"@"+msg.Header.Get(HeaderDLQDelivered)+"@"+msg.Header.Get(HeaderDLQError))
	}
	joined := strings.Join(reasons, ";")
	if !strings.Contains(joined, "rusak@1@") || !strings.Contains(joined, "sementara@3@database | putus") {
		t.Errorf("isi DLQ %q", joined)
	}
	if s := h.Snapshot(); s.DeadLettered != 2 || s.Retried != 2 || s.Applied != 1 {
		t.Errorf("stats %+v", s)
	}
}

func TestNewConsumerNeedsRawStream(t *testing.T) {
	nc, err := natsx.Connect(natstest.RunServer(t), "test", quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	h, _ := consume.New(nil, nil, consume.DefaultOptions(), time.Now)
	_, err = NewConsumer(context.Background(), js, "x", "geo-processor", h, quiet, nil)
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("err = %v, ingin ErrStreamNotFound", err)
	}
}

func TestHeaderValue(t *testing.T) {
	if got := headerValue("a\r\nb", 100); got != "a  | b" {
		t.Errorf("%q", got)
	}
	if got := headerValue("ééé", 3); got != "é" {
		t.Errorf("potongan harus di batas rune: %q", got)
	}
	cfg := ConsumerConfig("d")
	if cfg.FilterSubject != "raw.quake.>" || cfg.AckPolicy != jetstream.AckExplicitPolicy || cfg.MaxDeliver != -1 {
		t.Errorf("config %+v", cfg)
	}
}
