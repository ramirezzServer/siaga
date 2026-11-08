// Package firesql berisi SQL penyimpanan titik api: saringan sumber panas
// tetap di schema ref, kejadian hazard.fire, dan deteksi anggotanya.
// Dipisah dari adapter supaya bisa dibaca dan ditinjau sebagai SQL utuh.
package firesql

import "github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/eventsql"

// LockKey adalah kunci advisory pemrosesan titik api ("SIAGAFI").
const LockKey int64 = 0x53494147414649

// LockStatic mengunci penyelarasan saringan ("SIAGAST").
const LockStatic int64 = 0x53494147415354

// Lock mengunci sampai transaksi selesai.
const Lock = `SELECT pg_advisory_xact_lock($1)`

// HasDetection memeriksa apakah deteksi sudah menjadi anggota kejadian.
const HasDetection = `SELECT EXISTS (SELECT 1 FROM hazard.fire_detection WHERE detection_id = $1)`

// Nearby mengambil anggota kejadian aktif yang waktu deteksinya paling jauh
// $4 dari $3 dan berjarak ≤ $5 meter dari ($1 lintang, $2 bujur).
const Nearby = `SELECT d.event_id, d.detection_id, d.product, ST_Y(d.location), ST_X(d.location), d.detected_at, d.confidence,
  d.frp_mw, d.fetched_at
FROM hazard.fire_detection d
JOIN hazard.event e ON e.id = d.event_id AND e.kind = 'fire'
WHERE e.status = 'active'
  AND d.detected_at BETWEEN $3::timestamptz - $4::interval AND $3::timestamptz + $4::interval
  AND ST_DWithin(d.location::geography, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography, $5)
ORDER BY d.detected_at, d.detection_id`

// Episode membaca ringkasan kejadian titik api.
const Episode = `SELECT e.status, e.level, e.revision, e.content_sha256, f.opened_by, f.first_detected_at, e.expires_at, f.detection_count
FROM hazard.event e JOIN hazard.fire f ON f.event_id = e.id
WHERE e.id = $1 AND e.kind = 'fire'`

// Members mengambil semua deteksi anggota kejadian, urut waktu lalu ID.
const Members = `SELECT event_id, detection_id, product, ST_Y(location), ST_X(location), detected_at, confidence, frp_mw, fetched_at
FROM hazard.fire_detection WHERE event_id = $1
ORDER BY detected_at, detection_id`

// SaveEvent menyisipkan (status active) atau memperbarui baris kejadian
// titik api. Status tidak diubah saat memperbarui.
//
// $1 id, $2 tingkat, $3 deteksi pembuka, $4 deteksi pertama, $5 terima
// pertama, $6 kedaluwarsa, $7 lintang, $8 bujur, $9 judul, $10 ringkasan,
// $11 revisi, $12 SHA-256 isi.
const SaveEvent = `INSERT INTO hazard.event (
  id, kind, level, primary_source, source_event_id, occurred_at, detected_at, expires_at,
  location, area, title, summary, revision, content_sha256
) VALUES (
  $1, 'fire', $2, 'firms', $3, $4, $5, $6,
  ST_SetSRID(ST_MakePoint($8, $7), 4326), NULL, $9, $10, $11, $12
)
ON CONFLICT (id) DO UPDATE SET
  level = EXCLUDED.level,
  occurred_at = EXCLUDED.occurred_at,
  detected_at = EXCLUDED.detected_at,
  expires_at = EXCLUDED.expires_at,
  location = EXCLUDED.location,
  title = EXCLUDED.title,
  summary = EXCLUDED.summary,
  revision = EXCLUDED.revision,
  content_sha256 = EXCLUDED.content_sha256,
  updated_at = now()`

// SaveFire menyisipkan atau memperbarui detail titik api.
const SaveFire = `INSERT INTO hazard.fire (
  event_id, opened_by, detection_count, overpass_count, high_count, peak_detections, peak_overpasses,
  first_detected_at, last_detected_at, level_reached_at, first_fetched_at, last_fetched_at, products, max_frp_mw,
  siaga_detections, waspada_overpasses
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (event_id) DO UPDATE SET
  detection_count = EXCLUDED.detection_count,
  overpass_count = EXCLUDED.overpass_count,
  high_count = EXCLUDED.high_count,
  peak_detections = EXCLUDED.peak_detections,
  peak_overpasses = EXCLUDED.peak_overpasses,
  first_detected_at = EXCLUDED.first_detected_at,
  last_detected_at = EXCLUDED.last_detected_at,
  level_reached_at = EXCLUDED.level_reached_at,
  first_fetched_at = EXCLUDED.first_fetched_at,
  last_fetched_at = EXCLUDED.last_fetched_at,
  products = EXCLUDED.products,
  max_frp_mw = EXCLUDED.max_frp_mw,
  siaga_detections = EXCLUDED.siaga_detections,
  waspada_overpasses = EXCLUDED.waspada_overpasses`

// AddDetection mencatat deteksi anggota; deteksi yang sudah ada diabaikan.
const AddDetection = `INSERT INTO hazard.fire_detection (
  detection_id, event_id, product, location, detected_at, confidence, frp_mw, fetched_at
) VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($5, $4), 4326), $6, $7, $8, $9)
ON CONFLICT (detection_id) DO NOTHING`

// MoveDetections memindahkan anggota $1 ke $2.
const MoveDetections = `UPDATE hazard.fire_detection SET event_id = $2 WHERE event_id = $1`

// EndEvent, Enqueue, DueForExpiry: lihat eventsql.
const (
	EndEvent     = eventsql.EndEvent
	Enqueue      = eventsql.Enqueue
	DueForExpiry = eventsql.DueForExpiry
)

// UpsertStatic menulis sumber panas tetap dari array paralel dan
// mengembalikan satu baris per sumber yang disisipkan (true) atau diubah
// (false).
const UpsertStatic = `INSERT INTO ref.fire_static_source (
  id, location, radius_km, reason, detections, first_seen, last_seen, period_from, period_to, source_sha256
)
SELECT s.id, ST_SetSRID(ST_MakePoint(s.lon, s.lat), 4326), s.radius, s.reason, s.n, s.first_seen, s.last_seen, $9, $10, $11
FROM unnest(
  $1::text[], $2::float8[], $3::float8[], $4::float8[], $5::text[], $6::int4[], $7::date[], $8::date[]
) AS s(id, lat, lon, radius, reason, n, first_seen, last_seen)
ON CONFLICT (id) DO UPDATE SET
  location = EXCLUDED.location, radius_km = EXCLUDED.radius_km, reason = EXCLUDED.reason, detections = EXCLUDED.detections,
  first_seen = EXCLUDED.first_seen, last_seen = EXCLUDED.last_seen, period_from = EXCLUDED.period_from,
  period_to = EXCLUDED.period_to, source_sha256 = EXCLUDED.source_sha256, updated_at = now()
WHERE (ref.fire_static_source.radius_km, ref.fire_static_source.reason, ref.fire_static_source.detections,
       ref.fire_static_source.first_seen, ref.fire_static_source.last_seen, ref.fire_static_source.period_from,
       ref.fire_static_source.period_to, ref.fire_static_source.source_sha256)
   IS DISTINCT FROM (EXCLUDED.radius_km, EXCLUDED.reason, EXCLUDED.detections, EXCLUDED.first_seen, EXCLUDED.last_seen,
       EXCLUDED.period_from, EXCLUDED.period_to, EXCLUDED.source_sha256)
RETURNING (xmax = 0)`

// DeleteStatic menghapus sumber yang tidak ada lagi di kalibrasi.
const DeleteStatic = `DELETE FROM ref.fire_static_source WHERE id <> ALL($1::text[])`
