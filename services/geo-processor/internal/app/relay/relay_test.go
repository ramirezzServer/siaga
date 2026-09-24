package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

type memOutbox struct {
	mu   sync.Mutex
	msgs []ports.OutboxMessage
}

func (o *memOutbox) Drain(ctx context.Context, limit int, publish func(context.Context, ports.OutboxMessage) error) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for n < limit && n < len(o.msgs) {
		if err := publish(ctx, o.msgs[n]); err != nil {
			o.msgs = o.msgs[n:]
			return n, err
		}
		n++
	}
	o.msgs = o.msgs[n:]
	return n, nil
}

type memPub struct {
	mu      sync.Mutex
	got     []string
	failFor string
	headers map[string]string
}

func (p *memPub) Publish(_ context.Context, subject, msgID string, _ []byte, headers map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if msgID == p.failFor {
		return errors.New("broker mati")
	}
	p.got = append(p.got, subject+" "+msgID)
	p.headers = headers
	return nil
}

func msgs(n int) []ports.OutboxMessage {
	out := make([]ports.OutboxMessage, n)
	for i := range out {
		out[i] = ports.OutboxMessage{Subject: "hazard.quake.created", MsgID: string(rune('a' + i))}
	}
	return out
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestFlushPublishesInOrderAcrossBatches(t *testing.T) {
	out := &memOutbox{msgs: msgs(5)}
	pub := &memPub{}
	r := New(out, pub, quiet, time.Now, 2)
	n, err := r.Flush(context.Background())
	if err != nil || n != 5 || len(pub.got) != 5 || pub.got[0] != "hazard.quake.created a" || pub.got[4] != "hazard.quake.created e" {
		t.Fatalf("n=%d err=%v got=%v", n, err, pub.got)
	}
	if pub.headers["Content-Type"] != ContentType {
		t.Error("header Content-Type wajib")
	}
	if s := r.Snapshot(); s.Published != 5 || s.LastPublished.IsZero() {
		t.Errorf("stats %+v", s)
	}
}

func TestFlushStopsAtFailureAndKeepsRest(t *testing.T) {
	out := &memOutbox{msgs: msgs(4)}
	pub := &memPub{failFor: "c"}
	r := New(out, pub, quiet, time.Now, 10)
	n, err := r.Flush(context.Background())
	if err == nil || n != 2 || len(out.msgs) != 2 {
		t.Fatalf("n=%d err=%v sisa=%d", n, err, len(out.msgs))
	}
	if r.Snapshot().LastError == "" {
		t.Error("galat harus tercatat")
	}
	pub.failFor = ""
	if n, err := r.Flush(context.Background()); err != nil || n != 2 {
		t.Fatalf("setelah pulih: %d %v", n, err)
	}
}

func TestRunWakesAndStops(t *testing.T) {
	out := &memOutbox{}
	pub := &memPub{}
	r := New(out, pub, quiet, time.Now, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, time.Hour); close(done) }()
	out.mu.Lock()
	out.msgs = msgs(1)
	out.mu.Unlock()
	r.Wake()
	r.Wake() // tidak memblokir walau sinyal sebelumnya belum diambil
	deadline := time.After(5 * time.Second)
	for {
		pub.mu.Lock()
		n := len(pub.got)
		pub.mu.Unlock()
		if n == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Wake tidak menerbitkan pesan")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
