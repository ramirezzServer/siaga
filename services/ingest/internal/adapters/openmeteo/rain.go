package openmeteo

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/rawpb"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/catchment"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// ModelRain adalah ECMWF IFS 9 km: model yang sama dengan arsip klimatologi
// indeks hujan (make rain-threshold, ADR 0020).
const ModelRain = "ecmwf_ifs"

// RainCellOffset adalah selisih lintang/bujur terbesar (derajat) antara sel
// yang diminta dan sel yang dijawab. Daftar sel berisi pusat sel ecmwf_ifs,
// jadi dengan cell_selection=nearest sumber menjawab sel itu sendiri (dicek
// 2026-10-02: selisih terbesar 0,00005°). Selisih lebih besar berarti grid
// model berubah dan bobot sub-DAS tidak berlaku lagi.
const RainCellOffset = 0.001

// Jendela permintaan hujan: kemarin sampai 3 hari ke depan (tanggal UTC),
// supaya akumulasi 24 jam yang berakhir dini hari ini lengkap. Jendela
// tanggal yang tetap sepanjang hari membuat isi hanya berubah bila model
// diperbarui (deteksi "tidak berubah" di poll).
const (
	rainPastDays  = 1
	rainAheadDays = aheadDays
)

// RainConnector meminta hujan per jam di semua sel yang menutupi sub-DAS
// dalam satu request, lalu menerbitkan satu event raw.rain.openmeteo per
// sub-DAS berisi hujan rata-rata wilayah (bobot luas).
type RainConnector struct {
	endpoint string
	basins   []catchment.Basin
	cells    []series.LatLon
	now      func() time.Time
}

var _ ports.Connector = (*RainConnector)(nil)

// NewRainConnector membuat konektor openmeteo-hujan untuk basins.
func NewRainConnector(endpoint string, basins []catchment.Basin, now func() time.Time) (*RainConnector, error) {
	if len(basins) == 0 {
		return nil, fmt.Errorf("openmeteo-hujan: tanpa sub-DAS")
	}
	seen := map[string]bool{}
	for _, b := range basins {
		if !series.ValidSiteID(b.SiteID()) || seen[b.ID] || len(b.Cells) == 0 || len(b.Cells) != len(b.Weights) {
			return nil, fmt.Errorf("openmeteo-hujan: sub-DAS %q tidak valid atau ganda", b.ID)
		}
		seen[b.ID] = true
	}
	if _, err := url.Parse(endpoint); err != nil || endpoint == "" {
		return nil, fmt.Errorf("openmeteo-hujan: endpoint %q tidak valid", endpoint)
	}
	return &RainConnector{endpoint: endpoint, basins: basins, cells: catchment.Cells(basins), now: now}, nil
}

// Name mengembalikan "openmeteo-hujan".
func (c *RainConnector) Name() string { return "openmeteo-hujan" }

// ArchiveExt mengembalikan "json".
func (c *RainConnector) ArchiveExt() string { return "json" }

// Sites mengembalikan jumlah sel yang diminta; dipakai menghitung kuota
// Open-Meteo (satu lokasi = satu panggilan untuk 5 hari × 1 variabel).
func (c *RainConnector) Sites() int { return len(c.cells) }

// Request membentuk satu request untuk semua sel.
func (c *RainConnector) Request() ports.Request {
	now := c.now().UTC()
	lats := make([]string, len(c.cells))
	lons := make([]string, len(c.cells))
	for i, p := range c.cells {
		lats[i] = strconv.FormatFloat(p.Lat, 'f', -1, 64)
		lons[i] = strconv.FormatFloat(p.Lon, 'f', -1, 64)
	}
	q := url.Values{
		"latitude":       {strings.Join(lats, ",")},
		"longitude":      {strings.Join(lons, ",")},
		"hourly":         {"precipitation"},
		"models":         {ModelRain},
		"cell_selection": {"nearest"},
		"timeformat":     {"unixtime"},
		"timezone":       {"GMT"},
		"start_date":     {now.AddDate(0, 0, -rainPastDays).Format(time.DateOnly)},
		"end_date":       {now.AddDate(0, 0, rainAheadDays).Format(time.DateOnly)},
	}
	return ports.Request{
		URL:      c.endpoint + "?" + strings.ReplaceAll(q.Encode(), "%2C", ","),
		Accept:   "application/json",
		MaxBytes: maxPayload,
	}
}

// Parse membaca respons semua sel. Jumlah lokasi, satuan, waktu, atau sel
// yang tidak sesuai membuat seluruh payload ditolak, karena bobot sub-DAS
// hanya berlaku untuk sel yang diminta. Sub-DAS yang hujan rata-ratanya
// melanggar invarian ditolak sendiri-sendiri.
func (c *RainConnector) Parse(body []byte, fetchedAt time.Time) ([]ports.Event, []ports.Rejection, error) {
	locs, err := decode(body)
	if err != nil {
		return nil, nil, err
	}
	if len(locs) != len(c.cells) {
		return nil, nil, fmt.Errorf("%w: %d lokasi dijawab untuk %d sel", ErrStructure, len(locs), len(c.cells))
	}
	spec := series.RainfallSpec
	if err := checkUnits(locs[0], spec); err != nil {
		return nil, nil, err
	}
	times, data, err := c.cellSeries(locs)
	if err != nil {
		return nil, nil, err
	}
	var events []ports.Event
	var rejections []ports.Rejection
	for _, b := range c.basins {
		ev, err := c.basinEvent(b, times, data, fetchedAt)
		if err != nil {
			rejections = append(rejections, ports.Rejection{Key: b.SiteID(), Reason: err})
			continue
		}
		events = append(events, ev)
	}
	return events, rejections, nil
}

// checkUnits memastikan satuan waktu dan hujan sesuai spesifikasi.
func checkUnits(l location, spec series.Spec) error {
	if got := l.HourlyU["time"]; got != "unixtime" {
		return fmt.Errorf("%w: satuan waktu %q, harus unixtime", ErrStructure, got)
	}
	for _, v := range spec.Vars {
		if got, ok := l.HourlyU[v.Name]; !ok || got != v.Unit {
			return fmt.Errorf("%w: satuan %s %q, harus %q", ErrStructure, v.Name, got, v.Unit)
		}
	}
	return nil
}

// cellSeries memeriksa setiap lokasi (sel, zona waktu, waktu langkah yang
// sama di semua sel) dan mengembalikan deret hujan per sel dengan NaN untuk
// nilai kosong.
func (c *RainConnector) cellSeries(locs []location) ([]time.Time, map[series.LatLon][]float64, error) {
	var times []time.Time
	data := make(map[series.LatLon][]float64, len(locs))
	for i, l := range locs {
		want := c.cells[i]
		switch {
		case l.Latitude == nil || l.Longitude == nil:
			return nil, nil, fmt.Errorf("%w: lokasi %d tanpa koordinat sel", ErrStructure, i)
		case math.Abs(*l.Latitude-want.Lat) > RainCellOffset || math.Abs(*l.Longitude-want.Lon) > RainCellOffset:
			return nil, nil, fmt.Errorf("%w: sel %v,%v dijawab %v,%v; daftar sel sub-DAS perlu diperbarui", ErrStructure, want.Lat, want.Lon, *l.Latitude, *l.Longitude)
		case l.UTCOffset != nil && *l.UTCOffset != 0:
			return nil, nil, fmt.Errorf("%w: utc_offset_seconds %d, harus 0", ErrStructure, *l.UTCOffset)
		}
		ts := l.Hourly["time"]
		col, ok := l.Hourly["precipitation"]
		if !ok || len(col) != len(ts) {
			return nil, nil, fmt.Errorf("%w: lokasi %d: precipitation tidak ada atau tidak sejajar dengan waktu", ErrStructure, i)
		}
		if i == 0 {
			times = make([]time.Time, len(ts))
		} else if len(ts) != len(times) {
			return nil, nil, fmt.Errorf("%w: lokasi %d berisi %d langkah, lokasi 0 berisi %d", ErrStructure, i, len(ts), len(times))
		}
		for k, t := range ts {
			if t == nil || *t != math.Trunc(*t) || math.Abs(*t) > 1e11 {
				return nil, nil, fmt.Errorf("%w: lokasi %d: waktu langkah %d tidak valid", ErrStructure, i, k)
			}
			at := time.Unix(int64(*t), 0).UTC()
			if i == 0 {
				times[k] = at
			} else if !at.Equal(times[k]) {
				return nil, nil, fmt.Errorf("%w: lokasi %d: waktu langkah %d berbeda dari lokasi 0", ErrStructure, i, k)
			}
		}
		vs := make([]float64, len(col))
		for k, v := range col {
			vs[k] = math.NaN()
			if v != nil {
				vs[k] = *v
			}
		}
		data[catchment.Key(want)] = vs
	}
	return times, data, nil
}

// basinEvent menghitung hujan rata-rata wilayah satu sub-DAS lalu
// memvalidasinya sebagai deret RainfallSpec.
func (c *RainConnector) basinEvent(b catchment.Basin, times []time.Time, data map[series.LatLon][]float64, fetchedAt time.Time) (ports.Event, error) {
	areal, err := b.Areal(data)
	if err != nil {
		return nil, err
	}
	centroid := b.Centroid()
	s := series.Series{
		Site:   series.Site{ID: b.SiteID(), Name: b.Name, Requested: centroid},
		Cell:   centroid,
		Model:  ModelRain,
		Times:  times,
		Values: [][]*float64{make([]*float64, len(areal))},
	}
	for i, v := range areal {
		if math.IsNaN(v) {
			continue
		}
		// Dibulatkan 0,001 mm: nilai sel bersatuan 0,1 mm, jadi rata-rata
		// berbobot 4 desimal tidak membawa informasi lebih dan isi event
		// tetap stabil terhadap sisa galat floating point.
		x := math.Round(v*1000) / 1000
		s.Values[0][i] = &x
	}
	s = s.Compact()
	if err := s.Validate(series.RainfallSpec, fetchedAt); err != nil {
		return nil, err
	}
	return rawpb.NewCatchmentRainfallEvent(s, len(b.Cells))
}
