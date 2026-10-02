package archivebackup

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/emit"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/archivebundle"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/retention"
	"github.com/ramirezzServer/siaga/services/ingest/internal/ports"
)

// mem adalah arsip dalam memori dengan titik galat untuk test.
type mem struct {
	mu   sync.Mutex
	objs map[string][]byte
	// putErr/getErr/delErr dipakai untuk kunci yang mengandung kunci peta.
	putErr, getErr, delErr map[string]error
	// tamper mengubah isi yang dibaca Get untuk kunci yang mengandung kunci peta.
	tamper  map[string]func([]byte) []byte
	listErr error
	gets    int
	deletes []string
}

func newMem() *mem {
	return &mem{
		objs: map[string][]byte{}, putErr: map[string]error{}, getErr: map[string]error{},
		delErr: map[string]error{}, tamper: map[string]func([]byte) []byte{},
	}
}

func match[V any](m map[string]V, key string) (V, bool) {
	for k, v := range m {
		if strings.Contains(key, k) {
			return v, true
		}
	}
	var zero V
	return zero, false
}

func (m *mem) Put(_ context.Context, k string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := match(m.putErr, k); ok {
		return err
	}
	m.objs[k] = slices.Clone(b)
	return nil
}

func (m *mem) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	if err, ok := match(m.getErr, k); ok {
		return nil, err
	}
	b, ok := m.objs[k]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrArchiveNotFound, k)
	}
	b = slices.Clone(b)
	if f, ok := match(m.tamper, k); ok {
		b = f(b)
	}
	return b, nil
}

func (m *mem) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := match(m.delErr, k); ok {
		return err
	}
	m.deletes = append(m.deletes, k)
	delete(m.objs, k)
	return nil
}

func (m *mem) List(_ context.Context, prefix, after string) iter.Seq2[ports.ArchiveObject, error] {
	return func(yield func(ports.ArchiveObject, error) bool) {
		m.mu.Lock()
		if m.listErr != nil {
			m.mu.Unlock()
			yield(ports.ArchiveObject{}, m.listErr)
			return
		}
		var objs []ports.ArchiveObject
		for k, v := range m.objs {
			if strings.HasPrefix(k, prefix) && k > after {
				objs = append(objs, ports.ArchiveObject{Key: k, Size: int64(len(v))})
			}
		}
		m.mu.Unlock()
		slices.SortFunc(objs, func(a, b ports.ArchiveObject) int { return strings.Compare(a.Key, b.Key) })
		for _, o := range objs {
			if !yield(o, nil) {
				return
			}
		}
	}
}

func (m *mem) keys(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.objs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

var (
	d1    = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	clock = func(t time.Time) func() time.Time { return func() time.Time { return t } }
)

// put mengarsipkan payload seperti ingest dan mengembalikan kuncinya.
func put(t *testing.T, a ports.Archive, conn string, at time.Time, payload string) string {
	t.Helper()
	key, err := emit.Archive(t.Context(), a, conn, "json", emit.Sum([]byte(payload)), []byte(payload), at)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// fillDay mengisi n payload satu konektor pada hari day.
func fillDay(t *testing.T, a ports.Archive, conn string, day time.Time, n int) []string {
	t.Helper()
	keys := make([]string, n)
	for i := range n {
		keys[i] = put(t, a, conn, day.Add(time.Duration(i)*time.Minute), fmt.Sprintf(`{"konektor":%q,"i":%d,"isi":"%s"}`, conn, i, strings.Repeat("prakiraan ", i%7)))
	}
	return keys
}

func runBackup(t *testing.T, src, dst *mem, now time.Time) BackupReport {
	t.Helper()
	rep, err := Backup{Src: src, Dst: dst, Now: clock(now)}.Run(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestBackupVerifyRestoreRoundTrip(t *testing.T) {
	src, dst := newMem(), newMem()
	want := fillDay(t, src, "bmkg-prakiraan", d1, 40)
	want = append(want, fillDay(t, src, "usgs-2.5-day", d1, 5)...)
	fillDay(t, src, "usgs-2.5-day", d1.AddDate(0, 0, 1), 3) // hari ini: belum ditutup
	put(t, src, "asing", d1, "x")
	src.objs["bukan/kunci/baku.gz"] = []byte("x")

	var progress []PartReport
	now := d1.AddDate(0, 0, 1).Add(time.Hour)
	rep, err := Backup{Src: src, Dst: dst, Now: clock(now), Progress: func(p PartReport) { progress = append(progress, p) }}.
		Run(t.Context(), Filter{Connectors: []string{"bmkg-prakiraan", "usgs-2.5-day"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Parts) != 2 || rep.Covered != 0 || rep.Corrupt != 0 || rep.Foreign != 1 || rep.ClosedBefore != "2026-10-02" || len(progress) != 2 {
		t.Fatalf("%+v", rep)
	}
	if got := dst.keys(""); !slices.Equal(got, []string{
		"bmkg-prakiraan/2026/10/01.p1.n40.idx.json.zst", "bmkg-prakiraan/2026/10/01.p1.tar.zst",
		"usgs-2.5-day/2026/10/01.p1.n5.idx.json.zst", "usgs-2.5-day/2026/10/01.p1.tar.zst",
	}) {
		t.Fatal(got)
	}

	// Jalan ulang: tidak mengunduh atau menulis apa pun.
	gets := dst.gets
	rep = runBackup(t, src, dst, now)
	if len(rep.Parts) != 1 || rep.Parts[0].Connector != "asing" || dst.gets-gets != 1 { // hanya baca ulang bundel "asing"
		t.Fatalf("%+v gets %d", rep, dst.gets-gets)
	}
	rep = runBackup(t, src, dst, now)
	if len(rep.Parts) != 0 || rep.Covered != 3 {
		t.Fatalf("jalan ketiga %+v", rep)
	}

	vr, err := Verify(t.Context(), dst, Filter{From: d1, To: d1})
	if err != nil || vr.Parts != 3 || vr.Objects != 46 || vr.Corrupt != 0 {
		t.Fatalf("%+v %v", vr, err)
	}

	to := newMem()
	put(t, to, "usgs-2.5-day", d1, `{"konektor":"usgs-2.5-day","i":0,"isi":""}`) // sudah ada
	rr, err := Restore(t.Context(), dst, to, Filter{Connectors: []string{"bmkg-prakiraan", "usgs-2.5-day"}, From: d1, To: d1})
	if err != nil || rr.Parts != 2 || rr.Restored != 44 || rr.Existing != 1 || rr.Corrupt != 0 {
		t.Fatalf("%+v %v", rr, err)
	}
	for _, k := range want {
		if !slices.Equal(to.objs[k], src.objs[k]) {
			t.Fatalf("%s tidak sama byte per byte", k)
		}
	}
	if rr, err := Restore(t.Context(), dst, to, Filter{From: d1}); err != nil || rr.Restored != 1 || rr.Existing != 45 {
		t.Fatalf("restore ulang %+v %v", rr, err)
	}
}

func TestBackupAddsPartForLateObjects(t *testing.T) {
	src, dst := newMem(), newMem()
	fillDay(t, src, "c", d1, 3)
	now := d1.AddDate(0, 0, 1).Add(time.Hour)
	runBackup(t, src, dst, now)
	late := put(t, src, "c", d1.Add(23*time.Hour+59*time.Minute+59*time.Second), "terlambat")
	rep := runBackup(t, src, dst, now)
	if len(rep.Parts) != 1 || rep.Parts[0].Part != 2 || rep.Parts[0].Objects != 1 {
		t.Fatalf("%+v", rep)
	}
	covered, ixs, err := loadIndexes(t.Context(), dst, mustNames(t, dst, "c/"))
	if err != nil || len(covered) != 4 || len(ixs) != 2 {
		t.Fatalf("%d %d %v", len(covered), len(ixs), err)
	}
	if _, ok := covered[late]; !ok {
		t.Fatal("payload terlambat tidak tercakup")
	}
	// Bundel tanpa indeks (jalan sebelumnya terputus) tidak ditimpa.
	dst.objs["c/2026/10/01.p3.tar.zst"] = []byte("sisa")
	put(t, src, "c", d1.Add(23*time.Hour), "lagi")
	if rep := runBackup(t, src, dst, now); len(rep.Parts) != 1 || rep.Parts[0].Part != 4 {
		t.Fatalf("%+v", rep)
	}
	if string(dst.objs["c/2026/10/01.p3.tar.zst"]) != "sisa" {
		t.Fatal("bundel lama ditimpa")
	}
}

func mustNames(t *testing.T, dst *mem, prefix string) []archivebundle.Name {
	t.Helper()
	days, err := scanBackup(t.Context(), dst, prefix)
	if err != nil {
		t.Fatal(err)
	}
	var out []archivebundle.Name
	for _, d := range days {
		out = append(out, d.indexes...)
	}
	return out
}

func TestBackupSplitsParts(t *testing.T) {
	src, dst := newMem(), newMem()
	for i := range 30 {
		// Payload acak tidak terkompres, jadi ukuran bundel cepat naik.
		put(t, src, "c", d1.Add(time.Duration(i)*time.Minute), strings.Repeat(fmt.Sprintf("%x", i*7919), 9000))
	}
	rep, err := Backup{Src: src, Dst: dst, Now: clock(d1.AddDate(0, 0, 2)), MaxPartBytes: 1}.Run(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Parts) < 2 {
		t.Fatalf("tidak dipecah: %+v", rep)
	}
	total := 0
	for i, p := range rep.Parts {
		if p.Part != i+1 {
			t.Fatalf("nomor bagian %+v", rep.Parts)
		}
		total += p.Objects
	}
	if total != 30 {
		t.Fatal(total)
	}
	if vr, err := Verify(t.Context(), dst, Filter{}); err != nil || vr.Objects != 30 || vr.Corrupt != 0 {
		t.Fatalf("%+v %v", vr, err)
	}
}

func TestBackupSkipsCorruptSource(t *testing.T) {
	src, dst := newMem(), newMem()
	keys := fillDay(t, src, "c", d1, 3)
	src.objs[keys[1]] = []byte("bukan gzip")
	rep := runBackup(t, src, dst, d1.AddDate(0, 0, 3))
	if rep.Corrupt != 1 || len(rep.Samples) != 1 || len(rep.Parts) != 1 || rep.Parts[0].Objects != 2 {
		t.Fatalf("%+v", rep)
	}
	// Semua objek satu hari rusak: tidak ada bundel kosong.
	src2, dst2 := newMem(), newMem()
	k := put(t, src2, "c", d1, "x")
	src2.objs[k] = []byte("rusak")
	if rep := runBackup(t, src2, dst2, d1.AddDate(0, 0, 3)); len(rep.Parts) != 0 || len(dst2.objs) != 0 {
		t.Fatalf("%+v %v", rep, dst2.keys(""))
	}
}

func TestBackupFailures(t *testing.T) {
	boom := errors.New("galat")
	now := d1.AddDate(0, 0, 2)
	cases := map[string]func(src, dst *mem){
		"daftar sumber":   func(src, _ *mem) { src.listErr = boom },
		"daftar cadangan": func(_, dst *mem) { dst.listErr = boom },
		"baca sumber":     func(src, _ *mem) { src.getErr["c/"] = boom },
		"tulis bundel":    func(_, dst *mem) { dst.putErr[".tar.zst"] = boom },
		"baca ulang":      func(_, dst *mem) { dst.getErr[".tar.zst"] = boom },
		"tulis indeks":    func(_, dst *mem) { dst.putErr[".idx."] = boom },
		"bundel berubah": func(_, dst *mem) {
			dst.tamper[".tar.zst"] = func(b []byte) []byte { b[len(b)-1] ^= 1; return b }
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			src, dst := newMem(), newMem()
			fillDay(t, src, "c", d1, 2)
			setup(src, dst)
			if _, err := (Backup{Src: src, Dst: dst, Now: clock(now)}).Run(t.Context(), Filter{}); err == nil {
				t.Fatal("ingin galat")
			}
			for _, k := range dst.keys("") {
				if strings.Contains(k, ".idx.") {
					t.Fatalf("indeks tertulis walau gagal: %s", k)
				}
			}
		})
	}
	t.Run("indeks rusak", func(t *testing.T) {
		src, dst := newMem(), newMem()
		fillDay(t, src, "c", d1, 2)
		runBackup(t, src, dst, now)
		put(t, src, "c", d1.Add(time.Hour), "baru") // memaksa indeks dibaca
		dst.tamper[".idx."] = func([]byte) []byte { return []byte("bukan zstd") }
		if _, err := (Backup{Src: src, Dst: dst, Now: clock(now)}).Run(t.Context(), Filter{}); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("ingin ErrCorrupt, dapat %v", err)
		}
	})
	t.Run("tanpa jam", func(t *testing.T) {
		if _, err := (Backup{Src: newMem(), Dst: newMem()}).Run(t.Context(), Filter{}); err == nil {
			t.Fatal("ingin galat")
		}
	})
	t.Run("dibatalkan", func(t *testing.T) {
		src := newMem()
		fillDay(t, src, "c", d1, 2)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (Backup{Src: src, Dst: newMem(), Now: clock(now)}).Run(ctx, Filter{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestVerifyDetectsDamage(t *testing.T) {
	now := d1.AddDate(0, 0, 2)
	cases := map[string]func(dst *mem){
		"bundel diubah": func(dst *mem) {
			dst.tamper["c/2026/10/01.p1.tar.zst"] = func(b []byte) []byte { b[len(b)/2] ^= 0xff; return b }
		},
		"bundel hilang": func(dst *mem) { delete(dst.objs, "c/2026/10/01.p1.tar.zst") },
		"indeks diubah": func(dst *mem) {
			dst.tamper["c/2026/10/01.p1.n3.idx"] = func([]byte) []byte { return []byte("x") }
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			src, dst := newMem(), newMem()
			fillDay(t, src, "c", d1, 3)
			fillDay(t, src, "d", d1, 2)
			runBackup(t, src, dst, now)
			damage(dst)
			vr, err := Verify(t.Context(), dst, Filter{Connectors: []string{"c"}})
			if err != nil || vr.Corrupt != 1 || vr.Parts != 1 || len(vr.Samples) != 1 {
				t.Fatalf("%+v %v", vr, err)
			}
			rr, err := Restore(t.Context(), dst, newMem(), Filter{From: d1})
			if err != nil || rr.Corrupt != 1 || rr.Restored != 2 {
				t.Fatalf("restore %+v %v", rr, err)
			}
		})
	}
	t.Run("galat baca menghentikan", func(t *testing.T) {
		src, dst := newMem(), newMem()
		fillDay(t, src, "c", d1, 1)
		runBackup(t, src, dst, now)
		dst.getErr[".tar.zst"] = errors.New("jaringan")
		if _, err := Verify(t.Context(), dst, Filter{}); err == nil || errors.Is(err, ErrCorrupt) {
			t.Fatalf("Verify %v", err)
		}
		if _, err := Restore(t.Context(), dst, newMem(), Filter{}); err == nil || errors.Is(err, ErrCorrupt) {
			t.Fatalf("Restore %v", err)
		}
		dst.listErr = errors.New("daftar")
		if _, err := Verify(t.Context(), dst, Filter{}); err == nil {
			t.Fatal("Verify tanpa galat daftar")
		}
		if _, err := Restore(t.Context(), dst, newMem(), Filter{}); err == nil {
			t.Fatal("Restore tanpa galat daftar")
		}
	})
	t.Run("tujuan gagal", func(t *testing.T) {
		src, dst := newMem(), newMem()
		fillDay(t, src, "c", d1, 1)
		runBackup(t, src, dst, now)
		to := newMem()
		to.putErr["c/"] = errors.New("penuh")
		if _, err := Restore(t.Context(), dst, to, Filter{}); err == nil {
			t.Fatal("ingin galat tulis")
		}
		to = newMem()
		to.listErr = errors.New("daftar")
		if _, err := Restore(t.Context(), dst, to, Filter{}); err == nil {
			t.Fatal("ingin galat daftar tujuan")
		}
	})
}

func TestPrune(t *testing.T) {
	old := d1.AddDate(0, 0, -20)
	now := d1.Add(12 * time.Hour)
	src, dst := newMem(), newMem()
	prakOld := fillDay(t, src, "bmkg-prakiraan", old, 4)
	prakNew := fillDay(t, src, "bmkg-prakiraan", d1.AddDate(0, 0, -3), 2)
	gempa := fillDay(t, src, "bmkg-autogempa", old, 2)
	usgsOld := fillDay(t, src, "usgs-2.5-day", old, 2)
	runBackup(t, src, dst, now)
	// Objek yang muncul setelah cadangan dibuat belum tercakup.
	unbacked := put(t, src, "bmkg-prakiraan", old.Add(time.Hour), "belum")
	// usgs tidak dicadangkan sama sekali untuk hari lain.
	usgsOlder := fillDay(t, src, "usgs-2.5-day", old.AddDate(0, 0, -15), 1)

	policy := retention.Default()
	dry, err := Prune{Src: src, Backup: dst, Policy: policy, Now: clock(now)}.Run(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Applied || len(src.deletes) != 0 {
		t.Fatalf("dry-run menghapus: %v", src.deletes)
	}
	want := []ConnectorPrune{
		{Connector: "bmkg-prakiraan", KeepDays: 14, Cutoff: "2026-09-17", Days: 1, Deleted: 4, Bytes: size(src, prakOld), NotBackedUp: 1},
		{Connector: "usgs-2.5-day", KeepDays: 30, Cutoff: "2026-09-01", Days: 1, NotBackedUp: 1},
	}
	if !slices.Equal(dry.Connectors, want) {
		t.Fatalf("\n%+v\ningin\n%+v", dry.Connectors, want)
	}
	if len(dry.Samples) != 2 {
		t.Fatal(dry.Samples)
	}

	rep, err := Prune{Src: src, Backup: dst, Policy: policy, Now: clock(now), Apply: true}.Run(t.Context(), Filter{})
	if err != nil || !rep.Applied {
		t.Fatalf("%+v %v", rep, err)
	}
	if !slices.Equal(sorted(src.deletes), sorted(prakOld)) {
		t.Fatalf("dihapus %v", src.deletes)
	}
	for _, k := range slices.Concat(prakNew, gempa, usgsOld, usgsOlder, []string{unbacked}) {
		if _, ok := src.objs[k]; !ok {
			t.Fatalf("%s ikut terhapus", k)
		}
	}
	// Jalan ulang: tidak ada lagi yang bisa dihapus.
	rep, err = Prune{Src: src, Backup: dst, Policy: policy, Now: clock(now), Apply: true}.Run(t.Context(), Filter{})
	if err != nil || rep.Connectors[0].Deleted != 0 || rep.Connectors[0].NotBackedUp != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func size(m *mem, keys []string) int64 {
	var n int64
	for _, k := range keys {
		n += int64(len(m.objs[k]))
	}
	return n
}

func sorted(s []string) []string { return slices.Sorted(slices.Values(s)) }

func TestPruneLimitsAndFailures(t *testing.T) {
	old := d1.AddDate(0, 0, -40)
	now := d1
	setup := func(t *testing.T) (*mem, *mem) {
		src, dst := newMem(), newMem()
		fillDay(t, src, "usgs-2.5-day", old, 3)
		fillDay(t, src, "usgs-2.5-day", old.AddDate(0, 0, 1), 3)
		runBackup(t, src, dst, now)
		return src, dst
	}
	t.Run("batas hapus", func(t *testing.T) {
		src, dst := setup(t)
		rep, err := Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now), Apply: true, MaxDelete: 4}.Run(t.Context(), Filter{})
		if err != nil || !rep.Limited || len(src.deletes) != 4 || rep.Connectors[0].Deleted != 4 {
			t.Fatalf("%+v %v %d", rep, err, len(src.deletes))
		}
		rep, err = Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now), Apply: true, MaxDelete: 4}.Run(t.Context(), Filter{})
		if err != nil || rep.Limited || len(src.deletes) != 6 {
			t.Fatalf("lanjutan %+v %v", rep, err)
		}
	})
	t.Run("filter konektor", func(t *testing.T) {
		src, dst := setup(t)
		rep, err := Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now), Apply: true}.Run(t.Context(), Filter{Connectors: []string{"lain"}})
		if err != nil || len(rep.Connectors) != 0 || len(src.deletes) != 0 {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	errCases := map[string]func(src, dst *mem) Prune{
		"kosong": func(src, dst *mem) Prune {
			// Policy kosong sah dan tidak memangkas apa pun; dipakai untuk
			// memastikan galat lain di bawah benar-benar dari titik galatnya.
			return Prune{Src: src, Backup: dst, Policy: retention.Policy{}, Now: clock(now), Apply: true}
		},
		"tanpa jam": func(src, dst *mem) Prune { return Prune{Src: src, Backup: dst, Policy: retention.Default()} },
		"daftar": func(src, dst *mem) Prune {
			src.listErr = errors.New("x")
			return Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now)}
		},
		"daftar cadangan": func(src, dst *mem) Prune {
			dst.listErr = errors.New("x")
			return Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now)}
		},
		"indeks": func(src, dst *mem) Prune {
			dst.getErr[".idx."] = errors.New("x")
			return Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now)}
		},
		"hapus": func(src, dst *mem) Prune {
			src.delErr["usgs"] = errors.New("x")
			return Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now), Apply: true}
		},
	}
	for name, mk := range errCases {
		t.Run(name, func(t *testing.T) {
			src, dst := setup(t)
			p := mk(src, dst)
			_, err := p.Run(t.Context(), Filter{})
			if name == "kosong" {
				if err != nil || len(src.deletes) != 0 {
					t.Fatalf("%v %v", err, src.deletes)
				}
				return
			}
			if err == nil {
				t.Fatal("ingin galat")
			}
		})
	}
	t.Run("dibatalkan", func(t *testing.T) {
		src, dst := setup(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (Prune{Src: src, Backup: dst, Policy: retention.Default(), Now: clock(now)}).Run(ctx, Filter{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}
