# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 1b selesai di workspace asisten, menunggu verifikasi di WSL (2026-09-24)

Fase 0 terverifikasi (CI hijau sejak `a91f17d`). Fase 1a (ingest gempa) sudah di-commit dari WSL (`d5068c8`, `e396d6f`). Irisan 1b sudah ditulis, di-lint, dan dites di workspace asisten, termasuk uji integrasi PostgreSQL + PostGIS, uji ujung ke ujung dengan JetStream, dan replay payload BMKG/USGS asli lewat ingest → geo-processor; belum dijalankan di WSL dan CI.

### Fase 1b: geo-processor gempa

| Bagian         | Isi                                                                                                                                                                                                                                              | Terverifikasi di workspace asisten                                                                                                 |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| Kontrak        | `HazardExpired.reason/merged_into_hazard_id/revision`, `Hazard.revision/impacted_region_count`, `EarthquakeDetail.felt_radius_km/magnitude_type`, `SourceReport.depth_km/source_url`; stream `DLQ` milik bersama, `DLQSubject`, `subjects.dlq()` | buf lint, buf breaking terhadap `e396d6f`, generate Go + TS identik, test TS                                                       |
| Domain `quake` | Penyimpanan laporan tanpa bergantung urutan, gabungan feed, pengelompokan BMKG–USGS dengan `Settle`, radius dirasakan, tingkat PRD, digest isi                                                                                                   | Coverage 98,9%; 6 target fuzz, termasuk properti "satu kejadian per gempa untuk urutan apa pun" dan invarian saat gempa berdesakan |
| Use case       | `quakes` (satu transaksi per pesan + advisory lock, outbox), `consume` (ack/retry/DLQ), `relay`, kedaluwarsa 6 jam, `calibrate`                                                                                                                  | Coverage domain + app 96,9%; fuzz tingkat use case dengan penyimpanan memori                                                       |
| Database       | `00002_hazard_quake.sql`: `hazard.event`, `quake`, `event_source`, `impact_region`, `outbox`, grant baca untuk layanan lain                                                                                                                      | Naik-turun-naik di PostgreSQL 16 + PostGIS 3.4; uji constraint menolak invarian yang dilanggar                                     |
| Adapter        | pgx (`quakestore`), JetStream (consumer durable, DLQ, publisher), dekoder raw, encoder hazard, `/healthz` `/readyz` `/status`                                                                                                                    | Test dengan server JetStream dalam proses; test integrasi PostgreSQL; uji ujung ke ujung gempa Cianjur 2022                        |
| Kalibrasi      | `cmd/calibrate-dedup`, `scripts/fetch-calibration-data.sh`, `docs/calibration/dedup-gempa.md`, ADR 0008                                                                                                                                          | Laporan dihasilkan dari data asli; angka sama dengan analisis Python terpisah                                                      |
| Repo           | `make geo`, `make calibrate-dedup`, fuzz baru di `make fuzz` dan CI, `test-integration` dengan `-p 1`, ADR 0007–0008, `docs/events.md`                                                                                                           | —                                                                                                                                  |

Replay payload asli (fixture ingest 1a) lewat ingest → NATS → geo-processor: 6 laporan → 6 kejadian, 6 `hazard.quake.created`, lalu 6 `expired` setelah masa aktif habis, DLQ kosong.

### Verifikasi yang perlu dijalankan di WSL

```bash
make deps              # go.mod/go.sum geo-processor (nats.go, protobuf, contracts jadi dependensi langsung)
make check             # lint + test semua modul
make up migrate        # migrasi 00002
make test-integration  # PostgreSQL + JetStream, termasuk uji ujung ke ujung
make fuzz              # opsional, ±7 menit
make ingest            # terminal 1
make geo               # terminal 2; cek http://127.0.0.1:8082/status
make calibrate-dedup   # harus menghasilkan docs/calibration/dedup-gempa.md tanpa diff
```

Setelah `make ingest` dan `make geo` jalan beberapa menit: `psql` → `SELECT status, level, title FROM hazard.event ORDER BY occurred_at DESC LIMIT 10;` dan stream `HAZARD` di http://127.0.0.1:8222/jsz?streams=true.

### Temuan yang perlu ditindaklanjuti

- **Rumus radius dirasakan PRD terlalu kecil untuk gempa menengah.** Di fixture asli, gempa M4,6 kedalaman 20 km di selatan Sumur dilaporkan BMKG "dirasakan II–III", tetapi rumus PRD memberi radius 0 (R = 19,95 km < kedalaman). Kalibrasi rumus dengan arsip `gempadirasakan` perlu dijadwalkan (PRD memang menyebutnya).
- **Recall deduplikasi 98,5%**, di bawah target T4 99%. Sisa yang terlewat adalah pasangan berjarak > 100 km. Kalibrasi berikutnya memakai arsip real-time ingest (fase 1e). Detail di ADR 0008.

### Fase 1a: ingest gempa

Kontrak `siaga.raw.v1`, stream `RAW`/`HAZARD`, layanan ingest dengan konektor BMKG `autogempa`, `gempaterkini`, `gempadirasakan` dan USGS `2.5_day`, perekam payload (`make ingest-record`). Detail di ADR 0006 dan `docs/events.md`. Status CI commit `e396d6f` belum dicek asisten (workspace tidak bisa membuka API GitHub).

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

## Utang kecil yang diketahui

- Compose lite memakai `nats:2.11-alpine`, sedangkan test memakai server 2.15 dalam proses. Fitur yang dipakai (dedup `Nats-Msg-Id`, `Nats-Expected-Stream`, pull consumer, `NakWithDelay`) ada di keduanya; naikkan image saat Renovate aktif.
- ingest dan geo-processor belum punya Dockerfile dan manifest Kubernetes; dijalankan lewat `make ingest` dan `make geo`. Ditambahkan di fase 1e bersama Garage dan OpenTelemetry.
- `/status` geo-processor menampilkan ambang dengan durasi dalam nanodetik (JSON bawaan `time.Duration`). Kosmetik; rapikan saat dashboard Grafana dibuat.
- Renovate sudah dikonfigurasi (`renovate.json`) tetapi GitHub App Renovate belum dipasang di repo.
- Runner `ubuntu-latest` pindah ke Ubuntu 26 mulai 2026-10-19. Pantau run CI pertama setelah tanggal itu.
- Profil full (`make k3d-up tilt`) belum pernah dicoba. Bila CloudNativePG menolak image, lihat ADR 0002.
- `.trivyignore.yaml` mengecualikan CVE-2025-68121 untuk gosu sampai 2027-03-31; setelah itu CI merah dan harus ditinjau ulang.

## Berikutnya: sisa Fase 1 (pipa data)

Target demo fase 1: dashboard Grafana berisi data live semua sumber.

- [x] **1a** Kontrak `raw.*`, stream `RAW`/`HAZARD`, ingest dengan konektor gempa BMKG + USGS, perekam payload.
- [x] **1b** geo-processor gempa: consumer durable + DLQ, deduplikasi BMKG–USGS terkalibrasi (ADR 0007–0008), `hazard.event`/`event_source`/`impact_region`/`outbox`, wilayah terdampak, `hazard.quake.created|updated|expired`.
- [ ] **1c** BMKG CAP nowcast (RSS + XML CAP) dan prakiraan adm4 (sapuan ±5.900 desa, sub-anggaran 50/menit di dalam anggaran BMKG). geo-processor: kejadian `weather` dari poligon CAP (tabel `hazard.event` sudah umum; `event_source` perlu kolom CAP atau tabel detail sendiri).
- [ ] **1d** Open-Meteo (cuaca grid 0,25°, kualitas udara, banjir 38 titik), OpenAQ, NASA FIRMS (key gratis lewat SOPS), hypertable `ts.*`.
- [ ] **1e** Arsip ke Garage, uji replay dari arsip (termasuk set berlabel untuk T4 dan kalibrasi radius dirasakan), OpenTelemetry (trace ID di header `traceparent`) + dashboard Grafana Cloud, Dockerfile + manifest ingest dan geo-processor.

Titik pantau sungai (38 titik GloFAS + 7 sub-DAS indeks hujan) dan aturan tingkat peringatan ada di PRD, bagian Sungai yang dipantau dan Aturan bisnis.

## Catatan untuk asisten AI di chat baru

Baca berurutan: `CLAUDE.md`, file ini, lalu dokumen arsitektur dan PRD (tautan di README). Semua keputusan produk sudah disepakati di sana; jangan buka ulang tanpa diminta. Keputusan teknis baru dicatat sebagai ADR di `docs/adr/`.

Workspace cloud asisten tidak bisa menjangkau `proxy.golang.org`, `go.dev`, atau API GitHub, tetapi bisa `git clone` dari GitHub dan `apt`/`npm`/`pip`. Cara kerja yang terbukti di sesi 1b:

- Go 1.27.1 di-build dari tag `go1.27.1` repo `golang/go` (bootstrap Go 1.24 bawaan).
- Modul Go dengan path non-GitHub (`golang.org/x/*`, `google.golang.org/protobuf`) diganti lewat `replace` di file `go.work` sementara di luar repo (`GOWORK=...`), menunjuk clone mirror GitHub-nya; modul yang tidak dikompilasi cukup diberi `go.mod` kosong. `GOPROXY=direct GOSUMDB=off`.
- golangci-lint 2.13.2 di-build dari source dengan cara yang sama (linter `decorder` dibuang karena hostnya GitLab; tidak dipakai config SIAGA).
- `protoc-gen-go` v1.36.10 di-build dari mirror, dipakai lewat template `buf.gen.yaml` sementara; hasil generate identik dengan CI.
- PostgreSQL 16 + PostGIS 3.4 dari apt untuk test integrasi (tanpa TimescaleDB/h3/pgvector; migrasi gempa tidak memakainya).
- Menjalankan skrip pihak ketiga yang diunduh (misal `extract_tsv.py` repo-gempa) ditolak kebijakan workspace; pakai data yang sudah jadi.

Karena itu `go.sum` dari asisten tidak dipakai: patch tidak menyertakan `go.sum`/`go.work.sum`, dan `make deps` di WSL yang membuatnya.

Asisten bisa menulis ke `C:\KULIAH\PROJECT CODE\` lewat bridge desktop app (bukan ke folder WSL). File dari asisten dititipkan di sana (di luar folder repo), disalin ke `~/code/siaga`, dicek hash-nya, lalu file titipan dihapus. Perintah dijalankan pengguna di WSL, dan commit selalu dibuat dari WSL supaya hook lefthook ikut jalan.
