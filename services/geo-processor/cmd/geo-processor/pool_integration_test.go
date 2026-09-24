//go:build integration

package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

func mustPool(ctx context.Context, t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// seedRegions menyiapkan provinsi uji 98 dengan satu desa di sekitar episenter
// gempa uji, supaya uji ujung ke ujung tidak bergantung pada import Jawa Barat.
func seedRegions(ctx context.Context, t *testing.T, url string) {
	t.Helper()
	p := mustPool(ctx, t, url)
	defer p.Close()
	conn, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, q := range []string{
		`DELETE FROM hazard.impact_region WHERE region_code LIKE '98.%'`,
		`DELETE FROM ref.region WHERE code = '98' OR code LIKE '98.%'`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	box := func(code, name string, b [4]float64) region.Region {
		ring := region.Ring{{b[0], b[1]}, {b[2], b[1]}, {b[2], b[3]}, {b[0], b[3]}, {b[0], b[1]}}
		r, err := region.New(region.MustParseCode(code), name, region.MultiPolygon{{ring}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	all := [4]float64{106.5, -7.5, 107.5, -6.5}
	regions := []region.Region{
		box("98", "Provinsi Uji E2E", all),
		box("98.01", "Kabupaten Uji E2E", all),
		box("98.01.01", "Kecamatan Uji E2E", all),
		box("98.01.01.2001", "Desa Episenter", [4]float64{107.02, -6.86, 107.04, -6.84}),
	}
	if _, err := postgres.NewRegionStore(conn.Conn()).UpsertAll(ctx, regions, "test", "e2e"); err != nil {
		t.Fatal(err)
	}
}
