//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
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
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
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
			`DELETE FROM hazard.cap_message WHERE sent < '2002-01-01'`,
			`DELETE FROM hazard.event_source WHERE occurred_at < '2002-01-01'`,
			`UPDATE hazard.event SET merged_into = NULL, status = 'expired' WHERE occurred_at < '2002-01-01' AND status = 'merged'`,
			`DELETE FROM hazard.event WHERE occurred_at < '2002-01-01'`,
			`DELETE FROM ts.weather_forecast WHERE site_id IN ('adm4:98.01.01.2001', 'grid:-6.75:107.00')`,
			`DELETE FROM ts.aq_forecast WHERE site_id = 'grid:-6.75:107.00'`,
			`DELETE FROM ts.river_discharge WHERE site_id = 'river:uji-e2e'`,
			`DELETE FROM ts.series WHERE site_id IN ('adm4:98.01.01.2001', 'grid:-6.75:107.00', 'river:uji-e2e')`,
			`DELETE FROM ts.site WHERE id IN ('adm4:98.01.01.2001', 'grid:-6.75:107.00', 'river:uji-e2e')`,
		} {
			if _, err := pg.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
	}
	cleanup()
	seedRegions(ctx, t, dbURL)

	cfg := settings{databaseURL: dbURL, natsURL: natsURL, logLevel: slog.LevelInfo, rules: quake.DefaultRules(), weather: weather.DefaultPolicy()}
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

	checkWeather(ctx, t, js)
	checkSeries(ctx, t, js, dbURL)

	stop()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	cleanup()
}

// checkWeather menguji jalur raw.weather.* → hazard.weather.*: peringatan CAP
// (waktu 2001, jadi sudah kedaluwarsa saat diproses) di atas desa uji harus
// tercatat lengkap: created dengan wilayah terdampak, lalu expired ELAPSED.
// Peringatan di luar wilayah pantauan tidak membentuk kejadian.
func checkWeather(ctx context.Context, t *testing.T, js jetstream.JetStream) {
	t.Helper()
	sent := time.Date(2001, 11, 21, 7, 0, 0, 0, time.UTC)
	ring := func(lat0, lon0, lat1, lon1 float64) *rawv1.Ring {
		r := &rawv1.Ring{}
		for _, p := range [][2]float64{{lat0, lon0}, {lat0, lon1}, {lat1, lon1}, {lat1, lon0}, {lat0, lon0}} {
			r.Points = append(r.Points, &commonv1.Point{Latitude: p[0], Longitude: p[1]})
		}
		return r
	}
	warning := func(id string, area *rawv1.Ring) *rawv1.WeatherWarning {
		return &rawv1.WeatherWarning{
			Meta:   &rawv1.FetchMeta{Connector: "bmkg-cap", FetchedAt: timestamppb.New(sent.Add(2 * time.Minute))},
			Source: hazardv1.Source_SOURCE_BMKG, Identifier: id, Sender: "cuaca.ekstrem@bmkg.go.id",
			Sent: timestamppb.New(sent), Status: rawv1.CapStatus_CAP_STATUS_ACTUAL, MsgType: rawv1.CapMsgType_CAP_MSG_TYPE_ALERT,
			Category: "Met", EventCode: "OET-194", Urgency: rawv1.CapUrgency_CAP_URGENCY_IMMEDIATE,
			Severity: rawv1.CapSeverity_CAP_SEVERITY_SEVERE, Certainty: rawv1.CapCertainty_CAP_CERTAINTY_LIKELY,
			Effective: timestamppb.New(sent), Expires: timestamppb.New(sent.Add(2 * time.Hour)),
			Texts: []*rawv1.CapText{
				{Language: "en", Event: "Thunderstorm", Headline: "Thunderstorm in test area"},
				{Language: "id", Event: "Hujan Lebat dan Petir", Headline: "Hujan Lebat disertai Petir di wilayah uji", Description: "Uji ujung ke ujung."},
			},
			Areas:     []*rawv1.CapArea{{AreaDesc: "Provinsi Uji E2E", Polygons: []*rawv1.Ring{area}}},
			SourceUrl: "https://www.bmkg.go.id/alerts/nowcast/id/CUJ20011121001_alert.xml",
		}
	}
	for id, w := range map[string]*rawv1.WeatherWarning{
		// Menutupi seluruh desa uji "Desa Episenter" dan sedikit sekitarnya.
		"2.49.0.1.360.0.2001.11.21.07.98.001": warning("2.49.0.1.360.0.2001.11.21.07.98.001", ring(-6.87, 107.01, -6.83, 107.05)),
		// Di luar kotak wilayah uji (Gorontalo).
		"2.49.0.1.360.0.2001.11.21.07.75.001": warning("2.49.0.1.360.0.2001.11.21.07.75.001", ring(0.5, 122.9, 0.6, 123.0)),
	} {
		b, err := proto.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		msg := nats.NewMsg("raw.weather.bmkg")
		msg.Data = b
		msg.Header.Set(jetstream.MsgIDHeader, "cap-"+id)
		if _, err := js.PublishMsg(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	var created, expired []*jetstream.RawStreamMsg
	deadline := time.Now().Add(30 * time.Second)
	for len(created) == 0 || len(expired) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("hazard.weather.* tidak lengkap dalam 30 detik: created %d, expired %d", len(created), len(expired))
		}
		time.Sleep(100 * time.Millisecond)
		created, expired = nil, nil
		hazard, err := js.Stream(ctx, streams.Hazard.Name)
		if err != nil {
			continue
		}
		info, _ := hazard.Info(ctx)
		for seq := uint64(1); info != nil && seq <= info.State.LastSeq; seq++ {
			m, err := hazard.GetMsg(ctx, seq)
			switch {
			case err != nil:
			case m.Subject == "hazard.weather.created":
				created = append(created, m)
			case m.Subject == "hazard.weather.expired":
				expired = append(expired, m)
			}
		}
	}
	// Beri waktu pesan Gorontalo diproses, lalu pastikan tetap satu kejadian.
	time.Sleep(time.Second)
	var c hazardv1.HazardCreated
	if err := proto.Unmarshal(created[0].Data, &c); err != nil {
		t.Fatal(err)
	}
	h := c.GetHazard()
	w := h.GetWeather()
	if len(created) != 1 || h.GetKind() != hazardv1.HazardKind_HAZARD_KIND_WEATHER || h.GetLevel() != hazardv1.AlertLevel_ALERT_LEVEL_SIAGA ||
		!slices.ContainsFunc(h.GetImpactedRegions(), func(r *commonv1.RegionRef) bool { return r.GetCode() == "98.01.01.2001" }) ||
		w.GetCapSeverity() != "Severe" ||
		w.GetHeadlineEn() != "Thunderstorm in test area" || w.GetMessageCount() != 1 || w.GetAreaKm2() <= 0 {
		t.Fatalf("created %d: %v", len(created), h)
	}
	var e hazardv1.HazardExpired
	if err := proto.Unmarshal(expired[0].Data, &e); err != nil {
		t.Fatal(err)
	}
	if e.GetHazardId() != h.GetId() || e.GetReason() != hazardv1.ExpiryReason_EXPIRY_REASON_ELAPSED || e.GetRevision() != 2 {
		t.Fatalf("expired %v", &e)
	}
	t.Logf("%s: tingkat %s, %d kelurahan/desa terdampak, area %.0f km²", h.GetTitle(), h.GetLevel(), h.GetImpactedRegionCount(), w.GetAreaKm2())
}

// checkSeries menguji jalur deret waktu: prakiraan BMKG per desa, cuaca dan
// kualitas udara grid Open-Meteo, dan debit sungai masuk ke hypertable ts.*;
// pesan ulangan tidak menambah baris, pesan rusak masuk DLQ.
func checkSeries(ctx context.Context, t *testing.T, js jetstream.JetStream, dbURL string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Hour)
	meta := func(c string) *rawv1.FetchMeta {
		return &rawv1.FetchMeta{Connector: c, FetchedAt: timestamppb.New(now), ArchiveKey: c + "/k.json.gz"}
	}
	pf := func(v float64) *float64 { return &v }
	publish := func(subject, id string, m proto.Message) {
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
	bmkg := &rawv1.RegionForecast{
		Meta: meta("bmkg-prakiraan"), Source: hazardv1.Source_SOURCE_BMKG, RegionCode: "98.01.01.2001", Village: "Desa Episenter",
		Location: &commonv1.Point{Latitude: -6.85, Longitude: 107.03}, AnalysisTime: timestamppb.New(now.Add(-6 * time.Hour)),
	}
	for h := range 3 {
		bmkg.Steps = append(bmkg.Steps, &rawv1.ForecastStep{
			ValidTime:    timestamppb.New(now.Add(time.Duration(3*h) * time.Hour)),
			TemperatureC: 24, RelativeHumidityPct: 90, CloudCoverPct: 100, PrecipitationMm: 1.2, WeatherCode: 61, WindSpeedKmh: 5, WindFromDeg: 270,
		})
	}
	grid := &rawv1.ModelSite{
		Id: "grid:-6.75:107.00", Requested: &commonv1.Point{Latitude: -6.75, Longitude: 107},
		Cell: &commonv1.Point{Latitude: -6.76, Longitude: 107.01}, ElevationM: pf(420),
	}
	code := int32(3)
	wx := &rawv1.GridWeatherForecast{Meta: meta("openmeteo-cuaca"), Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: grid, Model: "best_match"}
	aq := &rawv1.AirQualityForecast{Meta: meta("openmeteo-udara"), Source: hazardv1.Source_SOURCE_OPEN_METEO, Site: grid, Model: "cams_global"}
	for h := range 4 {
		at := timestamppb.New(now.Add(time.Duration(h) * time.Hour))
		wx.Steps = append(wx.Steps, &rawv1.GridWeatherStep{ValidTime: at, TemperatureC: pf(22), RelativeHumidityPct: pf(80), WeatherCode: &code})
		aq.Steps = append(aq.Steps, &rawv1.AirQualityStep{ValidTime: at, Pm2_5Ugm3: pf(35), Pm10Ugm3: pf(40)})
	}
	day := now.Truncate(24 * time.Hour)
	flood := &rawv1.RiverDischargeForecast{
		Meta: meta("openmeteo-sungai"), Source: hazardv1.Source_SOURCE_OPEN_METEO, Model: "glofas_v4",
		Site: &rawv1.ModelSite{
			Id: "river:uji-e2e", Name: "Titik Uji", River: "Sungai Uji",
			Requested: &commonv1.Point{Latitude: -6.85, Longitude: 107.03}, Cell: &commonv1.Point{Latitude: -6.85, Longitude: 107.03},
		},
		Steps: []*rawv1.DischargeStep{
			{ValidDate: timestamppb.New(day), DischargeM3S: pf(8.8)},
			{ValidDate: timestamppb.New(day.AddDate(0, 0, 1)), DischargeM3S: pf(9.4), EnsembleMinM3S: pf(5), EnsembleMaxM3S: pf(17)},
		},
	}
	publish("raw.forecast.bmkg", "fc-1", bmkg)
	publish("raw.forecast.bmkg", "fc-1-ulang", bmkg) // isi sama setelah restart ingest
	publish("raw.forecast.openmeteo", "om-1", wx)
	publish("raw.aq.openmeteo", "aq-1", aq)
	publish("raw.flood.openmeteo", "fl-1", flood)
	msg := nats.NewMsg("raw.aq.openmeteo")
	msg.Data = []byte("bukan protobuf")
	if _, err := js.PublishMsg(ctx, msg); err != nil {
		t.Fatal(err)
	}

	pg := mustPool(ctx, t, dbURL)
	defer pg.Close()
	counts := func() (int, int, int, int, uint64) {
		var bm, om, a, f int
		_ = pg.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM ts.weather_forecast WHERE site_id = 'adm4:98.01.01.2001'),
			(SELECT count(*) FROM ts.weather_forecast WHERE site_id = 'grid:-6.75:107.00'),
			(SELECT count(*) FROM ts.aq_forecast WHERE site_id = 'grid:-6.75:107.00'),
			(SELECT count(*) FROM ts.river_discharge WHERE site_id = 'river:uji-e2e')`).Scan(&bm, &om, &a, &f)
		var dlq uint64
		if st, err := js.Stream(ctx, streams.DLQ.Name); err == nil {
			if info, _ := st.Info(ctx); info != nil {
				dlq = info.State.Msgs
			}
		}
		return bm, om, a, f, dlq
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		bm, om, a, f, dlq := counts()
		if bm == 3 && om == 4 && a == 4 && f == 2 && dlq >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deret waktu tidak lengkap dalam 30 detik: bmkg %d, grid %d, udara %d, debit %d, DLQ %d", bm, om, a, f, dlq)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var region string
	if err := pg.QueryRow(ctx, `SELECT region_code FROM ts.site WHERE id = 'river:uji-e2e'`).Scan(&region); err != nil || region != "98.01.01.2001" {
		t.Fatalf("region_code titik sungai %q, %v", region, err)
	}
	var fresh int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM ts.series WHERE site_id IN ('adm4:98.01.01.2001', 'grid:-6.75:107.00', 'river:uji-e2e')
		AND last_fetched_at = $1`, now).Scan(&fresh); err != nil || fresh != 4 {
		t.Fatalf("ts.series: %d deret segar, %v", fresh, err)
	}
	t.Logf("deret waktu: 3 langkah BMKG, 4 jam cuaca grid, 4 jam udara, 2 hari debit; titik sungai di %s", region)
}

func keys(m map[string][]*jetstream.RawStreamMsg) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[k] = len(v)
	}
	return out
}
