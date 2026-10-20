// Command fire-threshold mengambil arsip titik api NASA FIRMS untuk kotak
// Jawa Barat lalu menulis bahan kalibrasi hazard.fire.* (make
// fire-threshold, ADR 0023): sumber panas tetap yang harus disaring,
// jumlah kejadian per tahun menurut aturan tingkat PRD dan variannya, dan
// uji terhadap kebakaran tercatat.
//
//	fire-threshold -from 2012-01-01 -events docs/calibration/kebakaran-tercatat.csv \
//	  -report docs/calibration/titik-api.md -static docs/calibration/sumber-panas-tetap-32.csv
//
// Butuh FIRMS_MAP_KEY (sama dengan ingest). Jalan pertama ±2.900 request
// Area API (lima hari per request, S-NPP/NOAA-20/NOAA-21/MODIS 2012 sampai
// kemarin) dan bisa makan satu sampai dua jam; setiap potongan final
// disimpan di -cache, jadi jalan yang terputus dilanjutkan dan jalan ulang
// tidak memakai kuota. -offline hanya memakai cache.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/firms"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/fsarchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/httpfetch"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/sysclock"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/firearchive"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/firethreshold"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/firecluster"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "fire-threshold:", err)
		}
		os.Exit(1)
	}
}

// availabilityKey menyimpan jawaban ketersediaan terakhir di cache untuk -offline.
const availabilityKey = "availability.csv"

var mapKey = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

// deps dapat diganti test.
type deps struct {
	fetch ports.Fetcher
	clock ports.Clock
	now   func() time.Time
}

var defaultDeps = func() deps {
	return deps{fetch: httpfetch.New(180 * time.Second), clock: sysclock.Clock{}, now: time.Now}
}

func run(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) error {
	return runWith(ctx, args, getenv, stderr, defaultDeps())
}

func runWith(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer, d deps) error {
	fs := flag.NewFlagSet("fire-threshold", flag.ContinueOnError)
	fs.SetOutput(stderr)
	box := fs.String("bbox", or(getenv("INGEST_FIRMS_BBOX"), area.JawaBarat.String()), "kotak barat,selatan,timur,utara (bawaan INGEST_FIRMS_BBOX)")
	from := fs.String("from", "2012-01-01", "hari UTC pertama (VIIRS S-NPP mulai 2012-01-20)")
	to := fs.String("to", "", "hari UTC terakhir (kosong = kemarin)")
	sats := fs.String("satellites", "S-NPP,NOAA-20,NOAA-21,MODIS", "wahana yang diambil, dipisah koma")
	cacheDir := fs.String("cache", ".cache/fire-archive", "folder cache potongan arsip")
	events := fs.String("events", "docs/calibration/kebakaran-tercatat.csv", "kebakaran tercatat untuk uji (kosong = tanpa uji)")
	report := fs.String("report", "docs/calibration/titik-api.md", "laporan kalibrasi (Markdown)")
	static := fs.String("static", "docs/calibration/sumber-panas-tetap-32.csv", "daftar sumber panas tetap (CSV); GeoJSON ditulis di samping dengan akhiran .geojson")
	offline := fs.Bool("offline", false, "hanya pakai cache, tanpa request ke FIRMS")
	planOnly := fs.Bool("plan", false, "cetak rencana request lalu berhenti (satu request ketersediaan)")
	maxRequests := fs.Int("max-requests", 0, "batas request area baru satu kali jalan (0 = tanpa batas)")
	base := fs.String("url", or(getenv("FIRMS_BASE_URL"), firearchive.DefaultBaseURL), "host FIRMS")
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
	want, err := wantRange(*from, *to, d.now())
	if err != nil {
		return err
	}
	selected, err := satellites(*sats)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(getenv("FIRMS_MAP_KEY"))
	if !*offline && !mapKey.MatchString(key) {
		return errors.New("FIRMS_MAP_KEY kosong atau bukan MAP_KEY FIRMS (isi di .env; atau -offline untuk memakai cache saja)")
	}
	cache, err := fsarchive.New(*cacheDir)
	if err != nil {
		return err
	}
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	loader := &firearchive.Loader{
		Fetch: d.fetch, Clock: d.clock, Cache: cache, BaseURL: *base, Key: key, Box: bb, MaxRequests: *maxRequests,
		Check: func(p fire.Product, body []byte) error {
			_, _, err := firms.ParseArchive(p, body, d.now())
			return err
		},
		Notice: func(s string) { logf("  %s", s) },
	}

	av, err := availability(ctx, loader, cache, *offline)
	if err != nil {
		return err
	}
	chunks, spans := firearchive.Plan(selected, av, want, d.now())
	if len(chunks) == 0 {
		return fmt.Errorf("tidak ada produk FIRMS yang tersedia untuk %s s.d. %s", want.From.Format(time.DateOnly), want.To.Format(time.DateOnly))
	}
	cached := cachedKeys(ctx, cache)
	need := 0
	if *offline {
		chunks = slices.DeleteFunc(chunks, func(c firearchive.Chunk) bool { return !c.Final || !cached[c.Key()] })
		if len(chunks) == 0 {
			return errors.New("-offline: cache tidak berisi potongan untuk rentang ini; jalankan dulu tanpa -offline")
		}
	}
	for _, c := range chunks {
		if !c.Final || !cached[c.Key()] {
			need++
		}
	}
	for _, s := range spans {
		logf("%-8s %-17s %s s.d. %s", s.Satellite, s.Product, s.From.Format(time.DateOnly), s.To.Format(time.DateOnly))
	}
	logf("%d potongan %d hari, %d dari cache, %d request ke FIRMS; kotak %s", len(chunks), firearchive.ChunkDays, len(chunks)-need, need, bb)
	if *planOnly {
		return nil
	}

	var points []firecluster.Point
	rejected := 0
	start := time.Now()
	loader.Progress = func(done, total int, c firearchive.Chunk, fromCache bool) {
		if done%100 == 0 || done == total {
			logf("  %d/%d potongan (%s %s), %d deteksi, %v", done, total, c.Product, c.Start.Format(time.DateOnly), len(points), time.Since(start).Round(time.Second))
		}
	}
	st, err := loader.Load(ctx, chunks, func(c firearchive.Chunk, body []byte) error {
		dets, rej, err := firms.ParseArchive(c.Product, body, d.now())
		if err != nil {
			return fmt.Errorf("%s: %w", c.Key(), err)
		}
		rejected += len(rej)
		for _, a := range dets {
			points = append(points, firecluster.FromArchived(a))
		}
		return nil
	})
	if err != nil {
		return err
	}
	logf("%d potongan (%d dari cache, %d diambil, %d kali menunggu kuota), %d deteksi, %d baris ditolak", st.Chunks, st.Cached, st.Fetched, st.Waits, len(points), rejected)
	if st.LastStatus.Limit > 0 {
		logf("pemakaian MAP_KEY terakhir terbaca: %d/%d transaksi", st.LastStatus.Used, st.LastStatus.Limit)
	}

	var fires []firethreshold.RecordedFire
	if *events != "" {
		f, err := os.Open(*events)
		if err != nil {
			return err
		}
		fires, err = firethreshold.ReadRecorded(f)
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	res, err := firethreshold.Analyze(points, fires, firecluster.DefaultStaticRule, firethreshold.DefaultVariants())
	if err != nil {
		return err
	}
	cov := make([]firethreshold.Coverage, 0, len(spans))
	for _, s := range spans {
		cov = append(cov, firethreshold.Coverage{Satellite: s.Satellite, Product: string(s.Product), From: s.From, To: s.To})
	}
	var rep, csvOut, geo bytes.Buffer
	if err := firethreshold.WriteReport(&rep, d.now(), bb.String(), cov, res); err != nil {
		return err
	}
	if err := firethreshold.WriteStatic(&csvOut, res.Static); err != nil {
		return err
	}
	if err := firethreshold.WriteGeoJSON(&geo, res.Static); err != nil {
		return err
	}
	geoPath := strings.TrimSuffix(*static, filepath.Ext(*static)) + ".geojson"
	for path, b := range map[string][]byte{*report: rep.Bytes(), *static: csvOut.Bytes(), geoPath: geo.Bytes()} {
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	logf("%d sumber panas tetap (%d deteksi tersaring), %d uji kebakaran; laporan %s", len(res.Static), res.Masked, len(res.Checks), *report)
	return nil
}

func or(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func wantRange(from, to string, now time.Time) (firearchive.Range, error) {
	f, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return firearchive.Range{}, fmt.Errorf("-from: %w", err)
	}
	t := firearchive.Day(now).AddDate(0, 0, -1)
	if to != "" {
		if t, err = time.Parse(time.DateOnly, to); err != nil {
			return firearchive.Range{}, fmt.Errorf("-to: %w", err)
		}
	}
	r := firearchive.Range{From: f, To: t}
	if !r.Valid() {
		return r, errors.New("-to sebelum -from")
	}
	return r, nil
}

func satellites(s string) ([]fire.Satellite, error) {
	var out []fire.Satellite
	for name := range strings.SplitSeq(s, ",") {
		name = strings.TrimSpace(name)
		i := slices.IndexFunc(fire.Satellites, func(x fire.Satellite) bool { return strings.EqualFold(x.Name, name) })
		if i < 0 {
			return nil, fmt.Errorf("wahana %q tidak dikenal (S-NPP, NOAA-20, NOAA-21, MODIS)", name)
		}
		out = append(out, fire.Satellites[i])
	}
	return out, nil
}

// availability mengambil ketersediaan dari FIRMS (dan menyimpannya) atau
// dari cache saat offline.
func availability(ctx context.Context, l *firearchive.Loader, cache *fsarchive.Archive, offline bool) (firearchive.Availability, error) {
	if offline {
		b, err := cache.Get(ctx, availabilityKey)
		if err != nil {
			return nil, fmt.Errorf("-offline butuh %s di cache dari jalan sebelumnya: %w", availabilityKey, err)
		}
		return firearchive.ParseAvailability(b)
	}
	av, err := l.Availability(ctx)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("data_id,min_date,max_date\n")
	for _, p := range slices.Sorted(maps.Keys(av)) {
		fmt.Fprintf(&b, "%s,%s,%s\n", p, av[p].From.Format(time.DateOnly), av[p].To.Format(time.DateOnly))
	}
	if err := cache.Put(ctx, availabilityKey, b.Bytes()); err != nil {
		return nil, err
	}
	return av, nil
}

func cachedKeys(ctx context.Context, cache *fsarchive.Archive) map[string]bool {
	out := map[string]bool{}
	for o, err := range cache.List(ctx, "", "") {
		if err != nil {
			break
		}
		out[o.Key] = true
	}
	return out
}
