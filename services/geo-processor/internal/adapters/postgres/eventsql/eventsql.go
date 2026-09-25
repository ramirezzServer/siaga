// Package eventsql berisi SQL hazard.event dan hazard.outbox yang sama untuk
// semua jenis bahaya.
package eventsql

// Event membaca ringkasan kejadian.
const Event = `SELECT status, level, revision, content_sha256 FROM hazard.event WHERE id = $1`

// EndEvent menandai kejadian tidak aktif.
const EndEvent = `UPDATE hazard.event
SET status = $2, merged_into = $3, ended_at = $4, revision = $5, updated_at = now()
WHERE id = $1`

// DueForExpiry mengambil kejadian aktif satu jenis ($3) yang sudah lewat masa aktifnya.
const DueForExpiry = `SELECT id, status, level, revision, content_sha256
FROM hazard.event
WHERE kind = $3 AND status = 'active' AND expires_at <= $1
ORDER BY expires_at, id
LIMIT $2
FOR UPDATE SKIP LOCKED`

// Enqueue menulis pesan ke outbox. Pesan dengan msg_id sama diabaikan.
// traceparent kosong disimpan NULL.
const Enqueue = `INSERT INTO hazard.outbox (subject, msg_id, payload, traceparent) VALUES ($1, $2, $3, NULLIF($4, ''))
ON CONFLICT (msg_id) DO NOTHING`

// OutboxBatch mengambil pesan tertua yang tidak sedang dikunci proses lain.
const OutboxBatch = `SELECT id, subject, msg_id, payload, coalesce(traceparent, ''), created_at FROM hazard.outbox
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED`

// OutboxBacklog menghitung pesan yang belum terbit dan waktu tulis tertuanya.
const OutboxBacklog = `SELECT count(*), min(created_at) FROM hazard.outbox`

// OutboxDelete menghapus pesan yang sudah terbit.
const OutboxDelete = `DELETE FROM hazard.outbox WHERE id = ANY($1::bigint[])`

// ClearImpacts menghapus wilayah terdampak lama sebelum ditulis ulang.
const ClearImpacts = `DELETE FROM hazard.impact_region WHERE event_id = $1`
