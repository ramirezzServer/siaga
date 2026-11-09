// Package isclabels mengambil ISC Bulletin per minggu untuk set berlabel
// deduplikasi gempa (make dedup-labels, ADR 0024): satu request FDSN event
// per minggu di kotak Indonesia ingest dengan includeallorigins=true, setiap
// jawaban disimpan ter-gzip di cache, jadi jalan yang terputus dilanjutkan
// dan jalan ulang tidak menyentuh ISC.
//
// ISC tidak menerbitkan batas request; alat mengambil satu minggu sekali
// jalan, berurutan, dengan jeda di antara request.
package isclabels

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/quakeml"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/area"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/quakelabel"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// DefaultBaseURL adalah layanan FDSN event ISC.
const DefaultBaseURL = "https://www.isc.ac.uk/fdsnws/event/1/query"

// WeekDays adalah panjang satu potongan. Satu minggu Indonesia ±1,8–5 MB
// QuakeML dan ±15–20 dtk dijawab ISC.
const WeekDays = 7

// Bawaan Loader.
const (
	DefaultPause      = 2 * time.Second
	DefaultWait       = 30 * time.Second
	DefaultMaxRetries = 5
	// MaxBytes menampung minggu terpadat (gempa besar dengan ribuan susulan).
	MaxBytes = 128 << 20
)

// Range adalah rentang hari UTC inklusif.
type Range struct{ From, To time.Time }

// Valid melaporkan apakah rentang terisi dan tidak terbalik.
func (r Range) Valid() bool { return !r.From.IsZero() && !r.To.IsZero() && !r.To.Before(r.From) }

// Week adalah satu potongan [Start, Start+Days hari).
type Week struct {
	Start time.Time
	Days  int
}

// End adalah batas akhir eksklusif.
func (w Week) End() time.Time { return w.Start.AddDate(0, 0, w.Days) }

// Key adalah kunci cache, misal "2021/2021-01-01.d7.xml.gz".
func (w Week) Key() string {
	return fmt.Sprintf("%s/%s.d%d.xml.gz", w.Start.Format("2006"), w.Start.Format(time.DateOnly), w.Days)
}

// Plan memecah rentang menjadi potongan WeekDays hari mulai From; potongan
// terakhir dipendekkan sampai To.
func Plan(r Range) []Week {
	if !r.Valid() {
		return nil
	}
	from := r.From.UTC().Truncate(24 * time.Hour)
	end := r.To.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	var out []Week
	for s := from; s.Before(end); s = s.AddDate(0, 0, WeekDays) {
		days := WeekDays
		if e := s.AddDate(0, 0, days); e.After(end) {
			days = int(end.Sub(s) / (24 * time.Hour))
		}
		out = append(out, Week{Start: s, Days: days})
	}
	return out
}

// Cache menyimpan jawaban mentah (sudah ter-gzip oleh Loader).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte) error
}

// Loader mengambil potongan dari cache atau ISC.
type Loader struct {
	Fetch       ports.Fetcher
	Clock       ports.Clock
	Cache       Cache
	BaseURL     string
	Box         area.Box
	MaxRequests int
	// Offline: potongan yang tidak ada di cache adalah galat.
	Offline    bool
	Pause      time.Duration
	Wait       time.Duration
	MaxRetries int
	Progress   func(done, total int, w Week, cached bool)
	Notice     func(string)
}

// Stats merangkum Load.
type Stats struct {
	Weeks, Cached, Fetched, Empty, Retries int
	Bytes                                  int64
}

// BudgetError berarti -max-requests tercapai sebelum semua potongan diambil.
type BudgetError struct{ Remaining int }

func (e *BudgetError) Error() string {
	return fmt.Sprintf("batas request tercapai; %d minggu belum diambil (jalankan lagi untuk melanjutkan)", e.Remaining)
}

// ErrNotCached dikembalikan saat Offline dan potongan belum ada di cache.
var ErrNotCached = errors.New("minggu belum ada di cache")

// URL membentuk request satu minggu, dengan urutan parameter dan waktu tanpa
// pengodean persen seperti contoh di dokumentasi ISC (titik dua sah di query).
func (l *Loader) URL(w Week) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	const layout = "2006-01-02T15:04:05"
	return fmt.Sprintf("%s?starttime=%s&endtime=%s&minlatitude=%s&maxlatitude=%s&minlongitude=%s&maxlongitude=%s&includeallorigins=true",
		cmp.Or(l.BaseURL, DefaultBaseURL), w.Start.Format(layout), w.End().Format(layout),
		f(l.Box.South), f(l.Box.North), f(l.Box.West), f(l.Box.East))
}

func (l *Loader) notice(format string, a ...any) {
	if l.Notice != nil {
		l.Notice(fmt.Sprintf(format, a...))
	}
}

// Load memanggil visit untuk setiap minggu, urut sesuai weeks.
func (l *Loader) Load(ctx context.Context, weeks []Week, visit func(Week, []quakelabel.Event) error) (Stats, error) {
	if l.Cache == nil || (!l.Offline && (l.Fetch == nil || l.Clock == nil)) {
		return Stats{}, errors.New("isclabels: Cache, Fetch, dan Clock wajib diisi")
	}
	if err := l.Box.Validate(); err != nil {
		return Stats{}, err
	}
	st := Stats{Weeks: len(weeks)}
	for i, w := range weeks {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		gz, err := l.Cache.Get(ctx, w.Key())
		switch {
		case err == nil:
			body, err := gunzip(gz)
			if err != nil {
				return st, fmt.Errorf("cache %s rusak (hapus file itu lalu jalankan lagi): %w", w.Key(), err)
			}
			events, err := quakeml.Parse(body)
			if err != nil {
				return st, fmt.Errorf("cache %s rusak (hapus file itu lalu jalankan lagi): %w", w.Key(), err)
			}
			st.Cached++
			st.Bytes += int64(len(body))
			if err := visit(w, events); err != nil {
				return st, err
			}
			l.progress(i+1, len(weeks), w, true)
			continue
		case !errors.Is(err, ports.ErrArchiveNotFound):
			return st, fmt.Errorf("cache %s: %w", w.Key(), err)
		case l.Offline:
			return st, fmt.Errorf("%w: %s (jalankan tanpa -offline)", ErrNotCached, w.Key())
		}
		if l.MaxRequests > 0 && st.Fetched >= l.MaxRequests {
			return st, &BudgetError{Remaining: len(weeks) - i}
		}
		if st.Fetched > 0 {
			if err := l.Clock.Sleep(ctx, cmp.Or(l.Pause, DefaultPause)); err != nil {
				return st, err
			}
		}
		body, events, err := l.fetch(ctx, w, &st)
		if err != nil {
			return st, fmt.Errorf("minggu %s: %w", w.Start.Format(time.DateOnly), err)
		}
		st.Fetched++
		st.Bytes += int64(len(body))
		packed, err := gzipBytes(body)
		if err != nil {
			return st, err
		}
		if err := l.Cache.Put(ctx, w.Key(), packed); err != nil {
			return st, fmt.Errorf("menyimpan cache %s: %w", w.Key(), err)
		}
		if err := visit(w, events); err != nil {
			return st, err
		}
		l.progress(i+1, len(weeks), w, false)
	}
	return st, nil
}

func (l *Loader) progress(done, total int, w Week, cached bool) {
	if l.Progress != nil {
		l.Progress(done, total, w, cached)
	}
}

// fetch mengambil satu minggu, mengulang galat sementara (429/503 dengan
// Retry-After, 5xx, jaringan, dan jawaban yang bukan QuakeML utuh) dengan
// jeda bertingkat. 204 (tanpa data) menjadi dokumen kosong.
func (l *Loader) fetch(ctx context.Context, w Week, st *Stats) ([]byte, []quakelabel.Event, error) {
	req := ports.Request{URL: l.URL(w), Accept: "application/xml", MaxBytes: MaxBytes}
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
			l.notice("minggu %s: %v; ulang %d/%d setelah %v", w.Start.Format(time.DateOnly), last, attempt, tries-1, d)
			if err := l.Clock.Sleep(ctx, d); err != nil {
				return nil, nil, err
			}
			wait = min(2*wait, 10*time.Minute)
		}
		resp, err := l.Fetch.Fetch(ctx, req)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		var se ports.StatusCoder
		switch {
		case err == nil:
			events, perr := quakeml.Parse(resp.Body)
			if perr == nil {
				return resp.Body, events, nil
			}
			last = perr
		case errors.As(err, &se) && se.HTTPStatus() == 204:
			st.Empty++
			events, perr := quakeml.Parse([]byte(quakeml.Empty))
			return []byte(quakeml.Empty), events, perr
		case errors.As(err, &se) && se.HTTPStatus() < 500 && se.HTTPStatus() != 429:
			return nil, nil, err
		default:
			last = err
		}
	}
	return nil, nil, fmt.Errorf("gagal %d kali: %w", tries, last)
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
	return io.ReadAll(io.LimitReader(zr, 4*MaxBytes))
}

// Header kolom CSV set berlabel; dibaca adapter catalog geo-processor.
var Header = []string{
	"dasar_label", "kejadian_label", "sumber", "id", "waktu_utc", "lintang", "bujur",
	"kedalaman_km", "magnitudo", "tipe_magnitudo", "magnitudo_lain", "catatan",
}

// Meta adalah keterangan di komentar awal CSV.
type Meta struct {
	Range Range
	Box   area.Box
	Opt   quakelabel.Options
	Stats quakelabel.Stats
}

// Citation adalah sitasi ISC Bulletin yang diminta ISC.
const Citation = "International Seismological Centre (2026), On-line Bulletin, https://doi.org/10.31905/D808B830"

// WriteCSV menulis set berlabel. Baris # di awal adalah komentar.
func WriteCSV(w io.Writer, m Meta, rows []quakelabel.Row) error {
	f := func(v float64, digits int) string { return strconv.FormatFloat(v, 'f', digits, 64) }
	comments := []string{
		"# Set berlabel deduplikasi gempa BMKG–USGS dari ISC Bulletin (make dedup-labels, ADR 0024). Jangan diedit manual.",
		"# Sumber: " + Citation,
		fmt.Sprintf("# Periode %s..%s (UTC), kotak %s; origin NEIC wakil M >= %s, origin DJA wakil <= %s dtk dan <= %s km dari salah satunya",
			m.Range.From.Format(time.DateOnly), m.Range.To.Format(time.DateOnly), m.Box,
			f(m.Opt.MinUSGSMagnitude, 1), f(m.Opt.Window.Seconds, 0), f(m.Opt.Window.Km, 0)),
		fmt.Sprintf("# %d kejadian ISC, %d origin USGS (%d berpasangan BMKG), %d origin BMKG, %d pasangan negatif dalam jendela",
			m.Stats.Events, m.Stats.USGS, m.Stats.Positive, m.Stats.BMKG, m.Stats.Negative),
	}
	for _, c := range comments {
		if _, err := io.WriteString(w, c+"\n"); err != nil {
			return err
		}
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(Header); err != nil {
		return err
	}
	for _, r := range rows {
		depth := ""
		if !math.IsNaN(r.DepthKm) {
			depth = f(r.DepthKm, 1)
		}
		rec := []string{
			string(r.Basis), r.Label, string(r.Source), r.ID, r.Time.UTC().Format("2006-01-02T15:04:05.00Z"),
			f(r.Latitude, 4), f(r.Longitude, 4), depth, f(r.Magnitude, 2), r.MagnitudeType, r.Others, r.Note,
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
