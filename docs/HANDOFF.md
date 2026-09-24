# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 1c selesai di workspace asisten, menunggu verifikasi di WSL (2026-09-24)

Fase 0 dan 1a terverifikasi. Fase 1b terverifikasi di WSL dan CI (hijau setelah `2108fd5`). Irisan 1c sudah ditulis, di-lint, dan dites di workspace asisten, termasuk uji integrasi PostgreSQL + PostGIS, uji ujung ke ujung dengan JetStream, dan replay ingest → NATS 2.11.9 → geo-processor dengan server BMKG tiruan; belum dijalankan di WSL dan CI.

### Fase 1c: peringatan dini cuaca (CAP) dan prakiraan adm4

| Bagian              | Isi                                                                                                                                                                                                                                                                                               | Terverifikasi di workspace asisten                                                                                                                                                 |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Kontrak             | `siaga.raw.v1.WeatherWarning` (CAP 1.2 lengkap) dan `RegionForecast` (20 langkah per 3 jam), subjek `raw.weather.bmkg`, `raw.forecast.bmkg`; `WeatherWarningDetail` diperluas (teks Inggris, msgType, area, jumlah pesan)                                                                         | buf lint, buf breaking terhadap `2108fd5`, generate Go + TS identik, test TS                                                                                                       |
| Ingest              | Konektor `bmkg-cap` (RSS → dokumen CAP id + en, saring provinsi dari ID, terjemahan susulan sebagai revisi), sapuan `bmkg-prakiraan` (5.957 desa, zona fokus dulu, ETag, jeda saat gagal beruntun), jalur prioritas rendah `ReserveLow` di anggaran BMKG, daftar adm4 tersemat (`make adm4-list`) | Coverage domain + app 92–100%; fuzz `FuzzPriorityHeadroom`, `FuzzOrder`, `FuzzParseReferences`, `FuzzParseCAPDocuments`, `FuzzParseForecastDocument`; payload asli BMKG 2026-09-24 |
| Domain `weather`    | Invarian pesan CAP, rantai lewat `references`, ID kejadian dari pendiri rantai, `Derive` (isi, status, tingkat dari severity), wilayah terdampak dengan ambang cakupan                                                                                                                            | Coverage 99,6%; `FuzzDeriveOrderIndependent`                                                                                                                                       |
| Use case `warnings` | Satu transaksi per pesan + advisory lock, gabung rantai yang tersambung belakangan (termasuk menghidupkan lagi kejadian yang pernah digabung), created → expired untuk peringatan yang sudah lewat, kedaluwarsa tiap 30 detik                                                                     | Coverage 91%; `FuzzServiceOrderIndependent` (menemukan dua bug urutan yang sudah diperbaiki, input regresi disimpan)                                                               |
| Database            | `00003_hazard_weather.sql`: `hazard.cap_message`, `cap_reference`, `weather`; `event.area` jadi MultiPolygon untuk semua jenis; `impact_region.coverage`                                                                                                                                          | Naik-turun-naik (00002 + 00003) di PostgreSQL 16 + PostGIS 3.4                                                                                                                     |
| Adapter             | Dekoder `rawweather`, encoder hazard cuaca, `weatherstore` (ST_MakeValid + ST_UnaryUnion, irisan per desa), consumer durable `geo-processor-weather`                                                                                                                                              | Test integrasi `TestWeatherStoreLifecycle`; `TestEndToEnd` kini juga menguji `raw.weather.*` → `hazard.weather.created/expired`                                                    |
| Repo                | ADR 0009–0010, `docs/events.md`, `make adm4-list`, target fuzz baru di `make fuzz` dan CI (nama target di-anchor `^…$` karena paket `bmkg` kini punya tiga target berawalan `FuzzParse`)                                                                                                          | —                                                                                                                                                                                  |

Replay lengkap di workspace asisten: RSS asli 11 peringatan se-Indonesia + rantai CAP sintetis Jawa Barat (Alert Dayeuhkolot → Update Severe Dayeuhkolot + Baleendah → Cancel) disajikan server tiruan. Ingest mengambil hanya dokumen Jawa Barat (id + en), geo-processor membentuk satu kejadian: Waspada, 6 desa Dayeuhkolot (cakupan 0,92–0,98) → Siaga, 17 desa → `retracted`; tiga pesan `hazard.weather.*`, DLQ kosong. Sapuan 6 kode prakiraan: 1 terbit, 5 dicatat 404.

### Verifikasi yang perlu dijalankan di WSL

```bash
make deps              # go.sum ingest/geo-processor (patch tidak membawa go.sum)
make check             # lint + test semua modul
make up migrate        # migrasi 00003
make test-integration  # termasuk TestWeatherStoreLifecycle dan TestEndToEnd (gempa + cuaca)
make fuzz              # opsional, ±10 menit (20 target × 30 detik)
make adm4-list         # harus tanpa diff di services/ingest/internal/adapters/regionlist/data/adm4_32.txt
make ingest            # terminal 1; peringatan Jawa Barat biasanya muncul sore–malam
make geo               # terminal 2; cek http://127.0.0.1:8082/status bagian weather_consumer
```

Setelah jalan: `psql` → `SELECT e.status, e.level, e.title, w.message_count FROM hazard.event e JOIN hazard.weather w ON w.event_id = e.id ORDER BY e.detected_at DESC LIMIT 10;` dan kemajuan sapuan prakiraan di http://127.0.0.1:8081/status bagian `sweeps`.

### Temuan yang perlu ditindaklanjuti

- **Belum ada rekaman CAP Jawa Barat asli.** Saat direkam (2026-09-24 siang) tidak ada peringatan untuk Jawa Barat; uji Update/Cancel memakai dokumen sintetis. Rekam dengan `make ingest-record` saat ada peringatan Jawa Barat, lalu simpan satu rantai sebagai fixture.
- **Ambang cakupan `WEATHER_MIN_COVERAGE` 0,1** belum dikalibrasi; butuh arsip CAP Jawa Barat (fase 1e), bandingkan daftar kecamatan di teks BMKG dengan desa yang lolos ambang.
- **Kode adm4 yang tidak dikenal BMKG**: dari replay belum bisa dinilai (server tiruan). Setelah sapuan penuh pertama di WSL, cek `not_found` dan `not_found_sample` di `/status`; bila banyak, BMKG memakai versi kode Kemendagri yang berbeda dari `ref.region`.
- **Rumus radius dirasakan PRD** dan **recall deduplikasi 98,5%** (dari 1b) masih terbuka, menunggu arsip fase 1e.

### Fase 1b: geo-processor gempa

Consumer durable + DLQ, deduplikasi BMKG–USGS terkalibrasi (ADR 0007–0008), `hazard.event`/`event_source`/`impact_region`/`outbox`, wilayah terdampak, `hazard.quake.created|updated|expired`, alat kalibrasi (`make calibrate-dedup`).

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

- Compose lite memakai `nats:2.11-alpine`, sedangkan test memakai server 2.15 dalam proses. Replay 1c sudah dijalankan dengan binary `nats-server` 2.11.9 tanpa masalah; naikkan image saat Renovate aktif.
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
- [x] **1c** BMKG CAP nowcast (RSS + dokumen CAP id/en) dan sapuan prakiraan adm4 (5.957 desa, jalur prioritas rendah 50/menit di anggaran BMKG) (ADR 0009). geo-processor: kejadian `weather` dari rantai pesan CAP, wilayah terdampak dari irisan poligon (ADR 0010).
- [ ] **1d** Open-Meteo (cuaca grid 0,25°, kualitas udara, banjir 38 titik), OpenAQ, NASA FIRMS (key gratis lewat SOPS), hypertable `ts.*`, termasuk consumer `raw.forecast.>` → `ts.weather_forecast` (prakiraan BMKG sudah terbit sejak 1c).
- [ ] **1e** Arsip ke Garage, uji replay dari arsip (termasuk set berlabel untuk T4 dan kalibrasi radius dirasakan), OpenTelemetry (trace ID di header `traceparent`) + dashboard Grafana Cloud, Dockerfile + manifest ingest dan geo-processor.

Titik pantau sungai (38 titik GloFAS + 7 sub-DAS indeks hujan) dan aturan tingkat peringatan ada di PRD, bagian Sungai yang dipantau dan Aturan bisnis.

## Catatan untuk asisten AI di chat baru

Baca berurutan: `CLAUDE.md`, file ini, lalu dokumen arsitektur dan PRD (tautan di README). Semua keputusan produk sudah disepakati di sana; jangan buka ulang tanpa diminta. Keputusan teknis baru dicatat sebagai ADR di `docs/adr/`.

Workspace cloud asisten tidak bisa menjangkau `proxy.golang.org`, `go.dev`, atau API GitHub, tetapi bisa `git clone` dari GitHub dan `apt`/`npm`/`pip`. Cara kerja yang terbukti di sesi 1b–1c:

- Go 1.27.1 di-build dari tag `go1.27.1` repo `golang/go` (bootstrap Go 1.24 bawaan).
- Modul Go dengan path non-GitHub (`golang.org/x/*`, `google.golang.org/protobuf`) diganti lewat `replace` di file `go.work` sementara di luar repo (`GOWORK=...`), menunjuk clone mirror GitHub-nya; modul yang tidak dikompilasi cukup diberi `go.mod` kosong. `GOPROXY=direct GOSUMDB=off`.
- golangci-lint 2.13.2 di-build dari source dengan cara yang sama (linter `decorder` dibuang karena hostnya GitLab; tidak dipakai config SIAGA).
- `protoc-gen-go` v1.36.10 di-build dari mirror, dipakai lewat template `buf.gen.yaml` sementara; hasil generate identik dengan CI.
- PostgreSQL 16 + PostGIS 3.4 dari apt untuk test integrasi (tanpa TimescaleDB/h3/pgvector; migrasi gempa dan cuaca tidak memakainya).
- Host BMKG (`www.bmkg.go.id`, `api.bmkg.go.id`) tidak selalu terjangkau dari workspace; replay memakai server tiruan (`BMKG_CAP_BASE_URL`, `BMKG_FORECAST_URL`) dan binary `nats-server` dari rilis GitHub.
- Menjalankan skrip pihak ketiga yang diunduh (misal `extract_tsv.py` repo-gempa) ditolak kebijakan workspace; pakai data yang sudah jadi.

Karena itu `go.sum` dari asisten tidak dipakai: patch tidak menyertakan `go.sum`/`go.work.sum`, dan `make deps` di WSL yang membuatnya.

Asisten bisa menulis ke `C:\KULIAH\PROJECT CODE\` lewat bridge desktop app (bukan ke folder WSL). File dari asisten dititipkan di sana (di luar folder repo), disalin ke `~/code/siaga`, dicek hash-nya, lalu file titipan dihapus. Perintah dijalankan pengguna di WSL, dan commit selalu dibuat dari WSL supaya hook lefthook ikut jalan.
