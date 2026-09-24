# 0012. Schema ts: hypertable deret waktu dan consumer-nya

Tanggal: 2026-09-24 · Status: diterima

## Konteks

Sejak 1c, ingest menerbitkan prakiraan BMKG per kelurahan/desa (`raw.forecast.bmkg`), dan 1d menambah prakiraan cuaca dan udara per simpul grid serta debit per titik sungai dari Open-Meteo (ADR 0011). Dokumen arsitektur menetapkan schema `ts` milik geo-processor dengan hypertable TimescaleDB (`weather_forecast`, `aq_forecast`, `river_discharge`, kemudian `aq_observation`, `hotspot`), kompresi setelah 7 hari, dan penghapusan setelah 1 tahun. Target demo fase 1 adalah dashboard Grafana berisi data live; fase 4 butuh riwayat prakiraan untuk mengukur dan mengoreksi galat model per horizon.

## Keputusan

1. **Setiap keluaran model disimpan utuh.** Kunci baris adalah (titik, model, waktu terbit, waktu berlaku). Waktu terbit BMKG adalah `analysis_date`; Open-Meteo tidak menyebut waktu jalan modelnya, jadi waktu terbit adalah waktu ingest pertama kali menerima isi itu (`meta.fetched_at`). Karena ingest hanya menerbitkan isi yang berubah dan jendela Open-Meteo tetap per hari (ADR 0011), satu waktu terbit = satu versi keluaran. Pesan ulangan tidak mengubah apa pun (`ON CONFLICT … WHERE … IS DISTINCT FROM`).
2. **Titik dipelajari dari event.** `ts.site` (ID `adm4:`, `grid:`, `river:`) diisi dari event raw; untuk simpul grid dan titik sungai, kelurahan/desa tempatnya dicari di `ref.region` (`ST_Covers`), NULL bila di laut. Menyimpang dari dokumen arsitektur yang menyebut `ref.river_station`: daftar titik sungai dimiliki ingest (ADR 0011), dan geo-processor tidak perlu salinan kedua yang bisa berbeda. `ts.series` mencatat per (titik, jenis data, model) sel yang dijawab sumber, elevasi, waktu terbit dan waktu ambil terbaru, serta kunci arsip: bahan panel "data terlambat".
3. **Hypertable dipartisi waktu berlaku.** Chunk 7 hari untuk cuaca dan udara, 90 hari untuk debit harian; `segmentby` (titik, model), `orderby` waktu berlaku lalu waktu terbit. Kebijakan columnstore setelah 7 hari dan retensi 1 tahun dibuat migrasi dengan API TimescaleDB 2.18+ (`add_columnstore_policy`). Kolom nilai bertipe `real`: presisi 7 digit cukup untuk semua besaran dan separuh ukuran `double precision`.
4. **Invarian dua lapis.** Batas nilai, urutan statistik ensemble (min ≤ P25 ≤ median ≤ P75 ≤ maks, min ≤ rata-rata ≤ maks), tanggal debit pukul 00.00 UTC, jangkauan waktu berlaku terhadap waktu terbit, dan kecocokan jenis titik dengan ID dijaga di `internal/domain/series` dan constraint migrasi `00004_ts_series.sql`. Pembandingan urutan ensemble di domain memakai presisi `real` supaya nilai yang hanya berbeda di digit ke-9 tidak lolos domain lalu ditolak database.
5. **Satu consumer durable per subjek.** `geo-processor-forecast-bmkg`, `-forecast-openmeteo`, `-aq-openmeteo`, `-flood-openmeteo`, masing-masing satu jenis pesan Protobuf, memakai handler generik yang sama (ack, retry 1/5/15/30 detik, DLQ). Satu pesan = satu transaksi (titik, deret, semua langkah lewat `unnest` array). Consumer baru membaca stream `RAW` dari awal, jadi prakiraan BMKG yang terbit sejak 1c (retensi 7 hari) ikut tersimpan.
6. **Tampilan keluaran terbaru.** `ts.*_current` memilih waktu terbit terbaru per (titik, model, waktu berlaku) untuk jendela sekitar sekarang (cuaca dan udara mulai 1 hari lalu, debit 7 hari lalu), supaya dashboard dan API tidak memindai setahun data. Semua tabel dan tampilan bisa dibaca role layanan lain (`SELECT`).
7. **Tidak ada kejadian bahaya dari deret waktu di 1d.** `hazard.flood.*` dan `hazard.fire.*` butuh ambang terkalibrasi (persentil reanalisis GloFAS, arsip FIRMS) dan titik sungai terverifikasi; dikerjakan setelah arsip dan kalibrasi fase 1e.

## Konsekuensi

- Volume per hari: prakiraan BMKG ±5.957 desa × 20 langkah × jumlah analisis baru; grid Open-Meteo 70 × 96 langkah per keluaran berubah (cuaca beberapa kali sehari, CAMS 2 kali). Dalam urutan ratusan ribu baris per hari sebelum kompresi, wajar untuk TimescaleDB di laptop maupun VM 12 GB.
- Karena kode cuaca BMKG dan WMO berbeda sistem, `weather_code` harus dibaca bersama `model` (`bmkg` atau lainnya); deskripsi teks tidak disimpan per baris.
- Menulis ke chunk yang sudah dikompres (misal koreksi BMKG untuk analisis lama) tetap bisa, tetapi lebih lambat; kasus ini jarang.
- Uji integrasi deret waktu memakai waktu relatif terhadap sekarang dan titik di pojok kotak Indonesia (-11,5; 141,5), karena kebijakan retensi bisa membuang chunk bertahun 2001 yang dipakai uji lain.
