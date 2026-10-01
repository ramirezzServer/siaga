package reanalysis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Batas Open-Meteo dihitung per lokasi berbobot (Source.Weight): 600 per
// menit dan 5.000 per jam. Alat kalibrasi memakai paling banyak
// CallsPerMinute supaya ingest yang sedang jalan (paling banyak ±180 per
// menit) tetap punya sisa kuota; satu request berbobot paling banyak
// MaxCallsPerRequest dan berisi paling banyak MaxStepsPerRequest nilai supaya
// respons tetap kecil (deret per jam 8 tahun ±70 ribu nilai per sel).
const (
	MaxLocationsPerRequest = 100
	MaxCallsPerRequest     = 200
	MaxStepsPerRequest     = 400_000
	CallsPerMinute         = 400
)

// MinCoverage adalah bagian langkah yang harus berisi di setiap sel. Data
// historis seharusnya utuh; sel yang banyak kosong berarti periode di luar
// data sumber atau sel laut, dan menghentikan alat daripada menghasilkan
// ambang yang bias.
const MinCoverage = 0.99

// CellTolerance adalah selisih koordinat terbesar (derajat) antara sel yang
// diminta dan sel yang dijawab. Daftar titik memakai koordinat pusat sel
// dengan 4 desimal; Open-Meteo menjawab pusat sel dengan presisi float32.
const CellTolerance = 1e-3

var (
	// ErrEmpty berarti sumber tidak menjawab nilai sama sekali untuk titik
	// uji pertama: nama model atau periode kemungkinan salah.
	ErrEmpty = errors.New("deret historis kosong")
	// ErrCoverage berarti sebuah sel terlalu banyak langkah kosong.
	ErrCoverage = errors.New("deret historis tidak utuh")
	// ErrCellMismatch berarti sumber menjawab sel lain dari yang diminta.
	ErrCellMismatch = errors.New("sel yang dijawab berbeda dari yang diminta")
)

// BudgetError dikembalikan Load saat MaxCalls tercapai sebelum semua sel
// terambil.
type BudgetError struct {
	// Remaining adalah jumlah sel yang belum diambil.
	Remaining int
	// Calls adalah perkiraan panggilan Open-Meteo untuk sisanya.
	Calls float64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("batas panggilan Open-Meteo tercapai: %d sel belum diambil (±%.0f panggilan); jalankan lagi setelah kuota pulih, yang sudah diambil tersimpan di cache",
		e.Remaining, math.Ceil(e.Calls))
}

// Loader mengambil deret Source untuk sel yang belum ada di cache.
type Loader struct {
	Fetch ports.Fetcher
	Clock ports.Clock
	// Cache wajib diisi (NewCache untuk cache kosong) dan dibuat untuk
	// Source yang diambil.
	Cache *Cache
	// MaxCalls membatasi panggilan baru dalam satu kali jalan (0 = tanpa
	// batas). Bila tercapai, Load berhenti dengan BudgetError dan yang
	// sudah diambil tetap ada di cache.
	MaxCalls float64
	// Progress dipanggil setelah tiap request (boleh nil).
	Progress func(done, total int)
	// Checkpoint dipanggil setelah tiap request berhasil, untuk menyimpan
	// cache (boleh nil).
	Checkpoint func(*Cache) error
}

// Plan adalah perkiraan kerja satu kali jalan.
type Plan struct {
	// Cells adalah sel unik yang diminta, Cached bagiannya yang sudah ada.
	Cells, Cached int
	// Calls adalah perkiraan panggilan Open-Meteo untuk sisanya.
	Calls float64
}

// Plan menghitung sel yang belum ada di cache.
func (l *Loader) Plan(cells []series.LatLon) Plan {
	all, missing := l.split(cells)
	return Plan{Cells: len(all), Cached: len(all) - len(missing), Calls: float64(len(missing)) * l.Cache.Source.Weight()}
}

func (l *Loader) split(cells []series.LatLon) (all, missing []series.LatLon) {
	seen := map[series.LatLon]bool{}
	for _, p := range cells {
		p = Key(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		all = append(all, p)
		if _, ok := l.Cache.Get(p); !ok {
			missing = append(missing, p)
		}
	}
	return all, missing
}

// Load mengembalikan deret setiap sel (kunci = koordinat dibulatkan 4
// desimal), mengambil yang belum ada di cache.
func (l *Loader) Load(ctx context.Context, cells []series.LatLon) (map[series.LatLon][]float64, error) {
	if l.Cache == nil {
		return nil, errors.New("cache wajib diisi")
	}
	src := l.Cache.Source
	if err := src.Validate(); err != nil {
		return nil, err
	}
	all, missing := l.split(cells)
	batches := l.batches(missing, l.Cache.Len() == 0)
	w := src.Weight()
	spent, left := 0.0, len(missing)
	for bi, b := range batches {
		// Request terakhir dipotong supaya pas dengan sisa batas.
		if l.MaxCalls > 0 && spent+float64(len(b))*w > l.MaxCalls+1e-9 {
			b = b[:int((l.MaxCalls-spent)/w+1e-9)]
			if len(b) == 0 {
				return nil, &BudgetError{Remaining: left, Calls: float64(left) * w}
			}
		}
		if bi > 0 {
			prev := float64(len(batches[bi-1])) * w
			if err := l.Clock.Sleep(ctx, time.Duration(prev*float64(time.Minute)/CallsPerMinute)); err != nil {
				return nil, err
			}
		}
		got, err := l.fetch(ctx, b)
		if err != nil {
			return nil, err
		}
		if bi == 0 && l.Cache.Len() == 0 && valid(got[0]) == 0 {
			return nil, fmt.Errorf("%w: %s tidak menjawab nilai di %v,%v; periksa model dan periode",
				ErrEmpty, src, b[0].Lat, b[0].Lon)
		}
		for i, p := range b {
			if n := valid(got[i]); float64(n) < MinCoverage*float64(src.Steps()) {
				return nil, fmt.Errorf("%w: sel %v,%v hanya %d dari %d langkah berisi (%s)",
					ErrCoverage, p.Lat, p.Lon, n, src.Steps(), src)
			}
			l.Cache.Put(p, got[i])
		}
		spent += float64(len(b)) * w
		left -= len(b)
		if l.Checkpoint != nil {
			if err := l.Checkpoint(l.Cache); err != nil {
				return nil, err
			}
		}
		if l.Progress != nil {
			l.Progress(bi+1, len(batches))
		}
		if len(b) < len(batches[bi]) {
			return nil, &BudgetError{Remaining: left, Calls: float64(left) * w}
		}
	}
	out := make(map[series.LatLon][]float64, len(all))
	for _, p := range all {
		vs, _ := l.Cache.Get(p)
		out[p] = vs
	}
	return out, nil
}

// batches membagi sel menjadi request. Bila cache masih kosong, request
// pertama hanya satu sel: uji murah bahwa model dan periode dijawab sebelum
// kuota terpakai banyak.
func (l *Loader) batches(missing []series.LatLon, probe bool) [][]series.LatLon {
	src := l.Cache.Source
	size := min(MaxLocationsPerRequest, int(MaxCallsPerRequest/src.Weight()), MaxStepsPerRequest/src.Steps())
	size = max(1, size)
	var out [][]series.LatLon
	if probe && len(missing) > 0 {
		out = append(out, missing[:1])
		missing = missing[1:]
	}
	for len(missing) > 0 {
		n := min(size, len(missing))
		out = append(out, missing[:n])
		missing = missing[n:]
	}
	return out
}

func (l *Loader) fetch(ctx context.Context, pts []series.LatLon) ([][]float64, error) {
	src := l.Cache.Source
	lats := make([]string, len(pts))
	lons := make([]string, len(pts))
	for i, p := range pts {
		lats[i] = strconv.FormatFloat(p.Lat, 'f', -1, 64)
		lons[i] = strconv.FormatFloat(p.Lon, 'f', -1, 64)
	}
	q := url.Values{
		"latitude": {strings.Join(lats, ",")}, "longitude": {strings.Join(lons, ",")},
		string(src.Resolution): {src.Variable}, "models": {src.Model},
		"timeformat": {"unixtime"}, "timezone": {"GMT"}, "cell_selection": {"nearest"},
		"start_date": {Date(src.From)}, "end_date": {Date(src.To)},
	}
	resp, err := l.Fetch.Fetch(ctx, ports.Request{
		URL: src.Endpoint + "?" + strings.ReplaceAll(q.Encode(), "%2C", ","), Accept: "application/json", MaxBytes: 128 << 20,
	})
	if err != nil {
		return nil, fmt.Errorf("meminta %s untuk %d sel: %w", src.Variable, len(pts), err)
	}
	parse := openmeteo.ParseDaily
	if src.Resolution == Hourly {
		parse = openmeteo.ParseHourly
	}
	cells, err := parse(resp.Body, src.Variable)
	if err != nil {
		return nil, err
	}
	if len(cells) != len(pts) {
		return nil, fmt.Errorf("%w: %d lokasi dijawab untuk %d", openmeteo.ErrStructure, len(cells), len(pts))
	}
	out := make([][]float64, len(cells))
	for i, c := range cells {
		switch {
		case c.Unit != src.Unit:
			return nil, fmt.Errorf("%w: satuan %s %q, ingin %q", openmeteo.ErrStructure, src.Variable, c.Unit, src.Unit)
		case len(c.Values) != src.Steps():
			return nil, fmt.Errorf("%w: %d langkah dijawab untuk %d", openmeteo.ErrStructure, len(c.Values), src.Steps())
		case math.Abs(c.Cell.Lat-pts[i].Lat) > CellTolerance || math.Abs(c.Cell.Lon-pts[i].Lon) > CellTolerance:
			return nil, fmt.Errorf("%w: diminta %v,%v, dijawab %v,%v; pakai koordinat pusat sel",
				ErrCellMismatch, pts[i].Lat, pts[i].Lon, c.Cell.Lat, c.Cell.Lon)
		}
		vs := make([]float64, len(c.Values))
		for k, v := range c.Values {
			vs[k] = math.NaN()
			if v != nil && !math.IsNaN(*v) {
				if *v < 0 || math.IsInf(*v, 0) {
					return nil, fmt.Errorf("%w: nilai %v di sel %v,%v", openmeteo.ErrStructure, *v, c.Cell.Lat, c.Cell.Lon)
				}
				vs[k] = *v
			}
		}
		out[i] = vs
	}
	return out, nil
}

func valid(vs []float64) int {
	n := 0
	for _, v := range vs {
		if !math.IsNaN(v) {
			n++
		}
	}
	return n
}
