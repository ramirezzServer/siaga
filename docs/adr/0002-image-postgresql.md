# 0002. Satu image PostgreSQL untuk Compose dan CloudNativePG

Tanggal: 2026-09-23 · Status: diterima

## Konteks

SIAGA butuh PostGIS, TimescaleDB, pgvector, dan h3-pg dalam satu database. Image CloudNativePG resmi tidak membawa TimescaleDB, sedangkan image TimescaleDB resmi berbeda basis dengan image CNPG.

## Keputusan

Image sendiri di `deploy/images/postgres`, berbasis `postgres:17-bookworm` resmi. PostGIS, pgvector, dan h3 dari repo PGDG yang sudah ada di image resmi; TimescaleDB dari repo apt Timescale. Di CloudNativePG, `postgresUID`/`postgresGID` diset 999 agar cocok dengan user image resmi. SQL bootstrap (`infra/db/bootstrap`) dipakai dua jalur: `docker-entrypoint-initdb.d` untuk Compose dan `postInitApplicationSQLRefs` untuk CNPG.

TimescaleDB memakai edisi Community (lisensi Timescale License). Fitur kompresi dan continuous aggregate boleh dipakai gratis selama SIAGA tidak menjual TimescaleDB sebagai layanan database.

## Konsekuensi

Satu Dockerfile, perilaku sama di lokal dan produksi. Versi ekstensi mengikuti paket apt terbaru saat build; untuk rilis produksi, image di-tag dan dipindai Trivy di CI. Kompatibilitas CNPG dengan image berbasis resmi diverifikasi saat pertama kali menjalankan profil full; bila bermasalah, alternatifnya basis `ghcr.io/cloudnative-pg/postgresql` ditambah paket yang sama.
