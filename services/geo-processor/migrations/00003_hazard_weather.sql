-- +goose Up
-- Peringatan dini cuaca CAP (fase 1c). Invarian dijaga di sini sekaligus di
-- internal/domain/weather; lihat ADR 0010.

-- Area kejadian bisa terdiri dari banyak poligon: BMKG menggambar satu
-- poligon per kecamatan. Area gempa (lingkaran) menjadi MultiPolygon berisi satu.
ALTER TABLE hazard.event DROP CONSTRAINT event_area_valid;
ALTER TABLE hazard.event ALTER COLUMN area TYPE geometry(MultiPolygon, 4326) USING ST_Multi(area);
ALTER TABLE hazard.event ADD CONSTRAINT event_area_valid
  CHECK (area IS NULL OR (ST_IsValid(area) AND NOT ST_IsEmpty(area)));
-- Peringatan cuaca bisa diterbitkan jauh sebelum mulai berlaku, jadi batas
-- waktu deteksi terhadap waktu kejadian hanya berlaku untuk gempa.
ALTER TABLE hazard.event DROP CONSTRAINT event_detected_after_occurrence;
ALTER TABLE hazard.event ADD CONSTRAINT event_detected_after_occurrence
  CHECK (kind <> 'quake' OR detected_at >= occurred_at - interval '1 hour');
-- Kejadian cuaca selalu punya area: lokasi pengguna dicocokkan dengan poligonnya.
ALTER TABLE hazard.event ADD CONSTRAINT event_weather_has_area CHECK (kind <> 'weather' OR area IS NOT NULL);
CREATE INDEX event_area_gix ON hazard.event USING gist (area);

-- Wilayah terdampak cuaca dihitung dari irisan poligon, bukan jarak.
ALTER TABLE hazard.impact_region ADD COLUMN coverage double precision
  CONSTRAINT impact_region_coverage_range CHECK (coverage IS NULL OR (coverage > 0 AND coverage <= 1));
COMMENT ON COLUMN hazard.impact_region.coverage IS 'Cuaca: bagian luas kelurahan/desa di dalam area peringatan (0..1]. Gempa: NULL';
COMMENT ON COLUMN hazard.impact_region.within_felt IS 'Gempa: di dalam radius dirasakan. Cuaca: selalu true (beririsan dengan area)';

-- Pesan CAP terakhir per (penerbit, identifier), apa adanya dari raw.weather.*.
-- Rantai pembaruan (references) disusun menjadi kejadian lewat event_id.
CREATE TABLE hazard.cap_message (
  source         text NOT NULL CONSTRAINT cap_message_source_known CHECK (source IN ('bmkg')),
  identifier     text NOT NULL CONSTRAINT cap_message_identifier_format CHECK (identifier ~ '^[!-~]{1,128}$'),
  kind           text NOT NULL DEFAULT 'weather' CONSTRAINT cap_message_kind CHECK (kind = 'weather'),
  event_id       uuid,
  sender         text NOT NULL CONSTRAINT cap_message_sender_nonempty CHECK (sender <> ''),
  sent           timestamptz NOT NULL,
  msg_type       text NOT NULL CONSTRAINT cap_message_msg_type_known CHECK (msg_type IN ('alert', 'update', 'cancel')),
  category       text NOT NULL DEFAULT '',
  event_code     text NOT NULL DEFAULT '',
  urgency        text NOT NULL DEFAULT '',
  severity       text NOT NULL
                 CONSTRAINT cap_message_severity_known CHECK (severity IN ('minor', 'moderate', 'severe', 'extreme', 'unknown')),
  certainty      text NOT NULL DEFAULT '',
  effective      timestamptz NOT NULL,
  onset          timestamptz,
  expires        timestamptz NOT NULL,
  -- Teks per bahasa: [{"language":"id","event":…,"headline":…,…}, …], bahasa Indonesia wajib ada.
  texts          jsonb NOT NULL
                 CONSTRAINT cap_message_texts_shape CHECK (
                   jsonb_typeof(texts) = 'array' AND jsonb_path_exists(texts, '$[*] ? (@.language == "id")')
                 ),
  web            text NOT NULL DEFAULT '' CONSTRAINT cap_message_web_https CHECK (web = '' OR web LIKE 'https://%'),
  contact        text NOT NULL DEFAULT '',
  source_url     text NOT NULL DEFAULT '' CONSTRAINT cap_message_source_url_https CHECK (source_url = '' OR source_url LIKE 'https://%'),
  area_desc      text NOT NULL DEFAULT '',
  polygon_count  integer NOT NULL CONSTRAINT cap_message_polygon_count CHECK (polygon_count BETWEEN 0 AND 5000),
  -- Gabungan semua poligon setelah ST_MakeValid, jadi selalu valid.
  area           geometry(MultiPolygon, 4326)
                 CONSTRAINT cap_message_area_valid CHECK (area IS NULL OR (ST_IsValid(area) AND NOT ST_IsEmpty(area))),
  fetched_at     timestamptz NOT NULL,
  first_seen_at  timestamptz NOT NULL,
  archive_key    text NOT NULL DEFAULT '',
  content_sha256 bytea NOT NULL CONSTRAINT cap_message_content_sha256_len CHECK (length(content_sha256) = 32),
  updated_at     timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (source, identifier),
  CONSTRAINT cap_message_expires_after_effective CHECK (expires > effective),
  CONSTRAINT cap_message_valid_for CHECK (expires <= effective + interval '7 days'),
  CONSTRAINT cap_message_onset_before_expires CHECK (onset IS NULL OR onset < expires),
  CONSTRAINT cap_message_first_seen CHECK (first_seen_at <= fetched_at),
  CONSTRAINT cap_message_not_future CHECK (sent <= fetched_at + interval '10 minutes'),
  -- Hanya Cancel yang boleh tanpa area; poligon yang semuanya rusak ditolak.
  CONSTRAINT cap_message_has_area CHECK (msg_type = 'cancel' OR area IS NOT NULL),
  CONSTRAINT cap_message_area_needs_polygons CHECK (area IS NULL OR polygon_count > 0),
  CONSTRAINT cap_message_event_fk FOREIGN KEY (event_id, kind) REFERENCES hazard.event (id, kind)
);

COMMENT ON TABLE hazard.cap_message IS 'Pesan CAP peringatan dini cuaca; bahan kejadian cuaca dan riwayatnya';

CREATE INDEX cap_message_event_idx ON hazard.cap_message (event_id);
CREATE INDEX cap_message_sent_idx  ON hazard.cap_message (sent);

-- Rujukan antar-pesan (elemen references CAP). Pesan yang dirujuk belum tentu
-- pernah diterima, jadi tidak ada FK ke sisi yang dirujuk.
CREATE TABLE hazard.cap_reference (
  source         text NOT NULL,
  identifier     text NOT NULL,
  ref_identifier text NOT NULL CONSTRAINT cap_reference_identifier_format CHECK (ref_identifier ~ '^[!-~]{1,128}$'),
  ref_sender     text NOT NULL,
  ref_sent       timestamptz NOT NULL,
  PRIMARY KEY (source, identifier, ref_identifier),
  CONSTRAINT cap_reference_not_self CHECK (ref_identifier <> identifier),
  CONSTRAINT cap_reference_message_fk FOREIGN KEY (source, identifier)
    REFERENCES hazard.cap_message (source, identifier) ON DELETE CASCADE
);

CREATE INDEX cap_reference_target_idx ON hazard.cap_reference (source, ref_identifier);

-- Detail cuaca, 1:1 dengan hazard.event, dari pesan berarea terbaru dalam rantai.
CREATE TABLE hazard.weather (
  event_id       uuid PRIMARY KEY,
  kind           text NOT NULL DEFAULT 'weather' CONSTRAINT weather_kind CHECK (kind = 'weather'),
  cap_identifier text NOT NULL CONSTRAINT weather_cap_identifier_format CHECK (cap_identifier ~ '^[!-~]{1,128}$'),
  cap_msg_type   text NOT NULL CONSTRAINT weather_cap_msg_type_known CHECK (cap_msg_type IN ('alert', 'update', 'cancel')),
  severity       text NOT NULL
                 CONSTRAINT weather_severity_known CHECK (severity IN ('minor', 'moderate', 'severe', 'extreme', 'unknown')),
  urgency        text NOT NULL DEFAULT '',
  certainty      text NOT NULL DEFAULT '',
  event_code     text NOT NULL DEFAULT '',
  event          text NOT NULL DEFAULT '',
  headline       text NOT NULL DEFAULT '',
  description    text NOT NULL DEFAULT '',
  instruction    text NOT NULL DEFAULT '',
  event_en       text NOT NULL DEFAULT '',
  headline_en    text NOT NULL DEFAULT '',
  description_en text NOT NULL DEFAULT '',
  instruction_en text NOT NULL DEFAULT '',
  area_desc      text NOT NULL DEFAULT '',
  web_url        text NOT NULL DEFAULT '' CONSTRAINT weather_web_https CHECK (web_url = '' OR web_url LIKE 'https://%'),
  source_url     text NOT NULL DEFAULT '' CONSTRAINT weather_source_url_https CHECK (source_url = '' OR source_url LIKE 'https://%'),
  sent_at        timestamptz NOT NULL,
  effective_at   timestamptz NOT NULL,
  onset_at       timestamptz,
  area_km2       double precision NOT NULL CONSTRAINT weather_area_positive CHECK (area_km2 > 0),
  message_count  integer NOT NULL CONSTRAINT weather_message_count_positive CHECK (message_count >= 1),

  CONSTRAINT weather_event_fk FOREIGN KEY (event_id, kind) REFERENCES hazard.event (id, kind) ON DELETE CASCADE
);

COMMENT ON TABLE hazard.weather IS 'Detail peringatan dini cuaca per kejadian (teks asli BMKG, id dan en)';

GRANT SELECT ON hazard.cap_message, hazard.cap_reference, hazard.weather
  TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP TABLE hazard.weather;
DROP TABLE hazard.cap_reference;
DROP TABLE hazard.cap_message;
-- Kejadian cuaca tidak punya tempat di skema lama.
DELETE FROM hazard.event WHERE kind = 'weather';
ALTER TABLE hazard.impact_region DROP COLUMN coverage;
COMMENT ON COLUMN hazard.impact_region.within_felt IS NULL;
DROP INDEX hazard.event_area_gix;
ALTER TABLE hazard.event DROP CONSTRAINT event_weather_has_area;
ALTER TABLE hazard.event DROP CONSTRAINT event_detected_after_occurrence;
ALTER TABLE hazard.event ADD CONSTRAINT event_detected_after_occurrence
  CHECK (detected_at >= occurred_at - interval '1 hour');
ALTER TABLE hazard.event DROP CONSTRAINT event_area_valid;
ALTER TABLE hazard.event ALTER COLUMN area TYPE geometry(Polygon, 4326) USING ST_GeometryN(area, 1);
ALTER TABLE hazard.event ADD CONSTRAINT event_area_valid
  CHECK (area IS NULL OR (ST_IsValid(area) AND NOT ST_IsEmpty(area)));
