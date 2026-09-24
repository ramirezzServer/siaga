// Package quakesql berisi SQL penyimpanan gempa di schema hazard. Dipisah dari
// adapter supaya bisa dibaca dan ditinjau sebagai SQL utuh.
package quakesql

// LockKey adalah kunci advisory lock pengelompokan gempa ("SIAGAQK" dalam ASCII).
const LockKey int64 = 0x53494147414b

// Lock mengunci pengelompokan gempa sampai transaksi selesai.
const Lock = `SELECT pg_advisory_xact_lock($1)`

// reportColumns urutannya sama dengan scanReport di adapter.
const reportColumns = `source, source_event_id, feed, occurred_at, ST_Y(location), ST_X(location),
  magnitude, magnitude_type, depth_km, place, felt_description, tsunami, potential_text,
  shakemap_url, source_updated_at, review_status, alternate_ids, source_url,
  fetched_at, first_seen_at, archive_key`

// Report membaca satu laporan.
const Report = `SELECT ` + reportColumns + `
FROM hazard.event_source
WHERE source = $1 AND source_event_id = $2 AND feed = $3`

// IdentityReports membaca semua feed untuk satu kejadian sumber.
const IdentityReports = `SELECT ` + reportColumns + `
FROM hazard.event_source
WHERE source = $1 AND source_event_id = $2`

// SaveReport menyisipkan atau menimpa laporan. event_id hanya diubah untuk
// laporan yang ditarik sumber (dilepas).
const SaveReport = `INSERT INTO hazard.event_source (
  source, source_event_id, feed, occurred_at, location, magnitude, magnitude_type, depth_km,
  place, felt_description, tsunami, potential_text, shakemap_url, source_updated_at,
  review_status, alternate_ids, source_url, fetched_at, first_seen_at, archive_key, content_sha256
) VALUES (
  $1, $2, $3, $4, ST_SetSRID(ST_MakePoint($6, $5), 4326), $7, $8, $9,
  $10, $11, $12, $13, $14, $15,
  $16, $17, $18, $19, $20, $21, $22
)
ON CONFLICT (source, source_event_id, feed) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at,
  location = EXCLUDED.location,
  magnitude = EXCLUDED.magnitude,
  magnitude_type = EXCLUDED.magnitude_type,
  depth_km = EXCLUDED.depth_km,
  place = EXCLUDED.place,
  felt_description = EXCLUDED.felt_description,
  tsunami = EXCLUDED.tsunami,
  potential_text = EXCLUDED.potential_text,
  shakemap_url = EXCLUDED.shakemap_url,
  source_updated_at = EXCLUDED.source_updated_at,
  review_status = EXCLUDED.review_status,
  alternate_ids = EXCLUDED.alternate_ids,
  source_url = EXCLUDED.source_url,
  fetched_at = EXCLUDED.fetched_at,
  first_seen_at = EXCLUDED.first_seen_at,
  archive_key = EXCLUDED.archive_key,
  content_sha256 = EXCLUDED.content_sha256,
  -- Laporan yang ditarik sumber langsung lepas dari kejadian (constraint
  -- event_source_deleted_detached); pengelompokan ulang menyusul di transaksi yang sama.
  event_id = CASE WHEN EXCLUDED.review_status = 'deleted' THEN NULL ELSE hazard.event_source.event_id END,
  updated_at = now()`

// EventOf mencari kejadian tempat laporan tergabung.
const EventOf = `SELECT event_id FROM hazard.event_source
WHERE source = $1 AND source_event_id = $2 AND event_id IS NOT NULL
LIMIT 1`

// Clusters membaca semua anggota kejadian gempa aktif/kedaluwarsa yang punya
// laporan di jendela waktu, ditambah kejadian yang disebut eksplisit.
const Clusters = `WITH candidate AS (
  SELECT es.event_id
  FROM hazard.event_source es
  JOIN hazard.event e ON e.id = es.event_id
  WHERE es.kind = 'quake' AND es.occurred_at BETWEEN $1 AND $2
    AND e.status IN ('active', 'expired')
  UNION
  SELECT unnest($3::uuid[])
)
SELECT event_id, ` + reportColumns + `
FROM hazard.event_source
WHERE event_id IN (SELECT event_id FROM candidate)`

// EventExists melaporkan apakah ID sudah dipakai.
const EventExists = `SELECT EXISTS (SELECT 1 FROM hazard.event WHERE id = $1)`

// Event membaca ringkasan kejadian.
const Event = `SELECT status, level, revision, content_sha256 FROM hazard.event WHERE id = $1`

// RegionsWithin mencari kelurahan/desa dalam radius. Filter pertama memakai
// indeks GiST geometri dengan radius derajat yang sengaja lebih lebar ($4);
// filter kedua jarak geodesik sebenarnya dalam meter ($3).
const RegionsWithin = `WITH p AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326) AS g)
SELECT r.code, r.name, ST_Distance(r.geom::geography, p.g::geography) / 1000.0 AS km
FROM ref.region r, p
WHERE r.level = 4
  AND ST_DWithin(r.geom, p.g, $4)
  AND ST_DWithin(r.geom::geography, p.g::geography, $3)
ORDER BY km, r.code`

// SetMembership menautkan laporan identitas ke kejadian.
const SetMembership = `UPDATE hazard.event_source es
SET event_id = $1, updated_at = now()
FROM unnest($2::text[], $3::text[]) AS m(source, source_event_id)
WHERE es.source = m.source AND es.source_event_id = m.source_event_id
  AND es.event_id IS DISTINCT FROM $1`

// Detach melepas laporan identitas dari kejadian.
const Detach = `UPDATE hazard.event_source SET event_id = NULL, updated_at = now()
WHERE source = $1 AND source_event_id = $2 AND event_id IS NOT NULL`

// SaveEvent menyisipkan atau memperbarui baris kejadian. Status tidak diubah
// saat memperbarui; kejadian yang sudah kedaluwarsa tetap kedaluwarsa.
const SaveEvent = `INSERT INTO hazard.event (
  id, kind, level, primary_source, source_event_id, occurred_at, detected_at, expires_at,
  location, area, title, summary, revision, content_sha256
) VALUES (
  $1, 'quake', $2, $3, $4, $5, $6, $7,
  ST_SetSRID(ST_MakePoint($9, $8), 4326),
  CASE WHEN $10::float8 > 0
    THEN ST_Buffer(ST_SetSRID(ST_MakePoint($9, $8), 4326)::geography, $10::float8 * 1000.0, 'quad_segs=16')::geometry
  END,
  $11, $12, $13, $14
)
ON CONFLICT (id) DO UPDATE SET
  level = EXCLUDED.level,
  primary_source = EXCLUDED.primary_source,
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

// SaveQuake menyisipkan atau memperbarui detail gempa.
const SaveQuake = `INSERT INTO hazard.quake (
  event_id, magnitude, magnitude_type, depth_km, felt_radius_km, place, felt_description,
  tsunami, shakemap_url, source_url
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (event_id) DO UPDATE SET
  magnitude = EXCLUDED.magnitude,
  magnitude_type = EXCLUDED.magnitude_type,
  depth_km = EXCLUDED.depth_km,
  felt_radius_km = EXCLUDED.felt_radius_km,
  place = EXCLUDED.place,
  felt_description = EXCLUDED.felt_description,
  tsunami = EXCLUDED.tsunami,
  shakemap_url = EXCLUDED.shakemap_url,
  source_url = EXCLUDED.source_url`

// ClearImpacts menghapus wilayah terdampak lama sebelum ditulis ulang.
const ClearImpacts = `DELETE FROM hazard.impact_region WHERE event_id = $1`

// SaveImpacts menulis wilayah terdampak dari array paralel.
const SaveImpacts = `INSERT INTO hazard.impact_region (event_id, region_code, distance_km, level, within_felt)
SELECT $1, code, km, lvl, felt
FROM unnest($2::text[], $3::float8[], $4::int2[], $5::bool[]) AS t(code, km, lvl, felt)`

// EndEvent menandai kejadian tidak aktif.
const EndEvent = `UPDATE hazard.event
SET status = $2, merged_into = $3, ended_at = $4, revision = $5, updated_at = now()
WHERE id = $1`

// DueForExpiry mengambil kejadian gempa aktif yang sudah lewat masa aktifnya.
const DueForExpiry = `SELECT id, status, level, revision, content_sha256
FROM hazard.event
WHERE kind = 'quake' AND status = 'active' AND expires_at <= $1
ORDER BY expires_at, id
LIMIT $2
FOR UPDATE SKIP LOCKED`

// Enqueue menulis pesan ke outbox. Pesan dengan msg_id sama diabaikan.
const Enqueue = `INSERT INTO hazard.outbox (subject, msg_id, payload) VALUES ($1, $2, $3)
ON CONFLICT (msg_id) DO NOTHING`

// OutboxBatch mengambil pesan tertua yang tidak sedang dikunci proses lain.
const OutboxBatch = `SELECT id, subject, msg_id, payload FROM hazard.outbox
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED`

// OutboxDelete menghapus pesan yang sudah terbit.
const OutboxDelete = `DELETE FROM hazard.outbox WHERE id = ANY($1::bigint[])`
