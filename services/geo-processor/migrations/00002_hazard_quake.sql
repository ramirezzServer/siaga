-- +goose Up
-- Kejadian bahaya ternormalisasi (fase 1b: gempa). Invarian dijaga di sini
-- sekaligus di internal/domain/quake; lihat ADR 0007.

-- Satu baris per kejadian, semua jenis bahaya. Detail per jenis di tabel terpisah
-- yang dikunci ke jenisnya lewat FK (id, kind).
CREATE TABLE hazard.event (
  id              uuid PRIMARY KEY,
  kind            text NOT NULL
                  CONSTRAINT event_kind_known CHECK (kind IN ('quake', 'weather', 'flood', 'fire', 'aq')),
  status          text NOT NULL DEFAULT 'active'
                  CONSTRAINT event_status_known CHECK (status IN ('active', 'expired', 'merged', 'retracted')),
  merged_into     uuid REFERENCES hazard.event (id),
  -- 1 Info, 2 Waspada, 3 Siaga, 4 Bahaya (sama dengan siaga.hazard.v1.AlertLevel).
  level           smallint NOT NULL CONSTRAINT event_level_range CHECK (level BETWEEN 1 AND 4),
  primary_source  text NOT NULL
                  CONSTRAINT event_primary_source_known CHECK (primary_source IN ('bmkg', 'usgs', 'openmeteo', 'openaq', 'firms', 'drill')),
  source_event_id text NOT NULL CONSTRAINT event_source_event_id_format CHECK (source_event_id ~ '^[!-~]{1,128}$'),
  occurred_at     timestamptz NOT NULL,
  detected_at     timestamptz NOT NULL,
  expires_at      timestamptz NOT NULL,
  location        geometry(Point, 4326) NOT NULL
                  CONSTRAINT event_location_valid CHECK (ST_IsValid(location) AND NOT ST_IsEmpty(location)),
  -- Area terdampak (gempa: lingkaran radius dirasakan). NULL bila tidak ada.
  area            geometry(Polygon, 4326)
                  CONSTRAINT event_area_valid CHECK (area IS NULL OR (ST_IsValid(area) AND NOT ST_IsEmpty(area))),
  title           text NOT NULL CONSTRAINT event_title_nonempty CHECK (title <> ''),
  summary         text NOT NULL,
  drill           boolean NOT NULL DEFAULT false,
  -- Naik satu setiap kali isi yang diterbitkan berubah; sama dengan Hazard.revision.
  revision        integer NOT NULL CONSTRAINT event_revision_positive CHECK (revision >= 1),
  -- SHA-256 isi yang diterbitkan; pesan ulang dengan isi sama tidak menaikkan revisi.
  content_sha256  bytea NOT NULL CONSTRAINT event_content_sha256_len CHECK (length(content_sha256) = 32),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  ended_at        timestamptz,

  CONSTRAINT event_id_kind_unique UNIQUE (id, kind),
  CONSTRAINT event_merge_target CHECK ((status = 'merged') = (merged_into IS NOT NULL)),
  CONSTRAINT event_not_merged_into_self CHECK (merged_into <> id),
  CONSTRAINT event_ended_when_inactive CHECK ((status = 'active') = (ended_at IS NULL)),
  CONSTRAINT event_expires_after_occurrence CHECK (expires_at > occurred_at),
  -- Longgar: detected_at bisa berasal dari laporan sumber lain yang waktunya
  -- sedikit berbeda, dan tiap laporan boleh mendahului jam sampai 10 menit.
  CONSTRAINT event_detected_after_occurrence CHECK (detected_at >= occurred_at - interval '1 hour')
);

COMMENT ON TABLE hazard.event IS 'Kejadian bahaya ternormalisasi dan terdeduplikasi (geo-processor)';
COMMENT ON COLUMN hazard.event.status IS 'active; expired = masa aktif habis; merged = digabung ke merged_into; retracted = ditarik sumber';

CREATE INDEX event_occurred_idx     ON hazard.event (kind, occurred_at);
CREATE INDEX event_active_expiry_idx ON hazard.event (expires_at) WHERE status = 'active';
CREATE INDEX event_location_gix     ON hazard.event USING gist (location);

-- Detail gempa, 1:1 dengan hazard.event. FK (event_id, kind) menjamin detail
-- gempa hanya menempel pada kejadian berjenis gempa.
CREATE TABLE hazard.quake (
  event_id         uuid PRIMARY KEY,
  kind             text NOT NULL DEFAULT 'quake' CONSTRAINT quake_kind CHECK (kind = 'quake'),
  magnitude        double precision NOT NULL CONSTRAINT quake_magnitude_range CHECK (magnitude BETWEEN -2 AND 10),
  magnitude_type   text NOT NULL DEFAULT '',
  depth_km         double precision NOT NULL CONSTRAINT quake_depth_range CHECK (depth_km BETWEEN -10 AND 800),
  felt_radius_km   double precision NOT NULL CONSTRAINT quake_felt_radius_nonnegative CHECK (felt_radius_km >= 0),
  place            text NOT NULL DEFAULT '',
  felt_description text NOT NULL DEFAULT '',
  tsunami          text NOT NULL CONSTRAINT quake_tsunami_known CHECK (tsunami IN ('unknown', 'none', 'potential')),
  shakemap_url     text NOT NULL DEFAULT '' CONSTRAINT quake_shakemap_https CHECK (shakemap_url = '' OR shakemap_url LIKE 'https://%'),
  source_url       text NOT NULL DEFAULT '' CONSTRAINT quake_source_url_https CHECK (source_url = '' OR source_url LIKE 'https://%'),

  CONSTRAINT quake_event_fk FOREIGN KEY (event_id, kind) REFERENCES hazard.event (id, kind) ON DELETE CASCADE
);

-- Laporan terakhir per (sumber, ID sumber, feed), apa adanya dari event raw.*.
-- Pengelompokan ke kejadian lewat event_id; NULL bila sumber menarik laporannya.
CREATE TABLE hazard.event_source (
  source            text NOT NULL,
  source_event_id   text NOT NULL CONSTRAINT event_source_id_format CHECK (source_event_id ~ '^[!-~]{1,128}$'),
  feed              text NOT NULL,
  kind              text NOT NULL DEFAULT 'quake' CONSTRAINT event_source_kind CHECK (kind = 'quake'),
  event_id          uuid,
  occurred_at       timestamptz NOT NULL,
  location          geometry(Point, 4326) NOT NULL
                    CONSTRAINT event_source_location_valid CHECK (ST_IsValid(location) AND NOT ST_IsEmpty(location)),
  magnitude         double precision NOT NULL CONSTRAINT event_source_magnitude_range CHECK (magnitude BETWEEN -2 AND 10),
  magnitude_type    text NOT NULL DEFAULT '',
  depth_km          double precision NOT NULL CONSTRAINT event_source_depth_range CHECK (depth_km BETWEEN -10 AND 800),
  place             text NOT NULL DEFAULT '',
  felt_description  text NOT NULL DEFAULT '',
  tsunami           text NOT NULL CONSTRAINT event_source_tsunami_known CHECK (tsunami IN ('unknown', 'none', 'potential')),
  potential_text    text NOT NULL DEFAULT '',
  shakemap_url      text NOT NULL DEFAULT '' CONSTRAINT event_source_shakemap_https CHECK (shakemap_url = '' OR shakemap_url LIKE 'https://%'),
  source_updated_at timestamptz,
  review_status     text NOT NULL DEFAULT '',
  alternate_ids     text[] NOT NULL DEFAULT '{}'
                    CONSTRAINT event_source_alternate_ids_limit CHECK (cardinality(alternate_ids) <= 32),
  source_url        text NOT NULL DEFAULT '' CONSTRAINT event_source_url_https CHECK (source_url = '' OR source_url LIKE 'https://%'),
  fetched_at        timestamptz NOT NULL,
  first_seen_at     timestamptz NOT NULL,
  archive_key       text NOT NULL DEFAULT '',
  content_sha256    bytea NOT NULL CONSTRAINT event_source_content_sha256_len CHECK (length(content_sha256) = 32),
  updated_at        timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (source, source_event_id, feed),
  CONSTRAINT event_source_feed_known CHECK (feed IN ('bmkg-latest', 'bmkg-recent', 'bmkg-felt', 'usgs-summary')),
  CONSTRAINT event_source_feed_matches_source CHECK (split_part(feed, '-', 1) = source),
  CONSTRAINT event_source_first_seen CHECK (first_seen_at <= fetched_at),
  CONSTRAINT event_source_not_future CHECK (occurred_at <= fetched_at + interval '10 minutes'),
  -- Laporan yang ditarik sumber tidak boleh tergabung ke kejadian mana pun.
  CONSTRAINT event_source_deleted_detached CHECK (review_status <> 'deleted' OR event_id IS NULL),
  CONSTRAINT event_source_event_fk FOREIGN KEY (event_id, kind) REFERENCES hazard.event (id, kind)
);

COMMENT ON TABLE hazard.event_source IS 'Laporan terakhir per sumber dan feed; bahan deduplikasi dan detail kejadian';

CREATE INDEX event_source_event_idx    ON hazard.event_source (event_id);
CREATE INDEX event_source_occurred_idx ON hazard.event_source (kind, occurred_at);

-- Estimasi wilayah terdampak per kejadian, hanya tingkat Waspada ke atas.
CREATE TABLE hazard.impact_region (
  event_id    uuid NOT NULL REFERENCES hazard.event (id) ON DELETE CASCADE,
  region_code text NOT NULL REFERENCES ref.region (code)
              CONSTRAINT impact_region_adm4 CHECK (region_code ~ '^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{4}$'),
  distance_km double precision NOT NULL CONSTRAINT impact_region_distance_nonnegative CHECK (distance_km >= 0),
  level       smallint NOT NULL CONSTRAINT impact_region_level_range CHECK (level BETWEEN 2 AND 4),
  within_felt boolean NOT NULL,
  PRIMARY KEY (event_id, region_code)
);

COMMENT ON TABLE hazard.impact_region IS 'Estimasi kelurahan/desa terdampak (label "estimasi" di UI)';

CREATE INDEX impact_region_code_idx ON hazard.impact_region (region_code);

-- Outbox transaksional: pesan hazard.* ditulis dalam transaksi yang sama dengan
-- perubahan kejadian, lalu diterbitkan ke JetStream dan dihapus. Nats-Msg-Id
-- deterministik, jadi terbit ulang setelah crash ditolak JetStream sebagai duplikat.
CREATE TABLE hazard.outbox (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject    text NOT NULL CONSTRAINT outbox_subject_format CHECK (subject ~ '^hazard\.[a-z]+\.(created|updated|expired)$'),
  msg_id     text NOT NULL CONSTRAINT outbox_msg_id_unique UNIQUE,
  payload    bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Layanan lain membaca kejadian (core-api, alert-engine, Martin); tidak ada yang menulis.
GRANT USAGE ON SCHEMA hazard TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;
GRANT SELECT ON hazard.event, hazard.quake, hazard.event_source, hazard.impact_region
  TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP TABLE hazard.outbox;
DROP TABLE hazard.impact_region;
DROP TABLE hazard.event_source;
DROP TABLE hazard.quake;
DROP TABLE hazard.event;
REVOKE USAGE ON SCHEMA hazard FROM siaga_core, siaga_alert, siaga_ai, siaga_tiles;
