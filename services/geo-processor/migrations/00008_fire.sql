-- +goose Up
-- Kejadian titik api (fase 1e-3c-1b): saringan sumber panas tetap di schema
-- ref, kejadian hazard.fire.* dan deteksi anggotanya. Invarian dijaga di sini
-- sekaligus di internal/domain/fire; lihat ADR 0023.

-- Sumber panas tetap (industri, suar gas, PLTU, anjungan lepas pantai) dari
-- arsip FIRMS (make fire-threshold), disalin dari data yang disematkan ke
-- geo-processor setiap start. Deteksi di dalam jari-jarinya tidak menjadi
-- kejadian.
CREATE TABLE ref.fire_static_source (
  id            text PRIMARY KEY
                CONSTRAINT fire_static_source_id_format CHECK (id ~ '^-?[0-9]{1,2}\.[0-9]{5},-?[0-9]{1,3}\.[0-9]{5}$'),
  location      geometry(Point, 4326) NOT NULL
                CONSTRAINT fire_static_source_location_indonesia CHECK (ST_X(location) BETWEEN 94 AND 142 AND ST_Y(location) BETWEEN -12 AND 7),
  radius_km     double precision NOT NULL CONSTRAINT fire_static_source_radius_range CHECK (radius_km > 0 AND radius_km <= 20),
  reason        text NOT NULL CONSTRAINT fire_static_source_reason_known CHECK (reason IN ('persisten', 'type-firms', 'keduanya')),
  detections    integer NOT NULL CONSTRAINT fire_static_source_detections_positive CHECK (detections > 0),
  first_seen    date NOT NULL,
  last_seen     date NOT NULL,
  period_from   date NOT NULL,
  period_to     date NOT NULL,
  source_sha256 bytea NOT NULL CONSTRAINT fire_static_source_sha256_len CHECK (length(source_sha256) = 32),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  -- ID adalah pusat lima desimal.
  CONSTRAINT fire_static_source_id_matches CHECK (
    id = to_char(ST_Y(location), 'FM990.00000') || ',' || to_char(ST_X(location), 'FM9990.00000')
  ),
  CONSTRAINT fire_static_source_seen_order CHECK (first_seen <= last_seen),
  CONSTRAINT fire_static_source_period CHECK (period_from < period_to)
);

COMMENT ON TABLE ref.fire_static_source IS 'Sumber panas tetap yang deteksinya tidak menjadi kejadian titik api (arsip FIRMS, ADR 0023)';

CREATE INDEX fire_static_source_location_gix ON ref.fire_static_source USING gist (location);

-- Detail titik api, 1:1 dengan hazard.event.
CREATE TABLE hazard.fire (
  event_id          uuid PRIMARY KEY,
  kind              text NOT NULL DEFAULT 'fire' CONSTRAINT fire_kind CHECK (kind = 'fire'),
  -- ID deteksi yang membuka kejadian (dasar ID kejadian).
  opened_by         text NOT NULL CONSTRAINT fire_opened_by_format CHECK (opened_by ~ '^[A-Z0-9_]+:[0-9]{8}T[0-9]{4}:-?[0-9.]+:-?[0-9.]+$'),
  detection_count   integer NOT NULL CONSTRAINT fire_detection_count_positive CHECK (detection_count > 0),
  overpass_count    integer NOT NULL,
  high_count        integer NOT NULL,
  peak_detections   integer NOT NULL,
  peak_overpasses   integer NOT NULL,
  first_detected_at timestamptz NOT NULL,
  last_detected_at  timestamptz NOT NULL,
  level_reached_at  timestamptz NOT NULL,
  first_fetched_at  timestamptz NOT NULL,
  last_fetched_at   timestamptz NOT NULL,
  products          text[] NOT NULL,
  max_frp_mw        double precision CONSTRAINT fire_max_frp_range CHECK (max_frp_mw BETWEEN 0 AND 50000),
  siaga_detections  smallint NOT NULL CONSTRAINT fire_siaga_detections_positive CHECK (siaga_detections > 0),
  waspada_overpasses smallint NOT NULL CONSTRAINT fire_waspada_overpasses_range CHECK (waspada_overpasses >= 0),

  CONSTRAINT fire_counts CHECK (
    overpass_count BETWEEN 1 AND detection_count AND high_count BETWEEN 0 AND detection_count
    AND peak_detections BETWEEN 1 AND detection_count AND peak_overpasses BETWEEN 1 AND LEAST(peak_detections, overpass_count)
  ),
  CONSTRAINT fire_time_order CHECK (
    first_detected_at <= last_detected_at AND level_reached_at BETWEEN first_detected_at AND last_detected_at
    AND first_fetched_at <= last_fetched_at AND first_detected_at <= first_fetched_at + interval '1 hour'
  ),
  CONSTRAINT fire_products_known CHECK (
    cardinality(products) BETWEEN 1 AND 4
    AND products <@ ARRAY['VIIRS_SNPP_NRT', 'VIIRS_NOAA20_NRT', 'VIIRS_NOAA21_NRT', 'MODIS_NRT']::text[]
  ),
  CONSTRAINT fire_event_fk FOREIGN KEY (event_id, kind) REFERENCES hazard.event (id, kind) ON DELETE CASCADE
);

COMMENT ON TABLE hazard.fire IS 'Detail kejadian titik api dari deteksi satelit NASA FIRMS (ADR 0023)';

-- Deteksi anggota kejadian. Satu deteksi hanya milik satu kejadian;
-- penggabungan memindahkan anggota ke kejadian yang bertahan.
CREATE TABLE hazard.fire_detection (
  detection_id text PRIMARY KEY,
  event_id     uuid NOT NULL REFERENCES hazard.fire (event_id) ON DELETE CASCADE,
  product      text NOT NULL
               CONSTRAINT fire_detection_product_known CHECK (product IN ('VIIRS_SNPP_NRT', 'VIIRS_NOAA20_NRT', 'VIIRS_NOAA21_NRT', 'MODIS_NRT')),
  location     geometry(Point, 4326) NOT NULL
               CONSTRAINT fire_detection_location_indonesia CHECK (ST_X(location) BETWEEN 94 AND 142 AND ST_Y(location) BETWEEN -12 AND 7),
  detected_at  timestamptz NOT NULL CONSTRAINT fire_detection_minute CHECK (extract(epoch FROM detected_at) % 60 = 0),
  confidence   text NOT NULL CONSTRAINT fire_detection_confidence_known CHECK (confidence IN ('low', 'nominal', 'high')),
  frp_mw       double precision CONSTRAINT fire_detection_frp_range CHECK (frp_mw BETWEEN 0 AND 50000),
  fetched_at   timestamptz NOT NULL,

  -- ID harus sama persis dengan isinya (sama dengan ts.hotspot).
  CONSTRAINT fire_detection_id_matches CHECK (
    detection_id = product || ':' || to_char(detected_at AT TIME ZONE 'UTC', 'YYYYMMDD"T"HH24MI') || ':'
                   || to_char(ST_Y(location), 'FM990.00000') || ':' || to_char(ST_X(location), 'FM9990.00000')
  ),
  CONSTRAINT fire_detection_before_fetch CHECK (detected_at <= fetched_at + interval '1 hour')
);

COMMENT ON TABLE hazard.fire_detection IS 'Deteksi titik panas anggota kejadian titik api';

CREATE INDEX fire_detection_event_idx ON hazard.fire_detection (event_id);
CREATE INDEX fire_detection_location_gix ON hazard.fire_detection USING gist (location);
CREATE INDEX fire_detection_time_idx ON hazard.fire_detection (detected_at);

GRANT SELECT ON ref.fire_static_source TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;
GRANT SELECT ON hazard.fire, hazard.fire_detection TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP TABLE hazard.fire_detection;
DROP TABLE hazard.fire;
DELETE FROM hazard.impact_region WHERE event_id IN (SELECT id FROM hazard.event WHERE kind = 'fire');
DELETE FROM hazard.event WHERE kind = 'fire';
DROP TABLE ref.fire_static_source;
