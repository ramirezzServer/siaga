// Package riversnap adalah use case kalibrasi titik pantau sungai (make
// river-snap): membaca daftar titik berkoordinat perkiraan, meminta debit
// reanalisis GloFAS periode acuan untuk sel di sekitarnya (disimpan di
// cache), memilih sel per titik (domain/riversnap), lalu menulis daftar titik
// untuk ingest dan laporan pemeriksaan.
package riversnap

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/openmeteo"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/riversnap"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// Batas Open-Meteo dihitung per lokasi berbobot (Window.Weight): 600 per
// menit dan 5.000 per jam. Alat ini memakai paling banyak CallsPerMinute
// supaya ingest yang sedang jalan (paling banyak ±180 per menit) tetap punya
// sisa kuota, dan satu request berbobot paling banyak MaxCallsPerRequest.
const (
	MaxLocationsPerRequest = 100
	MaxCallsPerRequest     = 200
	CallsPerMinute         = 400
)

var slug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ReadStations membaca CSV sumber id,sungai,nama,lat,lon,radius,catatan
// (baris komentar # dilewati), urut hulu ke hilir per sungai.
func ReadStations(r io.Reader) ([]riversnap.Station, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	for line := range strings.SplitSeq(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	cr := csv.NewReader(&body)
	cr.FieldsPerRecord = 7
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV titik sungai: %w", err)
	}
	if len(recs) == 0 || strings.Join(recs[0], ",") != "id,sungai,nama,lat,lon,radius,catatan" {
		return nil, errors.New("header CSV titik sungai harus id,sungai,nama,lat,lon,radius,catatan")
	}
	var out []riversnap.Station
	seen := map[string]bool{}
	for _, rec := range recs[1:] {
		lat, err1 := strconv.ParseFloat(rec[3], 64)
		lon, err2 := strconv.ParseFloat(rec[4], 64)
		rad, err3 := strconv.ParseFloat(rec[5], 64)
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("titik %s: %w", rec[0], err)
		}
		st := riversnap.Station{
			ID: rec[0], River: strings.TrimSpace(rec[1]), Name: strings.TrimSpace(rec[2]),
			Approx: series.LatLon{Lat: lat, Lon: lon}, Radius: rad,
		}
		switch {
		case !slug.MatchString(st.ID) || seen[st.ID]:
			return nil, fmt.Errorf("id titik %q bukan slug atau ganda", st.ID)
		case st.River == "" || st.Name == "":
			return nil, fmt.Errorf("titik %s: sungai dan nama wajib diisi", st.ID)
		}
		if err := st.Validate(); err != nil {
			return nil, err
		}
		seen[st.ID] = true
		out = append(out, st)
	}
	return out, nil
}

// Snapper menjalankan kalibrasi.
type Snapper struct {
	Endpoint string
	Fetch    ports.Fetcher
	Clock    ports.Clock
	// Window adalah periode acuan reanalisis yang dirata-rata.
	Window Window
	// Cache berisi jawaban yang sudah diambil untuk Window; titik yang ada
	// di cache tidak diminta lagi. Wajib diisi (NewCache untuk cache kosong).
	Cache *Cache
	// MaxCalls membatasi panggilan Open-Meteo baru dalam satu kali jalan
	// (0 = tanpa batas). Bila tercapai, Run berhenti dengan BudgetError dan
	// yang sudah diambil tetap ada di cache.
	MaxCalls float64
	// Progress dipanggil setelah tiap request (boleh nil).
	Progress func(done, total int)
	// Checkpoint dipanggil setelah tiap request berhasil, untuk menyimpan
	// cache (boleh nil).
	Checkpoint func(*Cache) error
}

// ErrEmptyReanalysis berarti sumber tidak menjawab debit untuk titik uji
// pertama: nama model atau periode acuan kemungkinan salah.
var ErrEmptyReanalysis = errors.New("reanalisis GloFAS kosong")

// BudgetError dikembalikan Run saat MaxCalls tercapai sebelum semua titik
// permintaan terambil.
type BudgetError struct {
	// Remaining adalah jumlah titik permintaan yang belum diambil.
	Remaining int
	// Calls adalah perkiraan panggilan Open-Meteo untuk sisanya.
	Calls float64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("batas panggilan Open-Meteo tercapai: %d titik permintaan belum diambil (±%.0f panggilan); jalankan lagi setelah kuota harian pulih, yang sudah diambil tersimpan di cache",
		e.Remaining, math.Ceil(e.Calls))
}

// Plan adalah perkiraan kerja satu kali jalan.
type Plan struct {
	// Points adalah titik permintaan unik untuk semua titik pantau.
	Points int
	// Cached adalah bagian Points yang sudah ada di cache.
	Cached int
	// Calls adalah perkiraan panggilan Open-Meteo untuk sisanya.
	Calls float64
}

// Plan menghitung titik permintaan yang belum ada di cache.
func (s *Snapper) Plan(stations []riversnap.Station) Plan {
	all, missing := s.points(stations)
	return Plan{Points: len(all), Cached: len(all) - len(missing), Calls: float64(len(missing)) * s.Window.Weight()}
}

// points mengembalikan titik permintaan unik untuk semua titik pantau dan
// yang belum ada di cache. Urutannya tetap: per titik pantau, sel tengah
// dulu, jadi titik uji pertama adalah koordinat perkiraan titik pertama.
func (s *Snapper) points(stations []riversnap.Station) (all, missing []series.LatLon) {
	seen := map[series.LatLon]bool{}
	for _, st := range stations {
		offs := st.Offsets()
		mid := len(offs) / 2
		for _, p := range append([]series.LatLon{offs[mid]}, append(offs[:mid:mid], offs[mid+1:]...)...) {
			p = key(p)
			if seen[p] {
				continue
			}
			seen[p] = true
			all = append(all, p)
			if _, ok := s.Cache.Get(p); !ok {
				missing = append(missing, p)
			}
		}
	}
	return all, missing
}

// Run mengambil debit periode acuan untuk titik permintaan yang belum ada
// di cache, lalu memilih satu sel per titik pantau.
func (s *Snapper) Run(ctx context.Context, stations []riversnap.Station) ([]riversnap.Choice, error) {
	if err := s.Window.Validate(); err != nil {
		return nil, err
	}
	if s.Cache == nil || s.Cache.Window != s.Window {
		return nil, fmt.Errorf("%w: cache harus dibuat untuk %s", ErrCacheWindow, s.Window)
	}
	_, missing := s.points(stations)
	batches := s.batches(missing, s.Cache.Len() == 0)
	w := s.Window.Weight()
	spent, left := 0.0, len(missing)
	for bi, b := range batches {
		// Request terakhir dipotong supaya pas dengan sisa batas.
		if s.MaxCalls > 0 && spent+float64(len(b))*w > s.MaxCalls+1e-9 {
			b = b[:int((s.MaxCalls-spent)/w+1e-9)]
			if len(b) == 0 {
				return nil, &BudgetError{Remaining: left, Calls: float64(left) * w}
			}
		}
		cost := float64(len(b)) * w
		if bi > 0 {
			prev := float64(len(batches[bi-1])) * w
			if err := s.Clock.Sleep(ctx, time.Duration(prev*float64(time.Minute)/CallsPerMinute)); err != nil {
				return nil, err
			}
		}
		cells, err := s.fetch(ctx, b)
		if err != nil {
			return nil, err
		}
		if bi == 0 && s.Cache.Len() == 0 && cells[0].Valid == 0 {
			return nil, fmt.Errorf("%w: model %s tidak menjawab debit %s..%s di %v,%v; periksa -model, -from, dan -to",
				ErrEmptyReanalysis, s.Window.Model, date(s.Window.From), date(s.Window.To), b[0].Lat, b[0].Lon)
		}
		for i, p := range b {
			s.Cache.Put(p, cells[i])
		}
		spent += cost
		left -= len(b)
		if s.Checkpoint != nil {
			if err := s.Checkpoint(s.Cache); err != nil {
				return nil, err
			}
		}
		if s.Progress != nil {
			s.Progress(bi+1, len(batches))
		}
		if len(b) < len(batches[bi]) {
			return nil, &BudgetError{Remaining: left, Calls: float64(left) * w}
		}
	}
	return s.choose(stations)
}

// batches membagi titik permintaan menjadi request berbobot paling banyak
// MaxCallsPerRequest. Bila cache masih kosong, request pertama hanya satu
// titik: uji murah bahwa model dan periode acuan dijawab sebelum kuota
// terpakai banyak.
func (s *Snapper) batches(missing []series.LatLon, probe bool) [][]series.LatLon {
	size := max(1, min(MaxLocationsPerRequest, int(MaxCallsPerRequest/s.Window.Weight())))
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

// choose membentuk kandidat per titik pantau dari cache. Sel yang hari
// berisinya kurang dari MinCoverage periode acuan dianggap tanpa debit.
func (s *Snapper) choose(stations []riversnap.Station) ([]riversnap.Choice, error) {
	choices := make([]riversnap.Choice, len(stations))
	for i, st := range stations {
		var cands []riversnap.Candidate
		for _, p := range st.Offsets() {
			c, ok := s.Cache.Get(p)
			if !ok {
				return nil, fmt.Errorf("titik %s: sel %v,%v belum ada di cache", st.ID, p.Lat, p.Lon)
			}
			mean := c.Mean
			if c.Valid < s.Window.MinValid() {
				mean = math.NaN()
			}
			cands = append(cands, riversnap.Candidate{Cell: c.Cell, Mean: mean})
		}
		ch, err := riversnap.Choose(st, cands)
		if err != nil {
			return nil, err
		}
		choices[i] = ch
	}
	return riversnap.Review(choices), nil
}

func (s *Snapper) fetch(ctx context.Context, pts []series.LatLon) ([]CachedCell, error) {
	lats := make([]string, len(pts))
	lons := make([]string, len(pts))
	for i, p := range pts {
		lats[i] = strconv.FormatFloat(p.Lat, 'f', -1, 64)
		lons[i] = strconv.FormatFloat(p.Lon, 'f', -1, 64)
	}
	q := url.Values{
		"latitude": {strings.Join(lats, ",")}, "longitude": {strings.Join(lons, ",")},
		"daily": {"river_discharge"}, "models": {s.Window.Model}, "timeformat": {"unixtime"}, "timezone": {"GMT"},
		"start_date": {date(s.Window.From)}, "end_date": {date(s.Window.To)},
	}
	resp, err := s.Fetch.Fetch(ctx, ports.Request{
		URL: s.Endpoint + "?" + strings.ReplaceAll(q.Encode(), "%2C", ","), Accept: "application/json", MaxBytes: 64 << 20,
	})
	if err != nil {
		return nil, fmt.Errorf("meminta debit %d lokasi: %w", len(pts), err)
	}
	cells, err := openmeteo.ParseDaily(resp.Body, "river_discharge")
	if err != nil {
		return nil, err
	}
	if len(cells) != len(pts) {
		return nil, fmt.Errorf("%w: %d lokasi dijawab untuk %d", openmeteo.ErrStructure, len(cells), len(pts))
	}
	out := make([]CachedCell, len(cells))
	for i, c := range cells {
		if len(c.Values) != s.Window.Days() {
			return nil, fmt.Errorf("%w: %d hari dijawab untuk periode %d hari", openmeteo.ErrStructure, len(c.Values), s.Window.Days())
		}
		m, n := mean(c.Values)
		if n > 0 && m < 0 {
			return nil, fmt.Errorf("%w: debit rata-rata negatif di %v,%v", openmeteo.ErrStructure, c.Cell.Lat, c.Cell.Lon)
		}
		if !series.InIndonesia(c.Cell) {
			return nil, fmt.Errorf("%w: sel %v,%v di luar Indonesia", openmeteo.ErrStructure, c.Cell.Lat, c.Cell.Lon)
		}
		out[i] = CachedCell{Cell: c.Cell, Mean: m, Valid: n}
	}
	return out, nil
}

// mean adalah rata-rata nilai yang terisi (dibulatkan 4 desimal seperti di
// file cache, jadi jalan pertama dan jalan ulang dari cache sama persis)
// beserta jumlahnya; NaN bila semuanya kosong.
func mean(vs []*float64) (float64, int) {
	var sum float64
	n := 0
	for _, v := range vs {
		if v != nil {
			sum += *v
			n++
		}
	}
	if n == 0 {
		return math.NaN(), 0
	}
	return math.Round(sum/float64(n)*1e4) / 1e4, n
}

// WriteSites menulis daftar titik untuk ingest (sitelist/data/rivers_<PP>.csv).
func WriteSites(w io.Writer, province, source string, choices []riversnap.Choice) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Titik pantau debit sungai provinsi %s (PRD, bagian Sungai yang dipantau).\n", province)
	fmt.Fprintf(&b, "# Koordinat = pusat sel GloFAS terpilih. Dibuat `make river-snap` dari %s; jangan diedit manual.\n", source)
	fmt.Fprintf(&b, "# %d titik\n", len(choices))
	cw := csv.NewWriter(&b)
	_ = cw.Write([]string{"id", "sungai", "nama", "lat", "lon"})
	for _, c := range choices {
		_ = cw.Write([]string{c.ID, c.River, c.Name, coord(c.Cell.Lat), coord(c.Cell.Lon)})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteReport menulis laporan kalibrasi dalam Markdown.
func WriteReport(w io.Writer, source string, at time.Time, win Window, choices []riversnap.Choice) error {
	var b strings.Builder
	suspects := slices.IndexFunc(choices, riversnap.Choice.Suspect) >= 0
	fmt.Fprintf(&b, "# Kalibrasi titik pantau sungai\n\n")
	fmt.Fprintf(&b, "Dibuat `make river-snap` pada %s dari `%s`. Jangan diedit manual; ubah file sumber lalu jalankan ulang.\n\n", at.UTC().Format("2006-01-02 15:04 UTC"), source)
	fmt.Fprintf(&b, "Setiap titik pantau PRD adalah nama lokasi. Alat ini merata-rata debit harian reanalisis GloFAS v4 (Open-Meteo `models=%s`, grid 0,05°) periode acuan %s sampai %s (%d hari) untuk sel di sekitar koordinat perkiraan. ",
		win.Model, date(win.From), date(win.To), win.Days())
	b.WriteString("Debit rata-rata periode acuan hampir sebanding dengan luas daerah tangkapan di hulu sel, jadi pilihan sel tidak berubah dengan hujan beberapa hari terakhir; reanalisis juga tetap, jadi jalan ulang memberi hasil yang sama (ADR 0019). ")
	b.WriteString("Di dalam radius pencarian, sel alur utama adalah sel yang debit rata-ratanya paling sedikit 30% dari debit terbesar; dari sel itu dipilih yang paling dekat dengan koordinat perkiraan (memilih debit terbesar saja akan selalu menggeser titik ke hilir). ")
	fmt.Fprintf(&b, "Sel yang hari berisinya kurang dari %.0f%% periode acuan tidak dipakai. ", MinCoverage*100)
	b.WriteString("Aturan ini bisa salah saat sungai lain yang lebih besar ada di dalam radius, jadi pilihan yang meragukan ditandai untuk diperiksa di peta (PRD, bagian Batas kemampuan data).\n\n")
	b.WriteString("Tanda: `tepi` sel terpilih di tepi radius, `kecil` debit rata-rata < 0,5 m³/s, `hilir-lebih-kecil` debit lebih kecil dari titik hulu sebelumnya di sungai yang sama (wajar di hilir bendungan), `sel-ganda` sel dipakai titik lain, `manual` titik sudah diverifikasi (radius 0).\n\n")
	b.WriteString("Setelah memeriksa satu titik di peta OSM, tulis koordinat sel yang benar di file sumber dengan radius 0 dan catatan asal verifikasinya.\n\n")
	b.WriteString("| Titik | Sungai | Perkiraan | Radius | Sel terpilih | Geser (km) | Debit rata-rata periode acuan (m³/s) | Tanda |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range choices {
		flags := make([]string, len(c.Flags))
		for i, f := range c.Flags {
			flags[i] = "`" + string(f) + "`"
		}
		fmt.Fprintf(&b, "| %s (`%s`) | %s | %s, %s | %s° | %s, %s | %s | %s | %s |\n",
			c.Name, c.ID, c.River, coord(c.Approx.Lat), coord(c.Approx.Lon), num(c.Radius, 2),
			coord(c.Cell.Lat), coord(c.Cell.Lon), num(c.DistanceKm, 1), num(c.Mean, 2), strings.Join(flags, " "))
	}
	if suspects {
		b.WriteString("\nTitik bertanda (selain `manual`) belum boleh dipakai untuk ambang banjir sebelum diverifikasi.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func coord(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// num menulis angka dengan koma desimal (bahasa Indonesia).
func num(v float64, decimals int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', decimals, 64), ".", ",", 1)
}
