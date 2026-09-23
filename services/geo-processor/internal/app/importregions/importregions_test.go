package importregions_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/app/importregions"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

const square = `[[[-6.9,107.6],[-6.9,107.7],[-6.8,107.7],[-6.9,107.6]]]`

type fakeSource struct{ rows []ports.RawRegion }

func (s fakeSource) Name() string    { return "fake" }
func (s fakeSource) Version() string { return "v1" }
func (s fakeSource) Each(_ context.Context, fn func(ports.RawRegion) error) error {
	for _, r := range s.rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

type fakeStore struct {
	saved    []region.Region
	existing []string
	err      error
}

func (s *fakeStore) UpsertAll(_ context.Context, rs []region.Region, source, version string) (ports.UpsertResult, error) {
	if s.err != nil {
		return ports.UpsertResult{}, s.err
	}
	if source != "fake" || version != "v1" {
		return ports.UpsertResult{}, errors.New("metadata sumber tidak diteruskan")
	}
	s.saved = rs
	return ports.UpsertResult{Inserted: len(rs)}, nil
}

func (s *fakeStore) CodesUnder(context.Context, string) ([]string, error) { return s.existing, nil }

func raw(code, name string) ports.RawRegion {
	return ports.RawRegion{Code: code, Name: name, Path: []byte(square), Origin: "t.sql"}
}

func codesOf(rs []region.Region) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Code.String()
	}
	return out
}

func TestRunHappyPathOrdersParentsFirst(t *testing.T) {
	t.Parallel()
	src := fakeSource{rows: []ports.RawRegion{
		raw("32.73.02.1003", "Sadang Serang"), raw("32", "Jawa Barat"),
		raw("32.73.02", "Coblong"), raw("32.73", "Kota Bandung"),
	}}
	store := &fakeStore{existing: []string{"32", "32.73", "32.73.02", "32.73.02.1003", "32.73.99"}}
	rep, err := importregions.Run(context.Background(), src, store, importregions.Options{Province: "32"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"32", "32.73", "32.73.02", "32.73.02.1003"}
	if got := codesOf(store.saved); !slices.Equal(got, want) {
		t.Errorf("tersimpan %v; want %v", got, want)
	}
	if rep.Read != 4 || rep.Accepted != 4 || rep.PerKind[region.KindKota] != 1 || rep.PerKind[region.KindKelurahan] != 1 {
		t.Errorf("laporan salah: %+v", rep)
	}
	if !slices.Equal(rep.Stale, []string{"32.73.99"}) {
		t.Errorf("stale = %v", rep.Stale)
	}
}

func TestRunStrictByDefault(t *testing.T) {
	t.Parallel()
	src := fakeSource{rows: []ports.RawRegion{
		raw("32", "Jawa Barat"), raw("32.73", "Kota Bandung"), raw("32.7", "Rusak"),
	}}
	store := &fakeStore{}
	rep, err := importregions.Run(context.Background(), src, store, importregions.Options{Province: "32"})
	if !errors.Is(err, importregions.ErrTooManySkipped) {
		t.Fatalf("error = %v; want ErrTooManySkipped", err)
	}
	if store.saved != nil {
		t.Error("tidak boleh ada yang ditulis saat import gagal")
	}
	if rep.Skipped[importregions.SkipInvalidCode] != 1 || len(rep.SkipExamples) != 1 {
		t.Errorf("laporan salah: %+v", rep)
	}
}

func TestRunSkipReasonsAndOrphanCascade(t *testing.T) {
	t.Parallel()
	src := fakeSource{rows: []ports.RawRegion{
		raw("32", "Jawa Barat"),
		raw("32", "Duplikat"),
		raw("33.01", "Provinsi lain"),
		raw("32.73", "  "),
		{Code: "32.04", Name: "Geometri rusak", Path: []byte(`[[[0,0]]]`)},
		raw("32.73.02", "Anak dari kota bernama kosong"),
		raw("32.73.02.1003", "Cucu"),
		raw("32.01", "Kab. Bogor"),
	}}
	store := &fakeStore{}
	rep, err := importregions.Run(context.Background(), src, store, importregions.Options{Province: "32", MaxSkipped: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := codesOf(store.saved); !slices.Equal(got, []string{"32", "32.01"}) {
		t.Errorf("tersimpan %v", got)
	}
	want := map[string]int{
		importregions.SkipDuplicate:       1,
		importregions.SkipOtherProvince:   1,
		importregions.SkipInvalidName:     1,
		importregions.SkipInvalidGeometry: 1,
		importregions.SkipMissingParent:   2,
	}
	for k, v := range want {
		if rep.Skipped[k] != v {
			t.Errorf("skipped[%s] = %d; want %d (semua: %v)", k, rep.Skipped[k], v, rep.Skipped)
		}
	}
}

func TestRunRequiresRootAndValidProvince(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := importregions.Run(ctx, fakeSource{rows: []ports.RawRegion{raw("32.73", "Kota Bandung")}}, &fakeStore{}, importregions.Options{Province: "32", MaxSkipped: 5})
	if !errors.Is(err, importregions.ErrNoRoot) {
		t.Errorf("error = %v; want ErrNoRoot", err)
	}
	if _, err := importregions.Run(ctx, fakeSource{}, &fakeStore{}, importregions.Options{Province: "32.73"}); err == nil {
		t.Error("provinsi berlevel 2 harus ditolak")
	}
}

func TestRunDryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	rep, err := importregions.Run(context.Background(), fakeSource{rows: []ports.RawRegion{raw("32", "Jawa Barat")}}, store, importregions.Options{Province: "32", DryRun: true})
	if err != nil || store.saved != nil || rep.Accepted != 1 {
		t.Errorf("dry run: err=%v saved=%v rep=%+v", err, store.saved, rep)
	}
}

func TestRunPropagatesStoreError(t *testing.T) {
	t.Parallel()
	boom := errors.New("db mati")
	_, err := importregions.Run(context.Background(), fakeSource{rows: []ports.RawRegion{raw("32", "Jawa Barat")}}, &fakeStore{err: boom}, importregions.Options{Province: "32"})
	if !errors.Is(err, boom) {
		t.Errorf("error = %v; want db mati", err)
	}
}
