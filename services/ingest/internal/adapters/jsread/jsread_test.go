package jsread

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/jspub"
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}
	return js, ctx
}

func collect(t *testing.T, r *Reader, ctx context.Context, subjects []string, since time.Time) []ports.StreamMessage {
	t.Helper()
	var out []ports.StreamMessage
	for m, err := range r.Messages(ctx, subjects, since) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestMessages(t *testing.T) {
	js, ctx := setup(t)
	pub := jspub.New(js, streams.Raw.Name)
	fetched := time.Date(2026, 11, 20, 3, 4, 5, 600_000_000, time.UTC)
	var want []ports.StreamMessage
	for i := range 7 {
		subject := "raw.weather.bmkg"
		if i%3 == 2 {
			subject = "raw.quake.bmkg"
		}
		m := ports.Message{Subject: subject, ID: fmt.Sprintf("id-%d", i), Data: []byte{byte(i)}, FetchedAt: fetched.Add(time.Duration(i) * time.Minute)}
		if _, err := pub.Publish(ctx, m); err != nil {
			t.Fatal(err)
		}
		if subject == "raw.weather.bmkg" {
			want = append(want, ports.StreamMessage{Subject: subject, ID: m.ID, FetchedAt: m.FetchedAt})
		}
	}
	r := New(js, streams.Raw.Name)
	r.batch, r.wait = 2, 500*time.Millisecond // beberapa Fetch, berhenti di pesan terakhir
	if got := collect(t, r, ctx, []string{"raw.weather.bmkg"}, time.Time{}); !slices.Equal(got, want) {
		t.Fatalf("\n%+v\ningin\n%+v", got, want)
	}
	if got := collect(t, r, ctx, []string{"raw.weather.bmkg", "raw.quake.bmkg"}, time.Time{}); len(got) != 7 {
		t.Fatalf("%d pesan", len(got))
	}
	// since di masa depan: tidak ada pesan, berhenti setelah satu Fetch kosong.
	if got := collect(t, r, ctx, []string{"raw.weather.bmkg"}, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if got := collect(t, r, ctx, nil, time.Time{}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// Berhenti lebih awal.
	for _, err := range r.Messages(ctx, []string{"raw.weather.bmkg"}, time.Time{}) {
		if err != nil {
			t.Fatal(err)
		}
		break
	}

	// Header waktu ambil rusak dan stream yang tidak ada menjadi galat.
	bad := nats.NewMsg("raw.weather.bmkg")
	bad.Header.Set(jetstream.MsgIDHeader, "rusak")
	bad.Header.Set(natsx.HeaderFetchedAt, "kemarin")
	if _, err := js.PublishMsg(ctx, bad); err != nil {
		t.Fatal(err)
	}
	var errs int
	for _, err := range r.Messages(ctx, []string{"raw.weather.bmkg"}, time.Time{}) {
		if err != nil {
			errs++
		}
	}
	if errs != 1 {
		t.Fatalf("%d galat, ingin 1", errs)
	}
	for _, err := range New(js, "TIDAKADA").Messages(ctx, []string{"raw.x"}, time.Time{}) {
		if err == nil {
			t.Fatal("stream tidak ada seharusnya galat")
		}
	}
}
