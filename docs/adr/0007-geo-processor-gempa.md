# 0007. geo-processor: pengelompokan gempa, outbox, dan DLQ

Tanggal: 2026-09-24 · Status: diterima

## Konteks

Fase 1b menjadikan geo-processor konsumen pertama JetStream. Event `raw.quake.*` datang dari dua sumber (BMKG tiga feed, USGS satu feed), tidak berurutan, bisa berulang, dan bisa direvisi. BMKG tidak menerbitkan ID, jadi ingest memakai waktu kejadian sebagai ID; revisi waktu oleh BMKG menghasilkan ID baru untuk gempa yang sama. USGS bisa menarik kejadian ("deleted") dan kadang berganti ID utama. Dokumen arsitektur dan PRD hanya menyebut aturan deduplikasi (selisih waktu, jarak, magnitudo) dan target T4 (presisi dan recall ≥ 99%), belum cara menjaga hasilnya tetap benar di bawah kondisi itu.

## Keputusan

1. **Laporan disimpan per (sumber, ID sumber, feed) dengan urutan total.** Laporan terbaru menurut (waktu revisi sumber, waktu ambil, digest isi) menang, dan `first_seen_at` selalu waktu ambil paling awal. Keadaan tersimpan hanya bergantung pada himpunan pesan yang pernah diterima, bukan urutannya, jadi consumer boleh menerima ulangan dan pesan terlambat. Diuji dengan fuzzing (`FuzzApplyOrderIndependent`).
2. **Satu solusi per sumber, lalu dikelompokkan.** Feed BMKG untuk ID yang sama digabung menjadi satu solusi (angka dari laporan terbaru, teks dari laporan terbaru yang mengisinya). Kejadian adalah kumpulan solusi dengan aturan:
   - Solusi dari sumber berbeda bergabung bila memenuhi ambang lintas sumber (ADR 0008).
   - Dua ID dari sumber yang sama bergabung hanya bila memenuhi ambang revisi yang jauh lebih ketat (10 dtk, 50 km, 1,0), atau saling menyebut sebagai ID alternatif (USGS). Yang terakhir terlihat berlaku sebagai solusi sumber itu.
   - Satu kejadian tidak pernah memuat dua gempa berbeda dari satu sumber. Tanpa aturan ini, dua gempa berdekatan yang dilaporkan BMKG bisa tergabung karena USGS hanya melaporkan salah satunya.
   - Invarian yang dijaga setelah setiap pesan: setiap pasangan solusi yang berlaku dari sumber berbeda dalam satu kejadian memenuhi ambang lintas sumber. `quake.Settle` menempatkan ulang solusi lama yang kembali berlaku (misal revisi BMKG terbaru keluar dari kejadian) sampai stabil.
3. **Kejadian lengket, tetapi bisa bergabung dan berpisah.** Revisi yang masih cocok tetap di kejadiannya. Kejadian satu anggota yang revisinya masuk jangkauan kejadian lain digabung ke sana (status `merged`, `hazard.quake.expired` dengan `reason = MERGED` dan `merged_into_hazard_id`). Revisi yang tidak lagi cocok memisahkan diri menjadi kejadian baru. Laporan USGS yang ditarik keluar dari kejadian; kejadian tanpa anggota menjadi `retracted`.
4. **ID kejadian** = UUIDv8 dari SHA-256 (sumber, ID sumber) laporan pertama yang membentuk kejadian, tidak berubah walau sumber utamanya kemudian BMKG. ID yang pernah dipakai tidak dipakai ulang (generasi berikutnya dipakai), jadi kejadian yang sudah `merged` tidak pernah hidup kembali.
5. **Satu transaksi per pesan, dikunci advisory lock.** Laporan, keanggotaan, kejadian, wilayah terdampak, dan pesan keluar ditulis dalam satu transaksi PostgreSQL yang memegang `pg_advisory_xact_lock`. Pengelompokan butuh melihat kandidat dan menulis hasilnya secara atomik; gempa hanya beberapa per menit, jadi serialisasi global jauh lebih sederhana daripada penguncian per baris dan tetap cukup cepat.
6. **Outbox transaksional untuk `hazard.*`.** Pesan ditulis ke `hazard.outbox` dalam transaksi yang sama, lalu relay menerbitkannya ke JetStream dan menghapusnya (`FOR UPDATE SKIP LOCKED`, aman untuk banyak replika). `Nats-Msg-Id` = `<id kejadian>:r<revisi>`, jadi terbit ulang setelah crash ditolak sebagai duplikat. Revisi hanya naik bila digest isi yang diterbitkan berubah, sehingga pesan raw yang diproses ulang tidak menghasilkan event baru.
7. **DLQ dikelola aplikasi, bukan batas MaxDeliver server.** Consumer durable `geo-processor-quake` (pull, ack eksplisit, `MaxDeliver = -1`). Handler memutuskan: ack; coba ulang dengan jeda 1/5/15/30 dtk untuk galat sementara; atau salin ke `dlq.geo-processor` (stream `DLQ`, milik bersama) lalu `Term`. Pesan rusak atau melanggar invarian langsung ke DLQ; galat sementara ke DLQ setelah pengiriman ke-5. Dengan begitu pesan tidak pernah hilang diam-diam setelah batas server.
8. **Wilayah terdampak dihitung PostGIS, tingkat dihitung domain.** Radius dirasakan dan tabel tingkat mengikuti PRD (fungsi murni dengan property test monoton). PostGIS hanya mencari kelurahan/desa dalam radius (filter indeks geometri lalu jarak geodesik). Pesan membawa paling banyak 100 wilayah terdekat dan jumlah totalnya; daftar lengkap ada di `hazard.impact_region`.
9. **Tingkat kejadian** = tingkat tertinggi di wilayah pantauan (Info bila tidak ada), naik ke Bahaya bila BMKG menyatakan potensi tsunami dan ada wilayah pantauan yang terdampak. Aturan tsunami per lokasi pesisir menunggu data garis pantai di alert-engine (fase 3). Untuk M ≥ 6 di dalam radius dirasakan tetapi lebih dari 100 km, yang tidak disebut tabel PRD, dipakai Siaga supaya tingkat tidak pernah turun saat magnitudo naik.

## Konsekuensi

- Invarian dijaga dua kali: di domain (property test) dan di database (CHECK, FK `(id, kind)`, laporan yang ditarik wajib lepas dari kejadian). Constraint database sudah menangkap satu bug urutan tulis saat pengembangan.
- Kejadian yang diterbitkan bisa berakhir sebagai `merged`. Konsumen (realtime-gateway, alert-engine) wajib menangani `hazard.quake.expired` dengan `reason = MERGED` dan mengabaikan revisi yang lebih lama dari yang sudah dimiliki (`Hazard.revision`).
- Kejadian yang sudah kedaluwarsa tetap menerima revisi (status tetap `expired`), supaya koreksi data tetap tercatat.
- Satu instance geo-processor memproses gempa secara berurutan. Bila kelak butuh throughput lebih, kunci advisory bisa dipecah per sel waktu.
