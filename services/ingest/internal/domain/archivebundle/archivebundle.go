// Package archivebundle adalah format cadangan arsip payload mentah
// (ADR 0022): semua objek satu konektor pada satu hari UTC dikemas menjadi
// satu bundel tar terkompres zstd, ditemani satu indeks yang mencatat isi
// bundel. Bentuk ini dipakai karena arsip panas berisi ribuan objek kecil per
// hari (sapuan prakiraan BMKG), sedangkan object storage cadangan menghitung
// transaksi per objek dan kompresi lintas payload jauh lebih kecil.
//
// Kunci di penyimpanan cadangan (di bawah awalan URL cadangan):
//
//	<konektor>/<YYYY>/<MM>/<DD>.p<bagian>.tar.zst            bundel
//	<konektor>/<YYYY>/<MM>/<DD>.p<bagian>.n<objek>.idx.json.zst  indeks
//
// Bagian dimulai dari 1. Bagian baru hanya ditambahkan untuk objek hari itu
// yang belum tercakup bagian sebelumnya (misal payload yang ditulis setelah
// hari ditutup); bundel dan indeks lama tidak pernah ditimpa. Indeks ditulis
// setelah bundel dibaca ulang dan cocok, jadi adanya indeks berarti bundelnya
// utuh. Jumlah objek di nama indeks membuat cakupan satu hari bisa dihitung
// dari daftar kunci saja, tanpa mengunduh indeks.
package archivebundle

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivekey"
)

// Format adalah versi format bundel dan indeks.
const Format = 1

const (
	bundleExt = ".tar.zst"
	indexExt  = ".idx.json.zst"
)

// ErrInvalid menandai kunci atau indeks yang tidak sesuai format.
var ErrInvalid = errors.New("bundel arsip tidak sesuai format")

// Kind membedakan bundel dan indeks.
type Kind int

const (
	// KindBundle adalah bundel tar.zst.
	KindBundle Kind = iota + 1
	// KindIndex adalah indeks isi bundel.
	KindIndex
)

// Name adalah isi satu kunci cadangan.
type Name struct {
	Kind      Kind
	Connector string
	// Day adalah awal hari UTC.
	Day  time.Time
	Part int
	// Objects hanya diisi untuk indeks.
	Objects int
}

var (
	connectorRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	fileRe      = regexp.MustCompile(`^(\d{2})\.p([1-9]\d*)(?:\.n([1-9]\d*))?(\.tar\.zst|\.idx\.json\.zst)$`)
)

// BundleKey membentuk kunci bundel.
func BundleKey(connector string, day time.Time, part int) string {
	return fmt.Sprintf("%s%s.p%d%s", connector+"/", day.UTC().Format("2006/01/02"), part, bundleExt)
}

// IndexKey membentuk kunci indeks.
func IndexKey(connector string, day time.Time, part, objects int) string {
	return fmt.Sprintf("%s%s.p%d.n%d%s", connector+"/", day.UTC().Format("2006/01/02"), part, objects, indexExt)
}

// DayPrefix adalah awalan semua kunci cadangan satu konektor pada satu hari.
func DayPrefix(connector string, day time.Time) string {
	return connector + "/" + day.UTC().Format("2006/01/02") + "."
}

// String sama dengan BundleKey atau IndexKey sesuai jenisnya.
func (n Name) String() string {
	if n.Kind == KindIndex {
		return IndexKey(n.Connector, n.Day, n.Part, n.Objects)
	}
	return BundleKey(n.Connector, n.Day, n.Part)
}

// Parse membaca kunci yang dibentuk BundleKey atau IndexKey; hanya bentuk
// kanonik yang diterima.
func Parse(key string) (Name, error) {
	fail := func(why string) (Name, error) { return Name{}, fmt.Errorf("%w: %q (%s)", ErrInvalid, key, why) }
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		return fail("harus empat bagian dipisah /")
	}
	if !connectorRe.MatchString(parts[0]) {
		return fail("nama konektor")
	}
	m := fileRe.FindStringSubmatch(parts[3])
	if m == nil {
		return fail("nama file")
	}
	day, err := time.Parse("2006/01/02", parts[1]+"/"+parts[2]+"/"+m[1])
	if err != nil {
		return fail("tanggal")
	}
	n := Name{Connector: parts[0], Day: day}
	if n.Part, err = strconv.Atoi(m[2]); err != nil {
		return fail("nomor bagian")
	}
	switch m[4] {
	case bundleExt:
		if m[3] != "" {
			return fail("bundel tidak memuat jumlah objek")
		}
		n.Kind = KindBundle
	default:
		if m[3] == "" {
			return fail("indeks wajib memuat jumlah objek")
		}
		n.Kind = KindIndex
		if n.Objects, err = strconv.Atoi(m[3]); err != nil {
			return fail("jumlah objek")
		}
	}
	if n.String() != key {
		return fail("bukan bentuk kanonik")
	}
	return n, nil
}

// Entry adalah satu payload di bundel.
type Entry struct {
	// Key adalah kunci arsip panas (archivekey, berakhiran .gz).
	Key string `json:"key"`
	// Bytes dan SHA256 adalah ukuran dan SHA-256 payload sebelum dikompres.
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Member adalah nama berkas payload di dalam tar: kunci arsip tanpa ".gz".
func (e Entry) Member() string { return strings.TrimSuffix(e.Key, ".gz") }

// Index adalah isi bundel satu bagian.
type Index struct {
	Format    int    `json:"format"`
	Connector string `json:"connector"`
	// Day dalam bentuk YYYY-MM-DD (UTC).
	Day       string    `json:"day"`
	Part      int       `json:"part"`
	CreatedAt time.Time `json:"created_at"`
	// BundleBytes dan BundleSHA256 adalah ukuran dan SHA-256 bundel tar.zst.
	BundleBytes  int64   `json:"bundle_bytes"`
	BundleSHA256 string  `json:"bundle_sha256"`
	Entries      []Entry `json:"entries"`
}

// Name mengembalikan kunci indeks ini.
func (ix Index) Name() (Name, error) {
	day, err := time.Parse(time.DateOnly, ix.Day)
	if err != nil {
		return Name{}, fmt.Errorf("%w: tanggal indeks %q", ErrInvalid, ix.Day)
	}
	return Name{Kind: KindIndex, Connector: ix.Connector, Day: day, Part: ix.Part, Objects: len(ix.Entries)}, nil
}

var hexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate memastikan indeks konsisten dengan kuncinya (want) dan setiap
// entri adalah payload konektor dan hari yang sama, urut kunci tanpa duplikat,
// dengan SHA-256 yang cocok dengan potongan di kunci arsip.
func (ix Index) Validate(want Name) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: indeks %s: %s", ErrInvalid, want, fmt.Sprintf(format, args...))
	}
	if ix.Format != Format {
		return fail("format %d, didukung %d", ix.Format, Format)
	}
	got, err := ix.Name()
	if err != nil {
		return err
	}
	if want.Kind != KindIndex || got != want {
		return fail("isi tidak cocok dengan nama (%s)", got)
	}
	if ix.BundleBytes <= 0 || !hexRe.MatchString(ix.BundleSHA256) {
		return fail("ukuran atau SHA-256 bundel")
	}
	if len(ix.Entries) == 0 {
		return fail("tanpa entri")
	}
	if !slices.IsSortedFunc(ix.Entries, func(a, b Entry) int { return strings.Compare(a.Key, b.Key) }) {
		return fail("entri tidak urut kunci")
	}
	for i, e := range ix.Entries {
		if i > 0 && ix.Entries[i-1].Key == e.Key {
			return fail("kunci ganda %s", e.Key)
		}
		k, err := archivekey.Parse(e.Key)
		if err != nil {
			return fail("%v", err)
		}
		if k.Connector != ix.Connector || !sameDay(k.FetchedAt, want.Day) {
			return fail("%s bukan milik %s %s", e.Key, ix.Connector, ix.Day)
		}
		if e.Bytes < 0 || !hexRe.MatchString(e.SHA256) || !k.Matches(e.SHA256) {
			return fail("ukuran atau SHA-256 %s", e.Key)
		}
	}
	return nil
}

// Day adalah awal hari UTC dari t.
func Day(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func sameDay(a, b time.Time) bool { return Day(a).Equal(Day(b)) }
