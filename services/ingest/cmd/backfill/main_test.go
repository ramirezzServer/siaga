package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	rawv1 "github.com/ramirezzServer/siaga/libs/go/contracts/gen/siaga/raw/v1"
	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx/natstest"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/aqbackfill"
)

const testKey = "KUNCIUJIKUNCIUJIKUNCIUJIKUNCIUJI"

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                                   { return c.t }
func (c fixedClock) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// fakeOpenAQ menyajikan rekaman asli OpenAQ dan memastikan key terkirim.
func fakeOpenAQ(t *testing.T) *httptest.Server {
	t.Helper()
	files := map[string]string{
		"/v3/locations":                     "locations-2026-09-24.json",
		"/v3/sensors/17620437/measurements": "measurements-17620437-2026-09-26.json",
		"/v3/sensors/17000275/measurements": "measurements-17000275-2026-09-26.json",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != testKey {
			http.Error(w, `{"detail":"key"}`, http.StatusUnauthorized)
			return
		}
		name, ok := files[r.URL.Path]
		switch {
		case !ok:
			http.NotFound(w, r)
			return
		case r.URL.Query().Get("page") == "2":
			_, _ = w.Write([]byte(`{"meta":{"found":0},"results":[]}`))
			return
		}
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "openaq", "testdata", name))
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBackfillOpenAQ(t *testing.T) {
	old := clock
	clock = fixedClock{time.Date(2026, 9, 28, 13, 30, 0, 0, time.UTC)}
	t.Cleanup(func() { clock = old })
	srv := fakeOpenAQ(t)
	url := natstest.RunServer(t)
	dir := t.TempDir()
	env := lookup(map[string]string{
		"OPENAQ_API_KEY": testKey, "OPENAQ_BASE_URL": srv.URL + "/v3", "INGEST_ARCHIVE_URL": dir,
		"NATS_URL": url, "LOG_LEVEL": "warn",
	})
	args := []string{
		"openaq", "-from", "2026-09-26T12:00:00Z", "-to", "2026-09-26T19:00:00Z",
		"-stations", "openaq:6539694,openaq:6455006", "-json",
	}
	var out bytes.Buffer
	if err := run(t.Context(), args, env, &out, io.Discard); err != nil {
		t.Fatal(err, out.String())
	}
	var rep aqbackfill.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	// Dua stasiun AirGradient, masing-masing satu sensor PM2,5; 8 jam (12.00–19.00).
	tot := rep.Totals()
	if rep.Stations != 2 || len(rep.Sensors) != 2 || tot.InRange != 16 || tot.Published != 16 || tot.Rejected != 0 {
		t.Fatalf("%+v", rep)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	st, err := js.Stream(t.Context(), streams.Raw.Name)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := st.Info(t.Context())
	if strconv.FormatUint(info.State.Msgs, 10) != "16" {
		t.Fatalf("%d pesan", info.State.Msgs)
	}
	msg, err := st.GetMsg(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	var o rawv1.AirQualityObservation
	if err := proto.Unmarshal(msg.Data, &o); err != nil {
		t.Fatal(err)
	}
	r := o.GetReadings()
	if m := o.GetMeta(); m.GetConnector() != "openaq-jam" || !strings.HasPrefix(m.GetArchiveKey(), "openaq-jam/2026/09/28/") ||
		len(r) != 1 || o.GetStation().GetId() == "" || r[0].GetParameter() != "pm25" {
		t.Fatalf("%+v", &o)
	}
	// Payload di arsip tidak memuat key.
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // p dari WalkDir folder sementara test
		if bytes.Contains(b, []byte(testKey)) {
			t.Errorf("key di arsip %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tanpa -stations: stasiun rekaman terakhir melapor 24 Sep, jadi dilewati.
	out.Reset()
	if err := run(t.Context(), []string{"openaq", "-from", "2026-09-26", "-publish=false"}, env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0 stasiun diproses") || !strings.Contains(out.String(), "total") {
		t.Fatal(out.String())
	}
}

func TestBackfillFlags(t *testing.T) {
	setupTimeout = time.Second
	t.Cleanup(func() { setupTimeout = 30 * time.Second })
	old := clock
	clock = fixedClock{time.Date(2026, 9, 28, 13, 30, 0, 0, time.UTC)}
	t.Cleanup(func() { clock = old })
	srv := fakeOpenAQ(t)
	base := map[string]string{"OPENAQ_API_KEY": testKey, "OPENAQ_BASE_URL": srv.URL + "/v3"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"sumber":     {[]string{"glofas"}, base, "sumber tidak dikenal"},
		"kosong":     {nil, base, "sumber tidak dikenal"},
		"tanpa from": {[]string{"openaq"}, base, "-from wajib"},
		"from":       {[]string{"openaq", "-from", "kemarin"}, base, "-from"},
		"to":         {[]string{"openaq", "-from", "2026-09-26", "-to", "x"}, base, "-to"},
		"argumen":    {[]string{"openaq", "-from", "2026-09-26", "lebih"}, base, "argumen"},
		"bbox":       {[]string{"openaq", "-from", "2026-09-26", "-bbox", "1,2"}, base, "-bbox"},
		"log":        {[]string{"openaq", "-from", "2026-09-26"}, with("LOG_LEVEL", "x"), "level"},
		"key":        {[]string{"openaq", "-from", "2026-09-26"}, with("OPENAQ_API_KEY", ""), "openaq_api_key"},
		"arsip":      {[]string{"openaq", "-from", "2026-09-26", "-archive", "s3://siaga-arsip"}, base, "endpoint"},
		"nats":       {[]string{"openaq", "-from", "2026-09-26", "-nats", "nats://127.0.0.1:1"}, base, "nats"},
		"tua":        {[]string{"openaq", "-from", "2026-09-01", "-publish=false"}, base, "lebih tua"},
		"stasiun":    {[]string{"openaq", "-from", "2026-09-26", "-publish=false", "-stations", "openaq:1"}, base, "tidak ada di daftar"},
		"daftar gagal": {
			[]string{"openaq", "-from", "2026-09-26", "-publish=false", "-stations", "openaq:6539694"},
			with("OPENAQ_BASE_URL", srv.URL+"/salah"), "daftar stasiun",
		},
	} {
		err := run(t.Context(), tc.args, lookup(tc.env), io.Discard, io.Discard)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
			t.Errorf("%s: %v, ingin memuat %q", name, err, tc.want)
		}
	}
	if err := run(t.Context(), []string{"-h"}, lookup(nil), io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if err := run(t.Context(), []string{"openaq", "-h"}, lookup(base), io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}
