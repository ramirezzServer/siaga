# Kontrak event

Event antar-layanan didefinisikan di `contracts/proto` dan dikirim lewat NATS JetStream dalam format biner Protobuf. Definisi stream dan pembentuk subjek ada di `libs/go/contracts/streams` (Go) dan `subjects` di `@siaga/contracts` (TypeScript); keduanya harus sama dengan tabel di sini.

## Header pesan

| Header         | Isi                                                                                                      |
| -------------- | -------------------------------------------------------------------------------------------------------- |
| `Nats-Msg-Id`  | ID deterministik untuk deduplikasi JetStream (aturannya per jenis event, lihat di bawah)                 |
| `Content-Type` | `application/protobuf`                                                                                   |
| `traceparent`  | W3C Trace Context. Mulai diisi saat OpenTelemetry dipasang (fase 1e); konsumen harus toleran bila kosong |

## Stream

| Stream   | Subjek     | Retensi | Jendela duplikat | Batas ukuran | Pemilik       |
| -------- | ---------- | ------- | ---------------- | ------------ | ------------- |
| `RAW`    | `raw.>`    | 7 hari  | 24 jam           | 2 GiB        | ingest        |
| `HAZARD` | `hazard.>` | 30 hari | 24 jam           | 1 GiB        | geo-processor |
| `DLQ`    | `dlq.>`    | 30 hari | 24 jam           | 256 MiB      | bersama       |

Pemilik adalah satu-satunya layanan yang membuat dan memperbarui konfigurasi stream (`natsx.EnsureStream` menolak layanan lain), sama seperti satu role pemilik per schema database. `DLQ` milik bersama (`streams.OwnerShared`): setiap konsumen memastikan stream ada, dengan konfigurasi yang selalu berasal dari `libs/go/contracts/streams`.

## Consumer

| Consumer (durable)    | Stream | Filter        | Layanan       | Ack                         |
| --------------------- | ------ | ------------- | ------------- | --------------------------- |
| `geo-processor-quake` | `RAW`  | `raw.quake.>` | geo-processor | eksplisit, `AckWait` 30 dtk |

Batas percobaan diatur aplikasi, bukan server (`MaxDeliver = -1`): galat sementara dicoba ulang dengan jeda 1, 5, 15, 30 detik; pesan yang rusak, melanggar invarian, atau gagal 5 kali disalin ke `dlq.<layanan>` lalu dihentikan (`Term`). Lihat ADR 0007.

## Subjek

| Subjek                   | Pesan                           | Penerbit      | Stream   |
| ------------------------ | ------------------------------- | ------------- | -------- |
| `raw.quake.bmkg`         | `siaga.raw.v1.QuakeReport`      | ingest        | `RAW`    |
| `raw.quake.usgs`         | `siaga.raw.v1.QuakeReport`      | ingest        | `RAW`    |
| `hazard.<jenis>.created` | `siaga.hazard.v1.HazardCreated` | geo-processor | `HAZARD` |
| `hazard.<jenis>.updated` | `siaga.hazard.v1.HazardUpdated` | geo-processor | `HAZARD` |
| `hazard.<jenis>.expired` | `siaga.hazard.v1.HazardExpired` | geo-processor | `HAZARD` |
| `dlq.<layanan>`          | payload asli, apa adanya        | konsumen      | `DLQ`    |

`<jenis>`: `quake`, `weather`, `flood`, `fire`, `aq`. Subjek `raw.<jenis>.<sumber>` untuk cuaca, banjir, udara, dan titik api ditambahkan di fase 1c–1d; `aq.*`, `report.*`, `notify.*` di fase 3–4.

## Event raw

Event `raw.*` adalah satu record dari satu feed sumber, sudah divalidasi dan waktunya UTC, tetapi belum dinormalisasi atau dideduplikasi lintas sumber. Payload mentah utuh disimpan di arsip ingest; event membawa kuncinya di `meta.archive_key`.

- **ID pesan** = 128 bit pertama SHA-256 dari (nama konektor, ID record di sumber, isi record tanpa `meta`). Isi yang sama menghasilkan ID yang sama sehingga ditolak JetStream selama jendela duplikat; revisi dari sumber (misal magnitudo diperbarui) menghasilkan ID baru dan ikut terbit.
- **Tidak berurutan dan bisa berulang.** Ingest menerbitkan ulang record yang sama setelah restart (JetStream menolaknya bila masih dalam 24 jam). Konsumen wajib idempotent terhadap `source_event_id` + isi.
- **ID kejadian BMKG** adalah waktu kejadian UTC `YYYYMMDDhhmmss`, karena BMKG tidak menerbitkan ID. Nilainya sama di `autogempa`, `gempaterkini`, dan `gempadirasakan`, jadi geo-processor bisa menggabungkan ketiganya.
- **USGS** hanya diteruskan untuk gempa di kotak Indonesia (lintang −12..7, bujur 94..142). Flag `tsunami` USGS tidak dipakai karena bukan peringatan.

## Event hazard

Event `hazard.*` adalah keadaan kejadian ternormalisasi, diterbitkan geo-processor lewat outbox transaksional (ADR 0007).

- **ID kejadian** adalah UUIDv8 dari SHA-256 (sumber, ID sumber) laporan pertama yang membentuk kejadian. ID tidak berubah walau sumber utama berganti (misal USGS datang lebih dulu, lalu BMKG menjadi sumber utama).
- **ID pesan** = `<id kejadian>:r<revisi>`. `Hazard.revision` dan `HazardExpired.revision` naik satu setiap kali isi berubah; konsumen mengabaikan revisi yang lebih kecil dari yang sudah dimiliki. Pesan raw yang diproses ulang tidak menghasilkan event baru.
- **Transisi**: `created` saat kejadian baru, `updated` saat isi atau tingkat berubah (termasuk koreksi sesudah kedaluwarsa), `expired` saat masa aktif habis (`ELAPSED`, gempa 6 jam), digabung ke kejadian lain (`MERGED`, dengan `merged_into_hazard_id`), atau semua sumber menarik laporannya (`RETRACTED`).
- **Wilayah terdampak** di `impacted_regions` dibatasi 100 kelurahan/desa terdekat; jumlah lengkapnya di `impacted_region_count` dan daftar lengkapnya di tabel `hazard.impact_region`.
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
