package cahyadsn_test

import (
	"context"
	"testing"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/cahyadsn"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/ports"
)

func TestDirSourceReadsOnlyProvinceInOrder(t *testing.T) {
	t.Parallel()
	src, err := cahyadsn.NewDirSource("testdata/db", "32", "test-v1")
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	err = src.Each(context.Background(), func(r ports.RawRegion) error {
		codes = append(codes, r.Code)
		if len(r.Path) == 0 || r.Origin == "" {
			t.Errorf("baris %s tanpa path atau origin", r.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"32", "32.73", "32.73.02", "32.73.02.1003", "32.73.02.1004", "32.73.02.1005"}
	if len(codes) != len(want) {
		t.Fatalf("codes = %v; want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v; want %v", codes, want)
		}
	}
	if src.Version() != "test-v1" || src.Name() == "" {
		t.Error("metadata sumber salah")
	}
}

func TestNewDirSourceValidates(t *testing.T) {
	t.Parallel()
	if _, err := cahyadsn.NewDirSource("testdata/db", "3", "v"); err == nil {
		t.Error("kode provinsi 1 digit harus ditolak")
	}
	if _, err := cahyadsn.NewDirSource("testdata/db", "32", " "); err == nil {
		t.Error("versi kosong harus ditolak")
	}
	if _, err := cahyadsn.NewDirSource("testdata/tidak-ada", "32", "v"); err == nil {
		t.Error("folder tidak ada harus ditolak")
	}
	src, err := cahyadsn.NewDirSource("testdata/db", "33", "v")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Each(context.Background(), func(ports.RawRegion) error { return nil }); err == nil {
		t.Error("provinsi tanpa file kab/kec harus gagal")
	}
}

func TestDirSourceStopsOnCancelledContext(t *testing.T) {
	t.Parallel()
	src, err := cahyadsn.NewDirSource("testdata/db", "32", "v")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := src.Each(ctx, func(ports.RawRegion) error { return nil }); err == nil {
		t.Error("context yang dibatalkan harus menghentikan pembacaan")
	}
}
