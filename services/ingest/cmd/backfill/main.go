// Command backfill mengisi ulang data sumber untuk rentang waktu yang
// terlewat (fase 1e-3). Saat ini satu sumber:
//
//	backfill openaq -from 2026-09-26 -to 2026-09-27        # nilai mentah stasiun OpenAQ
//	backfill openaq -from 2026-09-26T13:00:00Z -stations openaq:6539694 -publish=false
//
// Nilai terbit ke NATS JetStream (stream RAW) seperti polling, dan payload
// mentah diarsipkan ke INGEST_ARCHIVE_URL (atau -archive). Key dari
// OPENAQ_API_KEY. Tanggal tanpa jam berarti pukul 00.00 WIB. OpenAQ hanya
// bisa diisi ulang sampai ±7 hari ke belakang (batas umur geo-processor).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ramirezzServer/siaga/libs/go/contracts/streams"
	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/libs/go/platform/logx"
	"github.com/ramirezzServer/siaga/libs/go/platform/natsx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archiveurl"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/jspub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/nopub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openaq"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/aqbackfill"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/runner"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// owner adalah pemilik stream RAW; backfill bagian dari layanan ingest.
const owner = "ingest"

var wib = time.FixedZone("WIB", 7*3600)

// setupTimeout membatasi tunggu NATS saat menyiapkan stream.
var setupTimeout = 30 * time.Second

// Anggaran OpenAQ backfill: 20/menit, di samping 30/menit milik ingest yang
// mungkin sedang jalan (batas OpenAQ 60/menit per key, ADR 0013).
const openAQPerMinute, openAQBurst = 20, 2

// clock bisa diganti di test.
var clock ports.Clock = sysclock.Clock{}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], envx.OS, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "backfill:", err)
		}
		os.Exit(1)
	}
}

type options struct {
	archive  string
	from, to time.Time
	stations []string
	publish  bool
	asJSON   bool
	natsURL  string
	key      string
	baseURL  string
	box      area.Box
}

func parseFlags(args []string, lookup envx.Lookup, stderr io.Writer) (options, archiveurl.Credentials, slog.Level, error) {
	env := envx.NewReader(lookup)
	fs := flag.NewFlagSet("backfill openaq", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	var from, to, stations, box string
	fs.StringVar(&o.archive, "archive", env.Default("INGEST_ARCHIVE_URL", env.Default("INGEST_ARCHIVE_DIR", "")),
		"URL arsip (folder, file:///..., atau s3://bucket/awalan?endpoint=...); kosong = tanpa arsip")
	fs.StringVar(&from, "from", "", "awal waktu ukur, RFC 3339 atau YYYY-MM-DD (00.00 WIB); wajib")
	fs.StringVar(&to, "to", "", "akhir waktu ukur (inklusif); kosong = sekarang")
	fs.StringVar(&stations, "stations", "", "ID stasiun dipisah koma (misal openaq:6539694); kosong = semua yang melapor sejak -from")
	fs.BoolVar(&o.publish, "publish", true, "terbitkan ke NATS; false hanya mengambil, mengarsipkan, dan memeriksa")
	fs.BoolVar(&o.asJSON, "json", false, "laporan dalam JSON")
	fs.StringVar(&o.natsURL, "nats", env.Default("NATS_URL", "nats://127.0.0.1:4222"), "URL NATS")
	fs.StringVar(&box, "bbox", env.Default("INGEST_OPENAQ_BBOX", area.JawaBarat.String()), "kotak stasiun, sama dengan ingest")
	if err := fs.Parse(args); err != nil {
		return o, archiveurl.Credentials{}, 0, err
	}
	var errs []error
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Errorf("argumen tidak dikenal: %v", fs.Args()))
	}
	var err error
	if o.from, err = parseTime(from); err != nil {
		errs = append(errs, fmt.Errorf("-from: %w", err))
	} else if o.from.IsZero() {
		errs = append(errs, errors.New("-from wajib diisi"))
	}
	if o.to, err = parseTime(to); err != nil {
		errs = append(errs, fmt.Errorf("-to: %w", err))
	}
	if o.box, err = area.ParseBox(box); err != nil {
		errs = append(errs, fmt.Errorf("-bbox: %w", err))
	}
	o.stations = split(stations)
	o.key = strings.TrimSpace(env.Default("OPENAQ_API_KEY", ""))
	o.baseURL = env.Default("OPENAQ_BASE_URL", openaq.DefaultBaseURL)
	level, err := logx.ParseLevel(env.Default("LOG_LEVEL", "info"))
	errs = append(errs, err)
	creds := archiveurl.Credentials{
		AccessKeyID:     strings.TrimSpace(env.Default("ARCHIVE_S3_ACCESS_KEY_ID", "")),
		SecretAccessKey: strings.TrimSpace(env.Default("ARCHIVE_S3_SECRET_ACCESS_KEY", "")),
	}
	return o, creds, level, errors.Join(append(errs, env.Err())...)
}

func parseTime(s string) (time.Time, error) {
	if s = strings.TrimSpace(s); s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.ParseInLocation(time.DateOnly, s, wib)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q bukan RFC 3339 atau YYYY-MM-DD", s)
	}
	return t, nil
}

func split(v string) []string {
	var out []string
	for c := range strings.SplitSeq(v, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

const usage = "pemakaian: backfill openaq -from WAKTU [-to WAKTU] [-stations ID,...] [-publish=false] [-json]"

func run(ctx context.Context, args []string, lookup envx.Lookup, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "openaq" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") {
			fmt.Fprintln(stderr, usage)
			return flag.ErrHelp
		}
		return fmt.Errorf("sumber tidak dikenal; %s", usage)
	}
	o, creds, level, err := parseFlags(args[1:], lookup, stderr)
	if err != nil {
		return err
	}
	log := logx.New(stderr, "ingest-backfill", level)
	src, err := openaq.New(o.baseURL, o.key, o.box)
	if err != nil {
		return err
	}
	var archive ports.Archive
	if o.archive != "" {
		store, err := archiveurl.Open(o.archive, creds, nil)
		if err != nil {
			return err
		}
		archive = store
		log.Info("arsip payload aktif", slog.String("lokasi", store.Location()))
	} else {
		log.Warn("arsip kosong: payload tidak diarsipkan")
	}
	var pub ports.Publisher = &nopub.Publisher{}
	if o.publish {
		nc, err := natsx.Connect(o.natsURL, "ingest-backfill", log)
		if err != nil {
			return err
		}
		defer func() { _ = nc.Drain() }()
		js, err := jetstream.New(nc)
		if err != nil {
			return fmt.Errorf("jetstream: %w", err)
		}
		setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
		_, err = natsx.EnsureStream(setupCtx, js, streams.Raw, owner)
		cancel()
		if err != nil {
			return err
		}
		pub = jspub.New(js, streams.Raw.Name)
	}
	limiter, err := runner.NewLimiter("openaq-backfill", openAQPerMinute, openAQBurst)
	if err != nil {
		return err
	}
	b := aqbackfill.New(src, httpfetch.New(30*time.Second), archive, pub, clock, runner.NewGate(clock, limiter))
	log.Info("pengisian ulang OpenAQ dimulai", slog.Time("from", o.from), slog.Time("to", o.to), slog.Bool("publish", o.publish))
	rep, runErr := b.Run(ctx, aqbackfill.Options{From: o.from, To: o.to, Stations: o.stations})
	if runErr != nil && errors.Is(runErr, aqbackfill.ErrOptions) {
		return runErr
	}
	if err := write(stdout, rep, o.asJSON); err != nil {
		return errors.Join(runErr, err)
	}
	if runErr != nil {
		return runErr
	}
	if failed := slices.IndexFunc(rep.Sensors, func(s aqbackfill.SensorReport) bool { return s.Error != "" }); failed >= 0 {
		return fmt.Errorf("sensor %d gagal: %s", rep.Sensors[failed].Sensor, rep.Sensors[failed].Error)
	}
	return nil
}

func write(w io.Writer, rep aqbackfill.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Fprintf(w, "rentang %s – %s WIB; %d stasiun diproses, %d diam sejak awal rentang\n",
		rep.From.In(wib).Format("2006-01-02 15:04"), rep.To.In(wib).Format("2006-01-02 15:04"), rep.Stations, rep.Silent)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "stasiun\tsensor\tparameter\thalaman\tbaris\tdi rentang\tterbit\tduplikat\tditolak\tawal (WIB)\takhir (WIB)\t")
	for _, s := range append(slices.Clone(rep.Sensors), rep.Totals()) {
		first, last := "-", "-"
		if !s.First.IsZero() {
			first, last = s.First.In(wib).Format("2006-01-02 15:04"), s.Last.In(wib).Format("2006-01-02 15:04")
		}
		sensor := "-"
		if s.Sensor != 0 {
			sensor = fmt.Sprint(s.Sensor)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t\n", s.Station, sensor, s.Parameter, s.Pages, s.Rows,
			s.InRange, s.Published, s.Duplicates, s.Rejected, first, last)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, e := range rep.ListErrors {
		fmt.Fprintf(w, "  daftar: %s\n", e)
	}
	for _, s := range rep.Sensors {
		if s.Error != "" {
			fmt.Fprintf(w, "  %s/%d GAGAL: %s\n", s.Station, s.Sensor, s.Error)
		}
		for _, x := range s.Samples {
			fmt.Fprintf(w, "  %s/%d: %s\n", s.Station, s.Sensor, x)
		}
	}
	return nil
}
