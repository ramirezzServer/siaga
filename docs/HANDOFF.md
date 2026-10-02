# Handoff

Dokumen ini adalah titik lanjut untuk sesi kerja berikutnya, termasuk chat baru dengan asisten AI. Perbarui setiap akhir sesi.

## Status: Fase 1e-2b-3 ditulis asisten (2026-10-02), menunggu verifikasi WSL; berikutnya 1e-3c

Fase 0 sampai 1e-3b-2b ter-commit dan terverifikasi, CI hijau (1e-3b-2b di `8848fc8..ee75b44`). Fase 1e dipecah: **1e-1** arsip Garage + replay, **1e-2a** OpenTelemetry + dashboard, **1e-2b** deploy (**1e-2b-1** image + CI + manifest, **1e-2b-2** Garage dan Collector di cluster, SOPS, default deny, **1e-2b-3** retensi dan backup arsip), **1e-3** backfill + kalibrasi. 1e-3 dipecah lagi: **1e-3a** pengisian ulang OpenAQ, replay prakiraan BMKG + OpenAQ, peta pemeriksaan titik sungai, **1e-3b** reanalisis GloFAS → ambang banjir → `hazard.flood.*` (**1e-3b-1** pemilihan sel dari reanalisis; **1e-3b-2a** alat ambang banjir dan ambang indeks hujan 7 sub-DAS; **1e-3b-2b** konektor hujan sub-DAS, ambang di geo-processor, `hazard.flood.*`, bagian ini), **1e-3c** arsip FIRMS SP → ambang titik api → `hazard.fire.*`, set berlabel T4, radius dirasakan, replay CAP.

### Fase 1e-2b-3: retensi arsip dan cadangan harian ke Backblaze B2

Keputusan lengkap di ADR 0022. Oracle Object Storage diganti Backblaze B2 karena pendaftaran Oracle gagal terus di verifikasi kartu; tujuan cadangan berupa URL S3 generik, jadi bisa pindah ke Oracle nanti. Ukuran arsip diukur dulu dengan `ukur-arsip.sh` (titipan): 25 Sep–2 Okt 67.860 objek / 95,0 MiB, `bmkg-prakiraan` ±94% objek dan ±72% byte, proyeksi 24 jam ±37 MiB/hari (±13 GiB/tahun) tanpa retensi.

| Bagian   | Isi                                                                                                                                                                                                                                                                                                                                                                                                           | Terverifikasi di workspace asisten                                                                                                                                                                                                  |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Domain   | `domain/archivebundle` (kunci `<konektor>/<YYYY>/<MM>/<DD>.p<n>.tar.zst` + `.p<n>.n<objek>.idx.json.zst`, indeks dengan SHA-256 payload dan bundel, validasi), `domain/retention` (aturan per konektor dalam hari UTC, minimum 8 hari, konektor tanpa aturan disimpan selamanya; bawaan prakiraan 14, USGS dan Open-Meteo cuaca/udara 30, sungai/hujan 90)                                                    | Coverage 95,6% dan 100%; `FuzzParse` (kunci kanonik), `FuzzExpiredMonotone`                                                                                                                                                         |
| Use case | `app/archivebackup`: `Backup` (hari UTC yang sudah ditutup, tenggang 15 menit; hari yang jumlah objeknya sama dengan nama indeks dilewati tanpa unduhan; objek baru jadi bagian berikutnya; bundel dibaca ulang sebelum indeks ditulis; pecah di 48 MiB), `Verify`, `Restore` (gzip identik byte per byte), `Prune` (hapus hanya kunci yang ada di indeks cadangan hari itu, dry-run bawaan, batas per jalan) | Coverage 90,5%; titik galat di setiap langkah, bundel/indeks diubah atau hilang, bagian terlambat, bundel yatim tidak ditimpa                                                                                                       |
| Adapter  | `ports.ArchiveDeleter` (fsarchive juga menghapus folder tanggal kosong, s3archive `DeleteObject`), subtest Delete di `archivetest` (ikut jalan terhadap Garage asli di job CI `archive`), `s3archive.Check` jatuh ke `ListObjectsV2` bila `HeadBucket` 403 (key B2 terbatas bucket), S3 tiruan `DenyHeadBucket`                                                                                               | Test adapter `-race`                                                                                                                                                                                                                |
| Perintah | `archive backup`, `backup-verify` (bawaan dua hari terakhir, `-all`), `restore` (`-from` wajib, `-into`), `prune` (`-apply`, `-retention`, `-max-delete`); env `ARCHIVE_BACKUP_URL`, `ARCHIVE_BACKUP_S3_*`, `ARCHIVE_RETENTION`; `make archive-backup`, `archive-backup-verify`, `archive-restore`, `archive-prune`, `archive-maintain`, `backup-setup`                                                       | Arsip asli 1 Okt (13.416 objek, 20,0 MiB gzip, 128,4 MiB mentah) → 15 bundel 2,4 MiB dalam ±5 dtk; jalan ulang 0 bagian (±0,1 dtk); verify 13.416 objek utuh; restore ke folder: kunci sama dan 13.416 berkas identik byte per byte |
| B2       | `scripts/b2-setup.sh`: master key lewat prompt tersembunyi, sesi CLI `b2` di folder sementara, bucket privat SSE-B2, key hanya bucket itu + awalan `arsip/` dengan `listBuckets,listFiles,readFiles,writeFiles` (tanpa hapus), ditulis ke `.env` tanpa dicetak; panduan `docs/setup/backblaze-b2.md`                                                                                                          | Skrip diuji dengan CLI `b2` tiruan (urutan perintah, `.env` mode 600, berhenti bila key sudah terisi); shellcheck bersih. Sintaks CLI dicek di source `Backblaze/B2_Command_Line_Tool`                                              |
| Cluster  | CronJob `archive-maintenance` (01.20 UTC, init container backup → verify → prune, image ingest), NetworkPolicy (Garage 3900 + HTTPS internet), izin masuk Garage, Secret `siaga-arsip-cadangan` dari `make secrets-prod` (awalan `/laptop/` → `/produksi/`)                                                                                                                                                   | kustomize 5.7.1 + kubeconform 0.7.0 strict (lokal 32, prod 28 resource valid); `prod-secrets.sh` diuji dengan sops 3.13.3 + age 1.3.2 (isi terdekripsi benar, jalan kedua tidak berubah)                                            |
| Repo     | ADR 0022 (catatan di ADR 0014 dan 0017), README, `.env.example`, `docs/setup/secrets.md`, fuzz baru di Makefile dan CI, `klauspost/compress` jadi dependensi langsung (versi dan `go.sum` sama)                                                                                                                                                                                                               | golangci-lint 2.13.2 bersih, semua test ingest `-race`, prettier                                                                                                                                                                    |

Zstd dengan jendela 32 MiB dipilih setelah membandingkan: gzip -9 per tar ±9,8 MiB, zstd 2,4 MiB untuk hari yang sama (USGS 3,5 MiB → 32 KiB karena feed 2 harinya berulang). Proyeksi B2 bila 24 jam: ±2 GiB/tahun, jadi tidak ada lifecycle di B2 (ditinjau di 8 GB). Lifecycle Garage v2.3.0 sebenarnya ada (`Expiration` + `Prefix`), tetapi tidak bisa mensyaratkan objek sudah dicadangkan.

### Verifikasi yang perlu dijalankan di WSL (1e-2b-3)

```bash
bash "/mnt/c/KULIAH/PROJECT CODE/siaga-titipan/terapkan-fase-1e-2b-3.sh"   # salin, cek hash, make check, commit
git push
make test-integration      # TestGarage kini juga menguji Delete ke Garage asli
make k8s-check             # overlay prod kini berisi CronJob archive-maintenance
```

Lalu sekali, ikuti `docs/setup/backblaze-b2.md`: Caps & Alerts B2 ke $0, `pipx install b2`, buat master key, `make env`, `make backup-setup`. Setelah itu:

```bash
make archive-backup          # hari 24 Sep–1 Okt (UTC) ke B2
make archive-backup-verify ARGS=-all
make archive-prune           # dry-run; 2 Okt belum ada yang kedaluwarsa (arsip mulai 24 Sep):
                             # prakiraan mulai dipangkas 9 Okt, USGS dan Open-Meteo cuaca/udara 25 Okt
make archive-restore FROM=2026-09-26 TO=2026-09-26 CONNECTORS=bmkg-prakiraan INTO=/tmp/pulih
INGEST_ARCHIVE_URL=/tmp/pulih make replay FROM=2026-09-26 TO=2026-09-27 CONNECTORS=bmkg-prakiraan ARGS="-publish=false -strict"
```

Yang dilaporkan: ringkasan `archive-backup` (jumlah bagian, ukuran total bundel), hasil verify, pemakaian bucket di web B2, hasil replay dari folder pulihan, dan CI. Prune pertama yang benar-benar menghapus terjadi ±9 Okt lewat `make archive-maintain`; periksa dulu keluarannya (kolom "belum dicadangkan" harus 0). Mulai sekarang `make archive-maintain` dijalankan setiap memulai sesi (catat juga di `MULAI-LAGI.md` titipan).

### Temuan yang perlu ditindaklanjuti (1e-2b-3)

- **Belum ada metrik cadangan di dashboard.** `archive` mencetak laporan saja; status CronJob di produksi terlihat dari Job yang gagal. Bila perlu, tambah metrik OTel (umur cadangan terakhir per konektor) saat fase 8.
- **Key B2 laptop dipakai juga untuk Secret produksi** sampai bootstrap fase 2; saat itu buat key kedua khusus cluster dengan `KEY_NAME=siaga-cadangan-produksi TARGET=produksi bash scripts/b2-setup.sh` (kosongkan dulu baris `ARCHIVE_BACKUP_*`, atau jalankan di salinan `.env`).
- **Oracle Always Free juga rencana VM produksi fase 2**; kegagalan verifikasi kartu membuat rencana itu berisiko. Perlu dicari alternatif VM gratis sebelum fase 2.
- Batas gratis B2 tidak konsisten antar-dokumen resmi (halaman harga 2026: Class B/C gratis; artikel bantuan 2022: 2.500/hari). Rancangan memakai yang lebih ketat; tinjau pemakaian di web B2 setelah seminggu.
- `backup-verify` tanpa `-connectors` mendaftar seluruh cadangan (±12 ribu kunci/tahun bila 24 jam); masih murah, tetapi bisa dibatasi per hari bila Class C menjadi masalah.

### Fase 1e-3b-2b: kejadian banjir dari debit GloFAS dan indeks hujan sub-DAS

Keputusan lengkap di ADR 0021 (melanjutkan ADR 0020 butir 4, 5, 8, 9). Koreksi bias hanya untuk rasio p98 `seamless_v4`/reanalisis di bawah 0,90 (saat ini hanya Nanjung: Siaga 378,7 → 329,5 m³/s); tiga titik hilir Jatiluhur dibatasi Siaga karena debit model di sana tidak tahu operasi waduk.

| Bagian   | Isi                                                                                                                                                                                                                                                                                                                                                    | Terverifikasi di workspace asisten                                                                                                                                             |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Kontrak  | `siaga.raw.v1.CatchmentRainfallForecast` (hujan per jam rata-rata sub-DAS, `cell_count`) di `raw.rain.openmeteo`; `siaga.hazard.v1.FloodDetail` (indikator, ambang, `FloodDay` per hari: debit median/P75/maks, akumulasi 3/6/24 jam, jendela penentu, "kemungkinan")                                                                                  | buf lint + breaking; generate Go + TS; test TS (round trip, JSON `hazard.flood`)                                                                                               |
| Ingest   | `domain/catchment` (CSV sub-DAS, bobot, `Areal` = `threshold.WeightedMean` yang sama dengan `make rain-threshold`); konektor `openmeteo-hujan` (47 sel `ecmwf_ifs`, `cell_selection=nearest`, kemarin..+3 hari, tiap 3 jam, 376 lokasi/hari); `sitelist.ForProvinces` + salinan `catchments_32.csv`                                                    | Coverage 95–100%; rekaman asli 47 sel 2026-10-02 (`testdata/hujan-sub-das-47.json`, jumlah per sel dicocokkan); `TestOpenMeteoQuota` 3.888 lokasi/hari; test drift salinan CSV |
| Domain   | `domain/flood` (geo-processor): ambang (koreksi, batas tingkat), penilaian debit (median, naik maks satu dari P75, maks hanya tampilan, tanpa ensemble pakai `river_discharge`), indeks hujan (puncak harian 3/6/24 jam WIB, maks Siaga, seri → jendela terpanjang), horizon hari ini..+3, kejadian (ID, kedaluwarsa 24 jam, judul, ringkasan, digest) | Coverage 98,9%; fuzz `FuzzDayMaxSumMatchesCalibration` (sama dengan `threshold.DailyMaxSum` ingest), `FuzzLevelMonotone`                                                       |
| Ambang   | `adapters/calibration` (CSV disematkan, salinan persis `docs/calibration` lewat `make calibration-copy`, periode + jumlah + SHA-256), migrasi `00007_flood.sql` (`ref.discharge_threshold` dengan kolom ambang efektif, `ref.rainfall_threshold`, `hazard.flood`, `flood_day`, `flood_site`, titik `catchment` di `ts.site`); sinkronisasi saat start  | Coverage 97,4%; migrasi naik-turun-naik; `TestFloodStoreSyncThresholds` (sisip, ubah, hapus, tanpa perubahan)                                                                  |
| Use case | `app/floods`: satu kejadian aktif per titik, buka saat ≥ Info, perbarui tiap keluaran baru, akhiri 24 jam setelah keluaran terakhir ≥ Info, abaikan keluaran yang tidak lebih baru, putaran kedaluwarsa tiap menit; outbox `hazard.flood.created/updated/expired`                                                                                      | Coverage 95,8%; `TestFloodStoreLifecycle`, `TestFloodRainfallInDatabase`, `TestFloodConstraints` (PostgreSQL asli, titik uji `uji-banjir`, ambang asli dipulihkan)             |
| Consumer | `geo-processor-rain-openmeteo` (simpan ke `ts.weather_forecast`), `geo-processor-hazard-flood-discharge`, `geo-processor-hazard-flood-rain`; titik tanpa ambang → DLQ; `/status` bagian `flood_consumers` dan `flood_calibration`                                                                                                                      | `TestEndToEnd` kini juga hujan 3 × 7 mm → Siaga sub-DAS uji, debit uji → Siaga, 48 baris hujan tersimpan; `TestTraceFromRawToHazard` lulus                                     |
| Repo     | ADR 0021 (catatan di ADR 0020 butir 4), `docs/events.md`, README, Makefile (`calibration-copy`, fuzz baru), CI fuzz, `banjir-tercatat.csv` + banjir Bandung Mei 2021                                                                                                                                                                                   | golangci-lint 2.13.2 bersih di 4 modul, semua test `-race`, prettier, redocly                                                                                                  |

Ambang asli (dari commit `c0f4545`) dinilai terhadap prakiraan `ecmwf_ifs` asli 2026-10-02: Cirasea Waspada 4 Okt (3 jam 11,3 mm), Cihaur Waspada 4–5 Okt (24 jam ±27 mm), Citarik dan Cikapundung Info, tiga sub-DAS lain di bawah Info. Prakiraan debit asli belum dinilai (Open-Meteo hanya terjangkau lewat browser).

### Verifikasi di WSL (1e-3b-2b)

Terverifikasi 2026-10-02: commit `8848fc8..ee75b44` di-push, CI hijau. `make check`, `make up migrate` (00007), `make test-integration` lulus; `make flood-threshold` dari cache 0 panggilan.

- Uji `bandung-2021`: Majalaya, Dayeuhkolot, dan Nanjung semuanya Bahaya pada 2021-05-24.
- Setelah ingest + geo dijalankan ulang 2 Okt 11.47 WIB: Cirasea dan Cihaur Waspada (puncak 4 Okt), Cikapundung dan Citarik Info; belum ada kejadian debit (musim kemarau, tidak ada titik ≥ p80).
- Query verifikasi sesuai: Nanjung koreksi 0,87 (Siaga 329,5 m³/s), hilir Jatiluhur/Karawang/muara `max_level` 3, 21 ambang hujan, 7 sub-DAS `ecmwf_ifs` di `ts.series`, 38 titik sungai dinilai di `hazard.flood_site`.
- ingest dan geo-processor dibiarkan jalan terus di WSL dengan kode terbaru.

Langkah yang dijalankan (untuk referensi):

```bash
bash "/mnt/c/KULIAH/PROJECT CODE/siaga-titipan/terapkan-fase-1e-3b-2b.sh"   # salin, cek hash, make check, commit
make up migrate        # migrasi 00007
make test-integration  # termasuk TestFlood* dan TestEndToEnd (banjir)
make flood-threshold   # dari cache, 0 panggilan; ambang-banjir.md kini memuat uji banjir Bandung Mei 2021, CSV tidak berubah
git add docs/calibration && git commit -m "chore(ingest): perbarui laporan ambang banjir dengan banjir Bandung 2021"
git push
make ingest            # terminal 1; /status :8081 berisi openmeteo-hujan
make geo               # terminal 2; log start "ambang banjir diselaraskan" (38 titik debit, 7 sub-DAS)
```

Setelah ±5 menit, `make psql`:

```sql
SELECT site_id, siaga_m3s, correction, max_level FROM ref.discharge_threshold WHERE correction < 1 OR max_level < 4 ORDER BY 1;
-- nanjung 329,5 / 0,87 / 4; hilir-jatiluhur, karawang, muara max_level 3
SELECT count(*) FROM ref.rainfall_threshold;   -- 21
SELECT site_id, model, last_fetched_at FROM ts.series WHERE site_id LIKE 'catchment:%' ORDER BY 1;   -- 7 baris ecmwf_ifs
SELECT e.status, e.level, f.indicator, f.site_id, f.outlook_level, f.peak_date, e.expires_at
  FROM hazard.event e JOIN hazard.flood f ON f.event_id = e.id ORDER BY e.updated_at DESC LIMIT 20;
```

Saat consumer banjir pertama kali jalan, stream `RAW` dibaca dari awal (7 hari): keluaran debit lama yang mencapai Info membuka kejadian yang langsung berakhir (`created` lalu `expired`), jadi wajar ada beberapa kejadian `expired` bertanggal lampau.

### Temuan yang perlu ditindaklanjuti (1e-3b-2b)

- **Banjir bandang hulu kecil** (Garut 2016) tidak tertangkap debit GloFAS. Sub-DAS hulu Cimanuk bisa ditambah ke indeks hujan dengan cara yang sama (±8 lokasi per 3 jam per sub-DAS); belum dikerjakan.
- **Kejadian banjir belum punya area dan wilayah terdampak**; aturan "≤ 2 km dari sungai" dihitung alert-engine (fase 3). Poligon sub-DAS bisa dibangun dari unit HydroBASINS bila frontend butuh.
- Setiap keluaran baru menerbitkan `updated` selama kejadian aktif (waktu terima dan nilai per hari berubah); alert-engine harus mengirim push hanya saat tingkat naik.
- Rasio bias dihitung ulang tiap kalibrasi tahunan; periode tumpang tindih yang makin panjang bisa membuat titik lain ikut dikoreksi tanpa mengubah kode.
- Berita yang dicek untuk hari hujan terbesar: 24 Mei 2021 malam (Pikiran Rakyat: Dayeuhkolot, Baleendah, tol Purbaleunyi) cocok dengan Citarik/Ciwidey/Ciminyak; 9 Feb 2019 malam (kumparan: banjir bandang Pasir Jati, Cilengkrang) cocok dengan Cirasea; 6 Des 2019 sore (Antara: banjir bandang Kertasari) cocok dengan Cikapundung/Cihaur. Dua artikel detik (Majalaya 23 Feb 2018, Baleendah 27 Apr 2017) tidak cocok tanggalnya dengan hari hujan terbesar.

### Fase 1e-3b-2a: ambang banjir persentil dan indeks hujan sub-DAS

Keputusan lengkap di ADR 0020. Temuan dari Open-Meteo asli (dicek lewat browser di laptop, 1 Okt) yang mengubah rencana: reanalisis `consolidated_v4` berisi **1997-01-01 sampai 2025-05-31** (bukan 1984 sampai Juli 2022), ERA5-Land tidak berisi hujan, arsip `ecmwf_ifs` mulai 2017 di sel yang sama dengan prakiraannya, dan `cell_selection` bawaan tidak memilih sel terdekat.

| Bagian         | Isi                                                                                                                                                                                                                                                                                                                                                              | Terverifikasi di workspace asisten                                                                                                                                                            |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Domain         | `domain/threshold`: persentil (metode 7), ambang naik tegas, tingkat 0–4, puncak tahunan, puncak harian akumulasi N jam (`DailyMaxSum`), rata-rata berbobot                                                                                                                                                                                                      | Coverage 100%; `FuzzDailyMaxSum` (dibanding definisi brute force), `FuzzLevel` (tingkat tidak turun saat nilai naik)                                                                          |
| Deret historis | `app/reanalysis`: Source (model, variabel, satuan, harian/per jam, periode), Loader dengan cache per sel, titik uji, batas kuota berbobot, `cell_selection=nearest`, cek satuan/langkah/sel/kelengkapan ≥ 99%; adapter `reanalysiscache` (tulis atomik); `openmeteo.ParseHourly` dan kolom `Unit`                                                                | Coverage 98,8%; sumber tiruan `openmeteotest` (batch, jeda, batas habis lalu lanjut dari cache, tulis ulang cache identik, sel/satuan/langkah salah, data bolong, titik uji kosong 1 request) |
| Ambang debit   | `make flood-threshold` (`cmd/flood-threshold`, `app/floodthreshold`): 38 sel, `consolidated_v4` 1997–2024 → `docs/calibration/ambang-banjir-32.csv` + `ambang-banjir.md`; uji `banjir-tercatat.csv` (Garut 2016 di dalam sampel, Bekasi 2025 di luar sampel, Pamanukan 2026 dengan `seamless_v4`); rasio p98 `seamless_v4`/reanalisis 2022-08..2024-12 per titik | Coverage 96%; biner dijalankan terhadap server Open-Meteo tiruan (Python) dengan 38 titik asli                                                                                                |
| Indeks hujan   | `docs/calibration/sub-das-citarum-hulu.csv` (7 sub-DAS dari HydroBASINS lev12, 47 sel `ecmwf_ifs`, bobot luas); `make rain-threshold` (`cmd/rain-threshold`, `app/rainthreshold`): puncak harian akumulasi 3/6/24 jam 2017–2024 → `ambang-hujan-sub-das.csv` + `.md`                                                                                             | Coverage 96%; biner terhadap server tiruan: 11 request (titik uji + 10 × 5 sel), jalan kedua 0 request dan CSV identik                                                                        |
| river-snap     | `ReanalysisStart` 1997-01-01                                                                                                                                                                                                                                                                                                                                     | Test lama lulus                                                                                                                                                                               |
| Repo           | ADR 0020, README, Makefile                                                                                                                                                                                                                                                                                                                                       | —                                                                                                                                                                                             |

Terverifikasi 2026-10-01: kode `5e54c80`, ADR 0020 + HANDOFF `d75fdf3`, ambang asli `c0f4545` (di-push). `make check` hijau; jalan kedua `flood-threshold`/`rain-threshold` 0 panggilan dari cache.

- **Uji kejadian**: Bekasi 2025 (di luar sampel) Siaga di `bekasi-kota`, `bekasi-p2c`, `cileungsi`, Waspada di `cikeas`; Pamanukan 2026 Siaga; Garut 2016 hanya Waspada (banjir bandang tidak tertangkap debit harian).
- **Rasio p98 seamless/reanalisis**: Nanjung 0,87 (satu-satunya di bawah 0,9), Majalaya 0,92, Dayeuhkolot 0,93, sisanya 0,94–1,06. Keputusan koreksi di ADR 0021.
- Citarum hilir Jatiluhur, Karawang, dan muara diatur waduk; ambangnya kurang bisa dipercaya (dibatasi Siaga di ADR 0021).

### Fase 1e-3b-1: pemilihan sel titik sungai dari reanalisis GloFAS

| Bagian     | Isi                                                                                                                                                                                                                                                                         | Terverifikasi di workspace asisten                                                                                                                                                              |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| river-snap | Debit rata-rata periode acuan reanalisis `consolidated_v4` 2020-07-01..2022-06-30 (730 hari, bobot ±5,2 per lokasi), bukan jendela prakiraan 14 hari; flag `-model`, `-from`, `-to`, `-cache`, `-max-calls` (bawaan 4.500); `make river-snap ARGS=…`                        | Test app 96,4%, domain 98,5%, `-race`; golangci-lint 2.13.2 bersih                                                                                                                              |
| Cache      | `.cache/river-snap/glofas-<model>-<dari>-<sampai>.json` per titik permintaan (sel dijawab, rata-rata 4 desimal, hari berisi), disimpan setelah tiap request; jalan ulang tanpa kuota                                                                                        | Server Flood tiruan dengan 38 titik asli: 816 titik permintaan unik; berhenti di `-max-calls 60` lalu dilanjutkan dari cache; jalan ketiga dari cache tanpa request dan `rivers_32.csv` identik |
| Pengaman   | Titik uji satu lokasi saat cache kosong (`ErrEmptyReanalysis` bila tanpa debit), jumlah hari per lokasi harus sama dengan periode, sel < 90% hari berisi tidak dipakai, maks 200 panggilan per request dan 400 per menit, request terakhir dipotong supaya pas dengan batas | Test unit (model kosong, hari kurang, sel jarang, batas habis di tengah dan tepat di awal, cache periode lain, galat checkpoint)                                                                |
| Repo       | ADR 0019, catatan di ADR 0011 butir 5, README, Makefile                                                                                                                                                                                                                     | —                                                                                                                                                                                               |

Belum pernah menyentuh Open-Meteo asli (tidak terjangkau dari workspace). Nama model `consolidated_v4` dan rumus bobot berasal dari source Open-Meteo (temuan 1e-1); titik uji pertama memastikannya dengan satu panggilan.

### Verifikasi di WSL (1e-3b-1)

Terverifikasi 2026-09-30, commit `5f9d217` di-push:

- `make river-snap` jalan pertama: 816 titik permintaan, 23 request, ±4.255 panggilan Open-Meteo tanpa 429; titik uji lolos, jadi `models=consolidated_v4` dan rumus bobot benar. 10 titik bertanda (laporan 24 Sep: 17).
- Jalan kedua: 816 dari cache, 0 panggilan, `git diff` tidak bertambah. Hasil jalan pertama di-commit sebagai `8955ef2`.

### Penetapan titik sungai (1e-3b-1)

Semua titik bertanda dan titik yang dulu pindah (ADR 0019, Konteks) ditelusuri dari debit sel kandidat di peta pemeriksaan: debit sel hilir kira-kira sama dengan jumlah sel hulunya, jadi arah aliran dan pemisahan sungai bisa dibaca dari angkanya. 14 titik ditetapkan dengan radius 0 di `docs/calibration/titik-sungai-32.csv` (catatan per titik ada di sana):

- **Ciliwung dan Cisadane** berjalan sejajar di barat Bogor: Cisadane di kolom 106.725 (8,0 → 9,7 → 15,4 → 19,4 → barat ke 56–61), Ciliwung di kolom 106.775 (Katulampa 11,3 → 13,6 → 15,7 → … → 27,4 di batas Jakarta). `cisadane-bogor`, `ciliwung-bogor`, dan `ciliwung-depok` dipindah ke kolom masing-masing; sel lama Depok ternyata alur Bekasi.
- **Kali Bekasi**: GloFAS baru menggabungkan alur barat (Cikeas) dan alur timur (Cileungsi) di -6.125, 106.975 (53,9), ±12 km di utara Bekasi kota. `bekasi-kota` dipasang di sel gabungan itu; `bekasi-p2c` di alur barat terdekat ke P2C (-6.325, 106.925), jadi hanya mewakili Cikeas. Ambang P2C perlu dibaca dengan catatan itu.
- `cilamaya-hilir` pindah dari sel Ciasem (107.675) ke sel Cilamaya (-6.225, 107.575); `citanduy-tasikmalaya` pindah dari cabang utara (Cimuntur) ke alur utama Citanduy (-7.325, 108.225).
- `cimanuk-muara` tetap di -6.375, 108.225: sel hilirnya (-6.325) sedikit lebih kecil dan akan bertanda `hilir-lebih-kecil`, hidrografnya sama.
- `citarum-majalaya`, `citarum-nanjung`, `cisanggarung-kuningan`, `cikeas`, `cileungsi`, `katulampa` diterima apa adanya dan dikunci.

Belum dicocokkan secara visual dengan alur OSM di geojson.io; bila kelak dicek dan berbeda, ubah baris CSV-nya lalu `make river-snap` (dari cache, hanya titik baru yang diminta).

### Fase 1e-3a: pengisian ulang OpenAQ, replay dari isi payload, peta titik sungai

| Bagian            | Isi                                                                                                                                                                                                                                                                                    | Terverifikasi di workspace asisten                                                                                                                                                             |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pengisian ulang   | Use case `app/aqbackfill` + perintah `backfill openaq` (`make backfill-openaq FROM= TO= STATIONS=`): daftar lokasi sekali, lalu `/v3/sensors/{id}/measurements` per sensor parameter SIAGA; satu event per nilai (`meta.connector = openaq-jam`), arsip `raw/openaq-jam/`; maks 7 hari | Rekaman asli 26 Sep dibandingkan dengan `ts.aq_observation`: waktu = akhir periode, nilai sama dengan `/measurements` (bukan `/hours`); perintah diuji dengan server OpenAQ tiruan + JetStream |
| Replay            | `app/replay` mendapat `MultiFeed` (beberapa awalan arsip) dan `PartialFeed` (isi terakhir per record lintas payload); feed `bmkg-prakiraan` (kode desa dari `lokasi.adm4`) dan `openaq-stasiun` (daftar + nilai terbaru); `stations.Observe` dipakai polling dan replay                | Isi event replay = isi event polling untuk rekaman asli prakiraan (3 desa) dan OpenAQ; `cmd/replay` memutar 11 payload ke JetStream                                                            |
| Peta titik sungai | `river-snap -geojson` (dipakai `make river-snap`): sel kandidat GloFAS per titik dengan debit relatif, sel terpilih, koordinat perkiraan, gaya simplestyle untuk geojson.io                                                                                                            | Test dengan sumber debit tiruan (kandidat unik, satu sel terpilih per titik)                                                                                                                   |
| Dashboard         | Panel "Target scrape": tanpa data abu-abu, merah hanya bila `up = 0`                                                                                                                                                                                                                   | Prettier; tampilan belum dilihat di Grafana                                                                                                                                                    |
| Image             | Biner `backfill` di image ingest (Makefile, Tiltfile, CI, uji asap)                                                                                                                                                                                                                    | —                                                                                                                                                                                              |
| Repo              | ADR 0018, README, `docs/events.md`, catatan ADR 0014                                                                                                                                                                                                                                   | —                                                                                                                                                                                              |

### Verifikasi di WSL (1e-3a)

Terverifikasi 2026-09-28: commit `9ae83a3..98854e2` di `main`, sudah di-push, CI hijau.

- `make archive-upload`: 0 disalin, 310 sudah ada (arsip lokal fase 1a–1d sudah di Garage).
- `make replay ARGS="-publish=false -strict"`: 37.342 payload dari 13 feed, 0 rusak, 0 gagal parse, 0 ditolak (`bmkg-prakiraan` 35.296 payload, `openaq-stasiun` 123 payload/82 event).
- `make backfill-openaq FROM=2026-09-26T12:00:00Z TO=2026-09-26T16:00:00Z`: 2 stasiun, 10 nilai terbit; `ts.aq_observation` kini berisi jam 14.00 UTC (sensor 17000275 = 51,6; 17620437 = 49,3), baris lain tidak berubah dan tidak ada yang bergeser.
- Dashboard diimpor ulang ke Grafana Cloud.
- FIRMS pulih: 4 produk sukses, 0 gagal sejak ingest start 28 Sep 18.42 WIB.
- `make river-snap` dijalankan ulang 28 Sep 22.16 WIB: 7 titik pindah sel tanpa perubahan file sumber. Hasil tidak di-commit; ditangani 1e-3b-1 (ADR 0019).

### Temuan yang perlu ditindaklanjuti (1e-3a)

- **Batas pengisian ulang 7 hari** mengikuti `series.MaxObservationAge` geo-processor. Celah yang lebih tua butuh batas itu dilonggarkan untuk pesan backfill (misal header khusus), belum dibutuhkan.
- Replay OpenAQ dengan `-from` di tengah hari bisa menolak nilai terbaru pertama (`ErrNoContext`) bila daftar lokasi terakhir diarsipkan sebelum `-from`. Wajar; putar dari awal hari atau sejak ingest start.
- Replay CAP pindah ke 1e-3c (lihat ADR 0018 butir 8).
- Ukuran arsip 28 Sep 20.21 WIB (±3,8 hari): 36.830 objek, 50,9 MiB, 37,0 MiB di antaranya `bmkg-prakiraan` (±34,7 ribu objek). Bahan keputusan retensi 1e-2b-3 (±13 MiB/hari, ±4,8 GiB/tahun tanpa retensi).
- FIRMS sempat timeout terus sejak 26 Sep 19.54 WIB sementara sumber lain jalan. Sudah pulih: `/status` 28 Sep 20.10 WIB menunjukkan keempat produk sukses (4 percobaan, 0 gagal sejak ingest start 18.42 WIB). Pantau panel keterlambatan sumber.

### Fase 1e-2b-2: Garage, OTel Collector, secret SOPS, dan default deny

| Bagian         | Isi                                                                                                                                                                                                                                                                                                              | Terverifikasi di workspace asisten                                                                                                                                                                                                                                                |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Garage         | `deploy/k8s/platform/garage`: StatefulSet satu node (`dxflrs/garage:v2.3.0`, `--single-node --default-bucket`), UID 65532, rootfs read-only, PVC meta 1 Gi + data 10 Gi, probe `/health`; ingest mengarsipkan ke `s3://siaga-arsip/raw?endpoint=http://garage:3900` dengan access key dari Secret `siaga-garage` | Kunci config (`compression_level = "none"`, `block_ram_buffer_max`) dan env `GARAGE_*` dicek di source v2.3.0 (mirror GitHub). **Belum pernah jalan di cluster**: image Garage tidak bisa ditarik di workspace                                                                    |
| OTel Collector | `deploy/k8s/platform/otel-collector`: `otelcol-k8s` 0.161.0, OTLP masuk dari layanan, scrape sidecar exporter NATS (`promExporter` di `nats-values.yaml`), `/metrics` Garage, dan metrik internal; kirim ke Grafana Cloud dengan kredensial dari Secret `siaga-otel-collector`                                   | Binary rilis 0.161.0 dengan config asli: `validate`, lalu end-to-end dengan nats-server 2.15.0 + prometheus-nats-exporter 0.20.2 + Garage tiruan (format `/metrics` dari dokumentasi v2.3.0) → pengganti Grafana Cloud (cek header auth) → Prometheus 3.15 OTLP; 56 seri platform |
| Secret SOPS    | `.sops.yaml` (recipient diisi otomatis, indentasi 2), `deploy/k8s/prod/secrets.enc.yaml` (`SopsSecret`, dibuat `make secrets-prod`), `make secrets-grafana`/`secrets-edit`/`secrets-check`, `scripts/sops-check.sh` di `k8s-check` dan CI; `dev-secrets.sh` membuat Secret dengan nama/key sama untuk k3d        | sops 3.13.3 + age 1.3.2: buat, jalankan ulang (tidak berubah), rotasi Grafana, round-trip dekripsi; `sops-check` menolak file plaintext dan campuran; gitleaks 8.30.1 bersih pada file terenkripsi; `dev-secrets.sh` diuji dengan kubectl tiruan                                  |
| NetworkPolicy  | `deploy/k8s/platform/networkpolicy`: `default-deny` + `allow-dns` untuk semua pod, policy NATS/nats-box dan CloudNativePG; policy Garage, Collector, ingest (diketatkan ke NATS/Garage/Collector/internet), geo-processor (PostgreSQL/NATS/Collector); overlay lokal `otel-collector-lokal`                      | Label pod chart NATS dicek di chart 2.15.0. **Belum pernah diterapkan di k3s**                                                                                                                                                                                                    |
| Integrasi      | Tilt (`dev-secrets`, `garage` port-forward 13900, `otel-collector`), `make otelcol-check`, job CI `manifests` (sops-check, validasi config Collector), baris dashboard "Platform di cluster" (6 panel), `OTLP_BIND` untuk Grafana lokal dari k3d, ADR 0017, panduan secret dan Grafana Cloud                     | kustomize 5.7.1 + kubeconform strict (32 dan 25 resource valid); Trivy 0.74 overlay prod tanpa CRITICAL/HIGH; actionlint bersih; semua query panel baru mengembalikan data di Prometheus uji; Prettier bersih                                                                     |

### Verifikasi di WSL (1e-2b-2)

Terverifikasi 2026-09-26/27: commit `5b4955c..92f294b` di `main` (termasuk perbaikan prompt `make secrets-prod` di `151baff`), CI hijau semua termasuk job Manifest Kubernetes (sops-check, validasi config Collector, kubeconform, Trivy). Secret produksi `deploy/k8s/prod/secrets.enc.yaml` berisi token Grafana Cloud `siaga-produksi` (belum dipakai sampai cluster produksi ada); laptop memakai token `siaga-laptop-2` di `.env`; token lama sudah dicabut. Kunci age pribadi ada di `~/.config/sops/age/keys.txt` dan salinannya di password manager.

Belum pernah dicoba, opsional: profil full `make k3d-up && make tilt`. Yang perlu diperhatikan: `garage` Ready, `otel-collector` Ready dan lognya tanpa galat ekspor, `geo-processor-migrate` selesai (bukti CloudNativePG lolos default deny, bagian paling berisiko), lalu nyalakan `ingest` manual dan pastikan log start `arsip payload aktif`. Setelah ±2 menit panel "Platform di cluster" terisi.

### Temuan yang perlu ditindaklanjuti (1e-2b-2)

- **sops-secrets-operator, kunci age cluster, CloudNativePG, NATS, dan Argo CD di produksi** dipasang saat bootstrap fase 2 (`sops updatekeys` setelah kunci cluster ditambahkan ke `.sops.yaml`).
- Lokal tetap memakai `dev-secrets.sh`, bukan SOPS dengan kunci lokal seperti di dokumen arsitektur (ADR 0017 butir 9).
- Default deny: setiap layanan baru wajib membawa NetworkPolicy dan ditambahkan ke daftar klien NATS/PostgreSQL/Garage. CloudNativePG di balik default deny paling berisiko; bila `siaga-db` tidak Ready setelah Tilt, cek dulu dengan `kubectl -n siaga delete networkpolicy siaga-db default-deny` lalu laporkan.
- Nama metrik counter Garage di Grafana Cloud diasumsikan `api_s3_request_counter_total` dan `block_bytes_written_total` (hasil terjemahan OTLP→Prometheus yang diuji dengan Prometheus 3.15); bila panel Garage kosong padahal target hidup, cek nama di Explore.
- Chart NATS di Tilt belum dipin versinya.

### Fase 1e-2b-1: image, job CI, dan manifest Kubernetes pipa data

| Bagian    | Isi                                                                                                                                                                                                                                                                                       | Terverifikasi di workspace asisten                                                                                                                                                         |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Dashboard | Panel "Keterlambatan sumber" dan "Sumber tepat waktu" mengukur umur data saat metrik dikumpulkan (`timestamp(x) - x`) plus kelebihan bila metrik berhenti datang; variabel tersembunyi `ekspor_metrik` = 60                                                                               | Prometheus 3.14 dengan data sintetis (kumpul tiap 60 dtk): autogempa sehat maks 0,13× (lama 2,1×), usgs gagal tetap naik, ingest mati → panel naik lalu "tepat waktu" 0% (lama kosong)     |
| Image     | `deploy/images/go/Dockerfile` (satu untuk semua layanan Go, `--target ingest`/`geo-processor`), cross-compile amd64/arm64, distroless static nonroot, `main.version` = commit, goose + `/migrations` di image geo-processor; `scripts/image-smoke.sh`, `make images`, `make images-smoke` | hadolint bersih. **Belum pernah di-build**: registry Docker dan daemon Docker tidak ada di workspace; build pertama di WSL (`make images-smoke`) dan CI                                    |
| CI        | Job `images` (matrix: build amd64, uji asap, Trivy CRITICAL/HIGH; di `main`: push multi-arch ke GHCR, SBOM + provenance, cosign keyless) dan `manifests` (kustomize + kubeconform + Trivy misconfig overlay prod)                                                                         | actionlint 1.7.12 bersih; checksum rilis kustomize 5.7.1 dan kubeconform 0.7.0 dicek                                                                                                       |
| Manifest  | `deploy/k8s/apps/ingest`, `apps/geo-processor` (Deployment, Service, NetworkPolicy, Job migrasi `geo-processor-migrate`), overlay `local` (Tilt) dan `prod` (image GHCR); Tilt membangun kedua image; `dev-secrets.sh` membuat Secret `siaga-ingest` dari `.env`                          | kustomize 5.7.1 build kedua overlay; kubeconform strict K8s 1.33 (16 + 9 resource valid); Trivy 0.74 config overlay prod tanpa CRITICAL/HIGH; `dev-secrets.sh` diuji dengan kubectl tiruan |
| Repo      | ADR 0016 (termasuk alasan Kustomize, bukan Helm chart per layanan), README                                                                                                                                                                                                                | —                                                                                                                                                                                          |

### Verifikasi yang perlu dijalankan di WSL (1e-2b-1)

```bash
bash "/mnt/c/KULIAH/PROJECT CODE/siaga-titipan/terapkan-fase-1e-2b-1.sh"   # check, images-smoke, k8s-check, commit
git push                   # job CI images + manifests; di main: push image ke GHCR
```

Setelah CI hijau di `main`: buka github.com/ramirezzServer?tab=packages, pastikan `siaga-ingest` dan `siaga-geo-processor` publik (Package settings → Change visibility), lalu cek tanda tangan: `cosign verify ghcr.io/ramirezzserver/siaga-ingest:main --certificate-identity-regexp 'https://github.com/ramirezzServer/siaga/.*' --certificate-oidc-issuer https://token.actions.githubusercontent.com`.

Opsional, profil full (belum pernah dicoba): `make k3d-up && make tilt`; resource `geo-processor-migrate` harus selesai, lalu `geo-processor` Ready (Secret DB dari `dev-secrets.sh`, NATS dari chart). `ingest` dinyalakan manual dari dashboard Tilt; tanpa Garage di cluster ia tidak mengarsipkan payload.

Dashboard: salin ulang `deploy/observability/grafana/dashboards/siaga-pipa-data.json` ke Grafana Cloud (Import → Overwrite). Grafana lokal membacanya otomatis.

### Temuan yang perlu ditindaklanjuti (1e-2b-1)

- **Helm chart per layanan diganti Kustomize** (ADR 0016 butir 6); dokumen arsitektur perlu diselaraskan saat diperbarui.
- NetworkPolicy baru membatasi ingest dan geo-processor; default deny seluruh namespace menunggu policy NATS, Garage, dan Collector (1e-2b-2). Probe kubelet diasumsikan lolos NetworkPolicy k3s (kube-router mengizinkan lalu lintas dari node); pastikan saat profil full dicoba.
- Image dasar belum dipin digest; Renovate akan mengusulkan setelah GitHub App dipasang (utang lama).
- Import wilayah di cluster masih lewat port-forward dari laptop (`regions-import` di Tilt). Untuk produksi perlu Job yang mengunduh data batas wilayah (fase 2).

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
- **DNS WSL**: DNS tunneling WSL (resolver `10.255.255.254`) tidak jalan di laptop ini. `/etc/wsl.conf` berisi `[network] generateResolvConf = false`, dan `/etc/resolv.conf` tetap berisi `nameserver 1.1.1.1`, `nameserver 8.8.8.8`, `options use-vc`. Bila `go mod download`, `docker pull`, atau ingest tiba-tiba timeout setelah WSL dijalankan ulang, cek dulu isi `/etc/resolv.conf`.
- **Docker Desktop**: sebelum `wsl --shutdown`, Quit Docker Desktop dulu. Kalau tidak, integrasi WSL Docker gagal dengan "Catastrophic failure" saat WSL hidup lagi.
- Cadangan arsip: akun Backblaze B2 (region US West); bucket dan key dibuat `make backup-setup` (docs/setup/backblaze-b2.md). Master key B2 tidak disimpan di repo atau `.env`.
- Jaringan laptop bisa berpindah (Wi-Fi ke paket data); koneksi WSL bisa putus beberapa jam tanpa galat yang jelas (celah data 26 Sep 20.42–22.55 WIB). Celah OpenAQ ditutup dengan `make backfill-openaq`; sumber lain (prakiraan, Open-Meteo) memperbarui diri di polling berikutnya.

## Utang kecil yang diketahui

- Compose lite memakai `nats:2.11-alpine`, sedangkan test memakai server 2.15 dalam proses. Replay 1c dan 1d-1 sudah dijalankan dengan binary `nats-server` 2.11.9 tanpa masalah; naikkan image saat Renovate aktif.
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
- [x] **1e-1** Arsip payload mentah ke Garage (Compose lite, adapter S3, `INGEST_ARCHIVE_URL`), perintah `archive` (ls, verify, cp) dan `replay` dari arsip ke NATS urut waktu ambil (ADR 0014). Garage dan ingest ke Garage terverifikasi; `make archive-upload` dan `make replay ARGS="-publish=false -strict"` masuk verifikasi 1e-3a.
- [x] **1e-2a** OpenTelemetry di ingest dan geo-processor (trace lewat header `traceparent` NATS dan kolom outbox, metrik sumber/kuota/antrean/latensi/query, log OTLP), Grafana lokal `make obs-up` dan dashboard pipa data, panduan Grafana Cloud (ADR 0015). Terverifikasi di WSL; dashboard jalan di Grafana Cloud.
- [x] **1e-2b-1** Image ingest dan geo-processor (satu Dockerfile, amd64 + arm64, distroless nonroot), job CI `images` (uji asap, Trivy, push GHCR + SBOM + cosign di `main`) dan `manifests`, manifest Kustomize ingest + geo-processor + Job migrasi dengan overlay local/prod, panel keterlambatan dashboard tanpa jeda ekspor (ADR 0016). Terverifikasi di WSL; CI hijau dan paket GHCR publik.
- [x] **1e-2b-2** Garage satu node di cluster (ingest mengarsipkan langsung), OTel Collector `otelcol-k8s` (satu-satunya pemegang token Grafana Cloud, scrape metrik NATS dan Garage), secret produksi dalam satu `SopsSecret` terenkripsi age (`make secrets-prod`, sops-secrets-operator di fase 2), NetworkPolicy default deny namespace, baris dashboard platform (ADR 0017). Terverifikasi di WSL; CI hijau.
- [ ] **1e-2b-3** Retensi arsip per konektor lewat `archive prune` yang hanya menghapus objek yang sudah dicadangkan, cadangan harian ke Backblaze B2 (bukan Oracle: pendaftaran gagal di verifikasi kartu) sebagai satu `tar.zst` + indeks per konektor per hari, `backup-verify`, `restore`, CronJob `archive-maintenance`, `make backup-setup` (ADR 0022). Kode ditulis asisten; menunggu verifikasi WSL dan pembuatan bucket/key B2.
- [x] **1e-3a** Pengisian ulang jam OpenAQ (`backfill openaq`, nilai mentah, maks 7 hari), replay prakiraan BMKG dan OpenAQ dengan konteks dari isi payload (`MultiFeed`, `PartialFeed`), peta GeoJSON pemeriksaan titik sungai, panel "Target scrape" abu-abu saat kosong (ADR 0018). Terverifikasi di WSL.
- [x] **1e-3b-1** river-snap memilih sel dari debit rata-rata reanalisis `consolidated_v4` 2020-07..2022-06 dengan cache, titik uji, dan batas kuota berbobot (ADR 0019); 14 titik ditetapkan dari penelusuran debit sel GloFAS. Terverifikasi di WSL.
- [x] **1e-3b-2a** `make flood-threshold` (persentil debit harian reanalisis `consolidated_v4` 1997–2024 per titik, uji kejadian tercatat, rasio bias `seamless_v4`) dan `make rain-threshold` (7 sub-DAS Citarum Hulu dari HydroBASINS, puncak harian akumulasi hujan 3/6/24 jam arsip ECMWF IFS 2017–2024) (ADR 0020). Terverifikasi di WSL; ambang asli di `c0f4545`.
- [x] **1e-3b-2b** Konektor hujan sub-DAS (`ecmwf_ifs`, 47 sel, tiap 3 jam), ambang di geo-processor (skema `ref`, constraint DB), tingkat debit median + p75 (naik paling banyak satu) dan indeks hujan (maks Siaga), `hazard.flood.created|updated|expired` dengan kedaluwarsa 24 jam (ADR 0020 butir 4, 5, 8, 9; ADR 0021). Terverifikasi di WSL; CI hijau.
- [ ] **1e-3c** Arsip FIRMS standar (SP) → ambang titik api → `hazard.fire.*`; set berlabel T4 dan kalibrasi radius dirasakan dari arsip gempa; replay CAP (pasangkan dokumen id/en dengan entri RSS).

Catatan tertunda (dari sesi 28 Sep, di luar fase):

- Profil full (`make k3d-up && make tilt`) belum pernah dicoba; CloudNativePG di balik default deny paling berisiko.
- Runner `ubuntu-latest` pindah ke Ubuntu 26 mulai 2026-10-19 (lihat Utang kecil).

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

- Sesi 1e-3a: modul Go tidak lagi dirakit dari mirror. Pengguna menjalankan `ekspor-modcache.sh` di WSL (cache modul SIAGA di folder sementara, dikemas ke titipan), lalu workspace memakai `GOPROXY=file://…` dengan `GOFLAGS=-mod=mod`. Go 1.27.1 dibangun dari tag `go1.27.1` repo `golang/go` (bootstrap Go 1.24 bawaan); golangci-lint 2.13.2 dari rilis GitHub. Endpoint OpenAQ baru direkam dulu lewat skrip titipan (`ambil-sampel-openaq-jam.sh`) sebelum kode ditulis.

- Sesi 1e-3b-1: `modcache-siaga.tgz` (±659 MB) melewati batas 400 MB per file bridge desktop app, jadi tidak bisa dipakai. Paket yang dibutuhkan river-snap hanya perlu `google.golang.org/protobuf` (clone `protocolbuffers/protobuf-go` tag v1.36.12); modul lain di `go.mod` cukup `replace` ke folder berisi `go.mod` kosong di `go.work` sementara (`GOPROXY=off`). Untuk paket yang lebih banyak, pecah titipan cache per modul atau kecilkan dengan `go mod download` hanya untuk paket yang diuji.

- Sesi 1e-2b-2: rilis GitHub yang bisa diunduh dan dipakai menguji: otelcol-k8s (checksum per file `<nama>.sha256`, bukan file checksums gabungan), nats-server, prometheus-nats-exporter (`checksums.txt`, arsip `linux-x86_64`), sops, age, gitleaks. Garage tidak punya binary di GitHub (rilis di `garagehq.deuxfleurs.fr`, tidak terjangkau); source-nya ada di mirror `deuxfleurs-org/garage`. Repo Helm `isindir.github.io` tidak terjangkau, tetapi repo GitHub operator bisa di-clone. Uji Collector memakai `/etc/hosts` untuk nama Service (`garage`, `nats-0.nats-headless`).

- Sesi 1e-2b-1: workspace punya klien Docker tanpa daemon, dan registry Docker/GHCR tetap tidak terjangkau, jadi image tidak bisa di-build di sana. Rilis GitHub bisa diunduh: kustomize, kubeconform, Trivy 0.74 (misconfig memakai check bawaan), actionlint, hadolint, Prometheus 3.14. `get.helm.sh` tidak terjangkau (salah satu alasan Kustomize). Query dashboard diuji dengan `promtool tsdb create-blocks-from openmetrics` lalu query instan pada waktu tertentu.

- Sesi 1e-2a: pemeriksa keamanan workspace kembali menolak `GOSUMDB=off` sampai pengguna mengizinkan. Mirror GitHub tambahan: `open-telemetry/opentelemetry-go` (v1.46.0, semua submodul), `opentelemetry-go-contrib` (tag v1.46.0 untuk `bridges/otelslog` v0.20.1 dan `instrumentation/runtime` v0.71.0), `opentelemetry-proto-go` (`otlp/v1.11.0`), `opentelemetry-go-instrumentation` (`sdk/v1.2.1`), `grpc/grpc-go` v1.83.2, `googleapis/go-genproto` (sparse `googleapis/api` dan `googleapis/rpc`), `uber-go/multierr` v1.11.0 (untuk goose). Modul non-GitHub yang dibawa `go.mod` grpc (cloud.google.com/…, dll.) cukup `go.mod` kosong. goose dibangun dengan tag `no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb`. Rilis GitHub Prometheus, Tempo, dan Loki bisa diunduh (cek SHA256) untuk menguji query dashboard; image Docker dan Grafana (dl.grafana.com) tidak.

- Sesi 1e-3b-2a: workspace dimulai ulang tanpa Go dan clone; Go 1.27.1 dibangun ulang dari tag (bootstrap Go 1.24.7 bawaan, ±6 menit), protobuf-go v1.36.12 di-clone, modul lain di `go.mod` ingest/platform diganti folder berisi `go.mod` kosong lewat `go.work` sementara (`GOPROXY=off GOSUMDB=off`; jangan pakai `GOFLAGS=-mod=mod`, ditolak di workspace mode). Hanya paket yang tidak butuh NATS/OTel/AWS yang bisa dikompilasi dan di-lint. Open-Meteo, HydroSHEDS, dan Overpass dijangkau lewat browser bawaan desktop app (`javascript_tool`, fetch dari halaman situs yang mengizinkan CORS); hasil besar diolah di browser dan hanya ringkasannya dibawa ke workspace.

- Sesi 1e-3b-2b: modul Go dari `modcache-siaga.tgz` (titipan 28 Sep, 659 MB) dipecah di shell desktop app (`split -b 300M`, folder `modcache-pecahan/`), tiap bagian di-stage ke workspace, disambung, lalu dipakai sebagai `GOPROXY=file://…/cache/download GOSUMDB=off GOTOOLCHAIN=local`; semua modul (pgx, goose, nats, OTel) terkompilasi, jadi seluruh test dan golangci-lint bisa dijalankan. `ekspor-modcache.sh` di titipan kini langsung menulis pecahan ≤ 300 MB plus `SHA256SUMS` ke `modcache-pecahan/` (jalankan ulang hanya bila `go.mod` berubah); `modcache-siaga.tgz` utuh tidak dibutuhkan lagi. PostgreSQL 16 + PostGIS + TimescaleDB di workspace dipakai untuk test integrasi (`TestEndToEnd` dengan nats-server dalam proses). Prakiraan hujan asli 47 sel diambil lewat browser bawaan (`javascript_tool`, fetch dari tab open-meteo.com) dan disimpan sebagai rekaman test.

- Sesi 1e-2b-3: workspace baru; Go 1.27.1 dibangun dari tag (bootstrap Go 1.24.7, ±6 menit), modul dari `modcache-pecahan/` (tiga bagian di-stage, disambung, `GOPROXY=file://… GOSUMDB=off GOTOOLCHAIN=local`; `GOFLAGS` dikosongkan). Rilis GitHub yang dipakai: golangci-lint 2.13.2, kustomize 5.7.1, kubeconform 0.7.0 (skema dari raw.githubusercontent.com terjangkau), sops 3.13.3, age 1.3.2. Source Garage v2.3.0 dari mirror `deuxfleurs-org/garage` (sparse checkout `src/api`, `src/model`, `doc/book`) untuk memastikan dukungan lifecycle; source CLI `Backblaze/B2_Command_Line_Tool` untuk sintaks `key create`/`bucket create` dan keluaran `account authorize`. Dokumen backblaze.com terjangkau lewat WebFetch. Rasio kompresi diukur dengan sampel satu hari UTC penuh (`ukur-arsip.sh` menulis `sampel-arsip-<tgl>.tar` ke titipan).

Karena itu `go.sum` dari asisten tidak dipakai: patch tidak menyertakan `go.sum`/`go.work.sum`, dan `make deps` di WSL yang membuatnya.

Asisten bisa menulis ke `C:\KULIAH\PROJECT CODE\` lewat bridge desktop app (bukan ke folder WSL). File dari asisten dititipkan di sana (di luar folder repo), disalin ke `~/code/siaga`, dicek hash-nya, lalu file titipan dihapus. Perintah dijalankan pengguna di WSL, dan commit selalu dibuat dari WSL supaya hook lefthook ikut jalan.
