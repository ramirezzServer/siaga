# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 0 selesai dan terverifikasi (2026-09-24)

Semua bagian fase 0 lolos di laptop (WSL2) dan di CI GitHub Actions; keenam job CI hijau sejak commit `a91f17d`.

| Bagian           | Isi                                                                                                | Terverifikasi                                                                                         |
| ---------------- | -------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Monorepo         | go.work, pnpm + Turborepo, Makefile, lefthook, commitlint, Renovate, editorconfig                  | `make doctor`, `make check`, hook pre-commit dan commit-msg jalan                                     |
| Database         | Image PostgreSQL 17 (PostGIS, TimescaleDB, h3, pgvector, pg_trgm), bootstrap role + schema         | Image di-build di laptop dan CI; Trivy tanpa temuan CRITICAL di paket Debian                          |
| Migrasi          | `services/geo-processor/migrations/00001_ref_region.sql`                                           | Naik-turun-naik lulus di CI                                                                           |
| Importer wilayah | Domain, parser dump SQL, use case, adapter pgx, CLI                                                | Unit test + fuzz (coverage domain + app 92–97%), test integrasi lulus, 6.612 wilayah Jawa Barat masuk |
| Kontrak          | Proto `siaga.common.v1`, `siaga.hazard.v1`; OpenAPI Core API 0.1.0                                 | buf lint, Redocly lint, generate Go + TS; CI memastikan kode hasil generate sesuai kontrak            |
| Paket TS         | `@siaga/contracts`, `@siaga/api-client`                                                            | Lint strict, typecheck, test, build lulus                                                             |
| Dev lokal        | Compose lite, k3d config, Tiltfile, manifest CNPG/Valkey/Mailpit/NATS                              | Compose lite jalan (`make up`); profil full k3d + Tilt belum pernah dijalankan                        |
| CI               | `.github/workflows/ci.yml` (secret scan, Go, TS, kontrak, klien Dart, database, Trivy, commitlint) | Hijau. Job commitlint hanya jalan di pull request, jadi belum pernah teruji                           |

## Lingkungan kerja

- Repo kerja: `~/code/siaga` di WSL2 Ubuntu 24.04. Jangan bekerja di `/mnt/c/...` (lambat, file watcher Tilt tidak jalan).
- GitHub: `github.com/ramirezzServer/siaga` (publik), login lewat `gh`.
- `C:\KULIAH\PROJECT CODE\siaga` adalah clone kedua untuk dibuka dari Windows. Perbarui dengan `git -C "/mnt/c/KULIAH/PROJECT CODE/siaga" pull --ff-only`; jangan diedit bersamaan dengan repo WSL.
- Versi alat: Go 1.27.1 (minimum bahasa 1.26, ADR 0005), Node 22, pnpm 10.28.0 lewat corepack, golangci-lint 2.13.2, gitleaks 8.30.1.

## Perubahan sesi 2026-09-24

- Minimum Go naik ke 1.26 karena goose v3.28.0; toolchain 1.27.1 di `go.work` (ADR 0005).
- `protoc-gen-go` dan `goose` dijalankan dengan `GOWORK=off`: `-modfile` ditolak di workspace mode, dan driver bawaan goose memicu ambiguous import genproto.
- CI: gitleaks lewat CLI dengan verifikasi checksum (gitleaks-action gagal pada push pertama), trivy-action dipin ke commit v0.36.0, analisis klien Dart hanya gagal pada error, pnpm/action-setup v6.
- `.trivyignore.yaml` mengecualikan CVE-2025-68121 hanya untuk `usr/local/bin/gosu` (gosu tidak memakai TLS). Kedaluwarsa 2027-03-31; setelah itu CI merah dan harus ditinjau ulang.

## Utang kecil yang diketahui

- Target migrasi di `Makefile` mencetak `DATABASE_URL` lengkap dengan password lokal ke terminal. Sembunyikan sebelum fase 1 menambah layanan.
- Renovate sudah dikonfigurasi (`renovate.json`) tetapi GitHub App Renovate belum dipasang di repo.
- Runner `ubuntu-latest` pindah ke Ubuntu 26 mulai 2026-10-19. Pantau run CI pertama setelah tanggal itu.
- Profil full (`make k3d-up tilt`) belum pernah dicoba. Bila CloudNativePG menolak image, lihat ADR 0002.

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

Asisten tidak bisa menjangkau folder WSL secara langsung. File dari asisten dititipkan di `C:\KULIAH\PROJECT CODE\` (di luar folder repo), disalin ke `~/code/siaga` dengan `cp`, dicek hash-nya, lalu file titipan dihapus. Perintah dijalankan pengguna di WSL, dan commit selalu dibuat dari WSL supaya hook lefthook ikut jalan.
