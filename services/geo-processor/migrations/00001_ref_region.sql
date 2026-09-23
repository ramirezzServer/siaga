-- +goose Up
-- Batas wilayah administrasi (provinsi s.d. desa/kelurahan) dengan kode Kemendagri.
-- Invarian kode, level, dan jenis dijaga di database sekaligus di kode domain Go.
CREATE TABLE ref.region (
  code           text PRIMARY KEY
                 CONSTRAINT region_code_format CHECK (code ~ '^[0-9]{2}(\.[0-9]{2}(\.[0-9]{2}(\.[0-9]{4})?)?)?$'),
  level          smallint GENERATED ALWAYS AS (array_length(string_to_array(code, '.'), 1)) STORED,
  parent_code    text GENERATED ALWAYS AS (
                   CASE WHEN strpos(code, '.') > 0 THEN regexp_replace(code, '\.[0-9]+$', '') END
                 ) STORED
                 REFERENCES ref.region (code) DEFERRABLE INITIALLY DEFERRED,
  kind           text NOT NULL
                 CONSTRAINT region_kind_known CHECK (kind IN ('provinsi', 'kabupaten', 'kota', 'kecamatan', 'kelurahan', 'desa')),
  name           text NOT NULL
                 CONSTRAINT region_name_trimmed CHECK (name <> '' AND name = btrim(name)),
  geom           geometry(MultiPolygon, 4326) NOT NULL
                 CONSTRAINT region_geom_valid CHECK (ST_IsValid(geom) AND NOT ST_IsEmpty(geom)),
  label_point    geometry(Point, 4326) GENERATED ALWAYS AS (ST_PointOnSurface(geom)) STORED,
  source         text NOT NULL,
  source_version text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT region_kind_matches_level CHECK (
       (level = 1 AND kind = 'provinsi')
    OR (level = 2 AND kind = CASE WHEN split_part(code, '.', 2)::int >= 71 THEN 'kota' ELSE 'kabupaten' END)
    OR (level = 3 AND kind = 'kecamatan')
    OR (level = 4 AND kind = CASE left(split_part(code, '.', 4), 1) WHEN '1' THEN 'kelurahan' WHEN '2' THEN 'desa' END)
  )
);

COMMENT ON TABLE ref.region IS 'Batas administrasi Kemendagri; sumber: cahyadsn/wilayah_boundaries (MIT)';
COMMENT ON COLUMN ref.region.label_point IS 'Titik yang dijamin berada di dalam poligon, untuk label peta';

CREATE INDEX region_geom_gix        ON ref.region USING gist (geom);
CREATE INDEX region_label_point_gix ON ref.region USING gist (label_point);
CREATE INDEX region_parent_idx      ON ref.region (parent_code);
CREATE INDEX region_level_idx       ON ref.region (level);
CREATE INDEX region_name_trgm_idx   ON ref.region USING gin (name gin_trgm_ops);

-- Layanan lain hanya membaca data referensi.
GRANT USAGE ON SCHEMA ref TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;
GRANT SELECT ON ref.region TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP TABLE ref.region;
