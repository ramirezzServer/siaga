package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
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
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/replay"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// archiveFixtures mengisi arsip dengan rekaman asli di testdata adapter,
// seolah-olah diambil ingest pada waktu rekamannya.
func archiveFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	a, err := fsarchive.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	for _, f := range []struct {
		conn, ext, file string
		at              time.Duration
	}{
		{"bmkg-autogempa", "json", "bmkg/testdata/autogempa.json", 0},
		{"usgs-2.5-day", "geojson", "usgs/testdata/2.5_day.geojson", 20 * time.Second},
		{"bmkg-gempaterkini", "json", "bmkg/testdata/gempaterkini.json", 40 * time.Second},
		{"bmkg-gempadirasakan", "json", "bmkg/testdata/gempadirasakan.json", time.Minute},
		{"firms-viirs-snpp-nrt", "csv", "firms/testdata/jabar-VIIRS_SNPP_NRT-2026-09-24.csv", 14 * time.Hour},
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", f.file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := emit.Archive(t.Context(), a, f.conn, f.ext, emit.Sum(body), body, base.Add(f.at)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// sameCount membandingkan jumlah pesan stream dengan hitungan laporan.
func sameCount(msgs uint64, n int) bool { return strconv.FormatUint(msgs, 10) == strconv.Itoa(n) }

func TestReplayToJetStream(t *testing.T) {
	dir := archiveFixtures(t)
	url := natstest.RunServer(t)
	env := lookup(map[string]string{"INGEST_ARCHIVE_URL": dir, "NATS_URL": url, "LOG_LEVEL": "warn"})
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-json", "-strict"}, env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var rep replay.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err, out.String())
	}
	tot := rep.Totals()
	if tot.Payloads != 5 || tot.Corrupt != 0 || tot.ParseErrors != 0 || tot.Published == 0 {
		t.Fatalf("%+v", tot)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := js.Stream(t.Context(), streams.Raw.Name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(t.Context(), jetstream.WithSubjectFilter(">"))
	if err != nil {
		t.Fatal(err)
	}
	if !sameCount(info.State.Msgs, tot.Published) {
		t.Fatalf("stream %d pesan, laporan %d", info.State.Msgs, tot.Published)
	}
	for subj := range info.State.Subjects {
		if !strings.HasPrefix(subj, "raw.quake.") && subj != "raw.fire.firms" {
			t.Fatalf("subjek tak terduga %s", subj)
		}
	}
	// Pesan pertama (urut waktu ambil) adalah autogempa BMKG dengan FetchMeta asli.
	msg, err := st.GetMsg(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	var q rawv1.QuakeReport
	if err := proto.Unmarshal(msg.Data, &q); err != nil {
		t.Fatal(err)
	}
	if m := q.GetMeta(); m.GetConnector() != "bmkg-autogempa" || !strings.HasPrefix(m.GetArchiveKey(), "bmkg-autogempa/2026/09/24/030000Z-") ||
		!m.GetFetchedAt().AsTime().Equal(time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)) || len(m.GetPayloadSha256()) != 64 {
		t.Fatalf("meta %+v", m)
	}

	// Replay kedua: JetStream menolak semua pesan sebagai duplikat.
	out.Reset()
	if err := run(t.Context(), []string{}, env, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if info, _ := st.Info(t.Context()); !sameCount(info.State.Msgs, tot.Published) {
		t.Fatalf("replay ulang menambah pesan: %d", info.State.Msgs)
	}
	if !strings.Contains(out.String(), "total") || !strings.Contains(out.String(), "2026-09-24 10:00:00") {
		t.Fatalf("laporan tabel:\n%s", out.String())
	}
}

func TestReplayRangeAndStrict(t *testing.T) {
	dir := archiveFixtures(t)
	env := lookup(map[string]string{"INGEST_ARCHIVE_DIR": dir})
	var out bytes.Buffer
	// Hanya titik panas (diambil 17.00 UTC = 24.00 WIB), tanpa NATS.
	err := run(t.Context(), []string{"-publish=false", "-json", "-from", "2026-09-24T12:00:00Z", "-to", "2026-09-25T12:00:00+07:00"}, env, &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var rep replay.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if tot := rep.Totals(); tot.Payloads != 1 || tot.Events == 0 {
		t.Fatalf("%+v", tot)
	}
	// Objek rusak: tanpa -strict hanya dilaporkan, dengan -strict gagal.
	bad := filepath.Join(dir, "bmkg-autogempa", "2026", "09", "24", "030500Z-000000000000.json.gz")
	if err := os.WriteFile(bad, []byte("bukan gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-publish=false", "-connectors", "bmkg-autogempa", "-from", "2026-09-24"}
	if err := run(t.Context(), args, env, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(t.Context(), append(args, "-strict"), env, &out, io.Discard); !errors.Is(err, errStrict) {
		t.Fatalf("-strict: %v", err)
	}
	if !strings.Contains(out.String(), "objek arsip rusak") {
		t.Fatalf("contoh galat tidak tercetak:\n%s", out.String())
	}
}

func TestReplayFlags(t *testing.T) {
	setupTimeout = time.Second
	t.Cleanup(func() { setupTimeout = 30 * time.Second })
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-list"}, lookup(nil), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bmkg-autogempa", "usgs-2.5-day", "openmeteo-sungai", "firms-modis-nrt"} {
		if !strings.Contains(out.String(), want+"\n") {
			t.Errorf("-list tanpa %s:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "bmkg-cap") {
		t.Error("bmkg-cap belum bisa diputar ulang")
	}
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"from":     {[]string{"-from", "kemarin"}, nil, "-from"},
		"to":       {[]string{"-to", "2026-13-01"}, nil, "-to"},
		"argumen":  {[]string{"lebih"}, nil, "argumen"},
		"feed":     {[]string{"-connectors", "bmkg-cap", "-archive", "/tmp"}, nil, "tidak dikenal"},
		"arsip":    {nil, nil, "arsip kosong"},
		"log":      {nil, map[string]string{"LOG_LEVEL": "x"}, "level"},
		"provinsi": {[]string{"-openmeteo-provinces", "99"}, nil, "99"},
		"url":      {[]string{"-archive", "s3://siaga-arsip"}, nil, "endpoint"},
		"nats":     {[]string{"-archive", t.TempDir(), "-nats", "nats://127.0.0.1:1"}, nil, "nats"},
		"urutan":   {[]string{"-archive", t.TempDir(), "-publish=false", "-from", "2026-09-25", "-to", "2026-09-24"}, nil, "sebelum"},
	} {
		err := run(t.Context(), tc.args, lookup(tc.env), io.Discard, io.Discard)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
			t.Errorf("%s: %v, ingin memuat %q", name, err, tc.want)
		}
	}
	if err := run(t.Context(), []string{"-h"}, lookup(nil), io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}
