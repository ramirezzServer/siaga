// Package importregions adalah use case: membaca batas wilayah dari sumber,
// memvalidasinya dengan aturan domain, lalu menyimpannya secara atomik.
package importregions

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

// Alasan baris dilewati. Nilainya stabil karena muncul di log dan laporan.
const (
	SkipInvalidCode     = "kode_tidak_valid"
	SkipOtherProvince   = "provinsi_lain"
	SkipDuplicate       = "kode_duplikat"
	SkipInvalidGeometry = "geometri_rusak"
	SkipInvalidName     = "nama_tidak_valid"
	SkipMissingParent   = "induk_tidak_ada"
)

// maxExamples membatasi contoh baris bermasalah yang disimpan di laporan.
const maxExamples = 10

// ErrTooManySkipped dikembalikan bila baris yang dilewati melebihi batas yang diizinkan.
// Tidak ada yang ditulis ke penyimpanan dalam kasus ini.
var ErrTooManySkipped = errors.New("terlalu banyak baris dilewati")

// ErrNoRoot dikembalikan bila baris provinsi itu sendiri tidak ada atau tidak valid.
var ErrNoRoot = errors.New("wilayah provinsi tidak ditemukan di sumber")

// Options mengatur perilaku import.
type Options struct {
	Province string // dua digit, misal "32"
	// MaxSkipped adalah jumlah baris bermasalah yang masih ditoleransi. Default 0:
	// satu baris bermasalah pun membatalkan import, supaya data tidak hilang diam-diam.
	MaxSkipped int
	DryRun     bool // validasi saja, tanpa menulis
	// GridStep > 0 mengisi Report.Grid dengan simpul grid berjarak GridStep
	// derajat yang menyentuh kelurahan/desa provinsi (make grid-list).
	GridStep float64
}

// Report merangkum satu kali import.
type Report struct {
	Read         int
	Accepted     int
	PerKind      map[region.Kind]int
	Skipped      map[string]int
	SkipExamples []string
	Repairs      region.GeometryRepair
	Result       ports.UpsertResult
	// Stale berisi kode yang ada di database tapi tidak ada di sumber (misal akibat
	// pemekaran wilayah). Tidak dihapus otomatis; perlu keputusan manusia.
	Stale []string
	// Villages adalah kode kelurahan/desa (adm4) yang diterima, urut kode.
	// Dipakai untuk daftar sapuan prakiraan ingest (make adm4-list).
	Villages []string
	// Grid adalah simpul grid untuk cuaca dan kualitas udara Open-Meteo
	// (make grid-list), bila Options.GridStep diisi.
	Grid []region.GridNode
}

// SkippedTotal menjumlahkan semua baris yang dilewati.
func (r Report) SkippedTotal() int {
	n := 0
	for _, v := range r.Skipped {
		n += v
	}
	return n
}

// Run menjalankan import.
func Run(ctx context.Context, src ports.RegionSource, store ports.RegionStore, opt Options) (Report, error) {
	rep := Report{PerKind: map[region.Kind]int{}, Skipped: map[string]int{}}
	skip := func(reason, detail string) {
		rep.Skipped[reason]++
		if len(rep.SkipExamples) < maxExamples {
			rep.SkipExamples = append(rep.SkipExamples, reason+": "+detail)
		}
	}

	root, err := region.ParseCode(opt.Province)
	if err != nil || root.Level() != region.LevelProvinsi {
		return rep, fmt.Errorf("opsi provinsi %q tidak valid", opt.Province)
	}

	byCode := map[region.Code]region.Region{}
	err = src.Each(ctx, func(raw ports.RawRegion) error {
		rep.Read++
		code, err := region.ParseCode(raw.Code)
		if err != nil {
			skip(SkipInvalidCode, fmt.Sprintf("%s (%s): %v", raw.Code, raw.Origin, err))
			return nil
		}
		if code.Province() != root.Province() {
			skip(SkipOtherProvince, raw.Code)
			return nil
		}
		if _, dup := byCode[code]; dup {
			skip(SkipDuplicate, fmt.Sprintf("%s (%s)", raw.Code, raw.Origin))
			return nil
		}
		boundary, repair, err := region.ParseLatLngPath(raw.Path)
		if err != nil {
			skip(SkipInvalidGeometry, fmt.Sprintf("%s (%s): %v", raw.Code, raw.Origin, err))
			return nil
		}
		r, err := region.New(code, raw.Name, boundary)
		if err != nil {
			skip(SkipInvalidName, fmt.Sprintf("%s (%s): %v", raw.Code, raw.Origin, err))
			return nil
		}
		rep.Repairs.ClosedRings += repair.ClosedRings
		rep.Repairs.DroppedRings += repair.DroppedRings
		rep.Repairs.DroppedPolygon += repair.DroppedPolygon
		byCode[code] = r
		return nil
	})
	if err != nil {
		return rep, fmt.Errorf("membaca sumber %s: %w", src.Name(), err)
	}

	if _, ok := byCode[root]; !ok {
		return rep, fmt.Errorf("%w: %s", ErrNoRoot, root)
	}

	accepted := dropOrphans(byCode, skip)
	rep.Accepted = len(accepted)
	var villages []region.MultiPolygon
	for _, r := range accepted {
		rep.PerKind[r.Kind()]++
		if r.Code.Level() == region.LevelDesaKelurahan {
			rep.Villages = append(rep.Villages, r.Code.String())
			villages = append(villages, r.Boundary)
		}
	}
	if opt.GridStep > 0 {
		if rep.Grid, err = region.GridNodes(villages, opt.GridStep); err != nil {
			return rep, err
		}
	}

	if n := rep.SkippedTotal(); n > opt.MaxSkipped {
		return rep, fmt.Errorf("%w: %d baris (batas %d)", ErrTooManySkipped, n, opt.MaxSkipped)
	}
	if opt.DryRun {
		return rep, nil
	}

	rep.Result, err = store.UpsertAll(ctx, accepted, src.Name(), src.Version())
	if err != nil {
		return rep, fmt.Errorf("menyimpan wilayah: %w", err)
	}

	existing, err := store.CodesUnder(ctx, root.String())
	if err != nil {
		return rep, fmt.Errorf("membaca kode tersimpan: %w", err)
	}
	for _, c := range existing {
		code, err := region.ParseCode(c)
		if err != nil {
			rep.Stale = append(rep.Stale, c)
			continue
		}
		if _, ok := byCode[code]; !ok {
			rep.Stale = append(rep.Stale, c)
		}
	}
	slices.Sort(rep.Stale)
	return rep, nil
}

// dropOrphans membuang wilayah yang induknya tidak ada, dari level teratas ke bawah,
// sehingga cucu dari induk yang hilang ikut terbuang. Hasil diurutkan menurut kode
// (induk selalu sebelum anak).
func dropOrphans(byCode map[region.Code]region.Region, skip func(reason, detail string)) []region.Region {
	all := make([]region.Region, 0, len(byCode))
	for _, r := range byCode {
		all = append(all, r)
	}
	slices.SortFunc(all, func(a, b region.Region) int {
		if d := int(a.Code.Level()) - int(b.Code.Level()); d != 0 {
			return d
		}
		return compareStrings(a.Code.String(), b.Code.String())
	})

	kept := map[region.Code]bool{}
	out := make([]region.Region, 0, len(all))
	for _, r := range all {
		if parent, ok := r.Code.Parent(); ok && !kept[parent] {
			skip(SkipMissingParent, fmt.Sprintf("%s (induk %s)", r.Code, parent))
			continue
		}
		kept[r.Code] = true
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b region.Region) int { return compareStrings(a.Code.String(), b.Code.String()) })
	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
