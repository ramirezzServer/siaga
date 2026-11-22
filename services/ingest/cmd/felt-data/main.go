// Command felt-data menyusun bahan kalibrasi radius dirasakan gempa (ADR 0026):
//
//   - sel intensitas DYFI USGS (cdi_geo.txt) untuk setiap kejadian ber-DYFI di
//     kotak Indonesia → docs/calibration/dirasakan-dyfi.csv;
//
//   - kolom "Dirasakan" BMKG dari arsip payload gempadirasakan (Garage atau
//     folder hasil archive restore) → docs/calibration/dirasakan-bmkg.csv.
//
//     felt-data -archive "$INGEST_ARCHIVE_URL"
//
// DYFI: satu request daftar, lalu dua request per kejadian (detail untuk
// produk DYFI, lalu cdi_geo.txt), berurutan dengan jeda (±4.300 request,
// ±1 jam). Setiap jawaban disimpan ter-gzip di -cache, jadi jalan yang
// terputus dilanjutkan; -offline hanya memakai cache. Pencocokan nama wilayah
// dan pemilihan rumus dikerjakan calibrate-felt di geo-processor.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archiveurl"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/bmkg"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/feltdata"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "felt-data:", err)
		}
		os.Exit(1)
	}
}

// deps dapat diganti test.
type deps struct {
	fetch   ports.Fetcher
	clock   ports.Clock
	lookup  envx.Lookup
	archive func(raw string, creds archiveurl.Credentials) (ports.ArchiveReader, error)
}

var defaultDeps = func() deps {
	return deps{
		fetch:  httpfetch.New(120 * time.Second),
		clock:  sysclock.Clock{},
		lookup: envx.OS,
		archive: func(raw string, creds archiveurl.Credentials) (ports.ArchiveReader, error) {
			return archiveurl.Open(raw, creds, nil)
		},
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	return runWith(ctx, args, stderr, defaultDeps())
}

func runWith(ctx context.Context, args []string, stderr io.Writer, d deps) error {
	env := envx.NewReader(d.lookup)
	fs := flag.NewFlagSet("felt-data", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "2000-01-01", "hari UTC pertama kejadian DYFI")
	to := fs.String("to", "2026-10-31", "hari UTC terakhir kejadian DYFI (dikunci supaya hasil bisa diulang)")
	box := fs.String("bbox", area.Indonesia.String(), "kotak barat,selatan,timur,utara (bawaan: kotak USGS ingest)")
	minFelt := fs.Int("min-felt", 1, "jumlah laporan DYFI minimum per kejadian")
	cacheDir := fs.String("cache", ".cache/dyfi", "folder cache jawaban USGS (gzip)")
	archive := fs.String("archive", env.Default("INGEST_ARCHIVE_URL", ""), "arsip payload (folder atau s3://…); kosong = lewati BMKG")
	outDYFI := fs.String("out-dyfi", "docs/calibration/dirasakan-dyfi.csv", "sel DYFI (CSV)")
	outBMKG := fs.String("out-bmkg", "docs/calibration/dirasakan-bmkg.csv", "gempa dirasakan BMKG (CSV)")
	bmkgUntil := fs.String("bmkg-until", "", "hari UTC terakhir waktu ambil payload BMKG yang dibaca (kosong = semua), supaya hasil bisa diulang")
	offline := fs.Bool("offline", false, "hanya pakai cache DYFI, tanpa request ke USGS")
	planOnly := fs.Bool("plan", false, "cetak rencana lalu berhenti, tanpa request")
	maxRequests := fs.Int("max-requests", 0, "batas request baru ke USGS satu kali jalan (0 = tanpa batas)")
	base := fs.String("url", feltdata.DefaultBaseURL, "endpoint FDSN event USGS")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("argumen tidak dikenal: %v", fs.Args())
	}
	bb, err := area.ParseBox(*box)
	if err != nil {
		return err
	}
	f, t, err := dayRange(*from, *to)
	if err != nil {
		return err
	}
	var until time.Time
	if *bmkgUntil != "" {
		d, err := time.Parse(time.DateOnly, *bmkgUntil)
		if err != nil {
			return fmt.Errorf("-bmkg-until: %w", err)
		}
		until = d.AddDate(0, 0, 1)
	}
	cache, err := fsarchive.New(*cacheDir)
	if err != nil {
		return err
	}
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	loader := &feltdata.DYFILoader{
		Fetch: d.fetch, Clock: d.clock, Cache: cache, BaseURL: *base, Box: bb, From: f, To: t,
		MinFelt: *minFelt, MaxRequests: *maxRequests, Offline: *offline,
		Notice: func(s string) { logf("  %s", s) },
	}
	_, listErr := cache.Get(ctx, loader.ListKey())
	listCached := listErr == nil
	logf("DYFI %s s.d. %s, kotak %s, >= %d laporan; daftar %s", f.Format(time.DateOnly), t.Format(time.DateOnly), bb, *minFelt, map[bool]string{true: "dari cache", false: "diambil dari USGS"}[listCached])
	logf("contoh request: %s", loader.ListURL())
	if *archive != "" {
		logf("arsip BMKG: %s/%s", strings.TrimRight(*archive, "/"), feltdata.FeltConnector)
	} else {
		logf("arsip BMKG: (kosong, dilewati)")
	}
	if *planOnly {
		return nil
	}

	start := time.Now()
	loader.Progress = func(done, total, fetched int) {
		if done%200 == 0 || done == total {
			logf("  %d/%d kejadian, %d request, %v", done, total, fetched, time.Since(start).Round(time.Second))
		}
	}
	events, st, err := loader.Load(ctx)
	if err != nil {
		return err
	}
	logf("DYFI: %d kejadian (%d tanpa produk, %d tanpa cdi_geo), %d sel; %d dari cache, %d diambil, %d ulang",
		st.Events, st.NoProduct, st.NoCDI, st.Cells, st.Cached, st.Fetched, st.Retries)
	var buf bytes.Buffer
	if err := feltdata.WriteDYFICSV(&buf, feltdata.DYFIMeta{From: f, To: t, Box: bb, MinFelt: *minFelt, Stats: st}, events); err != nil {
		return err
	}
	if err := os.WriteFile(*outDYFI, buf.Bytes(), 0o600); err != nil {
		return err
	}
	logf("tulis %s", *outDYFI)

	if *archive == "" {
		return nil
	}
	creds := archiveurl.Credentials{
		AccessKeyID:     strings.TrimSpace(env.Default("ARCHIVE_S3_ACCESS_KEY_ID", "")),
		SecretAccessKey: strings.TrimSpace(env.Default("ARCHIVE_S3_SECRET_ACCESS_KEY", "")),
	}
	if err := env.Err(); err != nil {
		return err
	}
	ar, err := d.archive(*archive, creds)
	if err != nil {
		return err
	}
	parse := func(body []byte, at time.Time) ([]quake.Report, []ports.Rejection, error) {
		return bmkg.ParseQuakeReports(body, quake.FeedBMKGFelt, at)
	}
	felt, bst, err := feltdata.CollectBMKG(ctx, ar, parse, until)
	if err != nil {
		return err
	}
	buf.Reset()
	if err := feltdata.WriteBMKGCSV(&buf, bst, felt); err != nil {
		return err
	}
	if err := os.WriteFile(*outBMKG, buf.Bytes(), 0o600); err != nil {
		return err
	}
	logf("BMKG: %d payload (%d tidak terbaca, %d gempa ditolak), %d gempa dirasakan (%d dengan isi Dirasakan berubah); tulis %s",
		bst.Payloads, bst.Unreadable, bst.Rejected, bst.Events, bst.Revised, *outBMKG)
	return nil
}

func dayRange(from, to string) (time.Time, time.Time, error) {
	f, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return f, f, fmt.Errorf("-from: %w", err)
	}
	t, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return f, t, fmt.Errorf("-to: %w", err)
	}
	if t.Before(f) {
		return f, t, errors.New("-to sebelum -from")
	}
	return f, t, nil
}
