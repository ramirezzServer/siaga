// Command dedup-labels menyusun set berlabel deduplikasi gempa BMKG–USGS
// (PRD T4, ADR 0024) dari ISC Bulletin: origin DJA (BMKG) dan NEIC (USGS)
// di kejadian ISC yang sama adalah pasangan, origin DJA di kejadian lain
// yang berdekatan adalah negatif.
//
//	dedup-labels -from 2021-01-01 -to 2024-10-31 -out docs/calibration/dedup-berlabel.csv
//
// Satu request FDSN event per minggu di kotak Indonesia ingest (±200 request,
// ±1 jam, ±400 MB mentah); setiap jawaban disimpan ter-gzip di -cache, jadi
// jalan yang terputus dilanjutkan dan jalan ulang tidak menyentuh ISC.
// -offline hanya memakai cache. Periode bawaan berhenti 2024-10-31 karena
// data sesudahnya belum ditinjau ISC dan origin DJA tidak lagi masuk bulletin.
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
	"syscall"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/isclabels"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quakelabel"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "dedup-labels:", err)
		}
		os.Exit(1)
	}
}

// deps dapat diganti test.
type deps struct {
	fetch ports.Fetcher
	clock ports.Clock
}

var defaultDeps = func() deps {
	return deps{fetch: httpfetch.New(180 * time.Second), clock: sysclock.Clock{}}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	return runWith(ctx, args, stderr, defaultDeps())
}

func runWith(ctx context.Context, args []string, stderr io.Writer, d deps) error {
	fs := flag.NewFlagSet("dedup-labels", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "2021-01-01", "hari UTC pertama")
	to := fs.String("to", "2024-10-31", "hari UTC terakhir (data ISC sesudah 2024-10-31 belum ditinjau dan tanpa origin DJA)")
	box := fs.String("bbox", area.Indonesia.String(), "kotak barat,selatan,timur,utara (bawaan: kotak USGS ingest)")
	minMag := fs.Float64("min-mag", quakelabel.DefaultOptions.MinUSGSMagnitude, "magnitudo minimum origin NEIC wakil")
	cacheDir := fs.String("cache", ".cache/isc", "folder cache jawaban ISC (gzip)")
	out := fs.String("out", "docs/calibration/dedup-berlabel.csv", "set berlabel (CSV)")
	offline := fs.Bool("offline", false, "hanya pakai cache, tanpa request ke ISC")
	planOnly := fs.Bool("plan", false, "cetak rencana lalu berhenti, tanpa request")
	maxRequests := fs.Int("max-requests", 0, "batas request baru satu kali jalan (0 = tanpa batas)")
	base := fs.String("url", isclabels.DefaultBaseURL, "endpoint FDSN event ISC")
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
	rg, err := dayRange(*from, *to)
	if err != nil {
		return err
	}
	opt := quakelabel.DefaultOptions
	opt.MinUSGSMagnitude = *minMag
	if err := opt.Validate(); err != nil {
		return err
	}
	cache, err := fsarchive.New(*cacheDir)
	if err != nil {
		return err
	}
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }

	weeks := isclabels.Plan(rg)
	cached := 0
	for _, w := range weeks {
		if _, err := cache.Get(ctx, w.Key()); err == nil {
			cached++
		} else if !errors.Is(err, ports.ErrArchiveNotFound) {
			return err
		}
	}
	logf("%d minggu %s s.d. %s, %d dari cache, %d request ke ISC; kotak %s", len(weeks),
		rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly), cached, len(weeks)-cached, bb)
	if len(weeks) > 0 {
		logf("contoh request: %s", (&isclabels.Loader{Box: bb, BaseURL: *base}).URL(weeks[0]))
	}
	if *planOnly {
		return nil
	}
	if *offline && cached < len(weeks) {
		return fmt.Errorf("-offline: %d dari %d minggu belum ada di cache; jalankan dulu tanpa -offline", len(weeks)-cached, len(weeks))
	}

	loader := &isclabels.Loader{
		Fetch: d.fetch, Clock: d.clock, Cache: cache, BaseURL: *base, Box: bb,
		MaxRequests: *maxRequests, Offline: *offline,
		Notice: func(s string) { logf("  %s", s) },
	}
	start := time.Now()
	var events []quakelabel.Event
	loader.Progress = func(done, total int, w isclabels.Week, fromCache bool) {
		if !fromCache || done%25 == 0 || done == total {
			src := "ISC"
			if fromCache {
				src = "cache"
			}
			logf("  %d/%d minggu (%s, %s), %d kejadian, %v", done, total, w.Start.Format(time.DateOnly), src, len(events), time.Since(start).Round(time.Second))
		}
	}
	st, err := loader.Load(ctx, weeks, func(_ isclabels.Week, evs []quakelabel.Event) error {
		events = append(events, evs...)
		return nil
	})
	if err != nil {
		return err
	}
	logf("%d minggu (%d dari cache, %d diambil, %d kosong, %d ulang), %.1f MB QuakeML", st.Weeks, st.Cached, st.Fetched, st.Empty, st.Retries, float64(st.Bytes)/1e6)

	events = uniqueEvents(events)
	rows, ls, err := quakelabel.Build(events, opt)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := isclabels.WriteCSV(&buf, isclabels.Meta{Range: rg, Box: bb, Opt: opt, Stats: ls}, rows); err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o600); err != nil {
		return err
	}
	logf("%d kejadian ISC: %d origin USGS M >= %.1f (%d berpasangan BMKG), %d origin BMKG, %d pasangan negatif, %d origin tanpa magnitudo dilewati; tulis %s",
		ls.Events, ls.USGS, opt.MinUSGSMagnitude, ls.Positive, ls.BMKG, ls.Negative, ls.NoMagnitude, *out)
	return nil
}

// uniqueEvents membuang kejadian yang muncul di dua minggu (ISC memilih
// kejadian menurut waktu origin pilihannya, jadi kejadian di batas minggu
// bisa terbawa dua kali bila origin agensinya melewati batas).
func uniqueEvents(events []quakelabel.Event) []quakelabel.Event {
	seen := map[string]bool{}
	out := events[:0]
	for _, e := range events {
		if !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out
}

func dayRange(from, to string) (isclabels.Range, error) {
	f, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return isclabels.Range{}, fmt.Errorf("-from: %w", err)
	}
	t, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return isclabels.Range{}, fmt.Errorf("-to: %w", err)
	}
	r := isclabels.Range{From: f, To: t}
	if !r.Valid() {
		return r, errors.New("-to sebelum -from")
	}
	return r, nil
}
