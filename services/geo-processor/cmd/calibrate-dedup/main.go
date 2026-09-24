// Command calibrate-dedup mengukur ambang deduplikasi gempa BMKG–USGS
// terhadap katalog historis dan menulis laporannya sebagai Markdown.
//
//	calibrate-dedup -bmkg katalog_gempa.csv -usgs a.csv,b.csv -out docs/calibration/dedup-gempa.md
//
// Data diunduh dengan scripts/fetch-calibration-data.sh (make calibrate-dedup).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/catalog"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/calibrate"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/quake"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "calibrate-dedup:", err)
		os.Exit(1)
	}
}

// Ambang yang dibandingkan. Baris pertama adalah angka awal PRD; baris kedua
// ambang bawaan layanan.
var candidates = []quake.Rule{
	quake.PRDInitialRule,
	quake.DefaultCrossSourceRule,
	{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 75, MaxMagnitudeDelta: 0.7},
	{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 0.7},
	{MaxTimeDelta: 20 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 1.0},
	{MaxTimeDelta: 60 * time.Second, MaxDistanceKm: 100, MaxMagnitudeDelta: 1.0},
	{MaxTimeDelta: 30 * time.Second, MaxDistanceKm: 120, MaxMagnitudeDelta: 1.0},
	quake.DefaultRevisionRule,
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("calibrate-dedup", flag.ContinueOnError)
	var (
		bmkgFiles = fs.String("bmkg", "", "file katalog RepoGempa BMKG, dipisah koma (CSV lama atau TSV v2)")
		usgsFiles = fs.String("usgs", "", "file CSV FDSN USGS, dipisah koma")
		boxFlag   = fs.String("box", "-9,-5.5,105,109.5", "kotak minLat,maxLat,minLon,maxLon")
		fromFlag  = fs.String("from", "", "awal periode YYYY-MM-DD (default: awal tumpang tindih kedua katalog)")
		toFlag    = fs.String("to", "", "akhir periode YYYY-MM-DD, eksklusif (default: akhir tumpang tindih)")
		out       = fs.String("out", "", "file Markdown keluaran (default: stdout)")
		misses    = fs.Int("misses", 15, "jumlah contoh gempa terlewat")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bmkgFiles == "" || *usgsFiles == "" {
		return errors.New("-bmkg dan -usgs wajib diisi")
	}
	box, err := parseBox(*boxFlag)
	if err != nil {
		return err
	}
	bmkg, err := load(*bmkgFiles, catalog.ReadBMKG)
	if err != nil {
		return err
	}
	usgs, err := load(*usgsFiles, catalog.ReadUSGS)
	if err != nil {
		return err
	}
	from, to, err := period(*fromFlag, *toFlag, bmkg, usgs)
	if err != nil {
		return err
	}
	b := calibrate.Filter(bmkg, box, from, to)
	u := calibrate.Filter(usgs, box, from, to)
	rep := calibrate.Evaluate(b, u, candidates, *misses)
	md := calibrate.Markdown(rep, calibrate.Meta{
		Box: box, From: from, To: to,
		BMKGSources: names(*bmkgFiles), USGSSources: names(*usgsFiles),
		Command: "make calibrate-dedup",
	})
	if *out == "" {
		_, err = io.WriteString(stdout, md)
		return err
	}
	return os.WriteFile(*out, []byte(md), 0o644) //nolint:gosec // laporan publik untuk repo, bukan rahasia
}

func load(list string, read func(io.Reader) ([]ports.CatalogEvent, int, error)) ([]ports.CatalogEvent, error) {
	var all []ports.CatalogEvent
	for _, path := range strings.Split(list, ",") {
		path = strings.TrimSpace(path)
		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			return nil, err
		}
		events, skipped, err := read(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "%s: %d gempa, %d baris dilewati\n", path, len(events), skipped)
		all = append(all, events...)
	}
	return all, nil
}

// names menulis nama file beserta 12 karakter pertama SHA-256-nya, supaya
// laporan menyebut persis data mana yang dipakai.
func names(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		label := "`" + filepath.Base(p) + "`"
		if b, err := os.ReadFile(filepath.Clean(p)); err == nil {
			sum := sha256.Sum256(b)
			label += " (sha256 " + hex.EncodeToString(sum[:])[:12] + ")"
		}
		out = append(out, label)
	}
	return out
}

func parseBox(s string) (calibrate.Box, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return calibrate.Box{}, fmt.Errorf("-box %q harus 4 angka", s)
	}
	v := make([]float64, 4)
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return calibrate.Box{}, fmt.Errorf("-box %q: %w", s, err)
		}
		v[i] = f
	}
	b := calibrate.Box{MinLat: v[0], MaxLat: v[1], MinLon: v[2], MaxLon: v[3]}
	if b.MinLat >= b.MaxLat || b.MinLon >= b.MaxLon {
		return b, fmt.Errorf("-box %q: batas bawah harus lebih kecil", s)
	}
	return b, nil
}

func period(fromS, toS string, a, b []ports.CatalogEvent) (time.Time, time.Time, error) {
	span := func(es []ports.CatalogEvent) (time.Time, time.Time) {
		lo, hi := es[0].OccurredAt, es[0].OccurredAt
		for _, e := range es {
			if e.OccurredAt.Before(lo) {
				lo = e.OccurredAt
			}
			if e.OccurredAt.After(hi) {
				hi = e.OccurredAt
			}
		}
		return lo, hi
	}
	if len(a) == 0 || len(b) == 0 {
		return time.Time{}, time.Time{}, errors.New("katalog kosong")
	}
	alo, ahi := span(a)
	blo, bhi := span(b)
	const day = 24 * time.Hour
	from, to := later(alo, blo).Truncate(day), earlier(ahi, bhi).Truncate(day).Add(day)
	var err error
	if fromS != "" {
		if from, err = time.Parse(time.DateOnly, fromS); err != nil {
			return from, to, err
		}
	}
	if toS != "" {
		if to, err = time.Parse(time.DateOnly, toS); err != nil {
			return from, to, err
		}
	}
	if !from.Before(to) {
		return from, to, fmt.Errorf("periode %s..%s kosong", from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	return from, to, nil
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
