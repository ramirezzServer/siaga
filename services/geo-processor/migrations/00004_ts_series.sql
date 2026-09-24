-- +goose Up
-- Deret waktu (fase 1d): prakiraan cuaca BMKG per kelurahan/desa dan
-- Open-Meteo per simpul grid, prakiraan kualitas udara CAMS, dan debit sungai
-- GloFAS. Tabel data adalah hypertable TimescaleDB yang dikompres setelah
-- 7 hari dan dihapus setelah 1 tahun (dokumen arsitektur, Lapisan data).
-- Invarian dijaga di sini sekaligus di internal/domain/series; lihat ADR 0012.

-- Titik pantau. Dipelajari dari event raw: ingest pemilik daftar titik
-- (adm4, grid, sungai), geo-processor menyimpannya bersama kelurahan/desa
-- tempat titik itu berada.
CREATE TABLE ts.site (
  id            text PRIMARY KEY
                CONSTRAINT site_id_format CHECK (
                     id ~ '^adm4:[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'
                  OR id ~ '^grid:-?[0-9]{1,2}\.[0-9]{2}:-?[0-9]{1,3}\.[0-9]{2}$'
                  OR id ~ '^river:[a-z0-9]+(-[a-z0-9]+)*$'
                ),
  kind          text NOT NULL CONSTRAINT site_kind_known CHECK (kind IN ('region', 'grid', 'river')),
  name          text NOT NULL DEFAULT '' CONSTRAINT site_name_trimmed CHECK (name = btrim(name) AND length(name) <= 100),
  river         text NOT NULL DEFAULT '' CONSTRAINT site_river_trimmed CHECK (river = btrim(river) AND length(river) <= 100),
  location      geometry(Point, 4326) NOT NULL
                CONSTRAINT site_location_indonesia CHECK (ST_X(location) BETWEEN 94 AND 142 AND ST_Y(location) BETWEEN -12 AND 7),
  -- Kelurahan/desa tempat titik berada. Prakiraan BMKG: kode yang diminta
  -- (belum tentu ada di ref.region). Grid dan sungai: hasil ST_Covers
  -- terhadap ref.region, NULL bila titik di laut atau di luar data wilayah.
  region_code   text CONSTRAINT site_region_code_format CHECK (region_code ~ '^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'),
  first_seen_at timestamptz NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT site_kind_matches_id CHECK (
    split_part(id, ':', 1) = CASE kind WHEN 'region' THEN 'adm4' ELSE kind END
  ),
  CONSTRAINT site_region_matches_id CHECK (kind <> 'region' OR region_code = substr(id, 6)),
  -- Titik pantau sungai selalu bernama dan menyebut sungainya; titik lain tidak.
  CONSTRAINT site_river_named CHECK ((kind = 'river') = (river <> '') AND (kind <> 'river' OR name <> ''))
);

COMMENT ON TABLE ts.site IS 'Titik pantau deret waktu: kelurahan/desa (adm4:), simpul grid 0,25° (grid:), titik pantau sungai (river:)';

CREATE INDEX site_location_gix ON ts.site USING gist (location);
CREATE INDEX site_region_idx   ON ts.site (region_code);

-- Satu deret = (titik, jenis data, model). Sel model yang dijawab sumber dan
-- kesegaran data, bahan panel "data terlambat" di dashboard.
CREATE TABLE ts.series (
  site_id          text NOT NULL REFERENCES ts.site (id),
  dataset          text NOT NULL CONSTRAINT series_dataset_known CHECK (dataset IN ('weather', 'air_quality', 'discharge')),
  model            text NOT NULL CONSTRAINT series_model_format CHECK (model ~ '^[a-z0-9_]{1,40}$'),
  source           text NOT NULL CONSTRAINT series_source_known CHECK (source IN ('bmkg', 'openmeteo')),
  cell             geometry(Point, 4326) NOT NULL
                   CONSTRAINT series_cell_indonesia CHECK (ST_X(cell) BETWEEN 94 AND 142 AND ST_Y(cell) BETWEEN -12 AND 7),
  elevation_m      double precision CONSTRAINT series_elevation_range CHECK (elevation_m BETWEEN -500 AND 6000),
  -- Waktu terbit keluaran terbaru (BMKG: waktu analisis; Open-Meteo: waktu
  -- keluaran berubah pertama kali diterima ingest).
  last_issued_at   timestamptz NOT NULL,
  last_fetched_at  timestamptz NOT NULL,
  last_archive_key text NOT NULL DEFAULT '',
  updated_at       timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (site_id, dataset, model),
  CONSTRAINT series_source_matches_dataset CHECK (source = 'openmeteo' OR dataset = 'weather'),
  CONSTRAINT series_issued_before_fetch CHECK (last_issued_at <= last_fetched_at + interval '1 hour')
);

COMMENT ON TABLE ts.series IS 'Deret waktu per (titik, jenis data, model): sel model dan kesegaran data';

-- Prakiraan cuaca. Satu baris per (titik, model, waktu terbit, waktu berlaku);
-- setiap keluaran model disimpan utuh supaya kesalahan prakiraan per
-- horizon bisa diukur (bahan koreksi bias fase 4).
CREATE TABLE ts.weather_forecast (
  site_id          text NOT NULL REFERENCES ts.site (id),
  model            text NOT NULL CONSTRAINT weather_forecast_model_format CHECK (model ~ '^[a-z0-9_]{1,40}$'),
  issued_at        timestamptz NOT NULL,
  valid_time       timestamptz NOT NULL,
  temperature_c    real CONSTRAINT weather_forecast_temperature_range CHECK (temperature_c BETWEEN -30 AND 50),
  humidity_pct     real CONSTRAINT weather_forecast_humidity_range CHECK (humidity_pct BETWEEN 0 AND 100),
  -- Akumulasi selama langkah sumber: BMKG 3 jam, Open-Meteo 1 jam.
  precipitation_mm real CONSTRAINT weather_forecast_precipitation_range CHECK (precipitation_mm BETWEEN 0 AND 500),
  -- Kode cuaca sumber: BMKG (0 cerah … 97 hujan petir) atau WMO 4677 (Open-Meteo).
  weather_code     smallint CONSTRAINT weather_forecast_code_range CHECK (weather_code BETWEEN 0 AND 999),
  cloud_cover_pct  real CONSTRAINT weather_forecast_cloud_range CHECK (cloud_cover_pct BETWEEN 0 AND 100),
  wind_speed_kmh   real CONSTRAINT weather_forecast_wind_range CHECK (wind_speed_kmh BETWEEN 0 AND 400),
  wind_from_deg    real CONSTRAINT weather_forecast_wind_dir_range CHECK (wind_from_deg BETWEEN 0 AND 360),
  wind_gust_kmh    real CONSTRAINT weather_forecast_gust_range CHECK (wind_gust_kmh BETWEEN 0 AND 500),
  pressure_hpa     real CONSTRAINT weather_forecast_pressure_range CHECK (pressure_hpa BETWEEN 500 AND 1100),
  boundary_layer_m real CONSTRAINT weather_forecast_blh_range CHECK (boundary_layer_m BETWEEN 0 AND 10000),
  visibility_m     real CONSTRAINT weather_forecast_visibility_range CHECK (visibility_m BETWEEN 0 AND 1000000),

  PRIMARY KEY (site_id, model, issued_at, valid_time),
  CONSTRAINT weather_forecast_horizon CHECK (
    valid_time BETWEEN issued_at - interval '10 days' AND issued_at + interval '20 days'
  )
);

COMMENT ON TABLE ts.weather_forecast IS 'Prakiraan cuaca per titik: BMKG (adm4, per 3 jam) dan Open-Meteo (grid, per jam)';

-- Prakiraan kualitas udara model (CAMS). Konsentrasi µg/m³ di permukaan.
CREATE TABLE ts.aq_forecast (
  site_id               text NOT NULL REFERENCES ts.site (id),
  model                 text NOT NULL CONSTRAINT aq_forecast_model_format CHECK (model ~ '^[a-z0-9_]{1,40}$'),
  issued_at             timestamptz NOT NULL,
  valid_time            timestamptz NOT NULL,
  pm2_5_ugm3            real CONSTRAINT aq_forecast_pm2_5_range CHECK (pm2_5_ugm3 BETWEEN 0 AND 5000),
  pm10_ugm3             real CONSTRAINT aq_forecast_pm10_range CHECK (pm10_ugm3 BETWEEN 0 AND 10000),
  co_ugm3               real CONSTRAINT aq_forecast_co_range CHECK (co_ugm3 BETWEEN 0 AND 200000),
  no2_ugm3              real CONSTRAINT aq_forecast_no2_range CHECK (no2_ugm3 BETWEEN 0 AND 5000),
  so2_ugm3              real CONSTRAINT aq_forecast_so2_range CHECK (so2_ugm3 BETWEEN 0 AND 10000),
  o3_ugm3               real CONSTRAINT aq_forecast_o3_range CHECK (o3_ugm3 BETWEEN 0 AND 5000),
  aerosol_optical_depth real CONSTRAINT aq_forecast_aod_range CHECK (aerosol_optical_depth BETWEEN 0 AND 20),
  dust_ugm3             real CONSTRAINT aq_forecast_dust_range CHECK (dust_ugm3 BETWEEN 0 AND 50000),

  PRIMARY KEY (site_id, model, issued_at, valid_time),
  CONSTRAINT aq_forecast_horizon CHECK (
    valid_time BETWEEN issued_at - interval '10 days' AND issued_at + interval '20 days'
  )
);

COMMENT ON TABLE ts.aq_forecast IS 'Prakiraan kualitas udara model per simpul grid (Open-Meteo CAMS global)';

-- Debit sungai harian (GloFAS). valid_date = tanggal UTC pukul 00.00.
-- Statistik ensemble hanya ada untuk hari prakiraan.
CREATE TABLE ts.river_discharge (
  site_id             text NOT NULL REFERENCES ts.site (id),
  model               text NOT NULL CONSTRAINT river_discharge_model_format CHECK (model ~ '^[a-z0-9_]{1,40}$'),
  issued_at           timestamptz NOT NULL,
  valid_date          timestamptz NOT NULL
                      CONSTRAINT river_discharge_midnight CHECK (extract(epoch FROM valid_date) % 86400 = 0),
  discharge_m3s       real CONSTRAINT river_discharge_range CHECK (discharge_m3s BETWEEN 0 AND 200000),
  ensemble_mean_m3s   real CONSTRAINT river_discharge_mean_range CHECK (ensemble_mean_m3s BETWEEN 0 AND 200000),
  ensemble_median_m3s real CONSTRAINT river_discharge_median_range CHECK (ensemble_median_m3s BETWEEN 0 AND 200000),
  ensemble_max_m3s    real CONSTRAINT river_discharge_max_range CHECK (ensemble_max_m3s BETWEEN 0 AND 200000),
  ensemble_min_m3s    real CONSTRAINT river_discharge_min_range CHECK (ensemble_min_m3s BETWEEN 0 AND 200000),
  ensemble_p25_m3s    real CONSTRAINT river_discharge_p25_range CHECK (ensemble_p25_m3s BETWEEN 0 AND 200000),
  ensemble_p75_m3s    real CONSTRAINT river_discharge_p75_range CHECK (ensemble_p75_m3s BETWEEN 0 AND 200000),

  PRIMARY KEY (site_id, model, issued_at, valid_date),
  CONSTRAINT river_discharge_horizon CHECK (
    valid_date BETWEEN issued_at - interval '31 days' AND issued_at + interval '31 days'
  ),
  -- Statistik ensemble harus berurutan; pasangan yang salah satunya NULL dilewati.
  CONSTRAINT river_discharge_ensemble_order CHECK (
        ensemble_min_m3s <= ensemble_max_m3s    IS NOT FALSE
    AND ensemble_min_m3s <= ensemble_p25_m3s    IS NOT FALSE
    AND ensemble_p25_m3s <= ensemble_median_m3s IS NOT FALSE
    AND ensemble_median_m3s <= ensemble_p75_m3s IS NOT FALSE
    AND ensemble_p75_m3s <= ensemble_max_m3s    IS NOT FALSE
    AND ensemble_min_m3s <= ensemble_mean_m3s   IS NOT FALSE
    AND ensemble_mean_m3s <= ensemble_max_m3s   IS NOT FALSE
  )
);

COMMENT ON TABLE ts.river_discharge IS 'Debit sungai harian per titik pantau (Open-Meteo GloFAS v4)';

-- Hypertable: dipartisi menurut waktu berlaku, supaya kompresi dan retensi
-- mengikuti umur data, bukan umur prakiraan.
SELECT create_hypertable('ts.weather_forecast', by_range('valid_time', INTERVAL '7 days'));
SELECT create_hypertable('ts.aq_forecast', by_range('valid_time', INTERVAL '7 days'));
SELECT create_hypertable('ts.river_discharge', by_range('valid_date', INTERVAL '90 days'));

ALTER TABLE ts.weather_forecast SET (
  timescaledb.enable_columnstore, timescaledb.segmentby = 'site_id, model', timescaledb.orderby = 'valid_time, issued_at'
);
ALTER TABLE ts.aq_forecast SET (
  timescaledb.enable_columnstore, timescaledb.segmentby = 'site_id, model', timescaledb.orderby = 'valid_time, issued_at'
);
ALTER TABLE ts.river_discharge SET (
  timescaledb.enable_columnstore, timescaledb.segmentby = 'site_id, model', timescaledb.orderby = 'valid_date, issued_at'
);

CALL add_columnstore_policy('ts.weather_forecast', after => INTERVAL '7 days');
CALL add_columnstore_policy('ts.aq_forecast', after => INTERVAL '7 days');
CALL add_columnstore_policy('ts.river_discharge', after => INTERVAL '7 days');
SELECT add_retention_policy('ts.weather_forecast', drop_after => INTERVAL '1 year');
SELECT add_retention_policy('ts.aq_forecast', drop_after => INTERVAL '1 year');
SELECT add_retention_policy('ts.river_discharge', drop_after => INTERVAL '1 year');

-- Keluaran terbaru per (titik, model, waktu berlaku), untuk dashboard dan
-- API: hanya jendela sekitar sekarang supaya tidak memindai setahun data.
CREATE VIEW ts.weather_forecast_current AS
SELECT DISTINCT ON (site_id, model, valid_time) *
FROM ts.weather_forecast
WHERE valid_time >= now() - interval '1 day'
ORDER BY site_id, model, valid_time, issued_at DESC;

CREATE VIEW ts.aq_forecast_current AS
SELECT DISTINCT ON (site_id, model, valid_time) *
FROM ts.aq_forecast
WHERE valid_time >= now() - interval '1 day'
ORDER BY site_id, model, valid_time, issued_at DESC;

CREATE VIEW ts.river_discharge_current AS
SELECT DISTINCT ON (site_id, model, valid_date) *
FROM ts.river_discharge
WHERE valid_date >= now() - interval '7 days'
ORDER BY site_id, model, valid_date, issued_at DESC;

GRANT USAGE ON SCHEMA ts TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;
GRANT SELECT ON ts.site, ts.series, ts.weather_forecast, ts.aq_forecast, ts.river_discharge,
  ts.weather_forecast_current, ts.aq_forecast_current, ts.river_discharge_current
  TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP VIEW ts.river_discharge_current;
DROP VIEW ts.aq_forecast_current;
DROP VIEW ts.weather_forecast_current;
DROP TABLE ts.river_discharge;
DROP TABLE ts.aq_forecast;
DROP TABLE ts.weather_forecast;
DROP TABLE ts.series;
DROP TABLE ts.site;
