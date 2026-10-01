package reanalysiscache

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ramirezzServer/siaga/services/ingest/internal/app/reanalysis"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/series"
)

func TestSaveLoad(t *testing.T) {
	src := reanalysis.Source{
		Endpoint: "https://flood.test/v1/flood", Model: "consolidated_v4", Variable: "river_discharge", Unit: "m³/s",
		Resolution: reanalysis.Daily, From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2000, 1, 3, 0, 0, 0, 0, time.UTC),
	}
	path := Path(filepath.Join(t.TempDir(), "baru"), src)
	if filepath.Base(path) != "consolidated_v4-river_discharge-2000-01-01-2000-01-03.json" {
		t.Fatal(path)
	}
	c, err := Load(path, src)
	if err != nil || c.Len() != 0 {
		t.Fatalf("file belum ada harus jadi cache kosong: %v", err)
	}
	p := series.LatLon{Lat: -6.125, Lon: 106.975}
	c.Put(p, []float64{1.234, math.NaN(), 3})
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file sementara tertinggal")
	}
	got, err := Load(path, src)
	if err != nil {
		t.Fatal(err)
	}
	vs, ok := got.Get(p)
	if !ok || vs[0] != 1.23 || !math.IsNaN(vs[1]) || vs[2] != 3 {
		t.Fatalf("%v", vs)
	}
	other := src
	other.Model = "seamless_v4"
	if _, err := Load(path, other); !errors.Is(err, reanalysis.ErrCacheSource) {
		t.Fatal(err)
	}
	if _, err := Load(t.TempDir(), src); err == nil {
		t.Fatal("folder bukan file cache")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(filepath.Join(blocker, "x.json"), c); err == nil {
		t.Fatal("folder tidak bisa dibuat")
	}
}
