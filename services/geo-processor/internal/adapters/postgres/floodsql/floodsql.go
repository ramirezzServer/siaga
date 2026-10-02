// Package floodsql berisi SQL penyimpanan banjir: ambang di schema ref dan
// kejadian hazard.flood beserta penilaian per harinya. Dipisah dari adapter
// supaya bisa dibaca dan ditinjau sebagai SQL utuh.
package floodsql

import "github.com/ramirezzServer/siaga/services/geo-processor/internal/adapters/postgres/eventsql"

// LockKey adalah kunci advisory pemrosesan banjir ("SIAGAFL").
const LockKey int64 = 0x5349414741464c

// Lock mengunci pemrosesan banjir sampai transaksi selesai.
const Lock = `SELECT pg_advisory_xact_lock($1)`

// Site membaca keadaan titik.
const Site = `SELECT last_fetched_at, event_id FROM hazard.flood_site WHERE site_id = $1`

// SaveSite mencatat keluaran terakhir yang dinilai dan kejadian titik.
const SaveSite = `INSERT INTO hazard.flood_site (site_id, last_fetched_at, event_id) VALUES ($1, $2, $3)
ON CONFLICT (site_id) DO UPDATE SET
  last_fetched_at = EXCLUDED.last_fetched_at,
  event_id = EXCLUDED.event_id,
  updated_at = now()`

// Episode membaca ringkasan kejadian banjir.
const Episode = `SELECT e.status, e.level, e.revision, e.content_sha256, f.opened_at, f.last_exceeded_at, e.expires_at
FROM hazard.event e JOIN hazard.flood f ON f.event_id = e.id
WHERE e.id = $1 AND e.kind = 'flood'`

// SaveEvent menyisipkan (status active) atau memperbarui baris kejadian
// banjir. Status tidak diubah saat memperbarui. Kejadian banjir tidak punya
// area: titiknya adalah sel GloFAS atau titik berat sel sub-DAS.
//
// $1 id, $2 tingkat, $3 ID titik, $4 waktu buka, $5 kedaluwarsa, $6 lintang,
// $7 bujur, $8 judul, $9 ringkasan, $10 revisi, $11 SHA-256 isi.
const SaveEvent = `INSERT INTO hazard.event (
  id, kind, level, primary_source, source_event_id, occurred_at, detected_at, expires_at,
  location, area, title, summary, revision, content_sha256
) VALUES (
  $1, 'flood', $2, 'openmeteo', $3, $4, $4, $5,
  ST_SetSRID(ST_MakePoint($7, $6), 4326), NULL, $8, $9, $10, $11
)
ON CONFLICT (id) DO UPDATE SET
  level = EXCLUDED.level,
  source_event_id = EXCLUDED.source_event_id,
  occurred_at = EXCLUDED.occurred_at,
  detected_at = EXCLUDED.detected_at,
  expires_at = EXCLUDED.expires_at,
  location = EXCLUDED.location,
  title = EXCLUDED.title,
  summary = EXCLUDED.summary,
  revision = EXCLUDED.revision,
  content_sha256 = EXCLUDED.content_sha256,
  updated_at = now()`

// SaveFlood menyisipkan atau memperbarui detail banjir.
const SaveFlood = `INSERT INTO hazard.flood (
  event_id, indicator, site_id, site_name, river, model, issued_at, outlook_level, possible, peak_date,
  max_level, opened_at, last_exceeded_at, thresholds
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (event_id) DO UPDATE SET
  indicator = EXCLUDED.indicator,
  site_id = EXCLUDED.site_id,
  site_name = EXCLUDED.site_name,
  river = EXCLUDED.river,
  model = EXCLUDED.model,
  issued_at = EXCLUDED.issued_at,
  outlook_level = EXCLUDED.outlook_level,
  possible = EXCLUDED.possible,
  peak_date = EXCLUDED.peak_date,
  max_level = EXCLUDED.max_level,
  opened_at = EXCLUDED.opened_at,
  last_exceeded_at = EXCLUDED.last_exceeded_at,
  thresholds = EXCLUDED.thresholds`

// ClearDays menghapus penilaian per hari lama sebelum ditulis ulang.
const ClearDays = `DELETE FROM hazard.flood_day WHERE event_id = $1`

// SaveDays menulis penilaian per hari dari array paralel.
const SaveDays = `INSERT INTO hazard.flood_day (
  event_id, date, level, possible, discharge_m3s, discharge_p75_m3s, discharge_max_m3s, from_ensemble,
  rain_3h_mm, rain_6h_mm, rain_24h_mm, window_hours
)
SELECT $1, d.date, d.level, d.possible, d.q, d.p75, d.qmax, d.ens, d.r3, d.r6, d.r24, d.win
FROM unnest(
  $2::timestamptz[], $3::int2[], $4::bool[], $5::float8[], $6::float8[], $7::float8[], $8::bool[],
  $9::float8[], $10::float8[], $11::float8[], $12::int2[]
) AS d(date, level, possible, q, p75, qmax, ens, r3, r6, r24, win)`

// Event, EndEvent, Enqueue: lihat eventsql.
const (
	EndEvent     = eventsql.EndEvent
	Enqueue      = eventsql.Enqueue
	DueForExpiry = eventsql.DueForExpiry
)

// LockThresholds mengunci penyelarasan ambang ("SIAGATH"), supaya dua replika
// yang start bersamaan tidak saling menimpa.
const LockThresholds int64 = 0x53494147415448

// UpsertDischarge menulis ambang debit dari array paralel dan mengembalikan
// satu baris per titik yang disisipkan (true) atau diubah (false).
const UpsertDischarge = `INSERT INTO ref.discharge_threshold (
  site_id, river, name, cell, p50, p80, p90, p98, p99_5, seamless_ratio, correction, max_level,
  period_from, period_to, source_sha256
)
SELECT t.id, t.river, t.name, ST_SetSRID(ST_MakePoint(t.lon, t.lat), 4326), t.p50, t.p80, t.p90, t.p98, t.p995,
  t.ratio, t.corr, t.maxl, $14, $15, $16
FROM unnest(
  $1::text[], $2::text[], $3::text[], $4::float8[], $5::float8[], $6::float8[], $7::float8[], $8::float8[],
  $9::float8[], $10::float8[], $11::float8[], $12::float8[], $13::int2[]
) AS t(id, river, name, lat, lon, p50, p80, p90, p98, p995, ratio, corr, maxl)
ON CONFLICT (site_id) DO UPDATE SET
  river = EXCLUDED.river, name = EXCLUDED.name, cell = EXCLUDED.cell,
  p50 = EXCLUDED.p50, p80 = EXCLUDED.p80, p90 = EXCLUDED.p90, p98 = EXCLUDED.p98, p99_5 = EXCLUDED.p99_5,
  seamless_ratio = EXCLUDED.seamless_ratio, correction = EXCLUDED.correction, max_level = EXCLUDED.max_level,
  period_from = EXCLUDED.period_from, period_to = EXCLUDED.period_to, source_sha256 = EXCLUDED.source_sha256,
  updated_at = now()
WHERE (ref.discharge_threshold.river, ref.discharge_threshold.name, ref.discharge_threshold.p50,
       ref.discharge_threshold.p80, ref.discharge_threshold.p90, ref.discharge_threshold.p98,
       ref.discharge_threshold.p99_5, ref.discharge_threshold.seamless_ratio, ref.discharge_threshold.correction,
       ref.discharge_threshold.max_level, ref.discharge_threshold.period_from, ref.discharge_threshold.period_to,
       ref.discharge_threshold.source_sha256)
   IS DISTINCT FROM (EXCLUDED.river, EXCLUDED.name, EXCLUDED.p50, EXCLUDED.p80, EXCLUDED.p90, EXCLUDED.p98,
       EXCLUDED.p99_5, EXCLUDED.seamless_ratio, EXCLUDED.correction, EXCLUDED.max_level, EXCLUDED.period_from,
       EXCLUDED.period_to, EXCLUDED.source_sha256)
   OR NOT ST_Equals(ref.discharge_threshold.cell, EXCLUDED.cell)
RETURNING (xmax = 0)`

// DeleteDischarge menghapus titik yang tidak ada lagi di kalibrasi.
const DeleteDischarge = `DELETE FROM ref.discharge_threshold WHERE site_id <> ALL($1::text[])`

// UpsertRainfall menulis ambang indeks hujan dari array paralel.
const UpsertRainfall = `INSERT INTO ref.rainfall_threshold (
  site_id, window_hours, name, p50, p80, p90, p98, p99_5, max_level, period_from, period_to, source_sha256
)
SELECT t.id, t.win, t.name, t.p50, t.p80, t.p90, t.p98, t.p995, t.maxl, $10, $11, $12
FROM unnest(
  $1::text[], $2::int2[], $3::text[], $4::float8[], $5::float8[], $6::float8[], $7::float8[], $8::float8[], $9::int2[]
) AS t(id, win, name, p50, p80, p90, p98, p995, maxl)
ON CONFLICT (site_id, window_hours) DO UPDATE SET
  name = EXCLUDED.name, p50 = EXCLUDED.p50, p80 = EXCLUDED.p80, p90 = EXCLUDED.p90, p98 = EXCLUDED.p98,
  p99_5 = EXCLUDED.p99_5, max_level = EXCLUDED.max_level, period_from = EXCLUDED.period_from,
  period_to = EXCLUDED.period_to, source_sha256 = EXCLUDED.source_sha256, updated_at = now()
WHERE (ref.rainfall_threshold.name, ref.rainfall_threshold.p50, ref.rainfall_threshold.p80,
       ref.rainfall_threshold.p90, ref.rainfall_threshold.p98, ref.rainfall_threshold.p99_5,
       ref.rainfall_threshold.max_level, ref.rainfall_threshold.period_from, ref.rainfall_threshold.period_to,
       ref.rainfall_threshold.source_sha256)
   IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.p50, EXCLUDED.p80, EXCLUDED.p90, EXCLUDED.p98, EXCLUDED.p99_5,
       EXCLUDED.max_level, EXCLUDED.period_from, EXCLUDED.period_to, EXCLUDED.source_sha256)
RETURNING (xmax = 0)`

// DeleteRainfall menghapus pasangan sub-DAS–jendela yang tidak ada lagi.
const DeleteRainfall = `DELETE FROM ref.rainfall_threshold t
WHERE NOT EXISTS (
  SELECT 1 FROM unnest($1::text[], $2::int2[]) AS k(id, win) WHERE k.id = t.site_id AND k.win = t.window_hours
)`
