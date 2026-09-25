# 0014. Arsip payload mentah di Garage dan replay dari arsip

Tanggal: 2026-09-25 · Status: diterima

## Konteks

Sejak fase 1a, ingest mengarsipkan setiap payload sumber yang berubah (gzip, kunci `<konektor>/<YYYY>/<MM>/<DD>/<hhmmss>Z-<sha256[:12]>.<ext>.gz`) ke folder lokal `.cache/ingest-archive`. Dokumen arsitektur menetapkan Garage sebagai object storage (pengganti MinIO yang edisi komunitasnya diarsipkan) untuk arsip payload mentah dan foto laporan, dengan backup ke Oracle Object Storage di produksi. Uji replay (payload historis diputar ulang ke pipa yang sama) adalah salah satu lapisan uji di dokumen arsitektur, dan PRD mengukur T3 dan T4 dengan data replay.

Yang dibutuhkan fase 1e-1:

- Arsip yang tidak hilang bersama laptop atau folder `.cache`, dengan API yang sama di lokal dan produksi.
- Cara membaca arsip kembali: memutar ulang ke NATS, memeriksa integritas, dan memindahkan arsip lama.
- Semua tetap Rp0 dan jalan di laptop 8–16 GB.

Garage v2.3.0 punya mode `server --single-node --default-bucket`: layout satu node disusun otomatis, lalu access key dan bucket dibuat dari environment bila belum ada. Image-nya `FROM scratch` (tanpa shell).

## Keputusan

1. **Garage single-node di Compose lite.** Service `garage` (`dxflrs/garage:v2.3.0`) dengan `deploy/compose/garage.toml` (engine metadata `sqlite` supaya tahan mati mendadak, `replication_factor = 1`, region `garage`). Rahasia (`GARAGE_RPC_SECRET` 32 byte heksadesimal, token admin dan metrik) serta access key arsip (`ARCHIVE_S3_ACCESS_KEY_ID` berawalan `GK`, `ARCHIVE_S3_SECRET_ACCESS_KEY`) ada di `.env`, dibuat acak oleh `make env`. `make env` kini juga menambahkan variabel baru dari `.env.example` ke `.env` yang sudah ada tanpa mengubah nilai lama. Healthcheck memakai CLI `garage status` karena image tanpa shell. Port S3 3900 dan admin 3903 hanya di 127.0.0.1.
2. **Satu bucket, awalan per jenis arsip.** Bucket `siaga-arsip`; payload ingest di awalan `raw/`. Backfill (fase 1e-3) dan foto laporan (fase 5) memakai awalan lain di bucket yang sama, jadi satu access key dan satu aturan backup.
3. **Satu URL untuk memilih arsip.** `INGEST_ARCHIVE_URL` menerima folder, `file:///folder`, atau `s3://bucket/awalan?endpoint=http://127.0.0.1:3900[&region=garage]` (adapter `archiveurl`). Kredensial tidak pernah ditulis di URL (URL tercetak di log) dan dibaca dari `ARCHIVE_S3_*`. `INGEST_ARCHIVE_DIR` fase 1a–1d tetap diterima sebagai folder; mengisi keduanya ditolak. `make ingest` memakai Garage, `make ingest-record` tetap menulis ke folder lokal untuk rekaman sampel.
4. **Gagal cepat saat start, tetap jalan saat berjalan.** Ingest memeriksa arsip sebelum polling pertama (`HeadBucket` atau folder bisa ditulis) dan menolak start bila gagal, supaya salah konfigurasi tidak diam-diam menghasilkan data tanpa arsip. Galat arsip saat berjalan tetap hanya dicatat per polling seperti sejak fase 1a: peringatan lebih penting daripada arsip.
5. **Adapter S3 dengan aws-sdk-go-v2.** `s3archive` memakai `service/s3` resmi AWS (path-style, region dari URL). minio-go tidak dipilih karena terikat ke proyek MinIO yang edisi komunitasnya sudah diarsipkan; SDK AWS adalah klien S3 yang paling banyak diuji terhadap server non-AWS. Checksum CRC bawaan SDK baru diset `WhenRequired` karena memakai badan `aws-chunked` dengan trailer; integritas transfer dijaga `Content-MD5` di setiap Put, dan integritas isi dijaga SHA-256 di kunci. Get dibatasi 64 MiB per objek.
6. **Port arsip bisa dibaca.** `ports.ArchiveReader` (`Get`, `List` berurutan byte kunci seperti `ListObjectsV2`) dan `ports.ArchiveStore`. `fsarchive` mengurutkan kunci utuh, bukan per komponen path, karena `"a-b/x" < "a/x"` di S3. Satu uji kesesuaian (`adapters/archivetest`) dijalankan untuk folder, server S3 tiruan dalam proses (dengan paginasi dan galat 500), dan Garage asli di uji integrasi.
7. **Format kunci jadi aturan domain.** `domain/archivekey` membentuk dan membaca kunci; hanya bentuk kanonik yang diterima (fuzz `FuzzParse`). Karena waktu tertulis dengan lebar tetap, urutan leksikografis kunci satu konektor sama dengan urutan waktu ambil, jadi rentang waktu cukup diterjemahkan ke `StartAfter`. `emit.Unarchive` adalah kebalikan `emit.Archive`: gzip dibaca dengan batas 256 MiB dan SHA-256 payload harus cocok dengan potongan di kunci.
8. **Replay memakai jalur yang sama dengan polling.** Perintah `replay` (use case `app/replay`) membaca arsip per feed dalam rentang `-from`/`-to`, menggabungkan semua feed menurut waktu ambil (seri diputus nama konektor lalu kunci, jadi deterministik), mem-parse dengan konektor yang sama dengan ingest, dan menerbitkan dengan FetchMeta asli (waktu ambil, kunci arsip, SHA-256). Aturan penerbitan sama dengan `poll`: hanya record yang isinya berubah sejak payload sebelumnya. Hasilnya, ID pesan sama dengan saat polling langsung; memutar ulang ke stream yang sudah berisi pesan itu tidak menambah apa pun (fuzz `FuzzRunOrdered`), dan memutar ke stream kosong membangunnya kembali. `-speed` mengatur tempo (0 secepatnya, 1 waktu asli), `-publish=false` hanya memeriksa, `-strict` gagal bila ada payload rusak atau tidak terbaca (untuk CI). Payload rusak dilewati dan dicontohkan di laporan, bukan menghentikan replay.
9. **Yang bisa diputar ulang: feed satu payload.** 11 feed yang satu payload-nya cukup untuk menghasilkan event: gempa BMKG (3) dan USGS, Open-Meteo (3), dan FIRMS (4; `firms.Parser` dipisah dari konektor supaya tidak butuh MAP_KEY). `bmkg-cap`, `bmkg-prakiraan`, dan `openaq-stasiun` belum: event-nya bergantung pada konteks request (dokumen CAP per entri RSS, kode desa di URL, gabungan daftar lokasi dan nilai terbaru) yang tidak tersimpan di kunci arsip.
10. **Alat perawatan arsip.** Perintah `archive`: `ls` (ringkasan per konektor), `verify` (setiap objek dibaca dan dicek SHA-256-nya), `cp` (menyalin objek yang belum ada atau ukurannya beda, setelah diverifikasi; aman diulang). `make archive-upload` memindahkan arsip lokal fase 1a–1d ke Garage.

## Konsekuensi

- `make up` kini juga menjalankan Garage (satu binary Rust, ringan). `.env` lama mendapat tujuh variabel baru lewat `make env` (dipanggil `make up`).
- Arsip ingest jalan di lokal dan produksi dengan kode yang sama; backup Garage ke Oracle Object Storage dan manifest Kubernetes-nya masuk fase 1e-2.
- Replay ke stack yang sedang berjalan aman untuk event yang sudah pernah diproses (JetStream menolak ID ganda dalam 24 jam, dan geo-processor idempotent terhadap sumber + isi), tetapi kejadian lama yang belum pernah masuk database akan dibuat lalu segera kedaluwarsa. Replay ke stack live dipakai untuk pemulihan; uji replay memakai stack terpisah.
- Uji replay jalan di CI (`cmd/replay` memutar rekaman asli BMKG, USGS, dan FIRMS ke JetStream dalam proses dan memeriksa pesan serta FetchMeta-nya), dan Garage asli diuji di job CI tersendiri.
- Replay CAP, prakiraan BMKG, dan OpenAQ butuh konteks request ikut diarsipkan (misal URL tersamar sebagai metadata objek). Ditunda sampai dibutuhkan: T3 (alarm palsu di replay 12 bulan) memerlukan CAP, jadi paling lambat sebelum alert-engine fase 3.
- Ukuran arsip belum diukur di Garage. Sapuan prakiraan BMKG (5.957 desa, ETag) kemungkinan penyumbang terbesar; retensi (lifecycle Garage) diputuskan di fase 1e-2 setelah seminggu data, berpatokan kuota backup Oracle 20 GB.
- Menambah 12 modul aws-sdk-go-v2 dan smithy-go (semua di GitHub) ke `services/ingest`.
