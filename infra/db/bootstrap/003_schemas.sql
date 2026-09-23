-- Schema per batas layanan (lihat dokumen arsitektur, bagian Lapisan data).
-- Pemilik schema adalah satu-satunya yang boleh membuat tabel di dalamnya; akses lintas
-- layanan diberikan eksplisit oleh migrasi pemilik schema, tidak pernah lewat superuser.
CREATE SCHEMA IF NOT EXISTS ref    AUTHORIZATION siaga_geo;
CREATE SCHEMA IF NOT EXISTS hazard AUTHORIZATION siaga_geo;
CREATE SCHEMA IF NOT EXISTS ts     AUTHORIZATION siaga_geo;
CREATE SCHEMA IF NOT EXISTS core   AUTHORIZATION siaga_core;
CREATE SCHEMA IF NOT EXISTS alert  AUTHORIZATION siaga_alert;
CREATE SCHEMA IF NOT EXISTS ai     AUTHORIZATION siaga_ai;

-- Tabel riwayat goose per layanan disimpan di schema miliknya sendiri, jadi setiap
-- layanan bisa bermigrasi mandiri tanpa hak atas schema lain.
