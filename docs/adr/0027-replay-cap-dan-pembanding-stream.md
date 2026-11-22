# 0027. Replay CAP dan pembanding replay dengan stream RAW

Tanggal: 2026-11-22 · Status: diterima

## Konteks

Fase 1e-3c-3 (ADR 0018 butir 8): arsip `bmkg-cap` belum bisa diputar ulang, padahal T3 (uji replay kejadian nyata) dan alert-engine fase 3 membutuhkannya. Poller CAP (`app/capfeed`, ADR 0009) mengarsipkan tiga jenis payload di awalan yang sama, `bmkg-cap/`: RSS nowcast (hanya bila isinya berubah, dan sekali tiap ingest start), dokumen CAP bahasa Indonesia, dan dokumen terjemahan. Event `raw.weather.bmkg` terbit sekali per peringatan dengan teks semua bahasa yang berhasil diambil dan FetchMeta dokumen bahasa Indonesia; bila terjemahan gagal diambil, pesan terbit tanpa terjemahan lalu terbit ulang sebagai revisi setelah terjemahannya berhasil di polling berikutnya. Memutar dokumen satu per satu akan menghasilkan revisi yang tidak pernah terjadi.

Yang diperiksa di arsip asli (sampel 2026-10-01, 53 objek `bmkg-cap`, dua peringatan Jawa Barat) sebelum kode ditulis:

- Dokumen id dan en untuk satu pesan punya `identifier` sama, sama dengan `guid` entri RSS-nya; `<language>` info pertama `id` atau `en`.
- **BMKG menimpa file yang sama untuk pesan baru.** Peringatan `…11.32.005` dan `…12.32.005` dua-duanya ditautkan ke `CJB20261001005_alert.xml`. Nama file tidak bisa dipakai sebagai kunci pasangan.
- **Kunci arsip berpresisi detik.** RSS, dokumen id, dan dokumen en 12.04.08 UTC berbagi detik, dan urutan kunci (potongan SHA-256) menempatkan dokumen id sebelum RSS yang mendaftarnya.
- Poller: interval RSS 2 menit, fetcher 20 dtk tanpa percobaan ulang di dalam satu polling, dokumen en diambil langsung setelah dokumen id (hanya ditahan throttle anggaran BMKG). Percobaan ulang terjemahan baru terjadi di polling berikutnya, ≥ 2 menit kemudian.

## Keputusan

1. **Feed replay bertunda.** `app/replay` mendapat antarmuka opsional `DeferredFeed` (`Add` per payload tanpa menerbitkan, `Due(t)` sebelum setiap payload feed mana pun dengan waktu ambil t, dan `Due` nol di akhir). Event membawa FetchMeta payload asalnya sendiri (`Emission.Meta`), jadi event yang terbentuk dari beberapa payload terbit dengan FetchMeta dokumen utama, di posisi waktunya di antara feed lain. Feed lain tidak berubah. Penolakan semua feed kini ikut dicontohkan di laporan (`ditolak <kunci>: <alasan>`).
2. **`capfeed.Replay`** memakai langkah yang sama dengan Poller (`acceptPrimary`, `checkTranslation`, `build` dipisah dari `processItem`), dan adapter `bmkg.CAPFeed.Inspect` yang mengenali payload (RSS atau alert CAP, `identifier`, bahasa info pertama):
   - Payload satu detik dikumpulkan lalu diolah dengan urutan satu putaran polling: RSS, dokumen utama, terjemahan.
   - Dokumen dipasangkan dengan entri RSS terakhir lewat `identifier` = `guid` (entri memberi nama file untuk `source_url`), terjemahan dengan dokumen utama lewat `identifier`. Dokumen tanpa pasangan ditolak dengan `ErrNoContext`, sama dengan OpenAQ (ADR 0018 butir 7).
   - Dokumen utama baru membuat state baru (Poller hanya mengambil ulang dokumen utama bila entri RSS berubah atau ingest dijalankan ulang, dua-duanya itemState baru), jadi pembanding "isi sama" ikut direset seperti di Poller.
   - Pesan terbit begitu semua bahasa tambahan sudah datang (berhasil atau rusak), atau setelah **jendela terjemahan 1 menit** lewat tanpa terjemahan (Poller sudah menerbitkannya tanpa terjemahan). Terjemahan yang datang setelah jendela adalah revisi. 1 menit di atas batas satu percobaan (20 dtk + throttle) dan di bawah interval polling (2 menit).
   - Bahasa tambahan dari `-cap-languages` (bawaan `INGEST_CAP_LANGUAGES`, `en`), dicocokkan dengan kode dasar bahasa info (`en-US` → `en`); dokumen berbahasa lain dianggap dokumen utama, sama dengan permintaan `id` saat polling.
   - Tidak ada saringan provinsi: dokumen ada di arsip berarti Poller mengambil dan memprosesnya saat itu.
3. **Uji bolak-balik.** Poller asli (adapter `bmkg.CAPFeed`, event `rawpb`, jam palsu berpresisi detik) menulis arsip dan menerbitkan; `Replay` atas arsip itu harus menerbitkan pesan yang sama persis (subjek, `Nats-Msg-Id`, isi termasuk FetchMeta, waktu ambil), sebagai himpunan dan berurutan per peringatan. Skenario bernama (terjemahan menyusul, entri berubah lalu keluar dari RSS, terjemahan tidak cocok/rusak, latihan, kedaluwarsa, ingest dijalankan ulang, payload satu detik) plus `FuzzReplayRoundTrip` yang mengacak urutan polling, kegagalan request, jeda, perubahan entri, dan jalan ulang. Jendela 3 menit (salah) membuat test gagal. Urutan antarperingatan dalam satu detik tidak diketahui dari kunci arsip dan tidak diuji.
4. **`replay -compare <subjek>`** tidak menerbitkan apa pun: ID pesan replay dibandingkan dengan pesan yang masih ada di stream RAW (7 hari) untuk subjek itu, dalam rentang waktu ambil `-from`/`-to` (header `Siaga-Fetched-At`, dibulatkan ke detik). Replay dimulai `-compare-lookback` (bawaan 24 jam) sebelum `-from` supaya RSS dan daftar stasiun terakhir ada. Stream dibaca lewat consumer berurutan sementara yang hanya mengambil header (`adapters/jsread`), jadi stream dan consumer layanan tidak berubah. Karena ID pesan adalah hash (konektor, kunci, isi tanpa FetchMeta), ID yang cocok berarti isi pesan replay sama dengan isi pesan polling. Keluar dengan galat bila ada pesan di satu sisi saja, dan mencetak contohnya.

## Konsekuensi

- `make replay` kini memutar 15 feed (`replay -list`). Uji replay CAP T3 bisa dijalankan terhadap arsip sejak 25 Sep (`bmkg-cap` disimpan selamanya, ADR 0022), dan `-compare` memastikannya terhadap data asli 7 hari terakhir.
- Pesan CAP yang terbit tanpa terjemahan muncul di replay ±1 menit setelah dokumen utamanya, bukan saat aslinya (±20 dtk); urutan terhadap feed lain di menit itu bisa berbeda. Isi dan ID pesan sama.
- Bila BMKG suatu saat memakai `guid` RSS yang berbeda dari `identifier` CAP, semua dokumen ditolak `ErrNoContext` dan `-compare` gagal; test `TestInspectRealPayloads` mengunci asumsi ini pada rekaman asli.
- Replay penuh pertama menemukan dokumen basi: file masih berisi pesan lama saat entri pesan barunya sudah ada di RSS. Poller kini mengambil ulang dokumen seperti itu, dan replay melupakan state entri yang keluar dari RSS (ADR 0028).
- Pesan yang terbit saat arsip gagal ditulis (kunci arsip kosong) tidak bisa diputar ulang dan muncul sebagai "hanya stream" di `-compare`.
- `-compare` juga bisa dipakai untuk feed lain yang memiliki subjeknya sendiri (misal `bmkg-prakiraan` → `raw.forecast.bmkg`). Untuk subjek bersama (`raw.quake.bmkg` dipakai tiga konektor gempa BMKG), pilih semua konektornya.
