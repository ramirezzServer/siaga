// Package riversnap memilih sel model hidrologi (GloFAS, grid 0,05°) untuk
// setiap titik pantau sungai. Koordinat titik di PRD adalah nama lokasi;
// sel GloFAS yang mewakili alur sungai bisa bergeser satu-dua sel. Aturannya:
// di dalam radius pencarian, sel "alur utama" adalah sel yang debitnya paling
// sedikit MainStemFraction dari debit terbesar; dari sel itu dipilih yang
// paling dekat dengan titik perkiraan. Memilih debit terbesar saja akan selalu
// menggeser titik ke hilir (debit naik ke arah hilir). Pilihan yang meragukan
// ditandai untuk diperiksa manual di peta (PRD, bagian Batas kemampuan data).
package riversnap

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

// ErrNoCandidate berarti tidak ada sel berdebit di dalam radius pencarian.
var ErrNoCandidate = errors.New("tidak ada sel berdebit di radius pencarian")

// CellStep adalah resolusi grid GloFAS v4 di Open-Meteo (derajat).
const CellStep = 0.05

// MaxRadius membatasi radius pencarian (PRD: digeser hingga 0,1°; sedikit
// longgar untuk titik yang koordinat awalnya kasar).
const MaxRadius = 0.15

// Station adalah titik pantau dengan koordinat perkiraan dari nama lokasinya.
type Station struct {
	ID, River, Name string
	Approx          series.LatLon
	// Radius pencarian dalam derajat (Chebyshev). 0 berarti titik sudah
	// diverifikasi manual: sel yang dipakai adalah sel di Approx.
	Radius float64
}

// Validate memeriksa radius dan koordinat.
func (s Station) Validate() error {
	switch {
	case !series.InIndonesia(s.Approx):
		return fmt.Errorf("titik %s di luar Indonesia", s.ID)
	case !(s.Radius >= 0 && s.Radius <= MaxRadius):
		return fmt.Errorf("titik %s: radius %v di luar 0..%v", s.ID, s.Radius, MaxRadius)
	}
	return nil
}

// Offsets mengembalikan titik permintaan di sekitar Approx: kisi berjarak
// CellStep sampai Radius ke segala arah (titik sendiri bila Radius 0).
func (s Station) Offsets() []series.LatLon {
	k := int(math.Round(s.Radius / CellStep))
	out := make([]series.LatLon, 0, (2*k+1)*(2*k+1))
	for dy := -k; dy <= k; dy++ {
		for dx := -k; dx <= k; dx++ {
			out = append(out, series.LatLon{
				Lat: round(s.Approx.Lat+float64(dy)*CellStep, 4),
				Lon: round(s.Approx.Lon+float64(dx)*CellStep, 4),
			})
		}
	}
	return out
}

// Candidate adalah satu sel yang dijawab sumber beserta debit rata-ratanya.
type Candidate struct {
	Cell series.LatLon
	// Mean adalah debit rata-rata (m³/s) selama jendela pengambilan.
	Mean float64
}

// Flag menandai pilihan yang perlu diperiksa manual.
type Flag string

// Tanda pemeriksaan.
const (
	// FlagEdge: sel terpilih di tepi radius; debit terbesar mungkin milik sungai lain.
	FlagEdge Flag = "tepi"
	// FlagSmall: debit rata-rata < SmallMean; sel mungkin bukan alur sungai utama.
	FlagSmall Flag = "kecil"
	// FlagDownstream: debit lebih kecil dari titik hulu sebelumnya di sungai yang sama.
	FlagDownstream Flag = "hilir-lebih-kecil"
	// FlagShared: sel yang sama dipakai titik lain.
	FlagShared Flag = "sel-ganda"
	// FlagPinned: titik sudah diverifikasi manual (radius 0), tidak dicari.
	FlagPinned Flag = "manual"
)

// SmallMean adalah debit rata-rata (m³/s) di bawah ini dianggap meragukan.
const SmallMean = 0.5

// MainStemFraction memisahkan alur utama dari anak sungai dan sel darat di
// dalam radius: anak sungai jarang membawa lebih dari sepertiga debit sungai
// utama di pertemuannya.
const MainStemFraction = 0.3

// Choice adalah sel terpilih untuk satu titik.
type Choice struct {
	Station
	Cell       series.LatLon
	Mean       float64
	DistanceKm float64
	Flags      []Flag
}

// Suspect melaporkan apakah pilihan perlu diperiksa manual.
func (c Choice) Suspect() bool {
	return slices.ContainsFunc(c.Flags, func(f Flag) bool { return f != FlagPinned })
}

// Choose memilih sel untuk satu titik dari kandidat yang dijawab sumber:
// sel alur utama terdekat, seri dipecah dengan debit lebih besar lalu
// koordinat, jadi hasilnya deterministik.
func Choose(st Station, cands []Candidate) (Choice, error) {
	if err := st.Validate(); err != nil {
		return Choice{}, err
	}
	var inside []Candidate
	peak := 0.0
	for _, c := range cands {
		if chebyshev(c.Cell, st.Approx) > st.Radius+CellStep/2+1e-9 || math.IsNaN(c.Mean) || c.Mean < 0 {
			continue
		}
		inside = append(inside, c)
		peak = math.Max(peak, c.Mean)
	}
	var best *Candidate
	var bestDist float64
	for i := range inside {
		c := &inside[i]
		if c.Mean < MainStemFraction*peak {
			continue
		}
		d := chebyshev(c.Cell, st.Approx)
		if best == nil || better(c, d, best, bestDist) {
			best, bestDist = c, d
		}
	}
	if best == nil {
		return Choice{}, fmt.Errorf("%w: %s", ErrNoCandidate, st.ID)
	}
	ch := Choice{Station: st, Cell: best.Cell, Mean: best.Mean, DistanceKm: km(st.Approx, best.Cell)}
	switch {
	case st.Radius == 0:
		ch.Flags = append(ch.Flags, FlagPinned)
	case bestDist >= st.Radius-CellStep/2-1e-9:
		ch.Flags = append(ch.Flags, FlagEdge)
	}
	if best.Mean < SmallMean {
		ch.Flags = append(ch.Flags, FlagSmall)
	}
	return ch, nil
}

func better(c *Candidate, d float64, best *Candidate, bestDist float64) bool {
	if d != bestDist {
		return d < bestDist
	}
	if c.Mean != best.Mean {
		return c.Mean > best.Mean
	}
	return cmp.Or(cmp.Compare(c.Cell.Lat, best.Cell.Lat), cmp.Compare(c.Cell.Lon, best.Cell.Lon)) < 0
}

// Review menambahkan tanda lintas titik: sel yang dipakai lebih dari satu
// titik, dan debit yang turun di hilir. choices harus urut hulu ke hilir per
// sungai (urutan file sumber). Bendungan dan pengambilan air memang bisa
// menurunkan debit, jadi tanda ini ajakan memeriksa, bukan kesalahan.
func Review(choices []Choice) []Choice {
	out := slices.Clone(choices)
	for i := range out {
		out[i].Flags = slices.Clone(out[i].Flags)
	}
	byCell := map[series.LatLon][]int{}
	for i, c := range out {
		byCell[c.Cell] = append(byCell[c.Cell], i)
	}
	for _, idx := range byCell {
		if len(idx) > 1 {
			for _, i := range idx {
				out[i].Flags = append(out[i].Flags, FlagShared)
			}
		}
	}
	prev := map[string]float64{}
	for i, c := range out {
		if p, ok := prev[c.River]; ok && c.Mean < p {
			out[i].Flags = append(out[i].Flags, FlagDownstream)
		}
		prev[c.River] = c.Mean
	}
	return out
}

func chebyshev(a, b series.LatLon) float64 {
	return math.Max(math.Abs(a.Lat-b.Lat), math.Abs(a.Lon-b.Lon))
}

// km adalah jarak lingkaran besar (haversine) dalam kilometer.
func km(a, b series.LatLon) float64 {
	const r = 6371.0
	la1, la2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dla, dlo := la2-la1, (b.Lon-a.Lon)*math.Pi/180
	h := math.Sin(dla/2)*math.Sin(dla/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dlo/2)*math.Sin(dlo/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}

func round(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}
