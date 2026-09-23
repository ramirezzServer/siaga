# Kontrak event

Event antar-layanan didefinisikan di `contracts/proto` dan dikirim lewat NATS JetStream dalam format biner Protobuf. Header pesan wajib: `Nats-Msg-Id` (ID deterministik untuk deduplikasi JetStream), `Content-Type: application/protobuf`, dan `traceparent` (W3C Trace Context).

| Subjek                   | Pesan                           | Penerbit      | Stream (retensi)   |
| ------------------------ | ------------------------------- | ------------- | ------------------ |
| `hazard.<jenis>.created` | `siaga.hazard.v1.HazardCreated` | geo-processor | `HAZARD` (30 hari) |
| `hazard.<jenis>.updated` | `siaga.hazard.v1.HazardUpdated` | geo-processor | `HAZARD`           |
| `hazard.<jenis>.expired` | `siaga.hazard.v1.HazardExpired` | geo-processor | `HAZARD`           |

`<jenis>`: `quake`, `weather`, `flood`, `fire`, `aq`. Pembentuk subjek di TypeScript: `subjects.hazard()` di `@siaga/contracts`.

Subjek `raw.*`, `aq.*`, `report.*`, `notify.*`, dan `dlq.*` dari dokumen arsitektur ditambahkan ke tabel ini saat kontraknya dibuat di fase 1–3. Aturan perubahan: `buf breaking` (WIRE_JSON) wajib lulus; field tidak pernah dihapus, hanya ditandai `reserved`.
