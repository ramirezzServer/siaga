# Kontrak event

Event antar-layanan didefinisikan di `contracts/proto` dan dikirim lewat NATS JetStream dalam format biner Protobuf. Definisi stream dan pembentuk subjek ada di `libs/go/contracts/streams` (Go) dan `subjects` di `@siaga/contracts` (TypeScript); keduanya harus sama dengan tabel di sini.

## Header pesan

| Header             | Isi                                                                                                                                                                                                                                               |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Nats-Msg-Id`      | ID deterministik untuk deduplikasi JetStream (aturannya per jenis event, lihat di bawah)                                                                                                                                                          |
| `Content-Type`     | `application/protobuf`                                                                                                                                                                                                                            |
| `traceparent`      | W3C Trace Context dari span penerbit (ADR 0015). Consumer memulai span pemrosesan sebagai anaknya; `hazard.*` membawa konteks dari pemrosesan `raw.*` penyebabnya (disimpan di `hazard.outbox.traceparent`). Boleh kosong: consumer harus toleran |
| `tracestate`       | W3C Trace Context, bila ada                                                                                                                                                                                                                       |
| `Siaga-Fetched-At` | Hanya `raw.*`: waktu ingest mengambil payload (RFC 3339 UTC, sama dengan `meta.fetched_at`), untuk metrik latensi pipa tanpa membuka isi pesan. Pesan dari sebelum fase 1e-2 tidak membawanya                                                     |

## Stream

| Stream   | Subjek     | Retensi | Jendela duplikat | Batas ukuran | Pemilik       |
| -------- | ---------- | ------- | ---------------- | ------------ | ------------- |
| `RAW`    | `raw.>`    | 7 hari  | 24 jam           | 2 GiB        | ingest        |
| `HAZARD` | `hazard.>` | 30 hari | 24 jam           | 1 GiB        | geo-processor |
| `DLQ`    | `dlq.>`    | 30 hari | 24 jam           | 256 MiB      | bersama       |

Pemilik adalah satu-satunya layanan yang membuat dan memperbarui konfigurasi stream (`natsx.EnsureStream` menolak layanan lain), sama seperti satu role pemilik per schema database. `DLQ` milik bersama (`streams.OwnerShared`): setiap konsumen memastikan stream ada, dengan konfigurasi yang selalu berasal dari `libs/go/contracts/streams`.

## Consumer

| Consumer (durable)                 | Stream | Filter                   | Layanan       | Ack                         |
| ---------------------------------- | ------ | ------------------------ | ------------- | --------------------------- |
| `geo-processor-quake`              | `RAW`  | `raw.quake.>`            | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-weather`            | `RAW`  | `raw.weather.>`          | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-forecast-bmkg`      | `RAW`  | `raw.forecast.bmkg`      | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-forecast-openmeteo` | `RAW`  | `raw.forecast.openmeteo` | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-aq-openmeteo`       | `RAW`  | `raw.aq.openmeteo`       | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-flood-openmeteo`    | `RAW`  | `raw.flood.openmeteo`    | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-aq-openaq`          | `RAW`  | `raw.aq.openaq`          | geo-processor | eksplisit, `AckWait` 30 dtk |
| `geo-processor-fire-firms`         | `RAW`  | `raw.fire.firms`         | geo-processor | eksplisit, `AckWait` 30 dtk |

Consumer deret waktu satu per subjek karena tiap subjek membawa jenis pesan yang berbeda; semuanya menulis ke hypertable schema `ts` (ADR 0012, 0013). Consumer baru membaca stream `RAW` dari awal (retensi 7 hari), jadi prakiraan BMKG yang terbit sejak fase 1c ikut tersimpan.

Batas percobaan diatur aplikasi, bukan server (`MaxDeliver = -1`): galat sementara dicoba ulang dengan jeda 1, 5, 15, 30 detik; pesan yang rusak, melanggar invarian, atau gagal 5 kali disalin ke `dlq.<layanan>` lalu dihentikan (`Term`). Lihat ADR 0007.

## Subjek

| Subjek                   | Pesan                                 | Penerbit      | Stream   |
| ------------------------ | ------------------------------------- | ------------- | -------- |
| `raw.quake.bmkg`         | `siaga.raw.v1.QuakeReport`            | ingest        | `RAW`    |
| `raw.quake.usgs`         | `siaga.raw.v1.QuakeReport`            | ingest        | `RAW`    |
| `raw.weather.bmkg`       | `siaga.raw.v1.WeatherWarning`         | ingest        | `RAW`    |
| `raw.forecast.bmkg`      | `siaga.raw.v1.RegionForecast`         | ingest        | `RAW`    |
| `raw.forecast.openmeteo` | `siaga.raw.v1.GridWeatherForecast`    | ingest        | `RAW`    |
| `raw.aq.openmeteo`       | `siaga.raw.v1.AirQualityForecast`     | ingest        | `RAW`    |
| `raw.flood.openmeteo`    | `siaga.raw.v1.RiverDischargeForecast` | ingest        | `RAW`    |
| `raw.aq.openaq`          | `siaga.raw.v1.AirQualityObservation`  | ingest        | `RAW`    |
| `raw.fire.firms`         | `siaga.raw.v1.FireDetection`          | ingest        | `RAW`    |
| `hazard.<jenis>.created` | `siaga.hazard.v1.HazardCreated`       | geo-processor | `HAZARD` |
| `hazard.<jenis>.updated` | `siaga.hazard.v1.HazardUpdated`       | geo-processor | `HAZARD` |
| `hazard.<jenis>.expired` | `siaga.hazard.v1.HazardExpired`       | geo-processor | `HAZARD` |
| `dlq.<layanan>`          | payload asli, apa adanya              | konsumen      | `DLQ`    |

`<jenis>`: `quake`, `weather`, `flood`, `fire`, `aq`. `aq.*`, `report.*`, `notify.*` ditambahkan di fase 3–4.

## Event raw

Event `raw.*` adalah satu record dari satu feed sumber, sudah divalidasi dan waktunya UTC, tetapi belum dinormalisasi atau dideduplikasi lintas sumber. Payload mentah utuh disimpan di arsip ingest (Garage, `s3://siaga-arsip/raw/`); event membawa kuncinya di `meta.archive_key`. Perintah `replay` menerbitkan ulang event dari arsip dengan `meta` dan `Nats-Msg-Id` yang sama seperti saat polling langsung, jadi konsumen tidak bisa (dan tidak perlu) membedakan event replay dari event asli (ADR 0014).

- **ID pesan** = 128 bit pertama SHA-256 dari (nama konektor, ID record di sumber, isi record tanpa `meta`). Isi yang sama menghasilkan ID yang sama sehingga ditolak JetStream selama jendela duplikat; revisi dari sumber (misal magnitudo diperbarui) menghasilkan ID baru dan ikut terbit.
- **Tidak berurutan dan bisa berulang.** Ingest menerbitkan ulang record yang sama setelah restart (JetStream menolaknya bila masih dalam 24 jam). Konsumen wajib idempotent terhadap `source_event_id` + isi.
- **ID kejadian BMKG** adalah waktu kejadian UTC `YYYYMMDDhhmmss`, karena BMKG tidak menerbitkan ID. Nilainya sama di `autogempa`, `gempaterkini`, dan `gempadirasakan`, jadi geo-processor bisa menggabungkan ketiganya.
- **Peringatan cuaca** (`raw.weather.bmkg`): satu event per pesan CAP BMKG (Alert, Update, Cancel) dengan teks bahasa Indonesia wajib dan bahasa Inggris bila tersedia; ID record = identifier CAP. Hanya provinsi di `INGEST_CAP_PROVINCES` (default `32`) yang diambil; pesan latihan, uji, draft, `Ack`, dan `Error` tidak diteruskan. Terjemahan yang datang belakangan terbit ulang sebagai revisi (ADR 0009).
- **Prakiraan cuaca** (`raw.forecast.bmkg`): satu event per kelurahan/desa (kode adm4) berisi 20 langkah per 3 jam; ID record = kode adm4. Hanya prakiraan yang isinya berubah yang terbit.
- **Keluaran model grid Open-Meteo** (`raw.forecast.openmeteo`, `raw.aq.openmeteo`, `raw.flood.openmeteo`): satu event per titik pantau (`site.id` = `grid:<lintang>:<bujur>` atau `river:<slug>`), berisi semua langkah jendela yang diminta (cuaca dan udara per jam dari 00.00 UTC hari ini sampai +3 hari, debit harian 4 hari lalu sampai +9 hari). `site.cell` adalah pusat sel model yang dijawab sumber; field kosong berarti model tidak memberi nilai. Hanya titik yang isinya berubah yang terbit; waktu terbit keluaran di `ts.*` adalah `meta.fetched_at` (ADR 0011, 0012).
- **Pengukuran stasiun OpenAQ** (`raw.aq.openaq`): satu event per stasiun (`station.id` = `openaq:<ID lokasi>`) berisi nilai terbaru setiap sensor parameter SIAGA (`pm25`, `pm10`, `no2`, `o3`, `so2`, `co`) dalam satuan sumber (`µg/m³`, `ppm`, `ppb`), urut ID sensor. Nilai yang lebih tua dari 1 hari tidak dikirim. Stasiun hanya diambil ulang bila `datetimeLast`-nya berubah, dan hanya terbit bila isinya berubah (ADR 0013).
- **Titik panas FIRMS** (`raw.fire.firms`): satu event per deteksi (`id` = `<produk>:<yyyymmddThhmm>:<lintang>:<bujur>`, lima desimal) dari produk NRT VIIRS SNPP, NOAA-20, NOAA-21, dan MODIS dalam kotak Jawa Barat, jendela 2 hari. Keyakinan MODIS dipetakan ke kelas (< 30 rendah, 30–79 nominal, >= 80 tinggi) dan persentase aslinya ikut dikirim (ADR 0013).
- **USGS** hanya diteruskan untuk gempa di kotak Indonesia (lintang −12..7, bujur 94..142). Flag `tsunami` USGS tidak dipakai karena bukan peringatan.

## Event hazard

Event `hazard.*` adalah keadaan kejadian ternormalisasi, diterbitkan geo-processor lewat outbox transaksional (ADR 0007).

- **ID kejadian gempa** adalah UUIDv8 dari SHA-256 (sumber, ID sumber) laporan pertama yang membentuk kejadian. ID tidak berubah walau sumber utama berganti (misal USGS datang lebih dulu, lalu BMKG menjadi sumber utama).
- **ID kejadian cuaca** adalah UUIDv8 dari SHA-256 (penerbit, identifier) pendiri rantai CAP: pesan dengan waktu kirim paling awal di antara pesan dan semua `references`-nya. Satu rantai Alert → Update → Cancel adalah satu kejadian, apa pun urutan kedatangannya (ADR 0010). Area kejadian ada di `area_geojson` (MultiPolygon hasil gabungan poligon BMKG), detailnya di `weather`.
- **ID pesan** = `<id kejadian>:r<revisi>`. `Hazard.revision` dan `HazardExpired.revision` naik satu setiap kali isi berubah; konsumen mengabaikan revisi yang lebih kecil dari yang sudah dimiliki. Pesan raw yang diproses ulang tidak menghasilkan event baru.
- **Transisi**: `created` saat kejadian baru, `updated` saat isi atau tingkat berubah (termasuk koreksi sesudah kedaluwarsa), `expired` saat masa aktif habis (`ELAPSED`, gempa 6 jam, cuaca sesuai `expires` CAP), digabung ke kejadian lain (`MERGED`, dengan `merged_into_hazard_id`), atau sumber menarik laporannya (`RETRACTED`, termasuk CAP Cancel). Kejadian yang sudah berakhir bisa aktif lagi lewat `updated` (misal pembaruan CAP yang berlaku lagi); konsumen selalu menerima `created` lebih dulu.
- **Wilayah terdampak** di `impacted_regions` dibatasi 100 kelurahan/desa (gempa: terdekat; cuaca: bagian luas tercakup terbesar, minimal `WEATHER_MIN_COVERAGE`); jumlah lengkapnya di `impacted_region_count` dan daftar lengkapnya di tabel `hazard.impact_region`.
- **Tingkat** (`level`) adalah tingkat tertinggi di wilayah pantauan. Tingkat per lokasi pengguna dihitung alert-engine (fase 3).

## Pesan DLQ

Isi pesan DLQ adalah payload asli tanpa perubahan. `Nats-Msg-Id` = `<stream asal>:<sequence asal>`, jadi pesan yang sama tidak masuk dua kali. Asal dan alasannya ada di header:

| Header                | Isi                                                |
| --------------------- | -------------------------------------------------- |
| `Siaga-Dlq-Subject`   | Subjek asal, misal `raw.quake.bmkg`                |
| `Siaga-Dlq-Stream`    | Stream asal                                        |
| `Siaga-Dlq-Sequence`  | Nomor urut pesan di stream asal                    |
| `Siaga-Dlq-Consumer`  | Nama consumer yang gagal                           |
| `Siaga-Dlq-Delivered` | Jumlah pengiriman sebelum dipindah                 |
| `Siaga-Dlq-Error`     | Galat terakhir, satu baris, paling banyak 512 byte |

## Aturan perubahan

`buf breaking` (WIRE_JSON) wajib lulus; field tidak pernah dihapus, hanya ditandai `reserved`. Perubahan subjek atau stream diperbarui di tabel ini, di `libs/go/contracts/streams`, dan di `subjects.ts` dalam commit yang sama.
