// Command replay memutar ulang payload mentah dari arsip menjadi event raw.*
// di NATS JetStream, urut waktu pengambilan asli (ADR 0014).
//
//	replay -from 2026-09-24 -to 2026-09-25                 # semua feed, secepatnya
//	replay -connectors bmkg-autogempa,usgs-2.5-day -speed 60
//	replay -publish=false -strict                          # cek arsip bisa diparse, tanpa NATS
//
// Arsip dibaca dari -archive, bawaan INGEST_ARCHIVE_URL (atau INGEST_ARCHIVE_DIR);
// kredensial S3 dari ARCHIVE_S3_ACCESS_KEY_ID dan ARCHIVE_S3_SECRET_ACCESS_KEY.
// Tanggal tanpa jam berarti pukul 00.00 WIB.
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
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/firms"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/jspub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/nopub"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sitelist"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/usgs"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/replay"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// owner adalah pemilik stream RAW; replay bagian dari layanan ingest.
const owner = "ingest"

// wib dipakai untuk membaca tanggal tanpa jam dan menampilkan laporan.
var wib = time.FixedZone("WIB", 7*3600)

// setupTimeout membatasi tunggu NATS saat menyiapkan stream (natsx.Connect
// terus mencoba tersambung, jadi NATS mati baru ketahuan di sini).
var setupTimeout = 30 * time.Second

// errStrict dikembalikan -strict bila ada payload rusak atau tidak terbaca.
var errStrict = errors.New("ada payload rusak atau tidak terbaca")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], envx.OS, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "replay:", err)
		}
		os.Exit(1)
	}
}

type options struct {
	archive    string
	connectors []string
	from, to   time.Time
	speed      float64
	publish    bool
	strict     bool
	asJSON     bool
	list       bool
	natsURL    string
	provinces  []string
}

func parseFlags(args []string, lookup envx.Lookup, stderr io.Writer) (options, archiveurl.Credentials, slog.Level, error) {
	env := envx.NewReader(lookup)
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defArchive := env.Default("INGEST_ARCHIVE_URL", env.Default("INGEST_ARCHIVE_DIR", ""))
	var o options
	var connectors, from, to, provinces string
	fs.StringVar(&o.archive, "archive", defArchive, "URL arsip (folder, file:///..., atau s3://bucket/awalan?endpoint=...)")
	fs.StringVar(&connectors, "connectors", "", "daftar feed dipisah koma; kosong = semua (lihat -list)")
	fs.StringVar(&from, "from", "", "awal waktu ambil, RFC 3339 atau YYYY-MM-DD (00.00 WIB); kosong = dari awal arsip")
	fs.StringVar(&to, "to", "", "akhir waktu ambil (eksklusif); kosong = sampai akhir arsip")
	fs.Float64Var(&o.speed, "speed", 0, "0 = secepatnya, 1 = waktu asli, 60 = satu jam per menit")
	fs.BoolVar(&o.publish, "publish", true, "terbitkan ke NATS; false hanya memverifikasi dan mem-parse")
	fs.BoolVar(&o.strict, "strict", false, "keluar dengan galat bila ada payload rusak atau tidak terbaca")
	fs.BoolVar(&o.asJSON, "json", false, "laporan dalam JSON")
	fs.BoolVar(&o.list, "list", false, "tampilkan feed yang bisa diputar ulang lalu keluar")
	fs.StringVar(&o.natsURL, "nats", env.Default("NATS_URL", "nats://127.0.0.1:4222"), "URL NATS")
	fs.StringVar(&provinces, "openmeteo-provinces", env.Default("INGEST_OPENMETEO_PROVINCES", "32"), "provinsi titik Open-Meteo, sama dengan saat direkam")
	if err := fs.Parse(args); err != nil {
		return o, archiveurl.Credentials{}, 0, err
	}
	if fs.NArg() > 0 {
		return o, archiveurl.Credentials{}, 0, fmt.Errorf("argumen tidak dikenal: %v", fs.Args())
	}
	var errs []error
	var err error
	if o.from, err = parseTime(from); err != nil {
		errs = append(errs, fmt.Errorf("-from: %w", err))
	}
	if o.to, err = parseTime(to); err != nil {
		errs = append(errs, fmt.Errorf("-to: %w", err))
	}
	o.connectors, o.provinces = split(connectors), split(provinces)
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

// feeds adalah semua konektor yang payload arsipnya bisa diputar ulang.
// URL endpoint tidak dipakai (replay tidak mengambil apa pun dari sumber).
func feeds(provinces []string) ([]replay.Feed, error) {
	var out []replay.Feed
	for _, c := range bmkg.NewQuakeConnectors(bmkg.DefaultTEWSBaseURL) {
		out = append(out, c)
	}
	out = append(out, usgs.NewSummaryConnector(usgs.DefaultSummaryURL))
	if len(provinces) > 0 {
		var grid, rivers []series.Site
		for _, p := range provinces {
			g, err := sitelist.Grid(p)
			if err != nil {
				return nil, err
			}
			r, err := sitelist.Rivers(p)
			if err != nil {
				return nil, err
			}
			grid, rivers = append(grid, g...), append(rivers, r...)
		}
		weather, err := openmeteo.NewWeatherConnector(openmeteo.DefaultWeatherURL, grid, time.Now)
		if err != nil {
			return nil, err
		}
		air, err := openmeteo.NewAirQualityConnector(openmeteo.DefaultAirURL, grid, time.Now)
		if err != nil {
			return nil, err
		}
		flood, err := openmeteo.NewDischargeConnector(openmeteo.DefaultFloodURL, rivers, time.Now)
		if err != nil {
			return nil, err
		}
		out = append(out, weather, air, flood)
	}
	for _, p := range firms.NewParsers() {
		out = append(out, p)
	}
	return out, nil
}

func pick(all []replay.Feed, names []string) ([]replay.Feed, error) {
	if len(names) == 0 {
		return all, nil
	}
	var out []replay.Feed
	var unknown []string
	for _, n := range names {
		i := slices.IndexFunc(all, func(f replay.Feed) bool { return f.Name() == n })
		if i < 0 {
			unknown = append(unknown, n)
			continue
		}
		out = append(out, all[i])
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("feed %v tidak dikenal atau belum bisa diputar ulang; pilihan: %s",
			unknown, strings.Join(replay.Names(all), ", "))
	}
	return out, nil
}

func run(ctx context.Context, args []string, lookup envx.Lookup, stdout, stderr io.Writer) error {
	o, creds, level, err := parseFlags(args, lookup, stderr)
	if err != nil {
		return err
	}
	all, err := feeds(o.provinces)
	if err != nil {
		return err
	}
	if o.list {
		for _, n := range replay.Names(all) {
			fmt.Fprintln(stdout, n)
		}
		return nil
	}
	selected, err := pick(all, o.connectors)
	if err != nil {
		return err
	}
	if o.archive == "" {
		return errors.New("arsip kosong: isi -archive atau INGEST_ARCHIVE_URL")
	}
	log := logx.New(stderr, "ingest-replay", level)
	store, err := archiveurl.Open(o.archive, creds, nil)
	if err != nil {
		return err
	}

	var pub ports.Publisher = &nopub.Publisher{}
	if o.publish {
		nc, err := natsx.Connect(o.natsURL, "ingest-replay", log)
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

	log.Info("replay dimulai", slog.String("arsip", store.Location()), slog.Any("feeds", replay.Names(selected)),
		slog.Bool("publish", o.publish), slog.Float64("speed", o.speed))
	payloads := 0
	rep, runErr := replay.New(store, pub, sysclock.Clock{}).Run(ctx, selected, replay.Options{
		From: o.from, To: o.to, Speed: o.speed,
		Progress: func(replay.FeedReport) {
			if payloads++; payloads%500 == 0 {
				log.Info("replay berjalan", slog.Int("payload", payloads))
			}
		},
	})
	if err := write(stdout, rep, o.asJSON); err != nil {
		return errors.Join(runErr, err)
	}
	if runErr != nil {
		return runErr
	}
	if tot := rep.Totals(); o.strict && tot.Corrupt+tot.ParseErrors > 0 {
		return fmt.Errorf("%w: %d rusak, %d tidak terbaca", errStrict, tot.Corrupt, tot.ParseErrors)
	}
	return nil
}

func write(w io.Writer, rep replay.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "feed\tpayload\tevent\tterbit\tduplikat\tsama\tditolak\trusak\tgagal parse\tawal (WIB)\takhir (WIB)\t")
	for _, f := range append(slices.Clone(rep.Feeds), rep.Totals()) {
		first, last := "-", "-"
		if !f.First.IsZero() {
			first, last = f.First.In(wib).Format("2006-01-02 15:04:05"), f.Last.In(wib).Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t\n", f.Connector, f.Payloads, f.Events, f.Published,
			f.Duplicates, f.AlreadySeen, f.Rejected, f.Corrupt, f.ParseErrors, first, last)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, f := range rep.Feeds {
		for _, s := range f.Samples {
			fmt.Fprintf(w, "  %s: %s\n", f.Connector, s)
		}
	}
	return nil
}
