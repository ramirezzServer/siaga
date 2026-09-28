# 0018. Pengisian ulang OpenAQ dan replay dengan konteks dari isi payload

Tanggal: 2026-09-28 · Status: diterima

## Konteks

Fase 1e-3 (backfill dan kalibrasi) dipecah tiga: **1e-3a** pengisian ulang jam OpenAQ yang terlewat, replay prakiraan BMKG dan OpenAQ, dan alat bantu memeriksa titik sungai; **1e-3b** reanalisis GloFAS dan ambang banjir; **1e-3c** arsip FIRMS standar, ambang titik api, set berlabel T4, radius dirasakan, dan replay CAP. ADR ini untuk 1e-3a.

Dua masalah dari fase sebelumnya:

- **Jam OpenAQ yang terlewat hilang.** Polling stasiun hanya membaca nilai terbaru (`/v3/locations/{id}/latest`). Saat ingest mati atau jaringan putus (misal 26 Sep 2026 ±20.42–22.55 WIB, Wi-Fi WSL pindah ke paket data), jam di antaranya tidak pernah terambil (temuan 1d-2).
- **Arsip prakiraan BMKG dan OpenAQ tidak bisa diputar ulang.** ADR 0014 butir 9 menganggap konteks request (kode desa di URL, gabungan daftar lokasi dan nilai terbaru) tidak ada di arsip, dan mengusulkan metadata objek.

Rekaman asli 2026-09-26 (dua sensor PM2,5 AirGradient, `/hours`, `/measurements`, `/latest`, dan baris `ts.aq_observation` untuk rentang yang sama) menunjukkan:

- Waktu di `/latest` (dan `observed_at` di database) adalah **akhir** periode: nilai periode 11.00–12.00 UTC tersimpan sebagai 12.00.
- `/measurements` (nilai mentah) sama persis dengan data live; `/hours` (agregat) bisa berbeda sedikit (118,0 vs 117,6 untuk jam 19.00).
- Sumber menyaring rentang dengan awal periode; `meta.found` bisa berupa teks (`">3"`).
- Celah 26 Sep hanya satu jam per sensor (14.00 UTC).

## Keputusan

1. **Perintah `backfill openaq`** (use case `app/aqbackfill`, `make backfill-openaq FROM=… TO=… STATIONS=…`). Daftar lokasi diambil sekali (diarsipkan dengan nama konektor polling `openaq-stasiun`), lalu nilai mentah setiap sensor parameter SIAGA diminta per halaman (`/v3/sensors/{id}/measurements`, 1.000 per halaman, maks 20 halaman). Stasiun dipilih dari `-stations`, atau semua yang `datetimeLast`-nya pada atau setelah awal rentang.
2. **Nilai mentah, waktu = akhir periode.** Sesuai rekaman di atas, supaya baris hasil pengisian ulang sama dengan baris live (kunci `(sensor_id, observed_at)`) dan upsert geo-processor tidak membuat baris bergeser satu jam. Awal request dilebarkan satu jam, lalu nilai disaring ulang dengan akhir periode.
3. **Satu event per nilai.** Setiap nilai terbit sebagai `raw.aq.openaq` berisi satu sensor, dengan `meta.connector = openaq-jam` dan `meta.archive_key` menunjuk halaman asalnya (arsip `raw/openaq-jam/…`). Kontrak tidak berubah (`readings` boleh satu). Rentang boleh tumpang tindih dengan data live dan aman diulang: ID pesan deterministik, dan geo-processor hanya mengubah baris yang nilainya berbeda.
4. **Batas 7 hari.** geo-processor menolak nilai yang lebih tua dari 7 hari terhadap waktu ambil (`series.MaxObservationAge`), jadi awal rentang dibatasi 7 hari kurang 1 jam dan ditolak lebih awal dengan pesan yang jelas. Celah yang lebih tua tidak bisa diisi tanpa mengubah batas itu; ingest sebaiknya diisi ulang dalam seminggu setelah mati.
5. **Anggaran sendiri 20/menit.** Di samping 30/menit milik ingest, jadi keduanya boleh jalan bersamaan di bawah batas OpenAQ 60/menit. 429/503 dan galat jaringan dicoba ulang maks 3 kali (Retry-After dihormati); 4xx lain tidak. Sensor yang gagal dilaporkan dan perintah keluar dengan galat setelah sensor lain selesai.
6. **Payload pengisian ulang tetap di awalan `raw/`.** ADR 0014 butir 2 menyisihkan awalan lain untuk backfill; ternyata payload backfill adalah payload mentah sumber biasa dengan kunci baku, jadi cukup dibedakan nama konektornya (`openaq-jam`). Awalan lain tetap untuk produk turunan dan foto laporan.
7. **Replay mengambil konteks dari isi payload, bukan metadata objek.** Format arsip tidak berubah, jadi arsip sejak 25 Sep langsung bisa diputar:
   - **Prakiraan BMKG**: kode desa dibaca dari `lokasi.adm4` di payload. Sapuan hanya mengarsipkan payload yang lolos pemeriksaan wilayah (kode yang diminta = kode di payload), jadi hasilnya sama.
   - **OpenAQ**: feed replay membaca dua awalan (`openaq-stasiun` dan `openaq-stasiun-latest`) urut waktu ambil. Daftar lokasi memperbarui konteks stasiun dan sensor; nilai terbaru dibaca dengan daftar terakhir sebelumnya, persis seperti polling saat itu (daftar diarsipkan setiap berubah dan setiap ingest start). Nilai tanpa daftar sebelumnya (misal `-from` di tengah hari) ditolak dengan alasan `ErrNoContext`.
   - `app/replay` mendapat dua antarmuka opsional: `MultiFeed` (beberapa awalan arsip, nama feed tetap dipakai di FetchMeta dan ID pesan) dan `PartialFeed` (satu payload hanya sebagian record, jadi isi terakhir per record diingat lintas payload, sama dengan sweep dan stations).
   - Langkah bersih-validasi-event stasiun dipisah menjadi `stations.Observe`, dipakai polling dan replay, supaya keduanya tidak bisa berbeda.
8. **CAP belum.** Dokumen CAP per bahasa harus dipasangkan dengan entri RSS dan satu sama lain (event terbit sekali dengan teks id + en); memutar dokumen satu per satu akan menghasilkan revisi yang tidak pernah terjadi saat live. Dipindah ke 1e-3c, tetap sebelum alert-engine fase 3 (T3).
9. **Peta pemeriksaan titik sungai.** `river-snap -geojson` (dipakai `make river-snap`, keluaran `.cache/titik-sungai.geojson`) menulis sel kandidat GloFAS setiap titik beserta debit relatifnya, sel terpilih, dan koordinat perkiraan, dengan gaya simplestyle untuk dibuka di geojson.io di atas peta OSM. Ini alat bantu memeriksa 17 titik bertanda sebelum ambang banjir 1e-3b; tidak di-commit karena berubah setiap dijalankan.

## Konsekuensi

- Celah OpenAQ sampai 7 hari ke belakang bisa ditutup dengan satu perintah, termasuk celah 26 Sep.
- `make replay` kini memutar 13 feed (11 lama + prakiraan BMKG + OpenAQ). Replay prakiraan membaca ±9 KB per desa; seminggu arsip (±35 ribu objek) butuh beberapa menit ke Garage lokal.
- Isi event replay OpenAQ dan prakiraan sama persis dengan polling (diuji dengan rekaman asli), jadi ID pesannya juga sama.
- Image ingest membawa biner keempat, `backfill`.
- Bila OpenAQ mengubah arti waktu di `/measurements` atau `/latest`, baris akan bergeser satu jam tanpa galat. Rekaman dan test `TestParseMeasurementsRecorded` mengunci asumsi ini; periksa ulang saat menambah sumber stasiun lain.
