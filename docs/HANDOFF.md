# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 1e-2a selesai di workspace asisten, menunggu verifikasi di WSL (2026-09-25)

Fase 0 sampai 1e-1 ter-commit (1e-1 di `2da995a`). Fase 1e dipecah: **1e-1** arsip Garage + replay, **1e-2a** OpenTelemetry + dashboard (bagian ini), **1e-2b** deploy (Dockerfile, manifest, collector, SOPS, retensi dan backup arsip), **1e-3** backfill + kalibrasi (lihat Berikutnya).

### Fase 1e-2a: OpenTelemetry dan dashboard pipa data

| Bagian        | Isi                                                                                                                                                                                                                                                                                   | Terverifikasi di workspace asisten                                                                                                                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Platform      | `otelx` (SDK dari env standar OTLP/HTTP, noop tanpa endpoint, resource `siaga/<layanan>`, metrik runtime Go, galat ekspor dibatasi), `natsx` (carrier header, span `send`/`process`, gauge stream dan consumer JetStream), `logx` (`trace_id`/`span_id`, handler OTLP tambahan)       | Exporter terhadap server OTLP tiruan (ketiga sinyal + header auth); `FuzzValidTraceParent` (sama dengan constraint database); trace melintasi NATS dalam proses                                                            |
| ingest        | Adapter `telemetry`: span per polling, per kode sapuan, per request HTTP (URL tersamar), per tulis arsip; histogram `http.client.request.duration`; gauge umur data, kegagalan beruntun, sisa anggaran; header `Siaga-Fetched-At` dan `traceparent` di `raw.*`; `ratelimit.Available` | Coverage adapter 99%; `FuzzAvailable`; `TestPublishPropagatesTrace`                                                                                                                                                        |
| geo-processor | Span consumer lanjut dari header, histogram pemrosesan/antrean/latensi pipa, span dan histogram per query (`pgx.QueryTracer`), `hazard.outbox.traceparent` (migrasi 00006) diteruskan relay, gauge outbox dan metrik dari statistik consumer                                          | `TestTraceFromRawToHazard` (PostgreSQL asli: trace `raw.*` → query → outbox → `hazard.quake.created`), `TestOutboxTraceParentAndBacklog`, migrasi naik-turun-naik                                                          |
| Grafana       | Profil Compose `obs` (`grafana/otel-lgtm:0.34.0`, Grafana di `:3300`, OTLP di `:4318`), `make obs-up`/`obs-down`, dashboard "SIAGA — Pipa data" (31 panel) di-provision, panduan Grafana Cloud                                                                                        | ingest + geo-processor jalan terhadap sumber tiruan, NATS, PostgreSQL, dan Prometheus 3.14 / Tempo 3.0.3 / Loki 3.7.8 (versi di image): semua query dashboard mengembalikan data, trace gempa lengkap 20 span dalam ±90 ms |
| Repo          | ADR 0015, `docs/events.md` (header), `docs/setup/grafana-cloud.md`, README, `.env.example`, target fuzz baru di `make fuzz` dan CI                                                                                                                                                    | —                                                                                                                                                                                                                          |

### Verifikasi yang perlu dijalankan di WSL (1e-2a)

```bash
bash "/mnt/c/KULIAH/PROJECT CODE/siaga-titipan/terapkan-fase-1e-2a.sh"   # deps, check, commit
make up migrate        # migrasi 00006
make test-integration  # termasuk TestTraceFromRawToHazard
make obs-up            # unduh image otel-lgtm (±1 GB) lalu jalankan
# isi OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 di .env, lalu:
make ingest            # terminal 1; log start: "trace":true,"metrics":true,"logs":true
make geo               # terminal 2
```

Buka http://127.0.0.1:3300 (dashboard "SIAGA — Pipa data" jadi halaman utama). Setelah ±5 menit: panel keterlambatan sumber terisi semua konektor, dan tabel "Trace gempa terbaru" berisi trace yang bisa diklik (menu Explore → Tempo juga bisa mencari `{name="poll bmkg-autogempa"}`). Tampilan panel di Grafana 13 belum pernah dilihat asisten (hanya query-nya yang diuji), jadi laporkan panel yang kosong atau berantakan.

### Temuan yang perlu ditindaklanjuti (1e-2a)

- **Keputusan retensi arsip masih menunggu data seminggu** (temuan 1e-1); pindah ke 1e-2b bersama backup ke Oracle Object Storage. Panel "Arsip payload mentah" kini menunjukkan byte per detik yang ditulis.
- Pesan `hazard.*.expired` dari putaran kedaluwarsa tidak punya trace induk (putaran berjalan di luar pemrosesan pesan); span penerbitannya menjadi root sendiri.
- Bridge log (`otel/log`, `otel/sdk/log`, `otlploghttp`, `otelslog`) masih v0.x di OpenTelemetry Go; API-nya bisa berubah saat Renovate menaikkan versi.
- `elapsed` di log polling tercetak dalam nanodetik (JSON bawaan `slog.Duration`), sama dengan utang `/status`; histogram `siaga_ingest_poll_duration_seconds` sudah dalam detik.
- Metrik server NATS dan Garage belum dikumpulkan (butuh collector di cluster, 1e-2b).

### Fase 1e-1: arsip di Garage dan replay dari arsip

| Bagian         | Isi                                                                                                                                                                                                                               | Terverifikasi di workspace asisten                                                                                                                              |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Garage         | Service `garage` v2.3.0 di Compose lite (`--single-node --default-bucket`, `deploy/compose/garage.toml`), bucket `siaga-arsip`, awalan `raw/`; rahasia dan access key di `.env`; `make env` menambah variabel baru ke `.env` lama | `make-env.sh` diuji untuk `.env` baru, `.env` lama, dan `.env` lengkap. Garage asli belum pernah jalan (image Docker tidak terjangkau dari workspace)           |
| Port + adapter | `ports.ArchiveReader`/`ArchiveStore`; `s3archive` (aws-sdk-go-v2, Content-MD5, checksum CRC `WhenRequired`); `fsarchive` bisa `Get`/`List`; `archiveurl` (`INGEST_ARCHIVE_URL` = folder, `file://`, atau `s3://…?endpoint=`)      | Uji kesesuaian bersama `archivetest` untuk folder dan S3 tiruan dalam proses (paginasi, galat 500, retry); uji integrasi Garage ditulis tetapi belum dijalankan |
| Domain         | `domain/archivekey` (bentuk kanonik kunci, batas rentang waktu), `emit.Unarchive` (gzip + SHA-256)                                                                                                                                | `FuzzParse`; coverage 97%                                                                                                                                       |
| Replay         | Use case `app/replay` + perintah `replay` (11 feed satu payload, gabung urut waktu ambil, FetchMeta asli, `-speed`, `-publish=false`, `-strict`, `-json`)                                                                         | Coverage 99%; `FuzzRunOrdered`; `cmd/replay` memutar rekaman asli BMKG/USGS/FIRMS ke JetStream dalam proses, replay kedua 0 pesan baru                          |
| Alat arsip     | Use case `app/archivetool` + perintah `archive` (`ls`, `verify`, `cp`); `make archive-ls`, `archive-verify`, `archive-upload`, `replay`                                                                                           | Salin folder → S3 tiruan → folder, verifikasi objek rusak dan kunci asing                                                                                       |
| Ingest         | Arsip dibuka dan dicek sebelum polling pertama (gagal start bila Garage mati atau kredensial salah); `INGEST_ARCHIVE_DIR` lama tetap diterima                                                                                     | `TestConfigArchive`, `TestOpenArchive`                                                                                                                          |
| Repo           | ADR 0014, README, `docs/events.md`, `.env.example`, job CI `archive` (Garage asli + uji integrasi), dua target fuzz baru                                                                                                          | —                                                                                                                                                               |

### Verifikasi yang perlu dijalankan di WSL (1e-1)

```bash
bash "/mnt/c/KULIAH/PROJECT CODE/siaga-titipan/terapkan-fase-1e-1.sh"   # deps, check, commit
make up                # menambah variabel Garage ke .env lalu menjalankan Garage
docker compose -f deploy/compose/compose.lite.yaml --env-file .env exec garage /garage bucket info siaga-arsip
make test-integration  # kini juga TestGarage (uji kesesuaian terhadap Garage asli, >1.000 objek)
make archive-upload    # pindahkan arsip lokal fase 1a–1d ke Garage (aman diulang)
make ingest            # log harus menyebut "arsip payload aktif" lokasi s3://siaga-arsip/raw/
make archive-ls        # setelah beberapa menit: objek bertambah per konektor
make replay ARGS="-publish=false -strict"   # semua payload yang bisa diputar harus terbaca
```

Uji replay ke NATS sungguhan sebaiknya di stack terpisah atau setelah `make reset` (lihat ADR 0014, Konsekuensi).

### Temuan yang perlu ditindaklanjuti (1e-1)

- **Kuota Open-Meteo ternyata dibagi jumlah variabel.** Kode Open-Meteo (`calculateQueryWeight`): bobot per lokasi = max(1, hari/14 × variabel/10). Reanalisis GloFAS 1984–2022 dengan satu variabel (`river_discharge`, `models=consolidated_v4`) hanya ±102 panggilan per titik (±3.900 untuk 38 titik), bukan ±1.000 per titik seperti catatan 1d-1. Backfill cukup beberapa menit (batas 600/menit), bukan berhari-hari.
- **Replay CAP, prakiraan BMKG, dan OpenAQ belum bisa**: konteks request (entri RSS, kode desa di URL, gabungan daftar lokasi + nilai terbaru) tidak ada di kunci arsip. Perlu metadata objek sebelum alert-engine fase 3 (T3 butuh replay CAP).
- **Ukuran arsip belum diukur.** Sapuan prakiraan BMKG kemungkinan penyumbang terbesar; lihat `make archive-ls` setelah seminggu, lalu putuskan retensi di 1e-2.
- Errorlint di workspace asisten dibangun dari mirror GitHub v1.8.0 (v1.9.0 hanya di Codeberg yang diblokir); CI memakai golangci-lint 2.13.2 resmi, jadi temuan errorlint versi baru baru terlihat di CI.

### Fase 1d-2: OpenAQ dan NASA FIRMS

| Bagian   | Isi                                                                                                                                                                                                                                              | Terverifikasi di workspace asisten                                                                                      |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------- |
| Kontrak  | `siaga.raw.v1.AirQualityObservation` (stasiun + nilai terbaru per sensor), `FireDetection` (+ enum `FireConfidence`); subjek `raw.aq.openaq`, `raw.fire.firms`                                                                                   | buf lint, generate Go + TS, test TS                                                                                     |
| Ingest   | Use case `stations` (daftar lokasi → `/latest` hanya untuk stasiun aktif yang `datetimeLast`-nya berubah), adapter `openaq`, konektor `firms-*` (4 produk NRT, CSV, jendela 2 hari), domain `airquality`, `fire`, `area`; key opsional lewat env | Coverage domain + app 97–100%; `FuzzClean`, `FuzzParseFIRMS`, `FuzzParseOpenAQ`; `TestPlanKeyedSources`                 |
| Rahasia  | `ports.Request.Header` dan `Secrets`; `httpfetch` menyamarkan key di galat `url.Error` dan isi respons; `Request.Redacted()` di pesan galat use case                                                                                             | `TestFetchSecrets`, `TestListFailures`; gitleaks 8.30.1 bersih (key palsu di test sengaja berentropi rendah)            |
| Database | `00005_ts_observation.sql`: `ts.site` jenis `station`, `ts.station`, `ts.series` dataset `aq_observation`, hypertable `aq_observation` dan `hotspot` (ID dicek sama dengan isi), tampilan `aq_observation_current`, `hotspot_recent`             | Naik-turun-naik dengan data; `TestObservationStoreLifecycle`, `TestHotspotStoreLifecycle`, `TestObservationConstraints` |
| Consumer | `geo-processor-aq-openaq`, `geo-processor-fire-firms` → use case `timeseries` (`Observation`, `Hotspot`)                                                                                                                                         | `TestEndToEnd` kini juga menguji keduanya, pesan ulangan, dan DLQ                                                       |
| Repo     | ADR 0013, `docs/events.md`, README, `.env.example`, target fuzz baru di `make fuzz` dan CI                                                                                                                                                       | —                                                                                                                       |

Status: ter-commit di `70b4700`, CI hijau. Replay di workspace asisten dengan rekaman asli: ingest `-once` terhadap server tiruan membaca 31 lokasi OpenAQ dan hanya mengirim 2 request `/latest` (dua stasiun aktif), lalu menerbitkan 2 stasiun dan 162 titik panas (VIIRS SNPP 30, NOAA-20 72, NOAA-21 57, MODIS 3; tanpa penolakan). geo-processor menyimpan semuanya tanpa galat dan DLQ kosong; 147 titik panas jatuh di desa Jawa Barat (terbanyak Kab. Sukabumi, Tasikmalaya, Sumedang), sisanya di Banten/Jawa Tengah/laut. Key tidak ada di arsip maupun log.

Perbaikan kecil di luar 1d-2: `TestEndToEnd` sesekali gagal karena putaran kedaluwarsa gempa 2001 bisa berjalan sebelum laporan USGS diproses (revisi 3, bukan 2); kini revisi yang diharapkan dihitung dari urutan pesan `expired`.

### Verifikasi 1d-2 di WSL

```bash
bash "/mnt/c/KULIAH/PROJECT CODE/siaga-titipan/terapkan-fase-1d-2.sh"   # deps, check, migrasi 00005, test integrasi, commit
# isi OPENAQ_API_KEY dan FIRMS_MAP_KEY di .env, lalu:
make ingest            # terminal 1; cek http://127.0.0.1:8081/status: openaq-stasiun dan firms-*
make geo               # terminal 2; cek http://127.0.0.1:8082/status bagian series_consumers
```

Setelah jalan: `psql` → `SELECT station_name, provider, parameter, unit, value, observed_at FROM ts.aq_observation_current ORDER BY 1;` dan `SELECT product, count(*), max(detected_at) FROM ts.hotspot_recent GROUP BY 1;`.

### Temuan yang perlu ditindaklanjuti (1d-2)

- **OpenAQ nyaris kosong untuk Jawa Barat.** Dari 31 lokasi di kotak, hanya 2 yang melapor dalam 48 jam: AirGradient "Griya Tugu Asri" (Depok, PM2,5) dan "BMKG 1" (Jakarta, di luar Jawa Barat). Tidak ada stasiun aktif di Bandung Raya; 7 lokasi tanpa `datetimeLast`, sisanya mati sejak 2016–2026. Koreksi bias dan interpolasi fase 4 praktis bergantung pada CAMS; sesuai risiko "Sensor OpenAQ di Bandung sedikit" di dokumen arsitektur, dan perlu dipertimbangkan sumber sensor gratis lain sebelum fase 4.
- **Kotak Jawa Barat ikut menutup Jakarta dan sebagian Banten.** Stasiun dan titik panas di luar provinsi tetap disimpan dengan `region_code` NULL. Wajar untuk konteks asap lintas batas; saring dengan `region_code LIKE '32.%'` bila hanya perlu Jawa Barat.
- MODIS kadang mendeteksi di kawasan padat (rekaman: dua dari tiga titik MODIS di Kab. Bandung dan Kab. Sumedang, keyakinan 58–60%); sebagian titik panas VIIRS/MODIS di perkotaan adalah atap panas atau industri, bukan kebakaran. Relevan saat kalibrasi ambang titik api di fase 1e.
- **Ambang tingkat titik api** belum dipakai; `hazard.fire.*` menunggu arsip FIRMS (fase 1e).
- Backfill jam yang terlewat saat ingest mati (`/v3/sensors/{id}/hours`) belum ada; masuk fase 1e.

### Fase 1d-1: Open-Meteo dan deret waktu `ts.*`

| Bagian                | Isi                                                                                                                                                                                                                                                 | Terverifikasi di workspace asisten                                                                                        |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Kontrak               | `siaga.raw.v1.ModelSite`, `GridWeatherForecast`, `AirQualityForecast`, `RiverDischargeForecast`; subjek `raw.forecast.openmeteo`, `raw.aq.openmeteo`, `raw.flood.openmeteo`                                                                         | buf lint, buf breaking terhadap `bfd3099`, generate Go + TS identik dengan CI                                             |
| Ingest                | Konektor `openmeteo-cuaca` (grid 0,25°, per jam), `openmeteo-udara` (CAMS, per jam), `openmeteo-sungai` (GloFAS, 6 jam); satu request untuk semua titik, jendela tanggal UTC tetap; `domain/series` (variabel, satuan, batas); anggaran `openmeteo` | Coverage domain + app 95–100%; `FuzzParse` (openmeteo); payload asli 2026-09-24; `TestOpenMeteoQuota` (3.512 lokasi/hari) |
| Titik pantau          | `make grid-list` (70 simpul dari batas desa, `import-regions -grid-out`), `make river-snap` (sel GloFAS alur utama terdekat, laporan `docs/calibration/titik-sungai.md`), sumber `docs/calibration/titik-sungai-32.csv`                             | `river-snap` dijalankan terhadap rekaman asli sel di sekitar 38 titik                                                     |
| Domain `series` (geo) | Invarian titik, deret, langkah, urutan ensemble dalam presisi `real`                                                                                                                                                                                | Coverage 99%; `FuzzWeatherValidate`                                                                                       |
| Database              | `00004_ts_series.sql`: `ts.site`, `ts.series`, hypertable `weather_forecast`, `aq_forecast`, `river_discharge` (columnstore 7 hari, retensi 1 tahun), tampilan `*_current`                                                                          | Naik-turun-naik; `TestSeriesStoreLifecycle`, `TestSeriesConstraints`                                                      |
| Consumer              | `geo-processor-forecast-bmkg`, `-forecast-openmeteo`, `-aq-openmeteo`, `-flood-openmeteo` → use case `timeseries` → `SeriesStore` (satu transaksi per pesan, `unnest`)                                                                              | `TestEndToEnd` kini juga menguji keempat subjek, pesan ulangan, dan DLQ                                                   |
| Repo                  | ADR 0011–0012, `docs/events.md`, README, `.env.example`, target fuzz baru di `make fuzz` dan CI                                                                                                                                                     | —                                                                                                                         |

Replay di workspace asisten: ingest `-once` terhadap server tiruan (rekaman Open-Meteo 2026-09-24 dan prakiraan BMKG dari 1c) menerbitkan 70 + 70 + 38 titik Open-Meteo dan 3 desa BMKG (2 kode lain 404); geo-processor menyimpan 6.720 baris cuaca grid, 6.720 baris udara, 532 baris debit, 60 baris prakiraan BMKG, DLQ kosong. 48 dari 70 simpul grid dan 36 dari 38 titik sungai berada di dalam desa `ref.region`.

### Verifikasi 1d-1 di WSL

```bash
make deps              # go.sum ingest/geo-processor (patch tidak membawa go.sum)
make check             # lint + test semua modul
make up migrate        # migrasi 00004 (butuh TimescaleDB di image, sudah ada sejak fase 0)
make test-integration  # termasuk TestSeriesStoreLifecycle dan TestEndToEnd (gempa + cuaca + deret waktu)
make fuzz              # opsional, ±12 menit (23 target × 30 detik)
make grid-list         # harus tanpa diff di services/ingest/internal/adapters/sitelist/data/grid025_32.txt
make ingest            # terminal 1
make geo               # terminal 2; cek http://127.0.0.1:8082/status bagian series_consumers
```

Setelah jalan beberapa menit: `psql` → `SELECT dataset, model, count(*), max(last_fetched_at) FROM ts.series GROUP BY 1, 2;` (harus ada `weather/best_match` 70, `air_quality/cams_global` 70, `discharge/glofas_v4` 38, dan `weather/bmkg` bertambah sejalan sapuan prakiraan). Consumer `geo-processor-forecast-bmkg` membaca stream `RAW` dari awal, jadi prakiraan BMKG yang sudah terbit sejak 1c langsung tersimpan.

### Temuan yang perlu ditindaklanjuti (1d-1)

- **17 dari 38 titik sungai perlu diperiksa manual** (tanda di `docs/calibration/titik-sungai.md`). Koordinat awal adalah perkiraan dari nama lokasi, dan debit musim kemarau kecil membuat alur utama sulit dibedakan (misal Cikeas dan Ciliwung di Depok memakai sel yang sama, dan beberapa titik muara jatuh di sel yang debitnya lebih kecil dari titik hulunya). Cara: buka sel di peta OSM, tulis koordinat sel yang benar di `docs/calibration/titik-sungai-32.csv` dengan radius 0 dan catatan asal verifikasinya, lalu `make river-snap`. Titik bertanda belum boleh dipakai untuk ambang banjir.
- **Batas Open-Meteo 600/menit dihitung per lokasi.** Rekaman pertama kena HTTP 429 tanpa `Retry-After`. Polling rutin memakai 178 lokasi sekaligus; `river-snap` membatasi diri 400 lokasi/menit. Jangan menjalankan `river-snap` dua kali bersamaan.
- **Ambang banjir belum ada.** Persentil reanalisis GloFAS 1984–2022 (±102 panggilan per titik, lihat temuan 1e-1); masuk fase 1e-3. `hazard.flood.*` belum diterbitkan.
- **Grid 70 simpul**, bukan ±120 seperti perkiraan dokumen arsitektur: hanya sel yang menyentuh desa Jawa Barat.
- **CAMS global** memberi PM2,5 ±135 µg/m³ di Bandung pada 00 UTC rekaman; wajar sebagai model mentah, bahan koreksi bias fase 4.
- Temuan 1c masih terbuka: rekaman CAP Jawa Barat asli, kalibrasi `WEATHER_MIN_COVERAGE`, cek `not_found` sapuan prakiraan BMKG; dari 1b: rumus radius dirasakan dan recall deduplikasi 98,5%.

### Fase 1c: peringatan dini cuaca (CAP) dan prakiraan adm4

Konektor `bmkg-cap` (RSS → CAP id + en) dan sapuan `bmkg-prakiraan` (5.957 desa, jalur prioritas rendah di anggaran BMKG), kejadian `weather` dari rantai pesan CAP dengan wilayah terdampak dari irisan poligon, `hazard.weather.created|updated|expired` (ADR 0009–0010).

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

- Compose lite memakai `nats:2.11-alpine`, sedangkan test memakai server 2.15 dalam proses. Replay 1c dan 1d-1 sudah dijalankan dengan binary `nats-server` 2.11.9 tanpa masalah; naikkan image saat Renovate aktif.
- ingest dan geo-processor belum punya Dockerfile dan manifest Kubernetes; dijalankan lewat `make ingest` dan `make geo`. Garage dan Grafana lokal juga baru ada di Compose lite. Semuanya masuk fase 1e-2b.
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
- [x] **1d-1** Open-Meteo (cuaca grid 0,25°, kualitas udara CAMS, debit GloFAS 38 titik) dan schema `ts` (`site`, `series`, hypertable `weather_forecast`, `aq_forecast`, `river_discharge`), consumer `raw.forecast.bmkg` → `ts.weather_forecast` (ADR 0011–0012).
- [x] **1d-2** OpenAQ v3 (stasiun dalam kotak Jawa Barat tiap 15 menit, `X-API-Key`) → `raw.aq.openaq` → `ts.aq_observation`; NASA FIRMS (VIIRS/MODIS NRT, satu kotak tiap 30 menit, MAP_KEY) → `raw.fire.firms` → `ts.hotspot`; key opsional di `.env`, disamarkan di galat dan log (ADR 0013). Menunggu rekaman asli.
- [x] **1e-1** Arsip payload mentah ke Garage (Compose lite, adapter S3, `INGEST_ARCHIVE_URL`), perintah `archive` (ls, verify, cp) dan `replay` dari arsip ke NATS urut waktu ambil (ADR 0014). Menunggu verifikasi di WSL.
- [x] **1e-2a** OpenTelemetry di ingest dan geo-processor (trace lewat header `traceparent` NATS dan kolom outbox, metrik sumber/kuota/antrean/latensi/query, log OTLP), Grafana lokal `make obs-up` dan dashboard pipa data, panduan Grafana Cloud (ADR 0015). Menunggu verifikasi di WSL.
- [ ] **1e-2b** Dockerfile ingest dan geo-processor (`-ldflags -X main.version`), manifest Kubernetes ingest, geo-processor, Garage, dan OTel Collector (ke Grafana Cloud, plus metrik server NATS dan Garage); key OpenAQ/FIRMS, kredensial arsip, dan token Grafana Cloud lewat Secret SOPS; retensi arsip (lifecycle Garage, setelah seminggu data) dan backup Garage ke Oracle Object Storage.
- [ ] **1e-3** Backfill dan kalibrasi: reanalisis GloFAS (`consolidated_v4`, ±3.900 panggilan) → ambang persentil banjir → `hazard.flood.*` (setelah 17 titik sungai bertanda diperiksa manual); arsip FIRMS standar (SP) → ambang titik api → `hazard.fire.*`; backfill jam OpenAQ yang terlewat (`/v3/sensors/{id}/hours`); set berlabel T4 dan kalibrasi radius dirasakan dari arsip gempa; replay CAP/prakiraan/OpenAQ (metadata request di objek arsip).

Titik pantau sungai (38 titik GloFAS + 7 sub-DAS indeks hujan) dan aturan tingkat peringatan ada di PRD, bagian Sungai yang dipantau dan Aturan bisnis.

## Catatan untuk asisten AI di chat baru

Baca berurutan: `CLAUDE.md`, file ini, lalu dokumen arsitektur dan PRD (tautan di README). Semua keputusan produk sudah disepakati di sana; jangan buka ulang tanpa diminta. Keputusan teknis baru dicatat sebagai ADR di `docs/adr/`.

Workspace cloud asisten tidak bisa menjangkau `proxy.golang.org`, `go.dev`, atau API GitHub, tetapi bisa `git clone` dari GitHub dan `apt`/`npm`/`pip`. Cara kerja yang terbukti di sesi 1b–1c:

- Go 1.27.1 di-build dari tag `go1.27.1` repo `golang/go` (bootstrap Go 1.24 bawaan).
- Modul Go dengan path non-GitHub (`golang.org/x/*`, `google.golang.org/protobuf`) diganti lewat `replace` di file `go.work` sementara di luar repo (`GOWORK=...`), menunjuk clone mirror GitHub-nya; modul yang tidak dikompilasi cukup diberi `go.mod` kosong. `GOPROXY=direct GOSUMDB=off`.
- golangci-lint 2.13.2 di-build dari source dengan cara yang sama (linter `decorder` dibuang karena hostnya GitLab; tidak dipakai config SIAGA).
- `protoc-gen-go` v1.36.10 di-build dari mirror, dipakai lewat template `buf.gen.yaml` sementara; hasil generate identik dengan CI.
- PostgreSQL 16 + PostGIS 3.4 dari apt untuk test integrasi; TimescaleDB 2.30.1 di-build dari tag GitHub (`./bootstrap -DREGRESS_CHECKS=OFF -DTAP_CHECKS=OFF`, lalu `make install`) karena repo packagecloud tidak terjangkau. Tanpa h3/pgvector (belum dipakai migrasi).
- Open-Meteo, OpenAQ, dan FIRMS juga tidak terjangkau dari workspace maupun shell desktop app; rekaman asli diambil pengguna dari WSL dengan skrip titipan (key dihapus dari hasil sebelum dikemas), lalu diputar lewat server tiruan Python.
- gitleaks 8.30.1 diunduh dari rilis GitHub untuk memeriksa patch sebelum dikirim; nilai key palsu di test harus berentropi rendah (misal `KUNCIUJIKUNCIUJI…`) supaya tidak ditandai `generic-api-key`.
- Host BMKG (`www.bmkg.go.id`, `api.bmkg.go.id`) tidak selalu terjangkau dari workspace; replay memakai server tiruan (`BMKG_CAP_BASE_URL`, `BMKG_FORECAST_URL`) dan binary `nats-server` dari rilis GitHub.
- Menjalankan skrip pihak ketiga yang diunduh (misal `extract_tsv.py` repo-gempa) ditolak kebijakan workspace; pakai data yang sudah jadi.

- Sesi 1e-1: membangun Go dari source sempat ditolak pemeriksa keamanan workspace dan baru jalan setelah pengguna mengizinkan eksplisit. golangci-lint butuh mirror GitHub untuk semua dependensi vanity; `codeberg.org` (go-errorlint v1.9.0, garif) diblokir, jadi dipakai mirror GitHub v1.8.0/v0.1.0 dengan nama modul diganti, dan `decorder` (GitLab) dibuang. Registry Docker juga diblokir, jadi Garage asli tidak bisa dijalankan di workspace; uji S3 memakai server tiruan (`s3archive/s3test`).

- Sesi 1e-2a: pemeriksa keamanan workspace kembali menolak `GOSUMDB=off` sampai pengguna mengizinkan. Mirror GitHub tambahan: `open-telemetry/opentelemetry-go` (v1.46.0, semua submodul), `opentelemetry-go-contrib` (tag v1.46.0 untuk `bridges/otelslog` v0.20.1 dan `instrumentation/runtime` v0.71.0), `opentelemetry-proto-go` (`otlp/v1.11.0`), `opentelemetry-go-instrumentation` (`sdk/v1.2.1`), `grpc/grpc-go` v1.83.2, `googleapis/go-genproto` (sparse `googleapis/api` dan `googleapis/rpc`), `uber-go/multierr` v1.11.0 (untuk goose). Modul non-GitHub yang dibawa `go.mod` grpc (cloud.google.com/…, dll.) cukup `go.mod` kosong. goose dibangun dengan tag `no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb`. Rilis GitHub Prometheus, Tempo, dan Loki bisa diunduh (cek SHA256) untuk menguji query dashboard; image Docker dan Grafana (dl.grafana.com) tidak.

Karena itu `go.sum` dari asisten tidak dipakai: patch tidak menyertakan `go.sum`/`go.work.sum`, dan `make deps` di WSL yang membuatnya.

Asisten bisa menulis ke `C:\KULIAH\PROJECT CODE\` lewat bridge desktop app (bukan ke folder WSL). File dari asisten dititipkan di sana (di luar folder repo), disalin ke `~/code/siaga`, dicek hash-nya, lalu file titipan dihapus. Perintah dijalankan pengguna di WSL, dan commit selalu dibuat dari WSL supaya hook lefthook ikut jalan.
