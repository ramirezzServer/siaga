package region_test

import (
	"errors"
	"testing"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

func square() region.MultiPolygon {
	return region.MultiPolygon{{{{107.6, -6.9}, {107.7, -6.9}, {107.7, -6.8}, {107.6, -6.9}}}}
}

func TestNewNormalisesName(t *testing.T) {
	t.Parallel()
	r, err := region.New(region.MustParseCode("32.73"), "  Kota   Bandung ", square())
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "Kota Bandung" || r.Kind() != region.KindKota {
		t.Errorf("r = %+v kind=%s", r, r.Kind())
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()
	if _, err := region.New(region.Code{}, "X", square()); !errors.Is(err, region.ErrInvalidCode) {
		t.Errorf("kode nol: %v", err)
	}
	if _, err := region.New(region.MustParseCode("32"), " \t ", square()); !errors.Is(err, region.ErrInvalidName) {
		t.Errorf("nama kosong: %v", err)
	}
	if _, err := region.New(region.MustParseCode("32"), "Jawa Barat", nil); !errors.Is(err, region.ErrInvalidGeometry) {
		t.Errorf("batas kosong: %v", err)
	}
}
