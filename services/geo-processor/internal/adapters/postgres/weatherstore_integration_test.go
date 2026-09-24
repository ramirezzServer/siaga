//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/hazardpb"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/warnings"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/weather"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// rect membuat cincin CAP (lat,lon) persegi panjang.
func rect(lon0, lat0, lon1, lat1 float64) weather.Ring {
	return weather.Ring{{Lat: lat0, Lon: lon0}, {Lat: lat0, Lon: lon1}, {Lat: lat1, Lon: lon1}, {Lat: lat1, Lon: lon0}, {Lat: lat0, Lon: lon0}}
}

func capMsg(id string, minute int, typ weather.MsgType, polygons []weather.Ring, refs ...weather.Reference) weather.Message {
	sent := base.Add(time.Duration(minute) * time.Minute)
	m := weather.Message{
		Key: weather.Key{Source: weather.SourceBMKG, Identifier: id}, Sender: "cuaca.ekstrem@bmkg.go.id", Sent: sent, MsgType: typ,
		Category: "Met", EventCode: "OET-194", Urgency: "Immediate", Certainty: "Observed", Severity: weather.SeverityModerate,
		Effective: sent.Add(15 * time.Minute), Expires: sent.Add(135 * time.Minute),
		Texts: []weather.Text{
			{Language: "en", Event: "Thunderstorm", Headline: "Thunderstorm in Test Province"},
			{Language: "id", Event: "Hujan Lebat dan Petir", Headline: "Hujan Lebat disertai Petir di Provinsi Uji", Description: "a\nb"},
		},
		AreaDesc: "Provinsi Uji", Polygons: polygons, HasArea: len(polygons) > 0, References: refs,
		SourceURL: "https://www.bmkg.go.id/alerts/nowcast/id/" + id + ".xml",
		FetchedAt: sent.Add(time.Minute), FirstSeenAt: sent.Add(time.Minute),
	}
	return m
}

func ref(id string, minute int) weather.Reference {
	return weather.Reference{
		Key: weather.Key{Source: weather.SourceBMKG, Identifier: id}, Sender: "cuaca.ekstrem@bmkg.go.id",
		Sent: base.Add(time.Duration(minute) * time.Minute),
	}
}

func weatherService(t *testing.T, store *postgres.WeatherStore, now time.Time) *warnings.Service {
	t.Helper()
	s, err := warnings.New(store, hazardpb.WeatherEncoder{}, weather.DefaultPolicy(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWeatherStoreLifecycle(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	store := postgres.NewWeatherStore(p)
	quakeStore := postgres.NewQuakeStore(p) // outbox bersama
	svc := weatherService(t, store, base.Add(40*time.Minute))

	// Dua poligon kecamatan yang bersinggungan dan satu "dasi kupu-kupu" yang
	// memotong diri (harus diperbaiki ST_MakeValid), semuanya di sekitar Desa Pusat.
	polys := []weather.Ring{
		rect(106.995, -7.005, 107.03, -6.98),
		rect(107.03, -7.005, 107.05, -6.98),
		{{Lat: -6.95, Lon: 107.0}, {Lat: -6.94, Lon: 107.01}, {Lat: -6.95, Lon: 107.01}, {Lat: -6.94, Lon: 107.0}, {Lat: -6.95, Lon: 107.0}},
	}
	a := capMsg("W-A", 0, weather.MsgAlert, polys)
	res, err := svc.Process(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 {
		t.Fatalf("%+v", res)
	}
	id := weather.EventIDFor(a.Key)
	var (
		kind, geomType, status string
		level, npoly           int
		inside, valid          bool
		areaKm2                float64
	)
	err = p.QueryRow(ctx, `SELECT e.kind, ST_GeometryType(e.area), ST_NumGeometries(e.area), ST_Intersects(e.location, e.area),
	  ST_IsValid(e.area), e.level, e.status, w.area_km2
	  FROM hazard.event e JOIN hazard.weather w ON w.event_id = e.id WHERE e.id = $1`, id).
		Scan(&kind, &geomType, &npoly, &inside, &valid, &level, &status, &areaKm2)
	if err != nil {
		t.Fatal(err)
	}
	// Dua persegi bersinggungan menyatu; dasi kupu-kupu menjadi dua segitiga.
	if kind != "weather" || geomType != "ST_MultiPolygon" || npoly != 3 || !inside || !valid || level != 2 || status != "active" ||
		areaKm2 < 16 || areaKm2 > 19 {
		t.Fatalf("kind=%s type=%s n=%d inside=%v valid=%v level=%d status=%s area=%.1f", kind, geomType, npoly, inside, valid, level, status, areaKm2)
	}
	var cov float64
	var within bool
	if err := p.QueryRow(ctx, `SELECT coverage, within_felt FROM hazard.impact_region WHERE event_id = $1 AND region_code = '99.01.01.2001'`, id).
		Scan(&cov, &within); err != nil {
		t.Fatal(err)
	}
	// Irisan 0,015° × 0,015° dari desa 0,02° × 0,02°.
	if math.Abs(cov-0.5625) > 1e-6 || !within {
		t.Fatalf("cakupan %v", cov)
	}
	var headline, headlineEn, capID string
	if err := p.QueryRow(ctx, `SELECT headline, headline_en, cap_identifier FROM hazard.weather WHERE event_id = $1`, id).
		Scan(&headline, &headlineEn, &capID); err != nil || headline != "Hujan Lebat disertai Petir di Provinsi Uji" ||
		headlineEn != "Thunderstorm in Test Province" || capID != "W-A" {
		t.Fatalf("%q %q %q %v", headline, headlineEn, capID, err)
	}

	// Pesan dibaca ulang tanpa poligon, dengan digest tersimpan; ulangan tidak mengubah apa pun.
	if res, err := svc.Process(ctx, a); err != nil || res.Changed {
		t.Fatalf("ulangan: %+v %v", res, err)
	}

	// Update dengan severity lebih tinggi.
	b := capMsg("W-B", 30, weather.MsgUpdate, polys[:1], ref("W-A", 0))
	b.Severity = weather.SeveritySevere
	b.Onset = b.Effective.Add(5 * time.Minute)
	if res, err := svc.Process(ctx, b); err != nil || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	err = store.InTx(ctx, func(tx ports.WeatherTx) error {
		chain, err := tx.Component(ctx, a.Key)
		if err != nil {
			return err
		}
		if len(chain) != 2 || chain[1].Key != b.Key || len(chain[1].References) != 1 || chain[1].References[0].Identifier != "W-A" ||
			!chain[1].HasArea || chain[1].Polygons != nil || chain[1].Event != id || chain[1].Onset.IsZero() ||
			len(chain[1].Texts) != 2 || chain[1].Digest != b.ContentDigest() {
			t.Fatalf("rantai %+v", chain)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cancel mengakhiri kejadian sebagai retracted.
	c := capMsg("W-C", 35, weather.MsgCancel, nil, ref("W-B", 30))
	if res, err := svc.Process(ctx, c); err != nil || res.Ended != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if err := p.QueryRow(ctx, `SELECT status, level FROM hazard.event WHERE id = $1`, id).Scan(&status, &level); err != nil ||
		status != "retracted" || level != 3 {
		t.Fatalf("%s %d %v", status, level, err)
	}

	// Rantai yang pesan awalnya datang belakangan: tetap satu kejadian.
	y := capMsg("W-Y", 90, weather.MsgUpdate, polys[:1], ref("W-X", 60))
	if _, err := svc.Process(ctx, y); err != nil {
		t.Fatal(err)
	}
	x := capMsg("W-X", 60, weather.MsgAlert, polys[1:2])
	if res, err := svc.Process(ctx, x); err != nil || res.Created != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	var n int
	if err := p.QueryRow(ctx, `SELECT count(DISTINCT event_id) FROM hazard.cap_message WHERE identifier IN ('W-X', 'W-Y')`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d kejadian, %v", n, err)
	}

	// Poligon yang semuanya garis (luas nol) ditolak permanen oleh constraint.
	line := capMsg("W-L", 100, weather.MsgAlert, []weather.Ring{{{Lat: -7, Lon: 107}, {Lat: -7, Lon: 107.01}, {Lat: -7, Lon: 107.02}, {Lat: -7, Lon: 107}}})
	if _, err := svc.Process(ctx, line); !errors.Is(err, weather.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	// Area di luar wilayah pantauan: disimpan, tanpa kejadian.
	far := capMsg("W-F", 100, weather.MsgAlert, []weather.Ring{rect(50, 10, 50.1, 10.1)})
	if res, err := svc.Process(ctx, far); err != nil || !res.Ignored {
		t.Fatalf("%+v %v", res, err)
	}

	// Kedaluwarsa: kejadian rantai X–Y berakhir.
	later := weatherService(t, store, base.Add(10*time.Hour))
	if n, err := later.Expire(ctx, base.Add(10*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}

	var subjects []string
	for {
		k, err := quakeStore.Drain(ctx, 50, func(_ context.Context, m ports.OutboxMessage) error {
			subjects = append(subjects, m.Subject)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if k == 0 {
			break
		}
	}
	want := []string{
		"hazard.weather.created", "hazard.weather.updated", "hazard.weather.expired", // A → B → C
		"hazard.weather.created", "hazard.weather.updated", // Y lalu X
		"hazard.weather.expired", // kedaluwarsa
	}
	if !slices.Equal(subjects, want) {
		t.Fatalf("outbox %v", subjects)
	}
}
