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

Pemilik adalah satu-satunya layanan yang membuat dan memperbarui konfigurasi stream (`natsx.EnsureStream` menolak layanan lain), sama seperti satu role pemilik per schema database. Stream `DLQ` (`dlq.<layanan>`) ditambahkan bersama consumer pertama di fase 1b.

## Subjek

| Subjek                   | Pesan                           | Penerbit      | Stream   |
| ------------------------ | ------------------------------- | ------------- | -------- |
| `raw.quake.bmkg`         | `siaga.raw.v1.QuakeReport`      | ingest        | `RAW`    |
| `raw.quake.usgs`         | `siaga.raw.v1.QuakeReport`      | ingest        | `RAW`    |
| `hazard.<jenis>.created` | `siaga.hazard.v1.HazardCreated` | geo-processor | `HAZARD` |
| `hazard.<jenis>.updated` | `siaga.hazard.v1.HazardUpdated` | geo-processor | `HAZARD` |
| `hazard.<jenis>.expired` | `siaga.hazard.v1.HazardExpired` | geo-processor | `HAZARD` |

`<jenis>`: `quake`, `weather`, `flood`, `fire`, `aq`. Subjek `raw.<jenis>.<sumber>` untuk cuaca, banjir, udara, dan titik api ditambahkan di fase 1c–1d; `aq.*`, `report.*`, `notify.*` di fase 3–4.

## Event raw

Event `raw.*` adalah satu record dari satu feed sumber, sudah divalidasi dan waktunya UTC, tetapi belum dinormalisasi atau dideduplikasi lintas sumber. Payload mentah utuh disimpan di arsip ingest; event membawa kuncinya di `meta.archive_key`.

- **ID pesan** = 128 bit pertama SHA-256 dari (nama konektor, ID record di sumber, isi record tanpa `meta`). Isi yang sama menghasilkan ID yang sama sehingga ditolak JetStream selama jendela duplikat; revisi dari sumber (misal magnitudo diperbarui) menghasilkan ID baru dan ikut terbit.
- **Tidak berurutan dan bisa berulang.** Ingest menerbitkan ulang record yang sama setelah restart (JetStream menolaknya bila masih dalam 24 jam). Konsumen wajib idempotent terhadap `source_event_id` + isi.
- **ID kejadian BMKG** adalah waktu kejadian UTC `YYYYMMDDhhmmss`, karena BMKG tidak menerbitkan ID. Nilainya sama di `autogempa`, `gempaterkini`, dan `gempadirasakan`, jadi geo-processor bisa menggabungkan ketiganya.
- **USGS** hanya diteruskan untuk gempa di kotak Indonesia (lintang −12..7, bujur 94..142). Flag `tsunami` USGS tidak dipakai karena bukan peringatan.

## Aturan perubahan

`buf breaking` (WIRE_JSON) wajib lulus; field tidak pernah dihapus, hanya ditandai `reserved`. Perubahan subjek atau stream diperbarui di tabel ini, di `libs/go/contracts/streams`, dan di `subjects.ts` dalam commit yang sama.
