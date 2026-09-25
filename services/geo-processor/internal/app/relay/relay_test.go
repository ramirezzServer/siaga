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

func TestFlushForwardsTraceParentAndObserves(t *testing.T) {
	created := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	ob := &memOutbox{msgs: []ports.OutboxMessage{
		{Subject: "hazard.quake.created", MsgID: "a", TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", CreatedAt: created},
		{Subject: "hazard.quake.expired", MsgID: "b"},
		{Subject: "hazard.quake.updated", MsgID: "c"},
	}}
	pub := &memPub{failFor: "c"}
	var seen []string
	var headers []map[string]string
	r := New(ob, recordingPub{pub, &headers}, quiet, time.Now, 10).Observe(func(_ context.Context, m ports.OutboxMessage, err error) {
		seen = append(seen, m.MsgID+":"+map[bool]string{true: "ok", false: "gagal"}[err == nil]+":"+m.CreatedAt.Format(time.RFC3339))
	})
	if _, err := r.Flush(context.Background()); err == nil {
		t.Fatal("galat broker harus dikembalikan")
	}
	if len(headers) != 3 || headers[0][HeaderTraceParent] != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" || headers[0]["Content-Type"] != ContentType {
		t.Fatalf("header pertama %v", headers)
	}
	if _, ok := headers[1][HeaderTraceParent]; ok {
		t.Fatalf("pesan tanpa traceparent tidak boleh membawa header kosong: %v", headers[1])
	}
	want := []string{"a:ok:2026-09-25T00:00:00Z", "b:ok:0001-01-01T00:00:00Z", "c:gagal:0001-01-01T00:00:00Z"}
	if len(seen) != 3 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] {
		t.Fatalf("pengamat %v", seen)
	}
}

// recordingPub menyimpan salinan header setiap percobaan terbit.
type recordingPub struct {
	next    *memPub
	headers *[]map[string]string
}

func (p recordingPub) Publish(ctx context.Context, subject, msgID string, data []byte, headers map[string]string) error {
	cp := map[string]string{}
	for k, v := range headers {
		cp[k] = v
	}
	*p.headers = append(*p.headers, cp)
	return p.next.Publish(ctx, subject, msgID, data, headers)
}
