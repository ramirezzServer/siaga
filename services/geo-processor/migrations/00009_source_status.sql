-- +goose Up
-- Status sumber data (fase 2a-2): keadaan terakhir setiap konektor ingest,
-- dilipat dari hasil polling di stream STATUS. Tahan restart ingest karena
-- disimpan di sini, bukan di memori proses; core-api menghitung status publik
-- (/v1/sources) dari tabel ini. Invarian dijaga di sini sekaligus di
-- internal/domain/sourcestatus; lihat ADR 0030.
CREATE TABLE ts.source_status (
  connector            text PRIMARY KEY
                       CONSTRAINT source_status_connector_format CHECK (
                         connector ~ '^[a-z0-9]+([.-][a-z0-9]+)*$' AND length(connector) <= 60
                       ),
  -- Subjek raw yang diisi konektor; jenis dan sumbernya sama dengan
  -- libs/go/contracts/streams.
  subject              text NOT NULL
                       CONSTRAINT source_status_subject_known CHECK (
                         subject ~ '^raw\.(quake|weather|forecast|aq|flood|rain|fire)\.(bmkg|usgs|openmeteo|openaq|firms)$'
                       ),
  poll_interval        interval NOT NULL
                       CONSTRAINT source_status_interval_range CHECK (poll_interval BETWEEN interval '1 second' AND interval '7 days'),
  last_attempt_at      timestamptz NOT NULL,
  last_success_at      timestamptz,
  last_change_at       timestamptz,
  -- Akhir percobaan gagal pertama sejak percobaan berhasil terakhir.
  failing_since        timestamptz,
  consecutive_failures integer NOT NULL DEFAULT 0 CONSTRAINT source_status_failures_nonnegative CHECK (consecutive_failures >= 0),
  updated_at           timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT source_status_time_order CHECK (
    last_success_at <= last_attempt_at AND last_change_at <= last_success_at
  ),
  CONSTRAINT source_status_change_needs_success CHECK (last_change_at IS NULL OR last_success_at IS NOT NULL),
  CONSTRAINT source_status_failing CHECK ((consecutive_failures = 0) = (failing_since IS NULL)),
  -- Percobaan terbaru berhasil: waktu coba = waktu berhasil.
  CONSTRAINT source_status_last_ok CHECK (
    consecutive_failures > 0 OR (last_success_at IS NOT NULL AND last_success_at = last_attempt_at)
  ),
  CONSTRAINT source_status_failing_window CHECK (
    failing_since IS NULL
    OR (failing_since <= last_attempt_at AND (last_success_at IS NULL OR failing_since > last_success_at))
  )
);

COMMENT ON TABLE ts.source_status IS 'Keadaan terakhir setiap konektor ingest dari stream STATUS (ADR 0030)';

GRANT SELECT ON ts.source_status TO siaga_core, siaga_alert, siaga_ai, siaga_tiles;

-- +goose Down
DROP TABLE ts.source_status;
