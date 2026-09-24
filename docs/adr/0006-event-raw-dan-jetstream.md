# 0006. Event raw dan pengelolaan JetStream

Tanggal: 2026-09-24 · Status: diterima

## Konteks

Fase 1 memulai pipa data: ingest mengambil data sumber dan menerbitkannya ke NATS JetStream untuk dinormalisasi geo-processor. Dokumen arsitektur menetapkan subjek `raw.<jenis>.<sumber>` dan ID event deterministik, tetapi belum menetapkan isi event raw, cara ID bekerja saat sumber merevisi data, siapa yang membuat stream, dan bagaimana menguji JetStream.

## Keputusan

1. **Event raw bertipe, payload mentah diarsip.** `raw.*` berisi pesan Protobuf per jenis data (`siaga.raw.v1.QuakeReport`), hasil parse dan validasi ingest dengan waktu UTC. Payload asli disimpan terpisah di arsip dengan kunci `<konektor>/<YYYY>/<MM>/<DD>/<hhmmss>Z-<sha256[:12]>.<ext>.gz`, dan kuncinya ikut di `meta.archive_key`. Konsumen tidak perlu tahu format BMKG atau USGS.
2. **ID pesan per revisi.** `Nats-Msg-Id` = hash (konektor, ID record, isi tanpa meta). Ingest menyimpan hash isi terakhir per record di memori dan hanya menerbitkan record baru atau yang berubah. Setelah restart, JetStream menolak ulangan lewat jendela duplikat 24 jam.
3. **Stream dimiliki penerbitnya.** Definisi stream ada di `libs/go/contracts/streams` sebagai data tanpa dependensi NATS. `natsx.EnsureStream` di `libs/go/platform` hanya mengizinkan pemilik (`RAW` → ingest, `HAZARD` → geo-processor) membuat atau memperbarui stream.
4. **JetStream diuji dengan server di dalam proses.** Test adapter memakai `nats-server/v2` sebagai library (`natsx/natstest`), bukan Testcontainers. Test berjalan di `go test` biasa, di laptop dan CI, tanpa Docker.
5. **Arsip filesystem dulu, Garage di fase 1e.** Port `Archive` sudah ada; adapter `fsarchive` dipakai sekarang. Kunci arsip sama dengan kunci objek Garage nanti, jadi folder arsip bisa diunggah apa adanya dan dipakai sebagai bahan uji replay. Kegagalan arsip tidak menghentikan penerbitan event.
6. **Anggaran request dengan GCRA.** Pembatas laju memakai aritmetika waktu bilangan bulat sehingga jaminannya bisa diuji dengan fuzzing: paling banyak `PerMinute + Burst − 1` request dalam jendela 60 detik mana pun. BMKG: 55/menit, burst 5 (≤ 59 < 60 per IP). USGS: 6/menit, burst 2.
7. **USGS lewat feed ringkasan `2.5_day`.** Bukan API query: feed statis lebih ringan untuk USGS, mendukung conditional request, dan jendela 24 jam menutup celah bila ingest mati beberapa jam. Disaring ke kotak Indonesia di ingest.

## Konsekuensi

- geo-processor menerima data yang sudah bersih dan seragam, tetapi harus idempotent terhadap ulangan dan revisi.
- Perubahan format BMKG hanya menyentuh `adapters/bmkg`; payload yang tak terbaca tetap diarsipkan untuk diselidiki dan konektor ditandai gagal di `/status`.
- `libs/go/platform` kini bergantung pada `libs/go/contracts` dan `nats.go`; modul yang memakai platform di luar workspace mode perlu `replace` ke contracts (sudah ada di geo-processor dan ingest).
- Menyimpang dari dokumen arsitektur yang menyebut Testcontainers untuk NATS; Testcontainers tetap dipakai untuk PostgreSQL.
