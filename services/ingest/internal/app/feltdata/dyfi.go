// Package feltdata menyusun bahan kalibrasi radius dirasakan gempa (make
// felt-radius, ADR 0026): sel intensitas DYFI USGS untuk kejadian di kotak
// Indonesia, dan kolom "Dirasakan" BMKG dari arsip payload gempadirasakan.
// Hasilnya dua CSV di docs/calibration yang dibaca calibrate-felt di
// geo-processor (pencocokan nama wilayah, jarak, dan pemilihan rumus).
package feltdata

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/dyfi"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultBaseURL adalah layanan FDSN event USGS.
const DefaultBaseURL = "https://earthquake.usgs.gov/fdsnws/event/1/query"

// Bawaan DYFILoader. USGS membalas 429 bila request terlalu rapat (dicoba
// 2026-11-22 dengan enam request paralel), jadi alat berjalan berurutan.
const (
	DefaultPause      = 500 * time.Millisecond
	DefaultWait       = 30 * time.Second
	DefaultMaxRetries = 5
	maxListBytes      = 64 << 20
	maxDetailBytes    = 8 << 20
	maxCDIBytes       = 8 << 20
)

// Cache menyimpan jawaban mentah (sudah ter-gzip oleh loader).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte) error
}

// DYFILoader mengambil daftar kejadian ber-DYFI, produk DYFI tiap
// kejadian, dan cdi_geo.txt-nya, dari cache atau USGS.
type DYFILoader struct {
	Fetch   ports.Fetcher
	Clock   ports.Clock
	Cache   Cache
	BaseURL string
	Box     area.Box
	// From, To: hari UTC inklusif.
	From, To time.Time
	// MinFelt adalah jumlah laporan DYFI minimum per kejadian.
	MinFelt     int
	MaxRequests int
	Offline     bool
	Pause       time.Duration
	Wait        time.Duration
	MaxRetries  int
	Progress    func(done, total int, fetched int)
	Notice      func(string)

	requests int
}

// EventData adalah satu kejadian beserta produk dan sel DYFI-nya.
type EventData struct {
	dyfi.Event
	Product    dyfi.Product
	HasProduct bool
	// HasCDI false bila produk tidak punya cdi_geo.txt atau berkasnya 404.
	HasCDI bool
	Cells  []dyfi.Cell
}

// DYFIStats merangkum Load.
type DYFIStats struct {
	Events, NoProduct, NoCDI, Cells, Cached, Fetched, Retries int
}

// BudgetError berarti -max-requests tercapai.
type BudgetError struct{ Remaining int }

func (e *BudgetError) Error() string {
	return fmt.Sprintf("batas request tercapai; %d kejadian belum diambil (jalankan lagi untuk melanjutkan)", e.Remaining)
}

// ErrNotCached dikembalikan saat Offline dan jawaban belum ada di cache.
var ErrNotCached = errors.New("belum ada di cache")

var eventIDRe = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)

// ListKey adalah kunci cache daftar kejadian.
func (l *DYFILoader) ListKey() string {
	return fmt.Sprintf("daftar/%s_%s.f%d.geojson.gz", l.From.Format(time.DateOnly), l.To.Format(time.DateOnly), l.MinFelt)
}

// ListURL adalah request daftar kejadian, urut waktu naik.
func (l *DYFILoader) ListURL() string {
	q := url.Values{}
	q.Set("format", "geojson")
	q.Set("producttype", "dyfi")
	q.Set("minfelt", strconv.Itoa(l.MinFelt))
	q.Set("starttime", l.From.Format(time.DateOnly))
	q.Set("endtime", l.To.AddDate(0, 0, 1).Format(time.DateOnly))
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	q.Set("minlatitude", f(l.Box.South))
	q.Set("maxlatitude", f(l.Box.North))
	q.Set("minlongitude", f(l.Box.West))
	q.Set("maxlongitude", f(l.Box.East))
	q.Set("orderby", "time-asc")
	q.Set("limit", "20000")
	return cmp.Or(l.BaseURL, DefaultBaseURL) + "?" + q.Encode()
}

// DetailURL adalah request detail satu kejadian.
func (l *DYFILoader) DetailURL(id string) string {
	return cmp.Or(l.BaseURL, DefaultBaseURL) + "?eventid=" + url.QueryEscape(id) + "&format=geojson"
}

func productKey(id string) string { return "produk/" + id + ".json.gz" }
func cdiKey(id string) string     { return "cdi_geo/" + id + ".txt.gz" }

func (l *DYFILoader) notice(format string, a ...any) {
	if l.Notice != nil {
		l.Notice(fmt.Sprintf(format, a...))
	}
}

// Load mengambil semua bahan. Request baru dihitung untuk MaxRequests.
func (l *DYFILoader) Load(ctx context.Context) ([]EventData, DYFIStats, error) {
	var st DYFIStats
	if l.Cache == nil || (!l.Offline && (l.Fetch == nil || l.Clock == nil)) {
		return nil, st, errors.New("feltdata: Cache, Fetch, dan Clock wajib diisi")
	}
	if err := l.Box.Validate(); err != nil {
		return nil, st, err
	}
	if l.From.IsZero() || l.To.Before(l.From) || l.MinFelt < 1 {
		return nil, st, errors.New("feltdata: rentang hari atau jumlah laporan minimum tidak sah")
	}
	body, err := l.get(ctx, l.ListKey(), l.ListURL(), maxListBytes, &st, false)
	if err != nil {
		return nil, st, fmt.Errorf("daftar kejadian: %w", err)
	}
	events, err := dyfi.ParseList(body)
	if err != nil {
		return nil, st, fmt.Errorf("daftar kejadian %s: %w", l.ListKey(), err)
	}
	out := make([]EventData, 0, len(events))
	for i, ev := range events {
		if err := ctx.Err(); err != nil {
			return out, st, err
		}
		if !eventIDRe.MatchString(ev.ID) {
			return out, st, fmt.Errorf("ID kejadian USGS %q tidak berbentuk huruf kecil, angka, dan garis bawah", ev.ID)
		}
		d, err := l.event(ctx, ev, &st)
		var be *BudgetError
		if errors.As(err, &be) {
			be.Remaining = len(events) - i
			return out, st, be
		}
		if err != nil {
			return out, st, fmt.Errorf("kejadian %s: %w", ev.ID, err)
		}
		out = append(out, d)
		if l.Progress != nil {
			l.Progress(i+1, len(events), st.Fetched)
		}
	}
	st.Events = len(out)
	return out, st, nil
}

func (l *DYFILoader) event(ctx context.Context, ev dyfi.Event, st *DYFIStats) (EventData, error) {
	d := EventData{Event: ev}
	raw, err := l.cached(ctx, productKey(ev.ID), st)
	switch {
	case errors.Is(err, ports.ErrArchiveNotFound):
		body, err := l.get(ctx, "", l.DetailURL(ev.ID), maxDetailBytes, st, false)
		if err != nil {
			return d, err
		}
		p, ok, err := dyfi.ParseDetail(body)
		if err != nil {
			return d, err
		}
		raw = []byte("null")
		if ok {
			if raw, err = marshalProduct(p); err != nil {
				return d, err
			}
		}
		if err := l.put(ctx, productKey(ev.ID), raw); err != nil {
			return d, err
		}
	case err != nil:
		return d, err
	}
	p, ok, err := dyfi.ParseProduct(raw)
	if err != nil {
		return d, fmt.Errorf("cache %s rusak (hapus file itu lalu jalankan lagi): %w", productKey(ev.ID), err)
	}
	d.Product, d.HasProduct = p, ok
	if !ok {
		st.NoProduct++
		return d, nil
	}
	if p.CDIURL() == "" {
		st.NoCDI++
		return d, nil
	}
	body, err := l.get(ctx, cdiKey(ev.ID), p.CDIURL(), maxCDIBytes, st, true)
	if err != nil {
		return d, err
	}
	if len(body) == 0 {
		st.NoCDI++
		return d, nil
	}
	cells, err := dyfi.ParseCells(body)
	if err != nil {
		return d, fmt.Errorf("%s: %w", cdiKey(ev.ID), err)
	}
	d.HasCDI, d.Cells = true, cells
	st.Cells += len(cells)
	return d, nil
}

func marshalProduct(p dyfi.Product) ([]byte, error) {
	b, err := jsonMarshal(p)
	if err != nil {
		return nil, fmt.Errorf("menyimpan produk: %w", err)
	}
	return b, nil
}

// cached membaca kunci dari cache; ErrArchiveNotFound bila belum ada (atau
// galat ErrNotCached bila Offline).
func (l *DYFILoader) cached(ctx context.Context, key string, st *DYFIStats) ([]byte, error) {
	gz, err := l.Cache.Get(ctx, key)
	if errors.Is(err, ports.ErrArchiveNotFound) {
		if l.Offline {
			return nil, fmt.Errorf("%w: %s (jalankan tanpa -offline)", ErrNotCached, key)
		}
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("cache %s: %w", key, err)
	}
	body, err := gunzip(gz)
	if err != nil {
		return nil, fmt.Errorf("cache %s rusak (hapus file itu lalu jalankan lagi): %w", key, err)
	}
	st.Cached++
	return body, nil
}

func (l *DYFILoader) put(ctx context.Context, key string, body []byte) error {
	gz, err := gzipBytes(body)
	if err != nil {
		return err
	}
	if err := l.Cache.Put(ctx, key, gz); err != nil {
		return fmt.Errorf("menyimpan cache %s: %w", key, err)
	}
	return nil
}

// get membaca dari cache (bila key tidak kosong) atau mengambil dari USGS
// lalu menyimpannya. allow404: 404 disimpan sebagai berkas kosong.
func (l *DYFILoader) get(ctx context.Context, key, u string, maxBytes int64, st *DYFIStats, allow404 bool) ([]byte, error) {
	if key != "" {
		body, err := l.cached(ctx, key, st)
		if !errors.Is(err, ports.ErrArchiveNotFound) {
			return body, err
		}
	}
	body, err := l.fetch(ctx, u, maxBytes, st, allow404)
	if err != nil {
		return nil, err
	}
	if key != "" {
		if err := l.put(ctx, key, body); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// fetch mengambil satu URL, mengulang 429/5xx/jaringan dengan jeda bertingkat
// (Retry-After dihormati); galat klien lain langsung dikembalikan.
func (l *DYFILoader) fetch(ctx context.Context, u string, maxBytes int64, st *DYFIStats, allow404 bool) ([]byte, error) {
	if l.MaxRequests > 0 && l.requests >= l.MaxRequests {
		return nil, &BudgetError{}
	}
	if l.requests > 0 {
		if err := l.Clock.Sleep(ctx, cmp.Or(l.Pause, DefaultPause)); err != nil {
			return nil, err
		}
	}
	l.requests++
	req := ports.Request{URL: u, Accept: "application/json, text/plain", MaxBytes: maxBytes}
	wait := cmp.Or(l.Wait, DefaultWait)
	tries := cmp.Or(l.MaxRetries, DefaultMaxRetries)
	var last error
	for attempt := range tries {
		if attempt > 0 {
			d := wait
			var ra *ports.RetryAfterError
			if errors.As(last, &ra) && ra.After > d {
				d = min(ra.After, 30*time.Minute)
			}
			st.Retries++
			l.notice("%s: %v; ulang %d/%d setelah %v", u, last, attempt, tries-1, d)
			if err := l.Clock.Sleep(ctx, d); err != nil {
				return nil, err
			}
			wait = min(2*wait, 10*time.Minute)
		}
		resp, err := l.Fetch.Fetch(ctx, req)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var se ports.StatusCoder
		switch {
		case err == nil:
			st.Fetched++
			return resp.Body, nil
		case errors.As(err, &se) && se.HTTPStatus() == 404 && allow404:
			st.Fetched++
			return []byte{}, nil
		case errors.As(err, &se) && se.HTTPStatus() < 500 && se.HTTPStatus() != 429:
			return nil, err
		default:
			last = err
		}
	}
	return nil, fmt.Errorf("gagal %d kali: %w", tries, last)
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(io.LimitReader(zr, maxListBytes*4))
}
