// Package weathersql berisi SQL penyimpanan peringatan dini cuaca di schema
// hazard. Dipisah dari adapter supaya bisa dibaca dan ditinjau sebagai SQL utuh.
package weathersql

import "github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/eventsql"

// LockKey adalah kunci advisory lock peringatan cuaca ("SIAGAWX" dalam ASCII).
const LockKey int64 = 0x53494147415758

// Lock mengunci pemrosesan peringatan cuaca sampai transaksi selesai.
const Lock = `SELECT pg_advisory_xact_lock($1)`

// messageColumns urutannya sama dengan scanMessage di adapter.
const messageColumns = `m.source, m.identifier, m.sender, m.sent, m.msg_type, m.category, m.event_code,
  m.urgency, m.severity, m.certainty, m.effective, m.onset, m.expires, m.texts, m.web, m.contact,
  m.source_url, m.area_desc, m.area IS NOT NULL, m.fetched_at, m.first_seen_at, m.archive_key,
  m.content_sha256, m.event_id`

// Message membaca satu pesan.
const Message = `SELECT ` + messageColumns + `
FROM hazard.cap_message m
WHERE m.source = $1 AND m.identifier = $2`

// SaveMessage menyisipkan atau menimpa pesan. Area dihitung di sini dari
// poligon WKT ($19): tiap poligon diperbaiki (ST_MakeValid), diambil bagian
// poligonnya, lalu digabung. event_id tidak diubah.
const SaveMessage = `INSERT INTO hazard.cap_message (
  source, identifier, sender, sent, msg_type, category, event_code, urgency, severity, certainty,
  effective, onset, expires, texts, web, contact, source_url, area_desc, polygon_count, area,
  fetched_at, first_seen_at, archive_key, content_sha256
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
  $11, $12, $13, $14, $15, $16, $17, $18, cardinality($19::text[]),
  (SELECT CASE WHEN u IS NULL OR ST_IsEmpty(u) THEN NULL ELSE ST_Multi(u) END
   FROM (
     SELECT ST_UnaryUnion(ST_Collect(ST_CollectionExtract(ST_MakeValid(ST_GeomFromText(w, 4326)), 3))) AS u
     FROM unnest($19::text[]) AS w
   ) s),
  $20, $21, $22, $23
)
ON CONFLICT (source, identifier) DO UPDATE SET
  sender = EXCLUDED.sender,
  sent = EXCLUDED.sent,
  msg_type = EXCLUDED.msg_type,
  category = EXCLUDED.category,
  event_code = EXCLUDED.event_code,
  urgency = EXCLUDED.urgency,
  severity = EXCLUDED.severity,
  certainty = EXCLUDED.certainty,
  effective = EXCLUDED.effective,
  onset = EXCLUDED.onset,
  expires = EXCLUDED.expires,
  texts = EXCLUDED.texts,
  web = EXCLUDED.web,
  contact = EXCLUDED.contact,
  source_url = EXCLUDED.source_url,
  area_desc = EXCLUDED.area_desc,
  polygon_count = EXCLUDED.polygon_count,
  area = EXCLUDED.area,
  fetched_at = EXCLUDED.fetched_at,
  first_seen_at = EXCLUDED.first_seen_at,
  archive_key = EXCLUDED.archive_key,
  content_sha256 = EXCLUDED.content_sha256,
  updated_at = now()`

// ClearReferences menghapus rujukan lama sebelum ditulis ulang.
const ClearReferences = `DELETE FROM hazard.cap_reference WHERE source = $1 AND identifier = $2`

// SaveReferences menulis rujukan dari array paralel.
const SaveReferences = `INSERT INTO hazard.cap_reference (source, identifier, ref_identifier, ref_sender, ref_sent)
SELECT $1, $2, r.id, r.sender, r.sent
FROM unnest($3::text[], $4::text[], $5::timestamptz[]) AS r(id, sender, sent)`

// Component membaca semua pesan tersimpan yang terhubung dengan ($1, $2)
// lewat rujukan ke dua arah, transitif. Node yang hanya dikenal dari rujukan
// (pesannya belum diterima) ikut dilalui, jadi dua pembaruan dari peringatan
// yang sama tetap tersambung walau peringatan awalnya tidak pernah diterima.
const Component = `WITH RECURSIVE reach(source, identifier) AS (
  SELECT $1::text, $2::text
  UNION
  SELECT n.source, n.identifier
  FROM reach r
  CROSS JOIN LATERAL (
    SELECT c.source, c.ref_identifier AS identifier FROM hazard.cap_reference c
    WHERE c.source = r.source AND c.identifier = r.identifier
    UNION ALL
    SELECT c.source, c.identifier FROM hazard.cap_reference c
    WHERE c.source = r.source AND c.ref_identifier = r.identifier
  ) n
)
SELECT ` + messageColumns + `
FROM hazard.cap_message m
JOIN reach r ON r.source = m.source AND r.identifier = m.identifier
ORDER BY m.sent, m.identifier`

// References membaca rujukan sekumpulan pesan.
const References = `SELECT identifier, ref_identifier, ref_sender, ref_sent
FROM hazard.cap_reference
WHERE source = $1 AND identifier = ANY($2::text[])
ORDER BY identifier, ref_identifier`

// Area membaca ringkasan area tersimpan satu pesan: GeoJSON (presisi 6 desimal,
// ±0,1 m), titik di dalam area, dan luas geodesik dalam km².
const Area = `SELECT ST_AsGeoJSON(area, 6), ST_Y(p), ST_X(p), ST_Area(area::geography) / 1e6
FROM (SELECT area, ST_PointOnSurface(area) AS p FROM hazard.cap_message WHERE source = $1 AND identifier = $2) a
WHERE area IS NOT NULL`

// Coverage menghitung kelurahan/desa yang beririsan dengan area tersimpan satu
// pesan dan bagian luasnya yang tercakup. Perbandingan luas dihitung di bidang
// derajat: distorsinya sama untuk pembilang dan penyebut di lintang yang sama.
const Coverage = `WITH a AS (SELECT area FROM hazard.cap_message WHERE source = $1 AND identifier = $2)
SELECT code, name, cov FROM (
  SELECT r.code, r.name, ST_Area(ST_Intersection(r.geom, a.area)) / NULLIF(ST_Area(r.geom), 0) AS cov
  FROM ref.region r, a
  WHERE r.level = 4 AND r.geom && a.area AND ST_Intersects(r.geom, a.area)
) c
WHERE cov > 0
ORDER BY cov DESC, code`

// SaveEvent menyisipkan (status active) atau memperbarui baris kejadian cuaca.
// Area diambil dari pesan berarea terbaru ($9). Status tidak diubah saat
// memperbarui.
const SaveEvent = `INSERT INTO hazard.event (
  id, kind, level, primary_source, source_event_id, occurred_at, detected_at, expires_at,
  location, area, title, summary, revision, content_sha256
) VALUES (
  $1, 'weather', $2, 'bmkg', $3, $4, $5, $6,
  ST_SetSRID(ST_MakePoint($8, $7), 4326),
  (SELECT area FROM hazard.cap_message WHERE source = 'bmkg' AND identifier = $9),
  $10, $11, $12, $13
)
ON CONFLICT (id) DO UPDATE SET
  level = EXCLUDED.level,
  source_event_id = EXCLUDED.source_event_id,
  occurred_at = EXCLUDED.occurred_at,
  detected_at = EXCLUDED.detected_at,
  expires_at = EXCLUDED.expires_at,
  location = EXCLUDED.location,
  area = EXCLUDED.area,
  title = EXCLUDED.title,
  summary = EXCLUDED.summary,
  revision = EXCLUDED.revision,
  content_sha256 = EXCLUDED.content_sha256,
  updated_at = now()`

// SaveWeather menyisipkan atau memperbarui detail cuaca.
const SaveWeather = `INSERT INTO hazard.weather (
  event_id, cap_identifier, cap_msg_type, severity, urgency, certainty, event_code,
  event, headline, description, instruction, event_en, headline_en, description_en, instruction_en,
  area_desc, web_url, source_url, sent_at, effective_at, onset_at, area_km2, message_count
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
ON CONFLICT (event_id) DO UPDATE SET
  cap_identifier = EXCLUDED.cap_identifier,
  cap_msg_type = EXCLUDED.cap_msg_type,
  severity = EXCLUDED.severity,
  urgency = EXCLUDED.urgency,
  certainty = EXCLUDED.certainty,
  event_code = EXCLUDED.event_code,
  event = EXCLUDED.event,
  headline = EXCLUDED.headline,
  description = EXCLUDED.description,
  instruction = EXCLUDED.instruction,
  event_en = EXCLUDED.event_en,
  headline_en = EXCLUDED.headline_en,
  description_en = EXCLUDED.description_en,
  instruction_en = EXCLUDED.instruction_en,
  area_desc = EXCLUDED.area_desc,
  web_url = EXCLUDED.web_url,
  source_url = EXCLUDED.source_url,
  sent_at = EXCLUDED.sent_at,
  effective_at = EXCLUDED.effective_at,
  onset_at = EXCLUDED.onset_at,
  area_km2 = EXCLUDED.area_km2,
  message_count = EXCLUDED.message_count`

// ClearImpacts sama dengan eventsql.ClearImpacts.
const ClearImpacts = eventsql.ClearImpacts

// SaveImpacts menulis wilayah terdampak cuaca dari array paralel.
const SaveImpacts = `INSERT INTO hazard.impact_region (event_id, region_code, distance_km, level, within_felt, coverage)
SELECT $1, code, 0, lvl, true, cov
FROM unnest($2::text[], $3::int2[], $4::float8[]) AS t(code, lvl, cov)`

// Reactivate mengaktifkan lagi kejadian yang sudah berakhir.
const Reactivate = `UPDATE hazard.event
SET status = 'active', merged_into = NULL, ended_at = NULL, updated_at = now()
WHERE id = $1 AND kind = 'weather'`

// SetMembership menautkan pesan ke kejadian.
const SetMembership = `UPDATE hazard.cap_message
SET event_id = $1, updated_at = now()
WHERE source = $2 AND identifier = ANY($3::text[]) AND event_id IS DISTINCT FROM $1`

// Event, EndEvent, Enqueue: lihat eventsql.
const (
	Event    = eventsql.Event
	EndEvent = eventsql.EndEvent
	Enqueue  = eventsql.Enqueue
)

// DueForExpiry mengambil kejadian cuaca aktif yang sudah lewat masa berlakunya.
const DueForExpiry = eventsql.DueForExpiry
