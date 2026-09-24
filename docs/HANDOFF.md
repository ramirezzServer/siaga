# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 1a selesai di workspace asisten, menunggu verifikasi di WSL (2026-09-24)

Fase 0 selesai dan terverifikasi (keenam job CI hijau sejak `a91f17d`). Fase 1 dipecah menjadi 1a–1e (lihat Berikutnya). Irisan 1a sudah ditulis, di-lint, dan dites di workspace asisten, termasuk uji ujung ke ujung ingest → NATS JetStream dengan payload BMKG dan USGS asli; belum dijalankan di WSL dan CI.

### Fase 1a: ingest gempa

| Bagian         | Isi                                                                                                                                           | Terverifikasi di workspace asisten                                                            |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Kontrak        | `siaga.raw.v1` (`FetchMeta`, `QuakeReport`), `libs/go/contracts/streams` (stream `RAW`, `HAZARD`, pembentuk subjek), `subjects.raw()`         | buf lint, generate Go + TS, test TS                                                           |
| Platform       | `natsx` (koneksi, `EnsureStream` dengan cek pemilik), `natstest` (server JetStream dalam proses untuk test)                                   | Test dengan server JetStream sungguhan                                                        |
| Layanan ingest | Domain (validasi gempa, GCRA, backoff, ID pesan), use case poll + runner, adapter HTTP, arsip file, JetStream, `/healthz` `/readyz` `/status` | golangci-lint bersih, `go test -race`, coverage domain + app 98,7%, fuzz 4 target             |
| Konektor       | BMKG `autogempa`, `gempaterkini`, `gempadirasakan` (tiap 30/60 dtk); USGS `2.5_day` disaring kotak Indonesia (60 dtk)                         | Fixture payload asli; ingest jalan melawan nats-server 2.15, restart tidak menggandakan pesan |
| Perekam        | `make ingest-record` = ingest `-once -publish=false`, arsip jadi bahan replay                                                                 | Menghasilkan 4 arsip `.gz`                                                                    |
| Repo           | Makefile (`ingest`, `ingest-record`, fuzz baru, URL database disamarkan), CI (lint, coverage, fuzz ingest), ADR 0006, `docs/events.md`        | —                                                                                             |

### Verifikasi yang perlu dijalankan di WSL

```bash
make deps              # membuat go.sum ingest dan platform, merapikan go.mod geo-processor
make check             # lint + test semua modul
make up && make ingest # biarkan beberapa menit, cek http://127.0.0.1:8081/status
make ingest-record     # arsip payload asli di .cache/ingest-archive
```

Setelah `make ingest` jalan, isi stream `RAW` terlihat di http://127.0.0.1:8222/jsz?streams=true (jumlah pesan bertambah saat ada gempa baru, tidak bertambah saat ingest di-restart).

### Fase 0: fondasi

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

- Compose lite memakai `nats:2.11-alpine`, sedangkan test memakai server 2.15 dalam proses. Fitur yang dipakai (dedup `Nats-Msg-Id`, `Nats-Expected-Stream`) ada di keduanya; naikkan image saat Renovate aktif.
- ingest belum punya Dockerfile dan manifest Kubernetes; dijalankan lewat `make ingest`. Ditambahkan di fase 1e bersama Garage dan OpenTelemetry.
- Renovate sudah dikonfigurasi (`renovate.json`) tetapi GitHub App Renovate belum dipasang di repo.
- Runner `ubuntu-latest` pindah ke Ubuntu 26 mulai 2026-10-19. Pantau run CI pertama setelah tanggal itu.
- Profil full (`make k3d-up tilt`) belum pernah dicoba. Bila CloudNativePG menolak image, lihat ADR 0002.

## Berikutnya: sisa Fase 1 (pipa data)

Target demo fase 1: dashboard Grafana berisi data live semua sumber.

- [x] **1a** Kontrak `raw.*`, stream `RAW`/`HAZARD`, ingest dengan konektor gempa BMKG + USGS, perekam payload.
- [ ] **1b** geo-processor sebagai consumer `raw.quake.*` (durable pull consumer, stream `DLQ`): normalisasi, deduplikasi BMKG–USGS (≤ 90 dtk, ≤ 75 km, Δmag ≤ 0,7) dengan property test, tabel `hazard.event` / `hazard.event_source` / `hazard.impact_region` (constraint di DB), radius dirasakan dari PRD × poligon kelurahan, terbit `hazard.quake.created|updated`. Kalibrasi awal dedup dengan katalog USGS historis Jawa Barat.
- [ ] **1c** BMKG CAP nowcast (RSS + XML CAP) dan prakiraan adm4 (sapuan ±5.900 desa, sub-anggaran 50/menit di dalam anggaran BMKG).
- [ ] **1d** Open-Meteo (cuaca grid 0,25°, kualitas udara, banjir 38 titik), OpenAQ, NASA FIRMS (key gratis lewat SOPS), hypertable `ts.*`.
- [ ] **1e** Arsip ke Garage, uji replay dari arsip, OpenTelemetry (trace ID di header `traceparent`) + dashboard Grafana Cloud, Dockerfile + manifest ingest dan geo-processor.

Titik pantau sungai (38 titik GloFAS + 7 sub-DAS indeks hujan) dan aturan tingkat peringatan ada di PRD, bagian Sungai yang dipantau dan Aturan bisnis.

## Catatan untuk asisten AI di chat baru

Baca berurutan: `CLAUDE.md`, file ini, lalu dokumen arsitektur dan PRD (tautan di README). Semua keputusan produk sudah disepakati di sana; jangan buka ulang tanpa diminta. Keputusan teknis baru dicatat sebagai ADR di `docs/adr/`.

Workspace cloud asisten tidak bisa menjangkau `proxy.golang.org`; asisten memakai toolchain Go dari rilis GitHub dan mirror modul dari repo GitHub untuk build, lint, dan test. Karena itu `go.sum` dari asisten tidak dipakai: patch dari asisten tidak menyertakan `go.sum`/`go.work.sum`, dan `make deps` di WSL yang membuatnya.

Asisten tidak bisa menjangkau folder WSL secara langsung. File dari asisten dititipkan di `C:\KULIAH\PROJECT CODE\` (di luar folder repo), disalin ke `~/code/siaga` dengan `cp`, dicek hash-nya, lalu file titipan dihapus. Perintah dijalankan pengguna di WSL, dan commit selalu dibuat dari WSL supaya hook lefthook ikut jalan.
