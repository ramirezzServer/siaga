// Package quakelabel menyusun set berlabel deduplikasi gempa BMKG–USGS (PRD
// T4, ADR 0024) dari ISC Bulletin. ISC mengelompokkan origin dari banyak
// agensi ke satu kejadian; origin agensi DJA (BMKG) dan NEIC (USGS) di
// kejadian ISC yang sama adalah pasangan positif, sedangkan origin DJA di
// kejadian ISC lain yang berdekatan adalah negatif. Alat kalibrasi
// geo-processor (calibrate-dedup -labels) mengukur presisi dan recall
// aturan deduplikasi terhadap set ini.
//
// Paket ini murni: tanpa I/O, jam, atau jaringan.
package quakelabel

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Agensi ISC yang dipakai.
const (
	AgencyBMKG = "DJA"
	AgencyUSGS = "NEIC"
)

// Magnitude adalah satu magnitudo yang menempel di origin.
type Magnitude struct {
	Type   string
	Value  float64
	Author string
}

// Origin adalah satu solusi pusat gempa dari satu agensi.
type Origin struct {
	ID         string
	Agency     string
	Time       time.Time
	Latitude   float64
	Longitude  float64
	DepthKm    float64
	HasDepth   bool
	Magnitudes []Magnitude
}

// Event adalah satu kejadian ISC beserta semua origin agensi.
type Event struct {
	ID      string
	Type    string
	Origins []Origin
}

// Prioritas tipe magnitudo untuk memilih origin wakil dan magnitudonya.
// Tipe dibandingkan peka huruf besar-kecil (mb dan mB berbeda). Urutan
// dipilih dari sebaran selisih DJA–NEIC Q1 2023 (catatan 1e-3c-2): mB
// paling meleset, jadi paling akhir.
var (
	BMKGPriority = []string{"Mw", "Mwp", "MLv", "Mjma", "mb", "mB"}
	USGSPriority = []string{"Mww", "Mwc", "Mwb", "Mwr", "mb", "Ms_20"}
)

// rank mengembalikan urutan tipe di daftar prioritas (len bila tidak ada).
func rank(t string, prio []string) int {
	if i := slices.Index(prio, t); i >= 0 {
		return i
	}
	return len(prio)
}

// own mengembalikan magnitudo origin yang dibuat agensi origin itu sendiri
// (author kosong dianggap agensi origin).
func (o Origin) own() []Magnitude {
	var out []Magnitude
	for _, m := range o.Magnitudes {
		if m.Author == "" || m.Author == o.Agency {
			out = append(out, m)
		}
	}
	return out
}

// best mengembalikan magnitudo berprioritas tertinggi milik origin.
func (o Origin) best(prio []string) (Magnitude, bool) {
	var out Magnitude
	r := len(prio)
	for _, m := range o.own() {
		if k := rank(m.Type, prio); k < r {
			out, r = m, k
		}
	}
	return out, r < len(prio)
}

// Pick adalah origin wakil satu agensi di satu kejadian.
type Pick struct {
	Origin    Origin
	Magnitude Magnitude
}

// Representative memilih origin wakil agensi di kejadian: origin yang
// membawa tipe magnitudo berprioritas tertinggi (NEIC kadang punya beberapa
// origin per kejadian, misal Mww dan mb di origin berbeda). Bila seri, yang
// pertama di urutan bulletin. Origin tanpa magnitudo berprioritas dilewati.
func Representative(e Event, agency string, prio []string) (Pick, bool) {
	var out Pick
	r := len(prio)
	for _, o := range e.Origins {
		if o.Agency != agency {
			continue
		}
		if m, ok := o.best(prio); ok {
			if k := rank(m.Type, prio); k < r {
				out, r = Pick{Origin: o, Magnitude: m}, k
			}
		}
	}
	return out, r < len(prio)
}

// Source adalah sumber SIAGA yang diwakili agensi.
type Source string

// Sumber di kolom CSV, sama dengan nama sumber geo-processor.
const (
	SourceBMKG Source = "bmkg"
	SourceUSGS Source = "usgs"
)

// Basis menyatakan dasar label.
type Basis string

// Dasar label.
const (
	// BasisISC: label dari pengelompokan kejadian ISC Bulletin.
	BasisISC Basis = "isc"
	// BasisArchive: label manual dari arsip payload real-time ingest.
	BasisArchive Basis = "arsip"
)

// Row adalah satu baris set berlabel: satu origin dan kejadian labelnya.
type Row struct {
	Basis     Basis
	Label     string
	Source    Source
	ID        string
	Time      time.Time
	Latitude  float64
	Longitude float64
	// DepthKm NaN bila tidak dilaporkan.
	DepthKm       float64
	Magnitude     float64
	MagnitudeType string
	// Others adalah magnitudo lain origin itu, "tipe nilai" dipisah "; ".
	Others string
	Note   string
}

// Window adalah jendela kandidat origin BMKG di sekitar origin USGS.
type Window struct {
	Seconds float64
	Km      float64
}

// Options mengatur Build.
type Options struct {
	// MinUSGSMagnitude: origin NEIC wakil di bawah ini dilewati.
	MinUSGSMagnitude float64
	// Window kandidat origin DJA di sekitar setiap origin NEIC terpilih.
	Window Window
}

// DefaultOptions sesuai catatan 1e-3c-2: NEIC M ≥ 4,0, kandidat ≤ 300 dtk
// dan ≤ 300 km (sama dengan calibrate.Candidate di geo-processor).
var DefaultOptions = Options{MinUSGSMagnitude: 4.0, Window: Window{Seconds: 300, Km: 300}}

// ErrInvalid menandai data yang tidak bisa dipakai.
var ErrInvalid = errors.New("data label tidak valid")

// Validate memeriksa opsi.
func (o Options) Validate() error {
	if !(o.Window.Seconds > 0 && o.Window.Seconds <= 3600) || !(o.Window.Km > 0 && o.Window.Km <= 1000) {
		return fmt.Errorf("%w: jendela %v dtk / %v km", ErrInvalid, o.Window.Seconds, o.Window.Km)
	}
	if math.IsNaN(o.MinUSGSMagnitude) || o.MinUSGSMagnitude < 0 || o.MinUSGSMagnitude > 10 {
		return fmt.Errorf("%w: magnitudo minimum %v", ErrInvalid, o.MinUSGSMagnitude)
	}
	return nil
}

// EarthRadiusKm adalah jari-jari bumi rata-rata (IUGG), sama dengan
// geo-processor.
const EarthRadiusKm = 6371.0088

// DistanceKm adalah jarak lingkaran besar (haversine).
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	p1, p2 := lat1*rad, lat2*rad
	dp, dl := p2-p1, (lon2-lon1)*rad
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * EarthRadiusKm * math.Asin(math.Sqrt(math.Min(1, h)))
}

// Stats merangkum hasil Build.
type Stats struct {
	Events int
	// USGS adalah origin NEIC wakil yang lolos magnitudo minimum.
	USGS int
	// BMKG adalah origin DJA wakil di dalam jendela salah satu origin USGS.
	BMKG int
	// Positive adalah origin USGS yang punya DJA di kejadian ISC yang sama
	// di dalam jendela.
	Positive int
	// Negative adalah pasangan USGS–BMKG di dalam jendela dari kejadian ISC
	// berbeda.
	Negative int
	// NoMagnitude adalah origin DJA/NEIC yang dilewati karena tidak punya
	// magnitudo berprioritas.
	NoMagnitude int
}

type pick struct {
	event string
	Pick
}

// Build menyusun baris berlabel dari kejadian ISC (urutan bebas; kejadian
// ber-ID sama digabung tidak diizinkan). Hasil terurut menurut waktu, lalu
// sumber, lalu ID, jadi bisa dibandingkan baris per baris antar-jalan.
func Build(events []Event, opt Options) ([]Row, Stats, error) {
	if err := opt.Validate(); err != nil {
		return nil, Stats{}, err
	}
	seen := map[string]bool{}
	var usgs, bmkg []pick
	st := Stats{Events: len(events)}
	for _, e := range events {
		if e.ID == "" || seen[e.ID] {
			return nil, Stats{}, fmt.Errorf("%w: ID kejadian ISC kosong atau ganda %q", ErrInvalid, e.ID)
		}
		seen[e.ID] = true
		for _, a := range []struct {
			agency string
			prio   []string
			out    *[]pick
		}{{AgencyUSGS, USGSPriority, &usgs}, {AgencyBMKG, BMKGPriority, &bmkg}} {
			p, ok := Representative(e, a.agency, a.prio)
			if !ok {
				if slices.ContainsFunc(e.Origins, func(o Origin) bool { return o.Agency == a.agency }) {
					st.NoMagnitude++
				}
				continue
			}
			if err := checkOrigin(p.Origin); err != nil {
				return nil, Stats{}, fmt.Errorf("kejadian %s: %w", e.ID, err)
			}
			*a.out = append(*a.out, pick{event: e.ID, Pick: p})
		}
	}
	usgs = slices.DeleteFunc(usgs, func(p pick) bool { return p.Magnitude.Value < opt.MinUSGSMagnitude })
	byTime := func(a, b pick) int {
		return cmp.Or(a.Origin.Time.Compare(b.Origin.Time), cmp.Compare(a.Origin.ID, b.Origin.ID))
	}
	slices.SortFunc(usgs, byTime)
	slices.SortFunc(bmkg, byTime)

	keep := map[int]bool{}
	w := time.Duration(opt.Window.Seconds * float64(time.Second))
	for _, u := range usgs {
		lo, _ := slices.BinarySearchFunc(bmkg, u.Origin.Time.Add(-w), func(p pick, t time.Time) int { return p.Origin.Time.Compare(t) })
		positive := false
		for i := lo; i < len(bmkg) && !bmkg[i].Origin.Time.After(u.Origin.Time.Add(w)); i++ {
			b := bmkg[i]
			if DistanceKm(u.Origin.Latitude, u.Origin.Longitude, b.Origin.Latitude, b.Origin.Longitude) > opt.Window.Km {
				continue
			}
			keep[i] = true
			if b.event == u.event {
				positive = true
			} else {
				st.Negative++
			}
		}
		if positive {
			st.Positive++
		}
	}
	st.USGS = len(usgs)
	rows := make([]Row, 0, len(usgs)+len(keep))
	for _, u := range usgs {
		rows = append(rows, row(SourceUSGS, u))
	}
	for i, b := range bmkg {
		if keep[i] {
			rows = append(rows, row(SourceBMKG, b))
			st.BMKG++
		}
	}
	SortRows(rows)
	return rows, st, nil
}

func checkOrigin(o Origin) error {
	switch {
	case o.ID == "":
		return fmt.Errorf("%w: origin tanpa ID", ErrInvalid)
	case o.Time.IsZero():
		return fmt.Errorf("%w: origin %s tanpa waktu", ErrInvalid, o.ID)
	case !(o.Latitude >= -90 && o.Latitude <= 90 && o.Longitude >= -180 && o.Longitude <= 180):
		return fmt.Errorf("%w: origin %s di luar koordinat bumi", ErrInvalid, o.ID)
	}
	return nil
}

func row(src Source, p pick) Row {
	r := Row{
		Basis: BasisISC, Label: p.event, Source: src, ID: p.Origin.ID, Time: p.Origin.Time.UTC(),
		Latitude: p.Origin.Latitude, Longitude: p.Origin.Longitude, DepthKm: math.NaN(),
		Magnitude: p.Magnitude.Value, MagnitudeType: p.Magnitude.Type,
	}
	if p.Origin.HasDepth {
		r.DepthKm = p.Origin.DepthKm
	}
	var others []string
	for _, m := range p.Origin.own() {
		if m != p.Magnitude {
			others = append(others, m.Type+" "+strconv.FormatFloat(m.Value, 'f', -1, 64))
		}
	}
	slices.Sort(others)
	r.Others = strings.Join(slices.Compact(others), "; ")
	return r
}

// SortRows mengurutkan baris menurut waktu, sumber, lalu ID.
func SortRows(rows []Row) {
	slices.SortFunc(rows, func(a, b Row) int {
		return cmp.Or(a.Time.Compare(b.Time), cmp.Compare(a.Source, b.Source), cmp.Compare(a.ID, b.ID))
	})
}
