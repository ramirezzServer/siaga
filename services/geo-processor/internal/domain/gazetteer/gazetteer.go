// Package gazetteer mencocokkan nama wilayah yang ditulis bebas (misal kolom
// "Dirasakan" BMKG: "Kab. Manggarai", "Kota Bima", "Cibeber", "Benteng
// Selayar") ke kode wilayah Kemendagri di semua tingkat. Fungsi murni tanpa
// I/O; daftar nama dan jarak ke batas wilayah disuntikkan pemanggil.
package gazetteer

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
)

// Level adalah tingkat Kemendagri: 1 provinsi, 2 kab/kota, 3 kecamatan, 4 desa/kelurahan.
type Level int

// Tingkat yang dikenal.
const (
	LevelProvince Level = iota + 1
	LevelRegency
	LevelDistrict
	LevelVillage
)

// Entry adalah satu wilayah Kemendagri.
type Entry struct {
	Code string // misal "53.10" atau "32.03.10.2001"
	Name string // apa adanya dari sumber, misal "Kabupaten Manggarai"
}

// Level diturunkan dari jumlah segmen kode.
func (e Entry) Level() Level { return levelOf(e.Code) }

// Parent adalah kode wilayah induk ("" untuk provinsi).
func (e Entry) Parent() string { return parentOf(e.Code) }

func levelOf(code string) Level { return Level(strings.Count(code, ".") + 1) }

func parentOf(code string) string {
	if i := strings.LastIndexByte(code, '.'); i >= 0 {
		return code[:i]
	}
	return ""
}

// Alias memetakan satu ejaan yang dipakai BMKG ke kode tertentu, untuk nama
// yang tidak bisa dicocokkan dengan aturan umum (misal "Anyer" untuk
// Kecamatan Anyar). Setiap alias wajib punya alasan.
type Alias struct {
	Name   string
	Code   string
	Reason string
}

// Kind adalah cara nama cocok, dicatat supaya hasilnya bisa diperiksa.
type Kind string

// Cara cocok, urut dari yang paling langsung.
const (
	KindAlias     Kind = "alias"
	KindExact     Kind = "nama"
	KindCompact   Kind = "tanpa-spasi"
	KindTownAsSub Kind = "kota-sebagai-kecamatan"
	KindQualified Kind = "nama+induk"
)

// Candidate adalah wilayah yang namanya cocok dengan sebuah penyebutan.
type Candidate struct {
	Entry
	Kind Kind
}

// Index adalah daftar nama siap dicari.
type Index struct {
	byKey   map[string][]int // kunci nama → posisi entri
	compact map[string][]int // kunci tanpa spasi → posisi entri
	entries []Entry
	byCode  map[string]int
	aliases map[string]Alias
}

// ErrInvalid menandai daftar nama atau alias yang tidak bisa dipakai.
var ErrInvalid = errors.New("daftar wilayah tidak valid")

// New membangun indeks. Kode harus unik dan berformat Kemendagri (2, 2, 2,
// 4 digit); alias harus menunjuk kode yang ada.
func New(entries []Entry, aliases []Alias) (*Index, error) {
	ix := &Index{
		byKey: map[string][]int{}, compact: map[string][]int{},
		byCode: make(map[string]int, len(entries)), aliases: map[string]Alias{},
	}
	var errs []error
	for _, e := range entries {
		if !validCode(e.Code) {
			errs = append(errs, fmt.Errorf("kode %q tidak berformat Kemendagri", e.Code))
			continue
		}
		if _, dup := ix.byCode[e.Code]; dup {
			errs = append(errs, fmt.Errorf("kode %s ganda", e.Code))
			continue
		}
		if Normalize(e.Name) == "" {
			errs = append(errs, fmt.Errorf("nama kosong untuk %s", e.Code))
			continue
		}
		i := len(ix.entries)
		ix.entries = append(ix.entries, e)
		ix.byCode[e.Code] = i
		for _, k := range nameKeys(e) {
			ix.byKey[k] = appendUnique(ix.byKey[k], i)
			ix.compact[strings.ReplaceAll(k, " ", "")] = appendUnique(ix.compact[strings.ReplaceAll(k, " ", "")], i)
		}
	}
	for _, a := range aliases {
		k := Normalize(a.Name)
		switch {
		case k == "":
			errs = append(errs, errors.New("alias tanpa nama"))
		case strings.TrimSpace(a.Reason) == "":
			errs = append(errs, fmt.Errorf("alias %q tanpa alasan", a.Name))
		case !ix.has(a.Code):
			errs = append(errs, fmt.Errorf("alias %q menunjuk kode %s yang tidak ada", a.Name, a.Code))
		default:
			if _, dup := ix.aliases[k]; dup {
				errs = append(errs, fmt.Errorf("alias %q ganda", a.Name))
			}
			ix.aliases[k] = a
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}
	return ix, nil
}

func (ix *Index) has(code string) bool { _, ok := ix.byCode[code]; return ok }

// Len adalah jumlah wilayah di indeks.
func (ix *Index) Len() int { return len(ix.entries) }

// Entry mengembalikan wilayah dengan kode itu.
func (ix *Index) Entry(code string) (Entry, bool) {
	i, ok := ix.byCode[code]
	if !ok {
		return Entry{}, false
	}
	return ix.entries[i], true
}

func appendUnique(s []int, v int) []int {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

func validCode(c string) bool {
	segs := strings.Split(c, ".")
	widths := []int{2, 2, 2, 4}
	if len(segs) > len(widths) {
		return false
	}
	for i, s := range segs {
		if len(s) != widths[i] || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
			return false
		}
	}
	return true
}

// Awalan jenis kab/kota sesudah Normalize.
const (
	prefixRegency = "kabupaten "
	prefixCity    = "kota "
	prefixIslands = "kepulauan "
)

// nameKeys adalah kunci pencarian satu wilayah: nama lengkap, dan untuk
// kab/kota juga nama tanpa "Kabupaten"/"Kota" (BMKG sering menulis
// "Manggarai" untuk Kabupaten Manggarai).
func nameKeys(e Entry) []string {
	k := Normalize(e.Name)
	keys := []string{k}
	add := func(base string) {
		if base != "" && !slices.Contains(keys, base) {
			keys = append(keys, base)
		}
	}
	switch e.Level() {
	case LevelRegency:
		for _, p := range []string{prefixRegency, prefixCity} {
			if base, ok := strings.CutPrefix(k, p); ok {
				add(base)
				// "Kabupaten Kepulauan Selayar" juga "Selayar".
				if island, ok := strings.CutPrefix(base, prefixIslands); ok {
					add(island)
				}
			}
		}
	case LevelDistrict, LevelVillage:
		// Kecamatan "Kota Sigli" disebut BMKG "Sigli".
		if base, ok := strings.CutPrefix(k, prefixCity); ok {
			add(base)
		}
	case LevelProvince:
	}
	return keys
}

// abbreviations adalah singkatan yang dipakai BMKG, diperluas setelah
// tanda baca dibuang: "Kab." → "kabupaten", "Kep." → "kepulauan".
var abbreviations = map[string]string{"kab": "kabupaten", "kep": "kepulauan", "kec": "kecamatan", "kel": "kelurahan"}

// Normalize membuat kunci nama: huruf kecil, apostrof dibuang, tanda baca
// menjadi spasi, singkatan Kab./Kep./Kec./Kel. diperluas, spasi dirapikan.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case r == '\'' || r == '’' || r == '`':
			// "Ma'rang" dan "Marang" sama
		default:
			b.WriteByte(' ')
		}
	}
	words := strings.Fields(b.String())
	for i, w := range words {
		if full, ok := abbreviations[w]; ok {
			words[i] = full
		}
	}
	return strings.Join(words, " ")
}

// Lookup mencari semua wilayah yang namanya cocok dengan penyebutan. Aturan,
// berurutan (aturan pertama yang menghasilkan kandidat dipakai):
//
//  1. alias;
//  2. nama sama persis (setelah Normalize) di semua tingkat; "Kab. X" hanya
//     mencari kabupaten, "Kota X" hanya kota;
//  3. nama sama tanpa spasi ("Palabuhan Ratu" = "Palabuhanratu");
//  4. "Kota X" yang tidak ada sebagai kota: kecamatan atau desa/kelurahan X
//     (BMKG menyebut pusat kabupaten sebagai kota, misal "Kota Cianjur");
//  5. "X Y" dengan Y nama kab/kota: X (nama atau alias) di dalam kab/kota
//     itu ("Benteng Selayar", "Jampea Kep. Selayar").
//
// Hasil diurutkan menurut kode.
func (ix *Index) Lookup(name string) []Candidate {
	key := Normalize(name)
	if key == "" {
		return nil
	}
	if a, ok := ix.aliases[key]; ok {
		return []Candidate{{Entry: ix.entries[ix.byCode[a.Code]], Kind: KindAlias}}
	}
	want := func(Entry) bool { return true }
	switch {
	case strings.HasPrefix(key, prefixRegency):
		want = func(e Entry) bool {
			return e.Level() == LevelRegency && strings.HasPrefix(Normalize(e.Name), prefixRegency)
		}
	case strings.HasPrefix(key, prefixCity):
		want = func(e Entry) bool {
			return e.Level() == LevelRegency && strings.HasPrefix(Normalize(e.Name), prefixCity)
		}
	}
	if c := ix.pick(ix.byKey[key], want, KindExact); len(c) > 0 {
		return c
	}
	if c := ix.pick(ix.compact[strings.ReplaceAll(key, " ", "")], want, KindCompact); len(c) > 0 {
		return c
	}
	if base, ok := strings.CutPrefix(key, prefixCity); ok {
		sub := func(e Entry) bool { return e.Level() >= LevelDistrict }
		if c := ix.pick(ix.byKey[base], sub, KindTownAsSub); len(c) > 0 {
			return c
		}
	}
	return ix.qualified(key)
}

func (ix *Index) pick(pos []int, want func(Entry) bool, kind Kind) []Candidate {
	var out []Candidate
	for _, i := range pos {
		if e := ix.entries[i]; want(e) {
			out = append(out, Candidate{Entry: e, Kind: kind})
		}
	}
	slices.SortFunc(out, func(a, b Candidate) int { return cmp.Compare(a.Code, b.Code) })
	return out
}

// qualified mencoba setiap pemisahan "X Y" (Y paling panjang dulu) dengan Y
// nama kab/kota dan X wilayah di bawahnya.
func (ix *Index) qualified(key string) []Candidate {
	words := strings.Fields(key)
	for cut := 1; cut < len(words); cut++ {
		head, tail := strings.Join(words[:cut], " "), strings.Join(words[cut:], " ")
		var parents []string
		for _, i := range ix.byKey[tail] {
			if e := ix.entries[i]; e.Level() == LevelRegency {
				parents = append(parents, e.Code+".")
			}
		}
		if len(parents) == 0 {
			continue
		}
		under := func(e Entry) bool {
			return e.Level() > LevelRegency && slices.ContainsFunc(parents, func(p string) bool { return strings.HasPrefix(e.Code, p) })
		}
		pos := ix.byKey[head]
		if a, ok := ix.aliases[head]; ok {
			pos = []int{ix.byCode[a.Code]}
		}
		if c := ix.pick(pos, under, KindQualified); len(c) > 0 {
			return c
		}
	}
	return nil
}

// Distance memberi jarak (km) dari episenter ke batas wilayah berkode itu;
// ok=false bila batasnya tidak tersedia.
type Distance func(code string) (km float64, ok bool)

// Match adalah hasil pencocokan satu penyebutan.
type Match struct {
	Candidate
	// DistanceKm adalah jarak episenter ke batas wilayah yang dipakai.
	DistanceKm float64
	// BoundaryCode adalah wilayah yang batasnya dipakai: wilayah itu sendiri,
	// atau induk terdekat yang batasnya ada (desa/kelurahan memakai kecamatan).
	BoundaryCode string
	// Candidates adalah jumlah wilayah dengan nama cocok.
	Candidates int
}

// Resolve memilih satu kandidat: yang batasnya paling dekat ke episenter
// (nama ganda di provinsi lain hampir selalu jauh). Desa/kelurahan memakai
// batas kecamatan induknya bila batasnya sendiri tidak tersedia; jaraknya
// tidak pernah lebih besar dari jarak ke desa itu, jadi tetap batas bawah
// jarak orang yang merasakan. Seri dipecah dengan kode terkecil. ok=false
// bila tidak ada kandidat yang batasnya tersedia.
func (ix *Index) Resolve(cands []Candidate, dist Distance) (Match, bool) {
	best := Match{DistanceKm: math.Inf(1)}
	found := false
	for _, c := range cands {
		code := c.Code
		km, ok := dist(code)
		for !ok && levelOf(code) > LevelDistrict {
			code = parentOf(code)
			km, ok = dist(code)
		}
		if !ok {
			continue
		}
		if !found || km < best.DistanceKm || (km == best.DistanceKm && c.Code < best.Code) {
			best = Match{Candidate: c, DistanceKm: km, BoundaryCode: code}
			found = true
		}
	}
	best.Candidates = len(cands)
	return best, found
}
