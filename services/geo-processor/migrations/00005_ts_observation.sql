-- +goose Up
-- Pengukuran (fase 1d-2): nilai sensor stasiun kualitas udara (OpenAQ) dan
-- deteksi titik panas satelit (NASA FIRMS). Keduanya hypertable TimescaleDB
-- yang dikompres setelah 7 hari dan dihapus setelah 1 tahun, sama dengan
-- 00004. Invarian dijaga di sini sekaligus di internal/domain/series dan
-- internal/domain/hotspot; lihat ADR 0013.

-- Titik jenis stasiun: ID "openaq:<ID lokasi OpenAQ>".
ALTER TABLE ts.site
  DROP CONSTRAINT site_id_format,
  DROP CONSTRAINT site_kind_known,
  DROP CONSTRAINT site_kind_matches_id;
ALTER TABLE ts.site
  ADD CONSTRAINT site_id_format CHECK (
       id ~ '^adm4:[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'
    OR id ~ '^grid:-?[0-9]{1,2}\.[0-9]{2}:-?[0-9]{1,3}\.[0-9]{2}$'
    OR id ~ '^river:[a-z0-9]+(-[a-z0-9]+)*$'
    OR id ~ '^openaq:[1-9][0-9]{0,11}$'
  ),
  ADD CONSTRAINT site_kind_known CHECK (kind IN ('region', 'grid', 'river', 'station')),
  ADD CONSTRAINT site_kind_matches_id CHECK (
    split_part(id, ':', 1) = CASE kind WHEN 'region' THEN 'adm4' WHEN 'station' THEN 'openaq' ELSE kind END
  );

COMMENT ON TABLE ts.site IS 'Titik pantau deret waktu: kelurahan/desa (adm4:), simpul grid 0,25° (grid:), titik pantau sungai (river:), stasiun kualitas udara (openaq:)';

-- Deret pengukuran stasiun: dataset aq_observation, model "sensor", sumber
-- openaq. Pasangan sumber–jenis data kini dinyatakan lengkap.
ALTER TABLE ts.series
  DROP CONSTRAINT series_dataset_known,
  DROP CONSTRAINT series_source_known,
  DROP CONSTRAINT series_source_matches_dataset;
ALTER TABLE ts.series
  ADD CONSTRAINT series_dataset_known CHECK (dataset IN ('weather', 'air_quality', 'discharge', 'aq_observation')),
  ADD CONSTRAINT series_source_known CHECK (source IN ('bmkg', 'openmeteo', 'openaq')),
  ADD CONSTRAINT series_source_matches_dataset CHECK (
       (source = 'bmkg' AND dataset = 'weather' AND model = 'bmkg')
    OR (source = 'openmeteo' AND dataset IN ('weather', 'air_quality', 'discharge'))
    OR (source = 'openaq' AND dataset = 'aq_observation' AND model = 'sensor')
  );

-- Keterangan stasiun. Satu baris per titik jenis stasiun.
CREATE TABLE ts.station (
  site_id    text PRIMARY KEY REFERENCES ts.site (id)
             CONSTRAINT station_site_is_station CHECK (site_id ~ '^openaq:'),
  source     text NOT NULL CONSTRAINT station_source_known CHECK (source = 'openaq'),
  provider   text NOT NULL CONSTRAINT station_provider_trimmed CHECK (provider = btrim(provider) AND length(provider) BETWEEN 1 AND 100),
  owner      text NOT NULL DEFAULT '' CONSTRAINT station_owner_trimmed CHECK (owner = btrim(owner) AND length(owner) <= 100),
  locality   text NOT NULL DEFAULT '' CONSTRAINT station_locality_trimmed CHECK (locality = btrim(locality) AND length(locality) <= 100),
  -- true untuk alat referensi (monitor resmi), false untuk sensor berbiaya rendah.
  is_monitor boolean NOT NULL,
  timezone   text NOT NULL DEFAULT ''
             CONSTRAINT station_timezone_format CHECK (timezone ~ '^([A-Za-z]+(/[A-Za-z0-9_+-]+){0,2})?$' AND length(timezone) <= 64),
  updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ts.station IS 'Keterangan stasiun pengukur kualitas udara (OpenAQ): penyedia, pemilik, jenis alat';

-- Nilai sensor stasiun. Satu baris per (sensor, waktu ukur). Satuan sesuai
-- sumber (tidak dikonversi); parameter dan satuan disalin per baris supaya
-- batas nilai bisa dijaga tanpa join dan riwayat tetap benar bila sensor
-- berganti satuan.
CREATE TABLE ts.aq_observation (
  site_id     text NOT NULL REFERENCES ts.site (id),
  sensor_id   bigint NOT NULL CONSTRAINT aq_observation_sensor_positive CHECK (sensor_id > 0),
  parameter   text NOT NULL CONSTRAINT aq_observation_parameter_known CHECK (parameter IN ('pm25', 'pm10', 'no2', 'o3', 'so2', 'co')),
  unit        text NOT NULL CONSTRAINT aq_observation_unit_known CHECK (unit IN ('µg/m³', 'ppm', 'ppb')),
  observed_at timestamptz NOT NULL,
  value       real NOT NULL,
  fetched_at  timestamptz NOT NULL,

  PRIMARY KEY (sensor_id, observed_at),
  CONSTRAINT aq_observation_site_is_station CHECK (site_id ~ '^openaq:'),
  CONSTRAINT aq_observation_before_fetch CHECK (observed_at <= fetched_at + interval '1 hour'),
  -- Batas atas per (parameter, satuan): sama dengan domain. Kombinasi yang
  -- tidak terdaftar (misal partikulat dalam ppm) diberi batas -1, jadi ditolak.
  CONSTRAINT aq_observation_value_range CHECK (
    value >= 0 AND value <= COALESCE(CASE parameter || ' ' || unit
      WHEN 'pm25 µg/m³' THEN 5000
      WHEN 'pm10 µg/m³' THEN 10000
      WHEN 'no2 µg/m³' THEN 5000
      WHEN 'no2 ppm' THEN 2.7
      WHEN 'no2 ppb' THEN 2700
      WHEN 'o3 µg/m³' THEN 5000
      WHEN 'o3 ppm' THEN 2.6
      WHEN 'o3 ppb' THEN 2600
      WHEN 'so2 µg/m³' THEN 10000
      WHEN 'so2 ppm' THEN 3.9
      WHEN 'so2 ppb' THEN 3900
      WHEN 'co µg/m³' THEN 200000
      WHEN 'co ppm' THEN 175
      WHEN 'co ppb' THEN 175000
    END, -1)
  )
);

COMMENT ON TABLE ts.aq_observation IS 'Nilai sensor stasiun kualitas udara (OpenAQ), satuan sesuai sumber';

-- Deteksi titik panas. ID stabil dari ingest: produk, menit lintasan, dan
-- pusat piksel lima desimal. Kelurahan/desa dicari di ref.region saat simpan.
CREATE TABLE ts.hotspot (
  id               text NOT NULL,
  detected_at      timestamptz NOT NULL
                   CONSTRAINT hotspot_detected_minute CHECK (extract(epoch FROM detected_at) % 60 = 0),
  product          text NOT NULL
                   CONSTRAINT hotspot_product_known CHECK (product IN ('VIIRS_SNPP_NRT', 'VIIRS_NOAA20_NRT', 'VIIRS_NOAA21_NRT', 'MODIS_NRT')),
  satellite        text NOT NULL CONSTRAINT hotspot_satellite_format CHECK (satellite ~ '^[A-Za-z0-9]{1,10}$'),
  instrument       text NOT NULL CONSTRAINT hotspot_instrument_known CHECK (instrument IN ('VIIRS', 'MODIS')),
  location         geometry(Point, 4326) NOT NULL
                   CONSTRAINT hotspot_location_indonesia CHECK (ST_X(location) BETWEEN 94 AND 142 AND ST_Y(location) BETWEEN -12 AND 7),
  confidence       text NOT NULL CONSTRAINT hotspot_confidence_known CHECK (confidence IN ('low', 'nominal', 'high')),
  confidence_pct   smallint CONSTRAINT hotspot_confidence_pct_range CHECK (confidence_pct BETWEEN 0 AND 100),
  brightness_k     real NOT NULL CONSTRAINT hotspot_brightness_range CHECK (brightness_k BETWEEN 200 AND 700),
  background_k     real CONSTRAINT hotspot_background_range CHECK (background_k BETWEEN 150 AND 450),
  frp_mw           real CONSTRAINT hotspot_frp_range CHECK (frp_mw BETWEEN 0 AND 50000),
  scan_km          real NOT NULL CONSTRAINT hotspot_scan_range CHECK (scan_km BETWEEN 0.1 AND 10),
  track_km         real NOT NULL CONSTRAINT hotspot_track_range CHECK (track_km BETWEEN 0.1 AND 10),
  daytime          boolean NOT NULL,
  version          text NOT NULL CONSTRAINT hotspot_version_format CHECK (version ~ '^[A-Za-z0-9._-]{1,16}$'),
  region_code      text CONSTRAINT hotspot_region_code_format CHECK (region_code ~ '^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'),
  first_fetched_at timestamptz NOT NULL,
  archive_key      text NOT NULL DEFAULT '',

  PRIMARY KEY (id, detected_at),
  -- ID harus sama persis dengan isinya.
  CONSTRAINT hotspot_id_matches CHECK (
    id = product || ':' || to_char(detected_at AT TIME ZONE 'UTC', 'YYYYMMDD"T"HH24MI') || ':'
         || to_char(ST_Y(location), 'FM990.00000') || ':' || to_char(ST_X(location), 'FM9990.00000')
  ),
  CONSTRAINT hotspot_instrument_matches CHECK (instrument = CASE WHEN product LIKE 'VIIRS%' THEN 'VIIRS' ELSE 'MODIS' END),
  -- Persentase hanya MODIS, dan kelasnya mengikuti panduan FIRMS.
  CONSTRAINT hotspot_confidence_pct_modis CHECK ((instrument = 'MODIS') = (confidence_pct IS NOT NULL)),
  CONSTRAINT hotspot_confidence_matches_pct CHECK (
    confidence_pct IS NULL
    OR confidence = CASE WHEN confidence_pct >= 80 THEN 'high' WHEN confidence_pct >= 30 THEN 'nominal' ELSE 'low' END
  ),
  CONSTRAINT hotspot_before_fetch CHECK (detected_at <= first_fetched_at + interval '1 hour')
);

COMMENT ON TABLE ts.hotspot IS 'Deteksi titik panas satelit near real-time (NASA FIRMS VIIRS dan MODIS)';

SELECT create_hypertable('ts.aq_observation', by_range('observed_at', INTERVAL '7 days'));
SELECT create_hypertable('ts.hotspot', by_range('detected_at', INTERVAL '30 days'));

CREATE INDEX aq_observation_site_idx ON ts.aq_observation (site_id, parameter, observed_at DESC);
CREATE INDEX hotspot_location_gix ON ts.hotspot USING gist (location);
CREATE INDEX hotspot_region_idx ON ts.hotspot (region_code, detected_at DESC);

ALTER TABLE ts.aq_observation SET (
  timescaledb.enable_columnstore, timescaledb.segmentby = 'sensor_id', timescaledb.orderby = 'observed_at'
);
ALTER TABLE ts.hotspot SET (
  timescaledb.enable_columnstore, timescaledb.segmentby = 'product', timescaledb.orderby = 'detected_at, id'
);
CALL add_columnstore_policy('ts.aq_observation', after => INTERVAL '7 days');
CALL add_columnstore_policy('ts.hotspot', after => INTERVAL '7 days');
SELECT add_retention_policy('ts.aq_observation', drop_after => INTERVAL '1 year');
SELECT add_retention_policy('ts.hotspot', drop_after => INTERVAL '1 year');

-- Nilai terakhir per sensor dalam 1 hari terakhir, lengkap dengan stasiun,
-- untuk dashboard dan API.
CREATE VIEW ts.aq_observation_current AS
SELECT DISTINCT ON (o.sensor_id)
  o.site_id, s.name AS station_name, st.provider, st.is_monitor, s.region_code, s.location,
  o.sensor_id, o.parameter, o.unit, o.observed_at, o.value
FROM ts.aq_observation o
JOIN ts.site s ON s.id = o.site_id
JOIN ts.station st ON st.site_id = o.site_id
WHERE o.observed_at >= now() - interval '1 day'
ORDER BY o.sensor_id, o.observed_at DESC;

-- Titik panas 48 jam terakhir.
CREATE VIEW ts.hotspot_recent AS
SELECT *
FROM ts.hotspot
WHERE detected_at >= now() - interval '48 hours';

GRANT SELECT ON ts.station, ts.aq_observation, ts.hotspot, ts.aq_observation_current, ts.hotspot_recent
  TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP VIEW ts.hotspot_recent;
DROP VIEW ts.aq_observation_current;
DROP TABLE ts.hotspot;
DROP TABLE ts.aq_observation;
DROP TABLE ts.station;
DELETE FROM ts.series WHERE source = 'openaq';
DELETE FROM ts.site WHERE kind = 'station';
ALTER TABLE ts.series
  DROP CONSTRAINT series_dataset_known,
  DROP CONSTRAINT series_source_known,
  DROP CONSTRAINT series_source_matches_dataset;
ALTER TABLE ts.series
  ADD CONSTRAINT series_dataset_known CHECK (dataset IN ('weather', 'air_quality', 'discharge')),
  ADD CONSTRAINT series_source_known CHECK (source IN ('bmkg', 'openmeteo')),
  ADD CONSTRAINT series_source_matches_dataset CHECK (source = 'openmeteo' OR dataset = 'weather');
ALTER TABLE ts.site
  DROP CONSTRAINT site_id_format,
  DROP CONSTRAINT site_kind_known,
  DROP CONSTRAINT site_kind_matches_id;
ALTER TABLE ts.site
  ADD CONSTRAINT site_id_format CHECK (
       id ~ '^adm4:[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'
    OR id ~ '^grid:-?[0-9]{1,2}\.[0-9]{2}:-?[0-9]{1,3}\.[0-9]{2}$'
    OR id ~ '^river:[a-z0-9]+(-[a-z0-9]+)*$'
  ),
  ADD CONSTRAINT site_kind_known CHECK (kind IN ('region', 'grid', 'river')),
  ADD CONSTRAINT site_kind_matches_id CHECK (
    split_part(id, ':', 1) = CASE kind WHEN 'region' THEN 'adm4' ELSE kind END
  );
