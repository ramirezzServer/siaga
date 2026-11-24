package pollstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	statusv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/status/v1"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

type fakePub struct {
	msgs     []ports.Message
	deadline bool
	err      error
}

func (p *fakePub) Publish(ctx context.Context, m ports.Message) (ports.PublishResult, error) {
	_, p.deadline = ctx.Deadline()
	p.msgs = append(p.msgs, m)
	return ports.PublishResult{}, p.err
}

var start = time.Date(2026, 11, 24, 15, 0, 0, 123_000_000, time.FixedZone("WIB", 7*3600))

func report() ports.PollReport {
	return ports.PollReport{
		Connector: "bmkg-autogempa", Subject: "raw.quake.bmkg", Interval: 30 * time.Second,
		StartedAt: start, FinishedAt: start.Add(800 * time.Millisecond), OK: true, Published: 1,
	}
}

func TestReportPublishesConnectorPoll(t *testing.T) {
	pub := &fakePub{}
	if err := New(pub).Report(t.Context(), report()); err != nil {
		t.Fatal(err)
	}
	if len(pub.msgs) != 1 || !pub.deadline {
		t.Fatalf("pesan %+v, batas waktu %v", pub.msgs, pub.deadline)
	}
	m := pub.msgs[0]
	if m.Subject != "status.connector.bmkg-autogempa" || m.ID != "bmkg-autogempa:2026-11-24T08:00:00.123Z" || !m.FetchedAt.IsZero() {
		t.Fatalf("pesan %+v", m)
	}
	var got statusv1.ConnectorPoll
	if err := proto.Unmarshal(m.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetConnector() != "bmkg-autogempa" || got.GetSubject() != "raw.quake.bmkg" ||
		got.GetInterval().AsDuration() != 30*time.Second || !got.GetStartedAt().AsTime().Equal(start) ||
		got.GetFinishedAt().AsTime().Sub(start) != 800*time.Millisecond ||
		got.GetOutcome() != statusv1.PollOutcome_POLL_OUTCOME_OK || got.GetPublished() != 1 {
		t.Fatalf("isi %v", &got)
	}

	usgs := report()
	usgs.Connector = "usgs-2.5-day"
	if m, err := Message(usgs); err != nil || m.Subject != "status.connector.usgs-2_5-day" || m.ID[:13] != "usgs-2.5-day:" {
		t.Fatalf("usgs: %+v %v", m, err)
	}

	failed := report()
	failed.OK, failed.Published = false, 0
	data, err := Encode(failed)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(data, &got); err != nil || got.GetOutcome() != statusv1.PollOutcome_POLL_OUTCOME_FAILED {
		t.Fatalf("hasil gagal %v %v", &got, err)
	}
}

func TestReportRejectsIncompleteReports(t *testing.T) {
	for name, mutate := range map[string]func(*ports.PollReport){
		"konektor":   func(r *ports.PollReport) { r.Connector = "BMKG autogempa" },
		"subjek":     func(r *ports.PollReport) { r.Subject = "" },
		"interval":   func(r *ports.PollReport) { r.Interval = 0 },
		"mulai":      func(r *ports.PollReport) { r.StartedAt = time.Time{} },
		"urut waktu": func(r *ports.PollReport) { r.FinishedAt = r.StartedAt.Add(-time.Second) },
	} {
		rep := report()
		mutate(&rep)
		pub := &fakePub{}
		if err := New(pub).Report(t.Context(), rep); err == nil || len(pub.msgs) != 0 {
			t.Errorf("%s: harus ditolak sebelum terbit (err %v)", name, err)
		}
	}
	boom := errors.New("nats putus")
	if err := New(&fakePub{err: boom}).Report(t.Context(), report()); !errors.Is(err, boom) {
		t.Fatalf("galat penerbit harus diteruskan: %v", err)
	}
}
