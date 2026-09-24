//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/common/v1"
	hazardv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/hazard/v1"
	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

// Uji ujung ke ujung: raw.quake.* masuk JetStream → geo-processor → PostgreSQL
// → outbox → hazard.quake.* keluar. Memakai gempa Cianjur 21 November 2022
// (BMKG dan USGS, parameter dari katalog masing-masing) dengan waktu digeser
// ke tahun 2001 supaya tidak bercampur dengan data nyata di database uji.
func TestEndToEnd(t *testing.T) {
	dbURL := os.Getenv("SIAGA_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("SIAGA_TEST_DATABASE_URL tidak diisi")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	natsURL := natstest.RunServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	nc, err := natsx.Connect(natsURL, "ingest-palsu", quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	if _, err := natsx.EnsureStream(ctx, js, streams.Raw, "ingest"); err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		pg := mustPool(ctx, t, dbURL)
		defer pg.Close()
		for _, q := range []string{
			`DELETE FROM hazard.outbox`,
			`DELETE FROM hazard.event_source WHERE occurred_at < '2002-01-01'`,
			`UPDATE hazard.event SET merged_into = NULL, status = 'expired' WHERE occurred_at < '2002-01-01' AND status = 'merged'`,
			`DELETE FROM hazard.event WHERE occurred_at < '2002-01-01'`,
		} {
			if _, err := pg.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
	}
	cleanup()
	seedRegions(ctx, t, dbURL)

	cfg := settings{databaseURL: dbURL, natsURL: natsURL, logLevel: slog.LevelInfo, rules: quake.DefaultRules()}
	svcCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serve(svcCtx, cfg, quiet) }()

	at := time.Date(2001, 11, 21, 6, 21, 9, 874_000_000, time.UTC)
	fetched := at.Add(4 * time.Minute)
	publish := func(m *rawv1.QuakeReport, subject, id string) {
		b, err := proto.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		msg := nats.NewMsg(subject)
		msg.Data = b
		msg.Header.Set(jetstream.MsgIDHeader, id)
		if _, err := js.PublishMsg(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	bmkg := &rawv1.QuakeReport{
		Meta:   &rawv1.FetchMeta{Connector: "bmkg-autogempa", FetchedAt: timestamppb.New(fetched)},
		Source: hazardv1.Source_SOURCE_BMKG, Feed: rawv1.QuakeFeed_QUAKE_FEED_BMKG_LATEST,
		SourceEventId: at.Truncate(time.Second).Format("20060102150405"), OccurredAt: timestamppb.New(at.Truncate(time.Second)),
		Epicenter: &commonv1.Point{Latitude: -6.85, Longitude: 107.03}, Magnitude: 5.6, DepthKm: 11,
		Place:            "Pusat gempa berada di darat 10 km barat daya Kab. Cianjur",
		FeltDescription:  "V Cianjur, IV Garut, III Bandung",
		TsunamiPotential: rawv1.TsunamiPotential_TSUNAMI_POTENTIAL_NONE,
	}
	usgs := &rawv1.QuakeReport{
		Meta:   &rawv1.FetchMeta{Connector: "usgs-2.5_day", FetchedAt: timestamppb.New(fetched.Add(3 * time.Minute))},
		Source: hazardv1.Source_SOURCE_USGS, Feed: rawv1.QuakeFeed_QUAKE_FEED_USGS_SUMMARY,
		SourceEventId: "us7000ir9t", OccurredAt: timestamppb.New(at.Add(-2808 * time.Millisecond)),
		Epicenter: &commonv1.Point{Latitude: -6.836, Longitude: 106.9968}, Magnitude: 5.6, MagnitudeType: "mww", DepthKm: 10,
		Place: "11 km NE of Sukabumi, Indonesia", SourceUpdatedAt: timestamppb.New(fetched.Add(2 * time.Minute)),
		ReviewStatus: "reviewed", SourceUrl: "https://earthquake.usgs.gov/earthquakes/eventpage/us7000ir9t",
	}
	publish(bmkg, "raw.quake.bmkg", "b1")
	publish(bmkg, "raw.quake.bmkg", "b1-ulang-setelah-restart") // isi sama, ID pesan beda
	publish(usgs, "raw.quake.usgs", "u1")
	broken := []byte("bukan protobuf")
	msg := nats.NewMsg("raw.quake.bmkg")
	msg.Data = broken
	if _, err := js.PublishMsg(ctx, msg); err != nil {
		t.Fatal(err)
	}

	// Tunggu created + updated dan satu pesan DLQ. Kejadian uji berwaktu 2001,
	// jadi pesan expired dari putaran kedaluwarsa boleh ikut muncul.
	var bySubject map[string][]*jetstream.RawStreamMsg
	deadline := time.Now().Add(30 * time.Second)
	for {
		bySubject = map[string][]*jetstream.RawStreamMsg{}
		dlqMsgs := uint64(0)
		if hazard, err := js.Stream(ctx, streams.Hazard.Name); err == nil {
			info, _ := hazard.Info(ctx)
			for seq := uint64(1); info != nil && seq <= info.State.LastSeq; seq++ {
				if m, err := hazard.GetMsg(ctx, seq); err == nil {
					bySubject[m.Subject] = append(bySubject[m.Subject], m)
				}
			}
		}
		if dlq, err := js.Stream(ctx, streams.DLQ.Name); err == nil {
			if info, _ := dlq.Info(ctx); info != nil {
				dlqMsgs = info.State.Msgs
			}
		}
		if len(bySubject["hazard.quake.updated"]) >= 1 && dlqMsgs >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pesan hazard/DLQ tidak lengkap dalam 30 detik: %v, DLQ %d", keys(bySubject), dlqMsgs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := len(bySubject["hazard.quake.created"]); n != 1 {
		t.Fatalf("created %d kali, ingin 1 (ulangan pesan raw tidak boleh menerbitkan lagi)", n)
	}
	if n := len(bySubject["hazard.quake.updated"]); n != 1 {
		t.Fatalf("updated %d kali, ingin 1", n)
	}
	for subject := range bySubject {
		switch subject {
		case "hazard.quake.created", "hazard.quake.updated", "hazard.quake.expired":
		default:
			t.Fatalf("subjek tak terduga %s", subject)
		}
	}
	var up hazardv1.HazardUpdated
	if err := proto.Unmarshal(bySubject["hazard.quake.updated"][0].Data, &up); err != nil {
		t.Fatal(err)
	}
	h := up.GetHazard()
	eq := h.GetEarthquake()
	if h.GetPrimarySource() != hazardv1.Source_SOURCE_BMKG || h.GetRevision() != 2 || len(eq.GetCorroboratingReports()) != 1 ||
		eq.GetCorroboratingReports()[0].GetSourceEventId() != "us7000ir9t" || h.GetImpactedRegionCount() == 0 ||
		h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA {
		t.Fatalf("hazard %v", h)
	}
	t.Logf("%s: tingkat %s, %d kelurahan/desa terdampak (terdekat %s), radius dirasakan %.0f km",
		h.GetTitle(), h.GetLevel(), h.GetImpactedRegionCount(), h.GetImpactedRegions()[0].GetName(), eq.GetFeltRadiusKm())

	stop()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	cleanup()
}

func keys(m map[string][]*jetstream.RawStreamMsg) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[k] = len(v)
	}
	return out
}
