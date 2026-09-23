//go:build integration

// Test integrasi: butuh PostgreSQL SIAGA yang sudah di-bootstrap dan dimigrasi.
// Jalankan: SIAGA_TEST_DATABASE_URL=postgres://siaga_geo:…@localhost:5432/siaga go test -tags integration ./...
package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres"
	"github.com/ramirezzServer/siaga/services/geo-processor/internal/domain/region"
)

func connect(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("SIAGA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SIAGA_TEST_DATABASE_URL tidak diisi")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	// Provinsi 99 tidak dipakai data nyata, jadi aman dibersihkan.
	if _, err := conn.Exec(ctx, `DELETE FROM ref.region WHERE code = '99' OR code LIKE '99.%'`); err != nil {
		t.Fatal(err)
	}
	return conn
}

func mustRegion(t *testing.T, code, name string, box [4]float64) region.Region {
	t.Helper()
	// Kotak berlawanan arah jarum jam dari [minLng, minLat, maxLng, maxLat].
	ring := region.Ring{{box[0], box[1]}, {box[2], box[1]}, {box[2], box[3]}, {box[0], box[3]}, {box[0], box[1]}}
	r, err := region.New(region.MustParseCode(code), name, region.MultiPolygon{{ring}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestUpsertAllIsIdempotentAndAtomic(t *testing.T) {
	conn := connect(t)
	ctx := context.Background()
	store := postgres.NewRegionStore(conn)
	regions := []region.Region{
		mustRegion(t, "99", "Provinsi Uji", [4]float64{100, -10, 110, 0}),
		mustRegion(t, "99.71", "Kota Uji", [4]float64{101, -9, 102, -8}),
	}

	res, err := store.UpsertAll(ctx, regions, "test", "v1")
	if err != nil || res.Inserted != 2 || res.Updated != 0 {
		t.Fatalf("insert pertama: %+v, %v", res, err)
	}
	res, err = store.UpsertAll(ctx, regions, "test", "v1")
	if err != nil || res.Unchanged != 2 {
		t.Fatalf("insert ulang harus tanpa perubahan: %+v, %v", res, err)
	}
	res, err = store.UpsertAll(ctx, regions, "test", "v2")
	if err != nil || res.Updated != 2 {
		t.Fatalf("versi baru harus memperbarui: %+v, %v", res, err)
	}

	// Anak tanpa induk melanggar FK: seluruh batch harus batal.
	orphan := append(regions[:1:1], mustRegion(t, "99.72.01", "Yatim", [4]float64{103, -9, 104, -8}))
	if _, err := store.UpsertAll(ctx, orphan, "test", "v3"); err == nil {
		t.Fatal("batch dengan anak tanpa induk harus gagal")
	}
	codes, err := store.CodesUnder(ctx, "99")
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 {
		t.Errorf("batch gagal tidak boleh meninggalkan sisa: %v", codes)
	}
	var version string
	if err := conn.QueryRow(ctx, `SELECT source_version FROM ref.region WHERE code = '99'`).Scan(&version); err != nil || version != "v2" {
		t.Errorf("versi = %q (%v); want v2 karena batch v3 batal", version, err)
	}
}
