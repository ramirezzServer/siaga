package firecluster

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/fire"
)

var t0 = time.Date(2019, 8, 7, 6, 0, 0, 0, time.UTC)

func pt(id string, lat, lon float64, at time.Time, c fire.Confidence) Point {
	return Point{ID: id, Satellite: "S-NPP", Instrument: fire.VIIRS, Lat: lat, Lon: lon, At: at, Confidence: c, Type: fire.UnknownType, Daytime: true}
}

func TestDistanceKm(t *testing.T) {
	if d := DistanceKm(-6, 107, -7, 107); math.Abs(d-111.195) > 0.01 {
		t.Fatalf("1 derajat lintang = %v km", d)
	}
	// Bandung (alun-alun) ke Jakarta (Monas) ±120 km garis lurus.
	if d := DistanceKm(-6.9218, 107.6070, -6.1754, 106.8272); d < 119 || d > 120.5 {
		t.Fatalf("Bandung–Jakarta %v km", d)
	}
	if DistanceKm(-6.9, 107.6, -6.9, 107.6) != 0 {
		t.Fatal("jarak titik sama bukan nol")
	}
}

func TestFromArchived(t *testing.T) {
	frp := 4.5
	a := fire.Archived{Detection: fire.Detection{
		Product: fire.VIIRSNOAA20SP, Instrument: fire.VIIRS, Lat: -6.9, Lon: 107.6, DetectedAt: t0,
		Confidence: fire.Nominal, FRPMW: &frp,
	}, Type: fire.StaticSource}
	p := FromArchived(a)
	if p.Satellite != "NOAA-20" || p.FRPMW != 4.5 || p.Type != fire.StaticSource || p.ID != a.ID() || p.Daytime {
		t.Fatalf("%+v", p)
	}
	a.FRPMW = nil
	if FromArchived(a).FRPMW != 0 {
		t.Fatal("FRP kosong bukan nol")
	}
}

func TestCluster(t *testing.T) {
	ps := []Point{
		pt("a", -6.9, 107.6, t0, fire.Nominal),
		// 1,5 km dari a, 3 jam kemudian: satu kejadian.
		pt("b", -6.9135, 107.6, t0.Add(3*time.Hour), fire.Nominal),
		// 1,5 km dari b (3 km dari a), berantai lewat b.
		pt("c", -6.927, 107.6, t0.Add(20*time.Hour), fire.Low),
		// 10 km jauhnya: kejadian lain.
		pt("d", -6.9, 107.69, t0.Add(21*time.Hour), fire.Nominal),
		// Di tempat c tetapi 25 jam setelah c: jeda > 24 jam, kejadian baru.
		pt("e", -6.927, 107.6, t0.Add(45*time.Hour+time.Minute), fire.Nominal),
		// Tepat 24 jam setelah e: masih tersambung.
		pt("f", -6.927, 107.6, t0.Add(69*time.Hour+time.Minute), fire.Nominal),
	}
	got, err := Cluster(ps, DefaultClusterRule)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{0, 1, 2}, {3}, {4, 5}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("%v, ingin %v", got, want)
	}
	if _, err := Cluster([]Point{ps[1], ps[0]}, DefaultClusterRule); !errors.Is(err, ErrRule) {
		t.Fatal("urutan salah diterima")
	}
	for _, r := range []ClusterRule{{LinkKm: 0, Gap: time.Hour}, {LinkKm: 2, Gap: 0}, {LinkKm: 25, Gap: time.Hour}, {LinkKm: 2, Gap: 8 * 24 * time.Hour}} {
		if _, err := Cluster(ps, r); !errors.Is(err, ErrRule) {
			t.Fatalf("aturan %+v diterima", r)
		}
	}
	if got, err := Cluster(nil, DefaultClusterRule); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestSortPoints(t *testing.T) {
	ps := []Point{pt("b", 0, 0, t0, fire.Low), pt("a", 0, 0, t0, fire.Low), pt("c", 0, 0, t0.Add(-time.Minute), fire.Low)}
	SortPoints(ps)
	if ps[0].ID != "c" || ps[1].ID != "a" || ps[2].ID != "b" {
		t.Fatal(ps)
	}
}

func TestEvaluate(t *testing.T) {
	low := func(i int, h time.Duration) Point { return pt(fmt.Sprint(i), -6.9, 107.6, t0.Add(h), fire.Low) }
	o, err := Evaluate([]Point{low(1, 0)}, PRDRule)
	if err != nil || o.Level != Info || o.Peak != 1 || o.Detections != 1 {
		t.Fatalf("%+v %v", o, err)
	}
	high := pt("h", -6.9, 107.6, t0.Add(2*time.Hour), fire.High)
	o, _ = Evaluate([]Point{low(1, 0), high}, PRDRule)
	if o.Level != Waspada || !o.ReachedAt.Equal(high.At) {
		t.Fatalf("%+v", o)
	}
	// Nominal tidak cukup untuk Waspada menurut PRD, cukup menurut varian.
	nominal := []Point{low(1, 0), pt("n", -6.9, 107.6, t0.Add(time.Hour), fire.Nominal)}
	if o, _ := Evaluate(nominal, PRDRule); o.Level != Info {
		t.Fatalf("PRD %+v", o)
	}
	variant := PRDRule
	variant.WaspadaMin = fire.Nominal
	if o, _ := Evaluate(nominal, variant); o.Level != Waspada {
		t.Fatalf("varian %+v", o)
	}
	// Lima deteksi dalam 24 jam: Siaga. Deteksi kelima pada 24 jam tepat
	// setelah yang pertama tidak lagi satu jendela dengan yang pertama.
	five := []Point{low(1, 0), low(2, 6*time.Hour), low(3, 12*time.Hour), low(4, 18*time.Hour), low(5, 24*time.Hour)}
	if o, _ := Evaluate(five, PRDRule); o.Level != Info || o.Peak != 4 {
		t.Fatalf("jendela %+v", o)
	}
	five[4] = low(5, 23*time.Hour)
	o, _ = Evaluate(five, PRDRule)
	if o.Level != Siaga || o.Peak != 5 || !o.ReachedAt.Equal(five[4].At) || !o.Start.Equal(t0) || !o.End.Equal(five[4].At) {
		t.Fatalf("siaga %+v", o)
	}
	// Satuan lintasan: lima piksel dari satu lintasan dihitung sekali.
	pass := []Point{low(1, 0), low(2, 0), low(3, 0), low(4, 0), low(5, 0)}
	r := PRDRule
	r.Unit = Overpasses
	if o, _ := Evaluate(pass, r); o.Level != Info || o.Peak != 1 {
		t.Fatalf("lintasan %+v", o)
	}
	other := low(6, 0)
	other.Satellite = "NOAA-20"
	r.SiagaCount = 2
	if o, _ := Evaluate(append(pass, other), r); o.Level != Siaga || o.Peak != 2 {
		t.Fatalf("dua lintasan %+v", o)
	}
	if o, err := Evaluate(nil, PRDRule); err != nil || o.Level != None {
		t.Fatal(o, err)
	}
	if _, err := Evaluate([]Point{low(2, time.Hour), low(1, 0)}, PRDRule); !errors.Is(err, ErrRule) {
		t.Fatal("urutan salah diterima")
	}
	for _, bad := range []LevelRule{
		{SiagaCount: 0, Unit: Detections, WaspadaMin: fire.High, Window: time.Hour},
		{SiagaCount: 5, Unit: 9, WaspadaMin: fire.High, Window: time.Hour},
		{SiagaCount: 5, Unit: Detections, WaspadaMin: 0, Window: time.Hour},
		{SiagaCount: 5, Unit: Detections, WaspadaMin: fire.High, Window: 0},
	} {
		if _, err := Evaluate(five, bad); !errors.Is(err, ErrRule) {
			t.Fatalf("aturan %+v diterima", bad)
		}
	}
}

func TestLevelAndUnitNames(t *testing.T) {
	if None.String() != "-" || Info.String() != "Info" || Waspada.String() != "Waspada" || Siaga.String() != "Siaga" {
		t.Fatal("nama tingkat")
	}
	if Detections.String() != "deteksi" || Overpasses.String() != "lintasan" {
		t.Fatal("nama satuan")
	}
}

// flare membuat sumber tetap: satu deteksi malam setiap ±45 hari selama 4
// tahun, tersebar ±300 m di sekitar (lat, lon).
func flare(lat, lon float64, typ fire.SourceType) []Point {
	var out []Point
	for i := range 32 {
		at := time.Date(2016, 1, 3, 18, 30, 0, 0, time.UTC).AddDate(0, 0, 45*i)
		dLat := 0.0027 * math.Sin(float64(i))
		dLon := 0.0027 * math.Cos(float64(i)*1.7)
		p := pt(fmt.Sprintf("flare-%v-%d", lon, i), lat+dLat, lon+dLon, at, fire.Nominal)
		p.Daytime, p.Type, p.FRPMW = false, typ, 12
		out = append(out, p)
	}
	return out
}

// seasonal membuat kebakaran musiman: 6 hari setiap Agustus–September
// selama 5 tahun di tempat yang sama (bulan kalender hanya 2).
func seasonal(lat, lon float64) []Point {
	var out []Point
	for y := range 5 {
		for d := range 6 {
			at := time.Date(2015+y, 8, 5+10*d, 6, 0, 0, 0, time.UTC)
			p := pt(fmt.Sprintf("musim-%d-%d", y, d), lat+0.001*float64(d%2), lon, at, fire.Nominal)
			p.Type = fire.VegetationFire
			out = append(out, p)
		}
	}
	return out
}

func TestFindStatic(t *testing.T) {
	var ps []Point
	ps = append(ps, flare(-6.3851, 108.3702, fire.UnknownType)...) // kilang (NRT, tanpa type)
	ps = append(ps, seasonal(-6.75, 108.45)...)                    // tebu musiman
	// Kawah yang ditandai FIRMS walau hanya 4 hari.
	for i := range 4 {
		p := pt(fmt.Sprintf("kawah-%d", i), -7.32+0.0005*float64(i), 107.73, t0.AddDate(0, 0, i), fire.Low)
		p.Type = fire.ActiveVolcano
		ps = append(ps, p)
	}
	// Satu deteksi lepas di sebelah kilang ikut sel tetangga sumbernya.
	ps = append(ps, pt("lepas", -6.3851+0.004, 108.3702, t0, fire.Low))
	SortPoints(ps)
	got, err := FindStatic(ps, DefaultStaticRule)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d sumber: %+v", len(got), got)
	}
	k := got[0]
	if k.Reason != ReasonPersistent || k.Detections != 33 || k.Days != 33 || k.Years != 4 || k.Months != 12 || k.Night != 32 ||
		DistanceKm(k.Lat, k.Lon, -6.3851, 108.3702) > 0.3 || k.RadiusKm < 0.75 || k.RadiusKm > 1.5 || k.MeanFRPMW == 0 {
		t.Fatalf("kilang %+v", k)
	}
	v := got[1]
	if v.Reason != ReasonFIRMS || v.Typed[fire.ActiveVolcano] != 4 || v.RadiusKm != DefaultStaticRule.MinRadiusKm || v.ID() != "-7.31925,107.73000" {
		t.Fatalf("kawah %+v", v)
	}
	m := Mask(got)
	if s, ok := m.Match(-6.3851, 108.3712); !ok || s.ID() != k.ID() {
		t.Fatal("deteksi di kilang tidak tersaring")
	}
	if _, ok := m.Match(-6.75, 108.45); ok {
		t.Fatal("kebakaran musiman tersaring")
	}
	// Kilang yang juga ditandai FIRMS: keduanya.
	both, _ := FindStatic(flare(-6.5, 107.0, fire.StaticSource), DefaultStaticRule)
	if len(both) != 1 || both[0].Reason != ReasonBoth || both[0].Typed[fire.StaticSource] != 32 {
		t.Fatalf("%+v", both)
	}
	if _, err := FindStatic(ps, StaticRule{}); !errors.Is(err, ErrRule) {
		t.Fatal("aturan kosong diterima")
	}
}

func FuzzClusterLinked(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	f.Add([]byte{0, 0, 0, 255, 255, 255, 0, 0, 0, 128, 128, 128, 7, 7, 7})
	f.Fuzz(func(t *testing.T, data []byte) {
		var ps []Point
		for i := 0; i+2 < len(data) && len(ps) < 40; i += 3 {
			// Titik di kotak ±10 km, waktu dalam 4 hari.
			ps = append(ps, pt(fmt.Sprint(i), -6.9+float64(data[i])/2800, 107.6+float64(data[i+1])/2800,
				t0.Add(time.Duration(data[i+2])*23*time.Minute), fire.Nominal))
		}
		SortPoints(ps)
		groups, err := Cluster(ps, DefaultClusterRule)
		if err != nil {
			t.Fatal(err)
		}
		of := make([]int, len(ps))
		n := 0
		for g, idx := range groups {
			n += len(idx)
			for _, i := range idx {
				of[i] = g
			}
		}
		if n != len(ps) {
			t.Fatalf("%d anggota dari %d titik", n, len(ps))
		}
		linked := func(a, b Point) bool {
			dt := a.At.Sub(b.At)
			return dt <= DefaultClusterRule.Gap && -dt <= DefaultClusterRule.Gap &&
				DistanceKm(a.Lat, a.Lon, b.Lat, b.Lon) <= DefaultClusterRule.LinkKm
		}
		for i := range ps {
			for j := range i {
				if linked(ps[i], ps[j]) && of[i] != of[j] {
					t.Fatalf("titik %d dan %d terhubung tetapi beda kejadian", i, j)
				}
			}
		}
		// Setiap kejadian terhubung: dari anggota pertama semua anggota
		// tercapai lewat pasangan yang terhubung.
		for _, idx := range groups {
			reach := map[int]bool{idx[0]: true}
			for changed := true; changed; {
				changed = false
				for _, i := range idx {
					if reach[i] {
						continue
					}
					for j := range reach {
						if linked(ps[i], ps[j]) {
							reach[i], changed = true, true
							break
						}
					}
				}
			}
			if len(reach) != len(idx) {
				t.Fatalf("kejadian %v tidak terhubung", idx)
			}
		}
	})
}

func FuzzEvaluateMonotone(f *testing.F) {
	f.Add([]byte{1, 1, 2, 2, 3, 3}, byte(2))
	f.Add([]byte{0, 3, 0, 3, 0, 3, 0, 3, 0, 3}, byte(1))
	f.Fuzz(func(t *testing.T, data []byte, extra byte) {
		var ps []Point
		at := t0
		for i := 0; i+1 < len(data) && len(ps) < 40; i += 2 {
			at = at.Add(time.Duration(data[i]) * 7 * time.Minute)
			p := pt(fmt.Sprint(i), -6.9, 107.6, at, fire.Confidence(data[i+1]%3+1))
			p.Satellite = []string{"S-NPP", "NOAA-20", "MODIS"}[data[i+1]%3]
			ps = append(ps, p)
		}
		for _, r := range []LevelRule{PRDRule, {Name: "lintasan", SiagaCount: 3, Unit: Overpasses, WaspadaMin: fire.Nominal, Window: 24 * time.Hour}} {
			before, err := Evaluate(ps, r)
			if err != nil {
				t.Fatal(err)
			}
			// Menambah deteksi di akhir tidak pernah menurunkan tingkat.
			add := pt("tambah", -6.9, 107.6, at.Add(time.Duration(extra)*time.Minute), fire.Confidence(extra%3+1))
			after, err := Evaluate(append(slices.Clone(ps), add), r)
			if err != nil {
				t.Fatal(err)
			}
			if after.Level < before.Level || after.Peak < before.Peak {
				t.Fatalf("%s: %+v → %+v", r.Name, before, after)
			}
			if len(ps) > 0 && before.Level == None {
				t.Fatal("kejadian berisi deteksi tanpa tingkat")
			}
		}
	})
}

func TestFindStaticNightShare(t *testing.T) {
	// Pembakaran jerami: siang, berulang 4 tahun di 8 bulan berbeda.
	var ps []Point
	for i := range 32 {
		at := time.Date(2016, 1, 5, 6, 0, 0, 0, time.UTC).AddDate(0, 0, 45*i)
		ps = append(ps, pt(fmt.Sprint(i), -6.4, 108.2, at, fire.Nominal))
	}
	if got, _ := FindStatic(ps, DefaultStaticRule); len(got) != 0 {
		t.Fatalf("jerami siang tersaring: %+v", got)
	}
	r := DefaultStaticRule
	r.MinNightShare = 0
	if got, _ := FindStatic(ps, r); len(got) != 1 {
		t.Fatal("tanpa syarat malam tidak tersaring")
	}
	r.MinNightShare = 1.5
	if _, err := FindStatic(ps, r); !errors.Is(err, ErrRule) {
		t.Fatal("bagian malam > 1 diterima")
	}
}
