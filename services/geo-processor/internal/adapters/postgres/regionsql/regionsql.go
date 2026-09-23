// Package regionsql menyimpan SQL untuk tabel ref.region. Dipisah dari adapter pgx
// supaya pernyataan yang sama persis bisa diuji langsung dengan psql.
package regionsql

// CreateStaging membuat tabel sementara untuk COPY; hilang saat transaksi selesai.
const CreateStaging = `CREATE TEMP TABLE region_import (
  code    text NOT NULL,
  kind    text NOT NULL,
  name    text NOT NULL,
  geojson text NOT NULL
) ON COMMIT DROP`

// StagingTable dan StagingColumns dipakai oleh COPY.
const StagingTable = "region_import"

// StagingColumns adalah urutan kolom COPY.
var StagingColumns = []string{"code", "kind", "name", "geojson"}

// Upsert memindahkan isi staging ke ref.region. Geometri diperbaiki dengan ST_MakeValid
// dan dipaksa menjadi MultiPolygon. Baris yang identik tidak disentuh (updated_at tetap).
// Setiap baris yang ditulis mengembalikan inserted = true bila baru.
// $1 = nama sumber, $2 = versi sumber.
const Upsert = `INSERT INTO ref.region AS r (code, kind, name, geom, source, source_version)
SELECT code, kind, name,
       ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_SetSRID(ST_GeomFromGeoJSON(geojson), 4326)), 3)),
       $1, $2
FROM region_import
ORDER BY code
ON CONFLICT (code) DO UPDATE
SET kind = EXCLUDED.kind,
    name = EXCLUDED.name,
    geom = EXCLUDED.geom,
    source = EXCLUDED.source,
    source_version = EXCLUDED.source_version,
    updated_at = now()
WHERE (r.kind, r.name, r.source, r.source_version) IS DISTINCT FROM
      (EXCLUDED.kind, EXCLUDED.name, EXCLUDED.source, EXCLUDED.source_version)
   OR ST_AsEWKB(r.geom) IS DISTINCT FROM ST_AsEWKB(EXCLUDED.geom)
RETURNING (xmax = 0) AS inserted`

// CodesUnder mengembalikan kode provinsi $1 dan semua turunannya.
const CodesUnder = `SELECT code FROM ref.region WHERE code = $1 OR code LIKE $1 || '.%' ORDER BY code`
