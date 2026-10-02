-- +goose Up
-- Potensi banjir (fase 1e-3b-2b): ambang debit per titik pantau sungai dan
-- ambang indeks hujan per sub-DAS di schema ref, hujan sub-DAS sebagai titik
-- deret waktu jenis catchment, dan kejadian hazard.flood.* beserta
-- penilaian per hari. Invarian dijaga di sini sekaligus di
-- internal/domain/flood dan internal/domain/series; lihat ADR 0020–0021.

-- Titik sub-DAS: ID "catchment:<slug>", wajib bernama, tanpa sungai. Lokasinya
-- titik berat sel model yang dirata-rata.
ALTER TABLE ts.site
  DROP CONSTRAINT site_id_format,
  DROP CONSTRAINT site_kind_known,
  DROP CONSTRAINT site_river_named;
ALTER TABLE ts.site
  ADD CONSTRAINT site_id_format CHECK (
       id ~ '^adm4:[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'
    OR id ~ '^grid:-?[0-9]{1,2}\.[0-9]{2}:-?[0-9]{1,3}\.[0-9]{2}$'
    OR id ~ '^river:[a-z0-9]+(-[a-z0-9]+)*$'
    OR id ~ '^openaq:[1-9][0-9]{0,11}$'
    OR id ~ '^catchment:[a-z0-9]+(-[a-z0-9]+)*$'
  ),
  ADD CONSTRAINT site_kind_known CHECK (kind IN ('region', 'grid', 'river', 'station', 'catchment')),
  ADD CONSTRAINT site_river_named CHECK (
    (kind = 'river') = (river <> '') AND (kind NOT IN ('river', 'catchment') OR name <> '')
  );

COMMENT ON TABLE ts.site IS 'Titik pantau deret waktu: kelurahan/desa (adm4:), simpul grid 0,25° (grid:), titik pantau sungai (river:), stasiun kualitas udara (openaq:), sub-DAS (catchment:)';

-- Ambang debit per titik pantau sungai: persentil debit harian reanalisis
-- GloFAS di sel titik (make flood-threshold), disalin dari data yang
-- disematkan ke geo-processor setiap start. Ambang efektif = persentil ×
-- correction (koreksi bias data prakiraan, ADR 0021).
CREATE TABLE ref.discharge_threshold (
  site_id        text PRIMARY KEY CONSTRAINT discharge_threshold_site_format CHECK (site_id ~ '^river:[a-z0-9]+(-[a-z0-9]+)*$'),
  river          text NOT NULL CONSTRAINT discharge_threshold_river_trimmed CHECK (river = btrim(river) AND length(river) BETWEEN 1 AND 100),
  name           text NOT NULL CONSTRAINT discharge_threshold_name_trimmed CHECK (name = btrim(name) AND length(name) BETWEEN 1 AND 100),
  cell           geometry(Point, 4326) NOT NULL
                 CONSTRAINT discharge_threshold_cell_indonesia CHECK (ST_X(cell) BETWEEN 94 AND 142 AND ST_Y(cell) BETWEEN -12 AND 7),
  p50            double precision NOT NULL,
  p80            double precision NOT NULL,
  p90            double precision NOT NULL,
  p98            double precision NOT NULL,
  p99_5          double precision NOT NULL,
  seamless_ratio double precision NOT NULL CONSTRAINT discharge_threshold_ratio_range CHECK (seamless_ratio > 0 AND seamless_ratio < 10),
  correction     double precision NOT NULL CONSTRAINT discharge_threshold_correction_range CHECK (correction > 0 AND correction <= 1),
  info_m3s       double precision GENERATED ALWAYS AS (p80 * correction) STORED,
  waspada_m3s    double precision GENERATED ALWAYS AS (p90 * correction) STORED,
  siaga_m3s      double precision GENERATED ALWAYS AS (p98 * correction) STORED,
  bahaya_m3s     double precision GENERATED ALWAYS AS (p99_5 * correction) STORED,
  -- 1 Info … 4 Bahaya; titik di hilir waduk dibatasi Siaga.
  max_level      smallint NOT NULL CONSTRAINT discharge_threshold_max_level_range CHECK (max_level BETWEEN 1 AND 4),
  period_from    date NOT NULL,
  period_to      date NOT NULL,
  source_sha256  bytea NOT NULL CONSTRAINT discharge_threshold_sha256_len CHECK (length(source_sha256) = 32),
  updated_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT discharge_threshold_order CHECK (
    p50 >= 0 AND p50 <= p80 AND p80 > 0 AND p80 < p90 AND p90 < p98 AND p98 < p99_5 AND p99_5 < 200000
  ),
  CONSTRAINT discharge_threshold_period CHECK (period_from < period_to)
);

COMMENT ON TABLE ref.discharge_threshold IS 'Ambang debit banjir per titik pantau sungai (persentil reanalisis GloFAS, ADR 0020–0021)';
COMMENT ON COLUMN ref.discharge_threshold.correction IS 'Faktor koreksi bias data prakiraan; ambang efektif = persentil × correction';

-- Ambang indeks hujan per sub-DAS dan jendela akumulasi: persentil puncak
-- harian akumulasi hujan rata-rata wilayah ECMWF IFS (make rain-threshold).
CREATE TABLE ref.rainfall_threshold (
  site_id       text NOT NULL CONSTRAINT rainfall_threshold_site_format CHECK (site_id ~ '^catchment:[a-z0-9]+(-[a-z0-9]+)*$'),
  window_hours  smallint NOT NULL CONSTRAINT rainfall_threshold_window_known CHECK (window_hours IN (3, 6, 24)),
  name          text NOT NULL CONSTRAINT rainfall_threshold_name_trimmed CHECK (name = btrim(name) AND length(name) BETWEEN 1 AND 100),
  p50           double precision NOT NULL,
  p80           double precision NOT NULL,
  p90           double precision NOT NULL,
  p98           double precision NOT NULL,
  p99_5         double precision NOT NULL,
  -- Indeks hujan paling tinggi Siaga (ADR 0020 butir 9).
  max_level     smallint NOT NULL CONSTRAINT rainfall_threshold_max_level_range CHECK (max_level BETWEEN 1 AND 3),
  period_from   date NOT NULL,
  period_to     date NOT NULL,
  source_sha256 bytea NOT NULL CONSTRAINT rainfall_threshold_sha256_len CHECK (length(source_sha256) = 32),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (site_id, window_hours),
  CONSTRAINT rainfall_threshold_order CHECK (
    p50 >= 0 AND p50 <= p80 AND p80 > 0 AND p80 < p90 AND p90 < p98 AND p98 < p99_5 AND p99_5 < 1000
  ),
  CONSTRAINT rainfall_threshold_period CHECK (period_from < period_to)
);

COMMENT ON TABLE ref.rainfall_threshold IS 'Ambang indeks hujan sub-DAS per jendela akumulasi (persentil ECMWF IFS, ADR 0020–0021)';

-- Detail banjir, 1:1 dengan hazard.event: penilaian keluaran model terbaru.
CREATE TABLE hazard.flood (
  event_id         uuid PRIMARY KEY,
  kind             text NOT NULL DEFAULT 'flood' CONSTRAINT flood_kind CHECK (kind = 'flood'),
  indicator        text NOT NULL CONSTRAINT flood_indicator_known CHECK (indicator IN ('discharge', 'rainfall')),
  site_id          text NOT NULL CONSTRAINT flood_site_format CHECK (site_id ~ '^(river|catchment):[a-z0-9]+(-[a-z0-9]+)*$'),
  site_name        text NOT NULL CONSTRAINT flood_site_name_trimmed CHECK (site_name = btrim(site_name) AND length(site_name) BETWEEN 1 AND 100),
  river            text NOT NULL DEFAULT '' CONSTRAINT flood_river_trimmed CHECK (river = btrim(river) AND length(river) <= 100),
  model            text NOT NULL CONSTRAINT flood_model_format CHECK (model ~ '^[a-z0-9_]{1,40}$'),
  -- Waktu terima keluaran yang dinilai.
  issued_at        timestamptz NOT NULL,
  -- Tingkat keluaran terbaru (0 = di bawah Info); tingkat kejadian di
  -- hazard.event paling rendah Info selama aktif.
  outlook_level    smallint NOT NULL CONSTRAINT flood_outlook_level_range CHECK (outlook_level BETWEEN 0 AND 4),
  possible         boolean NOT NULL,
  peak_date        timestamptz CONSTRAINT flood_peak_midnight CHECK (extract(epoch FROM peak_date) % 86400 = 0),
  max_level        smallint NOT NULL CONSTRAINT flood_max_level_range CHECK (max_level BETWEEN 1 AND 4),
  opened_at        timestamptz NOT NULL,
  last_exceeded_at timestamptz NOT NULL,
  -- Ambang efektif Info..Bahaya: 4 nilai (debit, m³/s) atau 12 nilai (indeks
  -- hujan, mm; jendela 3, 6, 24 jam berurutan).
  thresholds       double precision[] NOT NULL,

  CONSTRAINT flood_indicator_matches_site CHECK ((indicator = 'discharge') = (site_id LIKE 'river:%')),
  CONSTRAINT flood_river_only_discharge CHECK ((indicator = 'discharge') = (river <> '')),
  CONSTRAINT flood_rainfall_capped CHECK (indicator = 'discharge' OR max_level <= 3),
  CONSTRAINT flood_outlook_capped CHECK (outlook_level <= max_level),
  CONSTRAINT flood_peak_when_exceeded CHECK ((outlook_level > 0) = (peak_date IS NOT NULL)),
  CONSTRAINT flood_possible_needs_level CHECK (NOT possible OR outlook_level > 0),
  CONSTRAINT flood_exceeded_order CHECK (last_exceeded_at >= opened_at AND issued_at >= opened_at),
  CONSTRAINT flood_thresholds_shape CHECK (
    array_ndims(thresholds) = 1 AND cardinality(thresholds) = CASE indicator WHEN 'discharge' THEN 4 ELSE 12 END
  ),
  CONSTRAINT flood_event_fk FOREIGN KEY (event_id, kind) REFERENCES hazard.event (id, kind) ON DELETE CASCADE
);

COMMENT ON TABLE hazard.flood IS 'Detail potensi banjir per kejadian (debit model GloFAS atau indeks hujan sub-DAS)';

CREATE INDEX flood_site_idx ON hazard.flood (site_id);

-- Penilaian per hari (hari ini sampai +3, tanggal UTC) dari keluaran terbaru.
CREATE TABLE hazard.flood_day (
  event_id          uuid NOT NULL REFERENCES hazard.flood (event_id) ON DELETE CASCADE,
  date              timestamptz NOT NULL CONSTRAINT flood_day_midnight CHECK (extract(epoch FROM date) % 86400 = 0),
  level             smallint NOT NULL CONSTRAINT flood_day_level_range CHECK (level BETWEEN 0 AND 4),
  possible          boolean NOT NULL,
  discharge_m3s     double precision CONSTRAINT flood_day_discharge_range CHECK (discharge_m3s BETWEEN 0 AND 200000),
  discharge_p75_m3s double precision CONSTRAINT flood_day_p75_range CHECK (discharge_p75_m3s BETWEEN 0 AND 200000),
  discharge_max_m3s double precision CONSTRAINT flood_day_max_range CHECK (discharge_max_m3s BETWEEN 0 AND 200000),
  from_ensemble     boolean NOT NULL,
  rain_3h_mm        double precision CONSTRAINT flood_day_rain_3h_range CHECK (rain_3h_mm BETWEEN 0 AND 1000),
  rain_6h_mm        double precision CONSTRAINT flood_day_rain_6h_range CHECK (rain_6h_mm BETWEEN 0 AND 1000),
  rain_24h_mm       double precision CONSTRAINT flood_day_rain_24h_range CHECK (rain_24h_mm BETWEEN 0 AND 1000),
  -- Jendela indeks hujan penentu tingkat; 0 untuk debit atau di bawah Info.
  window_hours      smallint NOT NULL CONSTRAINT flood_day_window_known CHECK (window_hours IN (0, 3, 6, 24)),

  PRIMARY KEY (event_id, date),
  CONSTRAINT flood_day_possible_needs_level CHECK (NOT possible OR level > 0),
  CONSTRAINT flood_day_ensemble_has_value CHECK (NOT from_ensemble OR discharge_m3s IS NOT NULL),
  CONSTRAINT flood_day_window_when_level CHECK (window_hours = 0 OR level > 0)
);

COMMENT ON TABLE hazard.flood_day IS 'Penilaian potensi banjir per tanggal UTC dari keluaran model terbaru';

-- Keadaan per titik: keluaran terakhir yang dinilai (keluaran yang lebih tua
-- atau ulangan diabaikan) dan kejadian terakhir titik itu.
CREATE TABLE hazard.flood_site (
  site_id         text PRIMARY KEY CONSTRAINT flood_site_site_format CHECK (site_id ~ '^(river|catchment):[a-z0-9]+(-[a-z0-9]+)*$'),
  last_fetched_at timestamptz NOT NULL,
  event_id        uuid REFERENCES hazard.flood (event_id),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE hazard.flood_site IS 'Keluaran model terakhir yang dinilai per titik pantau banjir';

GRANT SELECT ON ref.discharge_threshold, ref.rainfall_threshold TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;
GRANT SELECT ON hazard.flood, hazard.flood_day, hazard.flood_site TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP TABLE hazard.flood_site;
DROP TABLE hazard.flood_day;
DROP TABLE hazard.flood;
DELETE FROM hazard.impact_region WHERE event_id IN (SELECT id FROM hazard.event WHERE kind = 'flood');
DELETE FROM hazard.event WHERE kind = 'flood';
DROP TABLE ref.rainfall_threshold;
DROP TABLE ref.discharge_threshold;
DELETE FROM ts.weather_forecast WHERE site_id LIKE 'catchment:%';
DELETE FROM ts.series WHERE site_id LIKE 'catchment:%';
DELETE FROM ts.site WHERE kind = 'catchment';
ALTER TABLE ts.site
  DROP CONSTRAINT site_id_format,
  DROP CONSTRAINT site_kind_known,
  DROP CONSTRAINT site_river_named;
ALTER TABLE ts.site
  ADD CONSTRAINT site_id_format CHECK (
       id ~ '^adm4:[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'
    OR id ~ '^grid:-?[0-9]{1,2}\.[0-9]{2}:-?[0-9]{1,3}\.[0-9]{2}$'
    OR id ~ '^river:[a-z0-9]+(-[a-z0-9]+)*$'
    OR id ~ '^openaq:[1-9][0-9]{0,11}$'
  ),
  ADD CONSTRAINT site_kind_known CHECK (kind IN ('region', 'grid', 'river', 'station')),
  ADD CONSTRAINT site_river_named CHECK ((kind = 'river') = (river <> '') AND (kind <> 'river' OR name <> ''));
COMMENT ON TABLE ts.site IS 'Titik pantau deret waktu: kelurahan/desa (adm4:), simpul grid 0,25° (grid:), titik pantau sungai (river:), stasiun kualitas udara (openaq:)';
