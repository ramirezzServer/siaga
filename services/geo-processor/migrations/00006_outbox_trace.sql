-- +goose Up
-- Konteks trace di outbox (fase 1e-2, ADR 0015). Pesan hazard.* ditulis ke
-- outbox di dalam span pemrosesan pesan raw.* penyebabnya, lalu diterbitkan
-- relay di goroutine lain. traceparent W3C disimpan bersama pesan supaya span
-- penerbitan masuk trace yang sama: satu gempa bisa dilacak dari polling
-- ingest sampai hazard.quake.created. NULL bila tidak ada span (telemetri
-- mati atau pesan dari sebelum migrasi ini). Bentuknya sama dengan
-- otelx.ValidTraceParent: versi 00, trace ID dan span ID bukan nol semua,
-- flag hanya bit sampled dan random (00–03).
ALTER TABLE hazard.outbox
  ADD COLUMN traceparent text
    CONSTRAINT outbox_traceparent_format CHECK (
          traceparent ~ '^00-[0-9a-f]{32}-[0-9a-f]{16}-0[0-3]$'
      AND substr(traceparent, 4, 32) <> repeat('0', 32)
      AND substr(traceparent, 37, 16) <> repeat('0', 16)
    );

-- +goose Down
ALTER TABLE hazard.outbox DROP COLUMN traceparent;
