// Command calibrate-felt mengkalibrasi rumus radius dirasakan gempa
// R = 10^(a·M − b) dari kolom "Dirasakan" BMKG dan sel DYFI USGS (ADR 0026),
// lalu menulis laporan Markdown dan hasil pencocokan nama wilayah.
//
//	calibrate-felt -bmkg dirasakan-bmkg.csv -dyfi dirasakan-dyfi.csv \
//	  -names .cache/wilayah/db/wilayah.sql -boundaries .cache/wilayah_boundaries/db \
//	  -out docs/calibration/radius-dirasakan.md
//
// Bahan dibuat make felt-radius (ingest, cmd/felt-data); nama dan batas
// wilayah diunduh scripts/fetch-region-data.sh dan fetch-gazetteer-data.sh.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/cahyadsn"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/catalog"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/feltcalib"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/gazetteer"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "calibrate-felt:", err)
		}
		os.Exit(1)
	}
}

// Rumus pembanding di laporan selain hasil fit dan rumus layanan.
var tauCandidates = []float64{0.5, 0.75}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("calibrate-felt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		bmkgFile  = fs.String("bmkg", "../../docs/calibration/dirasakan-bmkg.csv", "gempa dirasakan BMKG (felt-data)")
		dyfiFile  = fs.String("dyfi", "../../docs/calibration/dirasakan-dyfi.csv", "sel DYFI USGS (felt-data)")
		aliasFile = fs.String("alias", "../../docs/calibration/gazetir-alias.csv", "alias nama wilayah")
		names     = fs.String("names", "../../.cache/wilayah/db/wilayah.sql", "db/wilayah.sql repo cahyadsn/wilayah")
		bounds    = fs.String("boundaries", "../../.cache/wilayah_boundaries/db", "folder db/ repo cahyadsn/wilayah_boundaries")
		out       = fs.String("out", "../../docs/calibration/radius-dirasakan.md", "laporan Markdown")
		outMatch  = fs.String("out-matches", "../../docs/calibration/dirasakan-bmkg-cocok.csv", "hasil pencocokan nama (CSV)")
		tau       = fs.Float64("tau", 0.9, "kuantil wilayah dirasakan terjauh per gempa yang harus di dalam R")
		samples   = fs.Int("bootstrap", 500, "jumlah sampel bootstrap (0 = lewati)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("argumen tidak dikenal: %v", fs.Args())
	}
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }

	bmkg, err := readFile(*bmkgFile, catalog.ReadBMKGFelt)
	if err != nil {
		return err
	}
	cells, err := readFile(*dyfiFile, catalog.ReadDYFICells)
	if err != nil {
		return err
	}
	aliases, err := readFile(*aliasFile, catalog.ReadAliases)
	if err != nil {
		return err
	}
	entries, err := cahyadsn.ReadNames(*names)
	if err != nil {
		return err
	}
	ix, err := gazetteer.New(entries, aliases)
	if err != nil {
		return err
	}
	b, bst, err := cahyadsn.ReadBoundaries(*bounds)
	if err != nil {
		return err
	}
	logf("%d nama wilayah, %d alias, %d batas (%d berkas, %d rusak dilewati)", ix.Len(), len(aliases), len(b), bst.Files, bst.Invalid)

	mentions, unparsed := feltcalib.MatchBMKG(bmkg, ix, b)
	ext := feltcalib.Extents(mentions)
	fit, err := feltcalib.FitQuantile(ext, *tau)
	if err != nil {
		return err
	}
	var boot feltcalib.Bootstrap
	if *samples > 0 {
		if boot, err = feltcalib.BootstrapFit(ext, *tau, *samples, 26); err != nil {
			return err
		}
	}
	service := feltcalib.FromPolicy(quake.DefaultPolicy())
	scores := []feltcalib.Score{
		feltcalib.Evaluate("PRD awal", feltcalib.PRDFormula, ext, mentions, cells),
		feltcalib.Evaluate("Fit τ = "+tauText(*tau), fit.Formula, ext, mentions, cells),
		feltcalib.Evaluate("Layanan", service, ext, mentions, cells),
	}
	for _, t := range tauCandidates {
		f, err := feltcalib.FitQuantile(ext, t)
		if err != nil {
			return err
		}
		scores = append(scores, feltcalib.Evaluate("Fit τ = "+tauText(t), f.Formula, ext, mentions, cells))
	}
	events, suspect := map[string]bool{}, 0
	for _, c := range cells {
		events[c.ID] = true
		if c.Suspect {
			suspect++
		}
	}
	rep := feltcalib.Report{
		Input: feltcalib.Input{
			BMKGEvents: len(bmkg), DYFIEvents: len(events), DYFICells: len(cells), DYFISuspect: suspect,
			Names: ix.Len(), Boundaries: len(b), Aliases: len(aliases),
			NamesSource: "cahyadsn/wilayah `db/wilayah.sql`", BoundariesSource: "cahyadsn/wilayah_boundaries `db/{prov,kab,kec}`",
		},
		Mentions: mentions, Unparsed: unparsed, Extents: ext, Chosen: fit, Bootstrap: boot,
		Service: service, Scores: scores, Bins: feltcalib.Bins(service, ext, cells),
	}
	var buf bytes.Buffer
	if err := feltcalib.WriteMarkdown(&buf, rep); err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o600); err != nil {
		return err
	}
	buf.Reset()
	if err := feltcalib.WriteMatches(&buf, mentions); err != nil {
		return err
	}
	if err := os.WriteFile(*outMatch, buf.Bytes(), 0o600); err != nil {
		return err
	}
	unmatched := 0
	for _, m := range mentions {
		if !m.Matched {
			unmatched++
		}
	}
	logf("BMKG: %d gempa, %d penyebutan (%d tidak cocok, %d bagian tidak terbaca), %d gempa dipakai; DYFI: %d kejadian, %d sel",
		len(bmkg), len(mentions), unmatched, len(unparsed), len(ext), len(events), len(cells))
	logf("fit τ = %.2f: a = %.3f, b = %.3f (R M5 = %.0f km); layanan a = %.2f, b = %.2f (R M5 = %.0f km); tulis %s, %s",
		*tau, fit.Slope, fit.Intercept, fit.RadiusKm(5), service.Slope, service.Intercept, service.RadiusKm(5), *out, *outMatch)
	return nil
}

func tauText(t float64) string {
	return strings.Replace(strconv.FormatFloat(t, 'f', 2, 64), ".", ",", 1)
}

func readFile[T any](path string, read func(io.Reader) ([]T, error)) ([]T, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	v, err := read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
