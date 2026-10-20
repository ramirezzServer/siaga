// Package firearchive mengambil arsip deteksi titik panas FIRMS untuk
// kalibrasi (ADR 0023): standard processing (SP) selama tersedia, lalu
// near real-time (NRT) untuk hari-hari terbaru, lewat Area API dengan kotak
// wilayah yang sama dengan polling ingest. Setiap potongan lima hari
// disimpan di cache, jadi jalan ulang tidak memakai kuota MAP_KEY.
package firearchive

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultBaseURL adalah host FIRMS.
const DefaultBaseURL = "https://firms.modaps.eosdis.nasa.gov"

// ChunkDays adalah hari per request (Area API menerima 1..5).
const ChunkDays = 5

// Epoch adalah titik awal grid potongan: potongan selalu dimulai di
// Epoch + kelipatan ChunkDays, jadi kunci cache tetap antar-jalan.
var Epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// NRTSettle adalah umur minimal potongan NRT supaya dianggap final. Potongan
// NRT yang berakhir kurang dari ini sebelum hari ini masih bisa berubah (data
// terlambat), jadi tidak disimpan di cache dan diambil ulang setiap jalan.
const NRTSettle = 7 * 24 * time.Hour

// Range adalah rentang hari UTC inklusif.
type Range struct{ From, To time.Time }

// Valid true bila From dan To terisi dan tidak terbalik.
func (r Range) Valid() bool { return !r.From.IsZero() && !r.To.IsZero() && !r.To.Before(r.From) }

// Days adalah jumlah hari di rentang.
func (r Range) Days() int {
	if !r.Valid() {
		return 0
	}
	return int(r.To.Sub(r.From).Hours()/24) + 1
}

func (r Range) intersect(o Range) Range {
	out := Range{From: later(r.From, o.From), To: earlier(r.To, o.To)}
	if !out.Valid() {
		return Range{}
	}
	return out
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

// Day memotong t ke awal hari UTC.
func Day(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

// Availability adalah rentang tanggal setiap produk menurut FIRMS.
type Availability map[fire.Product]Range

// ParseAvailability membaca jawaban /api/data_availability/csv/KEY/ALL
// (kolom data_id, min_date, max_date). Produk yang tidak dipakai dilewati.
func ParseAvailability(body []byte) (Availability, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\ufeff"))))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("ketersediaan FIRMS bukan CSV: %q", snippet(body))
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, name := range []string{"data_id", "min_date", "max_date"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("ketersediaan FIRMS tanpa kolom %s: %q", name, snippet(body))
		}
	}
	out := Availability{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ketersediaan FIRMS: %w", err)
		}
		get := func(name string) string {
			if i := col[name]; i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		p := fire.Product(strings.ToUpper(get("data_id")))
		if fire.SatelliteOf(p) == "" {
			continue
		}
		from, err1 := time.Parse(time.DateOnly, get("min_date"))
		to, err2 := time.Parse(time.DateOnly, get("max_date"))
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("ketersediaan %s: %w", p, err)
		}
		if rg := (Range{From: from, To: to}); rg.Valid() {
			out[p] = rg
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ketersediaan FIRMS tidak memuat produk VIIRS/MODIS: %q", snippet(body))
	}
	return out, nil
}

// Chunk adalah satu request Area API.
type Chunk struct {
	Product fire.Product
	Start   time.Time
	Days    int
	// Final true bila isinya tidak akan berubah lagi (SP, atau NRT yang
	// sudah lewat NRTSettle), jadi boleh disimpan di cache.
	Final bool
}

// End adalah hari terakhir potongan.
func (c Chunk) End() time.Time { return c.Start.AddDate(0, 0, c.Days-1) }

// Key adalah kunci cache, misal "VIIRS_SNPP_SP/2019/2019-08-04.d5.csv".
func (c Chunk) Key() string {
	return fmt.Sprintf("%s/%04d/%s.d%d.csv", c.Product, c.Start.Year(), c.Start.Format(time.DateOnly), c.Days)
}

// Span adalah rentang yang diambil untuk satu produk.
type Span struct {
	Satellite string
	Product   fire.Product
	Range
}

// Plan membagi rentang setiap wahana menjadi potongan: SP sejauh tersedia
// di dalam want, lalu NRT untuk sisa hari sampai kemarin (UTC). Wahana
// tanpa SP maupun NRT di rentang itu dilewati.
func Plan(sats []fire.Satellite, av Availability, want Range, now time.Time) ([]Chunk, []Span) {
	yesterday := Day(now).AddDate(0, 0, -1)
	want = want.intersect(Range{From: Epoch, To: yesterday})
	var chunks []Chunk
	var spans []Span
	for _, s := range sats {
		next := want.From
		if sp := av[s.SP].intersect(want); sp.Valid() {
			spans = append(spans, Span{Satellite: s.Name, Product: s.SP, Range: sp})
			chunks = append(chunks, split(s.SP, sp, now)...)
			next = sp.To.AddDate(0, 0, 1)
		}
		rest := Range{From: next, To: want.To}
		if nrt := av[s.NRT].intersect(rest); nrt.Valid() {
			spans = append(spans, Span{Satellite: s.Name, Product: s.NRT, Range: nrt})
			chunks = append(chunks, split(s.NRT, nrt, now)...)
		}
	}
	return chunks, spans
}

// split memotong rg mengikuti grid Epoch.
func split(p fire.Product, rg Range, now time.Time) []Chunk {
	var out []Chunk
	settled := Day(now).Add(-NRTSettle)
	for start := rg.From; !start.After(rg.To); {
		k := int(start.Sub(Epoch).Hours()/24) / ChunkDays
		gridEnd := Epoch.AddDate(0, 0, (k+1)*ChunkDays-1)
		end := earlier(gridEnd, rg.To)
		c := Chunk{Product: p, Start: start, Days: int(end.Sub(start).Hours()/24) + 1}
		c.Final = p.Standard() || c.End().Before(settled)
		out = append(out, c)
		start = end.AddDate(0, 0, 1)
	}
	return out
}

// Batas transaksi MAP_KEY FIRMS: 5.000 per 10 menit. Alat ini berhenti
// sejenak bila pemakaian mencapai StatusShare, supaya polling ingest yang
// sedang jalan tetap punya sisa.
const (
	DefaultStatusEvery = 20
	DefaultStatusShare = 0.6
	DefaultWait        = time.Minute
	DefaultMaxRetries  = 6
)

// ErrKey berarti FIRMS menolak MAP_KEY atau parameter request (HTTP 400/401).
var ErrKey = errors.New("FIRMS menolak MAP_KEY atau parameter request")

// BudgetError dikembalikan Load saat MaxRequests tercapai.
type BudgetError struct{ Remaining int }

func (e *BudgetError) Error() string {
	return fmt.Sprintf("batas request tercapai: %d potongan belum diambil; jalankan lagi, yang sudah diambil tersimpan di cache", e.Remaining)
}

// Loader mengambil potongan arsip dari FIRMS atau cache.
type Loader struct {
	Fetch ports.Fetcher
	Clock ports.Clock
	// Cache menyimpan potongan final apa adanya (CSV dari FIRMS).
	Cache   ports.ArchiveStore
	BaseURL string
	Key     string
	Box     area.Box
	// Check memeriksa isi CSV potongan (misal firms.ParseArchive); galat
	// berarti jawaban bukan CSV FIRMS. Wajib diisi.
	Check func(fire.Product, []byte) error
	// MaxRequests membatasi request area baru satu kali jalan (0 = tanpa batas).
	MaxRequests int
	// StatusEvery, StatusShare, Wait, MaxRetries: nol = bawaan.
	StatusEvery int
	StatusShare float64
	Wait        time.Duration
	MaxRetries  int
	// Progress dipanggil setelah setiap potongan (boleh nil).
	Progress func(done, total int, c Chunk, cached bool)
	// Notice menerima pesan untuk operator, misal saat menunggu kuota (boleh nil).
	Notice func(string)
}

// Stats adalah ringkasan Load.
type Stats struct {
	Chunks, Cached, Fetched, Waits int
	// LastStatus adalah pemakaian MAP_KEY terakhir yang terbaca (nol bila
	// tidak pernah terbaca).
	LastStatus Status
}

func (l *Loader) secrets() []string { return []string{l.Key} }

func (l *Loader) base() string { return strings.TrimRight(cmp.Or(l.BaseURL, DefaultBaseURL), "/") }

func (l *Loader) notice(format string, a ...any) {
	if l.Notice != nil {
		l.Notice(fmt.Sprintf(format, a...))
	}
}

// Availability mengambil rentang tanggal semua produk (satu request).
func (l *Loader) Availability(ctx context.Context) (Availability, error) {
	req := ports.Request{
		URL:    fmt.Sprintf("%s/api/data_availability/csv/%s/ALL", l.base(), l.Key),
		Accept: "text/csv", MaxBytes: 1 << 20, Secrets: l.secrets(),
	}
	body, err := l.fetchRetry(ctx, req, nil)
	if err != nil {
		return nil, fmt.Errorf("ketersediaan FIRMS: %w", err)
	}
	return ParseAvailability(body)
}

// Status adalah pemakaian transaksi MAP_KEY.
type Status struct{ Used, Limit int }

// Status membaca /mapserver/mapkey_status/. ok false bila jawabannya tidak
// berisi angka yang dikenali (format tidak didokumentasikan FIRMS).
func (l *Loader) Status(ctx context.Context) (Status, bool, error) {
	resp, err := l.Fetch.Fetch(ctx, ports.Request{
		URL:    fmt.Sprintf("%s/mapserver/mapkey_status/?MAP_KEY=%s", l.base(), l.Key),
		Accept: "application/json", MaxBytes: 64 << 10, Secrets: l.secrets(),
	})
	if err != nil {
		return Status{}, false, err
	}
	var m map[string]any
	if json.Unmarshal(resp.Body, &m) != nil {
		return Status{}, false, nil
	}
	num := func(k string) (int, bool) {
		switch v := m[k].(type) {
		case float64:
			return int(v), true
		case string:
			var n int
			_, err := fmt.Sscan(v, &n)
			return n, err == nil
		}
		return 0, false
	}
	used, ok1 := num("current_transactions")
	limit, ok2 := num("transaction_limit")
	if !ok1 || !ok2 || limit <= 0 {
		return Status{}, false, nil
	}
	return Status{Used: used, Limit: limit}, true, nil
}

// Load mengambil setiap potongan (dari cache bila final dan sudah ada) dan
// memanggil visit dengan isinya, urut sesuai chunks.
func (l *Loader) Load(ctx context.Context, chunks []Chunk, visit func(Chunk, []byte) error) (Stats, error) {
	if l.Check == nil || l.Fetch == nil || l.Clock == nil || l.Cache == nil {
		return Stats{}, errors.New("firearchive: Fetch, Clock, Cache, dan Check wajib diisi")
	}
	if err := l.Box.Validate(); err != nil {
		return Stats{}, err
	}
	st := Stats{Chunks: len(chunks)}
	every := cmp.Or(l.StatusEvery, DefaultStatusEvery)
	sinceStatus := every // periksa sebelum request pertama
	for i, c := range chunks {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		if c.Final {
			body, err := l.Cache.Get(ctx, c.Key())
			switch {
			case err == nil:
				st.Cached++
				if err := visit(c, body); err != nil {
					return st, err
				}
				l.progress(i+1, len(chunks), c, true)
				continue
			case !errors.Is(err, ports.ErrArchiveNotFound):
				return st, fmt.Errorf("cache %s: %w", c.Key(), err)
			}
		}
		if l.MaxRequests > 0 && st.Fetched >= l.MaxRequests {
			return st, &BudgetError{Remaining: len(chunks) - i}
		}
		if sinceStatus >= every {
			s, ok, err := l.waitForBudget(ctx, &st)
			if err != nil {
				return st, err
			}
			if ok {
				st.LastStatus = s
			}
			sinceStatus = 0
		}
		req := ports.Request{
			URL: fmt.Sprintf("%s/api/area/csv/%s/%s/%s/%d/%s", l.base(), l.Key, c.Product, l.Box, c.Days, c.Start.Format(time.DateOnly)),
			// Satu potongan lima hari di kotak Jawa Barat paling banyak
			// ribuan baris; 32 MiB menampung kotak yang jauh lebih besar.
			Accept: "text/csv", MaxBytes: 32 << 20, Secrets: l.secrets(),
		}
		body, err := l.fetchRetry(ctx, req, func(b []byte) error { return l.Check(c.Product, b) })
		if err != nil {
			return st, fmt.Errorf("%s %s: %w", c.Product, c.Start.Format(time.DateOnly), err)
		}
		st.Fetched++
		sinceStatus++
		if c.Final {
			if err := l.Cache.Put(ctx, c.Key(), body); err != nil {
				return st, fmt.Errorf("menyimpan cache %s: %w", c.Key(), err)
			}
		}
		if err := visit(c, body); err != nil {
			return st, err
		}
		l.progress(i+1, len(chunks), c, false)
	}
	return st, nil
}

func (l *Loader) progress(done, total int, c Chunk, cached bool) {
	if l.Progress != nil {
		l.Progress(done, total, c, cached)
	}
}

// waitForBudget menunggu selama pemakaian MAP_KEY di atas StatusShare.
func (l *Loader) waitForBudget(ctx context.Context, st *Stats) (Status, bool, error) {
	share := cmp.Or(l.StatusShare, DefaultStatusShare)
	wait := cmp.Or(l.Wait, DefaultWait)
	for range 30 {
		s, ok, err := l.Status(ctx)
		var se ports.StatusCoder
		switch {
		case err != nil && errors.As(err, &se) && se.HTTPStatus() == 403:
			// FIRMS menjawab 403 untuk key salah maupun kuota habis; key
			// salah akan ketahuan di request area (400).
		case err != nil:
			// Status hanya penunjang; request area tetap dibatasi galatnya.
			l.notice("status MAP_KEY tidak terbaca (%v); lanjut tanpa pemeriksaan", ports.Redact(err.Error(), l.secrets()))
			return Status{}, false, nil
		case !ok:
			return Status{}, false, nil
		case float64(s.Used) < share*float64(s.Limit):
			return s, true, nil
		}
		st.Waits++
		if ok {
			l.notice("pemakaian MAP_KEY %d/%d transaksi; menunggu %v", s.Used, s.Limit, wait)
		} else {
			l.notice("MAP_KEY sedang dibatasi FIRMS; menunggu %v", wait)
		}
		if err := l.Clock.Sleep(ctx, wait); err != nil {
			return Status{}, false, err
		}
	}
	return Status{}, false, errors.New("kuota MAP_KEY tidak pulih setelah 30 kali menunggu")
}

// fetchRetry mengambil req dan mengulang saat FIRMS lambat, membatasi, atau
// galat sementara. check (boleh nil) memeriksa isi; jawaban berisi teks
// batas transaksi juga diulang.
func (l *Loader) fetchRetry(ctx context.Context, req ports.Request, check func([]byte) error) ([]byte, error) {
	wait := cmp.Or(l.Wait, DefaultWait)
	tries := cmp.Or(l.MaxRetries, DefaultMaxRetries)
	var last error
	for attempt := range tries {
		if attempt > 0 {
			l.notice("%s: %v; ulang %d/%d setelah %v", redactedPath(req, l.secrets()), last, attempt, tries-1, wait)
			if err := l.Clock.Sleep(ctx, wait); err != nil {
				return nil, err
			}
			wait = min(2*wait, 10*time.Minute)
		}
		resp, err := l.Fetch.Fetch(ctx, req)
		if err == nil && check != nil {
			if cerr := check(resp.Body); cerr != nil {
				if limited(resp.Body) {
					last = fmt.Errorf("batas transaksi: %q", snippet(resp.Body))
					continue
				}
				return nil, cerr
			}
		}
		if err == nil {
			return resp.Body, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var se ports.StatusCoder
		if errors.As(err, &se) {
			switch s := se.HTTPStatus(); {
			case s == 400 || s == 401:
				return nil, fmt.Errorf("%w: %w", ErrKey, err)
			case s == 403 || s >= 500:
			default:
				return nil, err
			}
		}
		last = err
	}
	return nil, fmt.Errorf("gagal %d kali: %w", tries, last)
}

func limited(body []byte) bool {
	s := strings.ToLower(snippet(body))
	return strings.Contains(s, "exceed") || strings.Contains(s, "transaction") || strings.Contains(s, "try again later")
}

func redactedPath(req ports.Request, secrets []string) string {
	return ports.Redact(req.URL, secrets)
}

func snippet(b []byte) string {
	return strings.TrimSpace(string(b[:min(len(b), 120)]))
}
