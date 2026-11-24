# 0030. Status sumber data yang tahan restart

Tanggal: 2026-11-24 · Status: diterima

## Konteks

Peta publik perlu tahu apakah data tiap lapisan masih segar (label "data terlambat", PRD: "satu sumber data mati → UI menampilkan status terlambat"), dan dokumen arsitektur meminta halaman status publik per sumber data. Sampai 2a-1, kesehatan sumber hanya ada di memori proses ingest (`runner.Status`, `/status`, metrik OpenTelemetry). Demo 1f-1 menunjukkan akibatnya: tepat setelah ingest dimulai ulang, panel "Sumber tepat waktu" turun ke 86% karena `bmkg-prakiraan` belum punya angka sampai sapuan pertamanya selesai (±2 jam), padahal datanya di database tidak basi. Pelajarannya: status yang tampil ke publik dihitung dari keadaan yang tahan restart.

Pilihan yang dipertimbangkan:

1. **Waktu data terakhir per sumber di database** (`ts.series.last_fetched_at`, `hazard.event_source.fetched_at`, dan sejenisnya). Tidak butuh komponen baru, tetapi tidak bisa membedakan sumber yang sepi dari sumber yang mati: autogempa tanpa gempa baru, RSS CAP tanpa peringatan, dan FIRMS tanpa titik panas tidak menulis baris apa pun walau polling-nya sehat.
2. **ingest menulis status ke database.** ingest tidak punya akses database (aturan satu schema per layanan, ADR 0006/0007); menambahkannya membuat layanan pengambil data bergantung pada PostgreSQL.
3. **ingest menerbitkan hasil setiap percobaan ke NATS, geo-processor menyimpannya, core-api membacanya.** Alurnya sama dengan data (ingest → JetStream → geo-processor → PostgreSQL → core-api), keadaan tersimpan di database, dan sumber yang sepi tetap terlihat sehat karena polling-nya tercatat.

## Keputusan

1. **Pilihan 3.** Setelah setiap polling (dan setiap sapuan `bmkg-prakiraan` yang selesai), ingest menerbitkan `siaga.status.v1.ConnectorPoll` ke subjek `status.connector.<konektor>`: subjek raw yang diisi konektor, interval normal, awal dan akhir percobaan, hasil (OK/FAILED), dan jumlah pesan raw baru yang diterima broker. Percobaan yang terputus karena layanan berhenti tidak dilaporkan, dan mode rekam (`-once`) tidak menerbitkan apa pun. Galat penerbitan hanya dicatat; polling berikutnya tidak tertahan (batas waktu 5 detik).
2. **Stream `STATUS` terpisah dari `RAW`** (milik ingest, `status.>`, retensi 2 hari, 64 MiB, jendela duplikat 1 jam). Isinya bukan data sumber: tidak diarsipkan, tidak diputar ulang, tidak ikut `replay -compare`, dan hanya keadaan terakhir yang penting. ID pesan = konektor + waktu mulai. Titik di nama konektor (`usgs-2.5-day`) diganti `_` di subjek karena titik memisahkan token NATS; nama asli tetap di isi pesan. Subjek raw tiap konektor ditetapkan di `cmd/ingest` dan dijaga test terhadap semua konektor yang direncanakan.
3. **geo-processor melipat hasil polling ke `ts.source_status`** (consumer `geo-processor-source-status`, migrasi 00009): satu baris per konektor dengan waktu coba, berhasil, dan data baru terakhir, kegagalan beruntun, dan akhir kegagalan pertamanya. Aturannya fungsi murni (`domain/sourcestatus.Apply`): pesan yang tidak lebih baru dari percobaan terakhir diabaikan, jadi aman diulang dan datang terlambat; dibaca dan ditulis dalam satu transaksi `FOR UPDATE`. Invarian dijaga dua kali (domain dan constraint), dan pesan yang melanggar masuk DLQ. Tabel di schema `ts` karena schema itu sudah memuat kesegaran data (`ts.series`, ADR 0012), dan `siaga_core` mendapat `SELECT` seperti tabel lain.
4. **"Data baru" = ada pesan raw baru yang diterima broker**, bukan "payload berbeda". ingest tidak mengingat hash payload setelah restart, jadi polling pertama setelah restart selalu tampak berbeda, sedangkan JetStream menolak pesan yang sama dalam jendela duplikat 24 jam. Uji asap di workspace menunjukkan masalah ini sebelum aturannya diganti.
5. **Status publik dihitung core-api terhadap jamnya sendiri** (`GET /v1/sources`, kontrak 0.3.0), bukan disimpan: keadaan di database tidak berubah saat ingest mati, tetapi statusnya ikut menua. Batas terlambat = berhasil terakhir + 2 × interval + 10 menit (satu polling boleh terlewat, ditambah jeda terlama ingest saat sumber terus gagal); `bmkg-prakiraan` (6 jam) terlambat setelah 12 jam 10 menit. Status: `ok`; `degraded` bila masih sebelum batas terlambat tetapi ≥ 3 percobaan terakhir gagal berturut-turut (ambang yang sama dengan log ERROR ingest); `late` bila batas terlambat lewat atau belum pernah berhasil. Respons memuat `late_after`, `failing_since`, dan `hazard_kinds` per konektor (debit sungai dan hujan sub-DAS → `flood`, prakiraan cuaca → tidak ada), supaya web bisa menandai lapisan tanpa aturan sendiri.
6. **Galat dan pesan sumber tidak dipublikasikan.** Teks galat bisa memuat URL dan potongan payload; rinciannya tetap di log dan dashboard Grafana. `/status` ingest dan metriknya tidak berubah dan tetap menjadi alat operator.

## Konsekuensi

- Status tetap benar setelah ingest dimulai ulang: uji asap di workspace (nats-server 2.11, BMKG tiruan, ingest, geo-processor, core-api) mencatat 2 kegagalan, ingest dimulai ulang, lalu kegagalan ketiga menjadikan status publik `degraded` dengan waktu berhasil terakhir yang lama, sementara `/status` ingest hanya tahu 1 kegagalan. Setelah sumber pulih, status kembali `ok`.
- Satu pesan kecil per polling (±8.200 per hari dengan semua konektor) dan satu `UPSERT` per pesan di PostgreSQL; ukuran stream jauh di bawah 64 MiB.
- Bila geo-processor mati, status ikut menua menjadi `late` walau ingest sehat. Itu disengaja: data dari sumber itu juga tidak sampai ke database.
- Konektor yang dihentikan permanen (misal key FIRMS dicabut) tetap tampil `late`. Hapus barisnya dengan role `siaga_geo` (`DELETE FROM ts.source_status WHERE connector = '…'`); konektor yang belum pernah berjalan tidak muncul sama sekali.
- geo-processor siap (`/readyz`) hanya setelah stream `STATUS` ada, sama seperti `RAW`: ingest versi baru harus jalan sekali. Keduanya di-deploy bersama.
- Web (2e) dan halaman status publik (Fase 8) memakai `/v1/sources`; dashboard Grafana tetap membaca metrik proses.
