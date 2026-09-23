# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 0 selesai di sisi kode (2026-09-23)

| Bagian           | Isi                                                                                                 | Sudah diverifikasi                                                                                                                                                                                                                                                                                                                   |
| ---------------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Monorepo         | go.work, pnpm + Turborepo, Makefile, lefthook, commitlint, Renovate, editorconfig                   | Ya                                                                                                                                                                                                                                                                                                                                   |
| Database         | Image PostgreSQL (PostGIS, TimescaleDB, h3, pgvector, pg_trgm), bootstrap role + schema per layanan | SQL diuji di PostgreSQL 16 + TimescaleDB 2.21; image Docker belum pernah di-build                                                                                                                                                                                                                                                    |
| Migrasi          | `services/geo-processor/migrations/00001_ref_region.sql`                                            | Naik-turun-naik lulus; semua constraint diuji menolak data buruk                                                                                                                                                                                                                                                                     |
| Importer wilayah | Domain, parser dump SQL, use case, adapter pgx, CLI                                                 | Domain, parser, use case: unit test + fuzz, coverage 92–97%. Data asli Jawa Barat: 6.612 wilayah valid, upsert idempotent. Adapter pgx, CLI, dan test integrasi lolos type-check terhadap API pgx v5, dan SQL-nya diuji langsung dengan psql; belum pernah dikompilasi penuh karena proxy modul Go diblokir di lingkungan pembuatnya |
| Kontrak          | Proto `siaga.common.v1`, `siaga.hazard.v1`; OpenAPI Core API 0.1.0                                  | buf lint, Redocly lint, generate TS, test TS lulus. Generate Go belum dijalankan                                                                                                                                                                                                                                                     |
| Paket TS         | `@siaga/contracts`, `@siaga/api-client`                                                             | Lint strict, typecheck, test, build lulus                                                                                                                                                                                                                                                                                            |
| Dev lokal        | Compose lite, k3d config, Tiltfile, manifest CNPG/Valkey/Mailpit/NATS                               | Sintaks YAML dan skrip valid; belum dijalankan                                                                                                                                                                                                                                                                                       |
| CI               | `.github/workflows/ci.yml` (secret scan, Go, TS, kontrak, klien Dart, database, Trivy, commitlint)  | Belum pernah jalan                                                                                                                                                                                                                                                                                                                   |

## Langkah pertama di laptop (wajib, urut)

1. Ikuti `docs/setup/windows-wsl2.md` sampai `make doctor` hijau.
2. `make deps`: membuat `go.sum` untuk tiap modul Go. Commit hasilnya (`chore(deps): kunci go.sum`).
3. `make gen`: generate kode Go dari proto (`libs/go/contracts/gen`). Commit.
4. `make lint test`: bila ada error kompilasi di `internal/adapters/postgres` atau `cmd/import-regions`, perbaiki di sini dulu.
5. `make up seed`: image PostgreSQL di-build, migrasi jalan, 6.612 wilayah masuk.
6. `make test-integration`.
7. Buat repo GitHub `siaga` (publik, supaya CI dan secret scanning gratis), push, pastikan CI hijau.
8. Opsional: `make k3d-up tilt` untuk profil full. Bila CloudNativePG menolak image, lihat ADR 0002.

## Berikutnya: Fase 1 (pipa data)

Target demo: dashboard Grafana berisi data live semua sumber.

1. Kontrak `raw.*` di proto + stream JetStream `RAW` dan `HAZARD`.
2. Layanan `ingest` (Go): konektor BMKG gempa (autogempa, gempaterkini, gempadirasakan), BMKG CAP nowcast, BMKG prakiraan adm4, USGS, Open-Meteo (cuaca, kualitas udara, flood), OpenAQ, NASA FIRMS. Rate limiter per sumber (BMKG total < 60 req/menit per IP), arsip payload mentah ke Garage.
3. `geo-processor`: normalisasi, deduplikasi gempa (≤ 90 dtk, ≤ 75 km, Δmag ≤ 0,7), estimasi wilayah terdampak, hypertable `ts.*`.
4. Rekaman payload historis untuk uji replay.
5. OpenTelemetry + Grafana Cloud free tier.

Titik pantau sungai (38 titik GloFAS + 7 sub-DAS indeks hujan) dan aturan tingkat peringatan ada di PRD, bagian Sungai yang dipantau dan Aturan bisnis.

## Catatan untuk asisten AI di chat baru

Baca berurutan: `CLAUDE.md`, file ini, lalu dokumen arsitektur dan PRD (tautan di README). Semua keputusan produk sudah disepakati di sana; jangan buka ulang tanpa diminta. Keputusan teknis baru dicatat sebagai ADR di `docs/adr/`.
