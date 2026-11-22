# 0028. Dokumen CAP basi: file belum ditimpa saat entri RSS baru muncul

Tanggal: 2026-11-22 · Status: diterima

## Konteks

Replay penuh pertama arsip `bmkg-cap` (verifikasi 1e-3c-3, arsip 25 Sep–22 Nov: 447 payload, 50 event) menolak 4 dokumen di tengah arsip, 27 Sep 12.01 UTC dan 20 Okt 12.18 UTC, karena identifier-nya sudah keluar dari RSS terakhir. ADR 0027 mencatat BMKG menimpa file CAP yang sama untuk pesan baru. Ternyata file itu bisa masih berisi pesan lama saat entri pesan baru (guid baru, tautan file sama) sudah ada di RSS.

Poller (ADR 0009) memakai dokumen yang diambil untuk sebuah entri tanpa memeriksa identifier-nya:

- isi pesan lama terbit lagi untuk entri baru (ID pesan sama dengan pesan lama, jadi ditolak broker sebagai duplikat, atau tersimpan ulang bila sudah di luar jendela duplikat);
- entri baru dianggap selesai, jadi dokumennya tidak diambil lagi sampai entrinya di RSS berubah atau ingest dijalankan ulang. Pesan baru tidak terbit sama sekali.

Uji bolak-balik ADR 0027 tidak menangkapnya karena dunia sintetisnya memakai satu file per peringatan. Replay menolak dokumen basi (`ErrNoContext`), jadi di kasus ini replay dan polling berbeda (di luar rentang `-compare` 22 Nov).

## Keputusan

1. **Dokumen basi dicoba lagi, bukan diterbitkan.** Dokumen bahasa utama yang identifier-nya bukan guid entrinya adalah dokumen basi (`capfeed.ErrStale`). Dokumen itu tidak diterbitkan dan tidak ditolak. Poller mencatatnya sebagai kegagalan (`Result.Failures` dengan kunci entri, log `record gagal diambil`) lalu mengambilnya lagi di polling berikutnya. Terjemahan yang identifier-nya bukan identifier dokumen utama diperlakukan sama dengan terjemahan yang gagal diambil: peringatan terbit tanpa terjemahan itu, terjemahannya diambil lagi, lalu terbit sebagai revisi. Sebelumnya terjemahan seperti itu dilewati permanen sebagai "terjemahan untuk pesan lain".
2. **Batas sendiri, ±1 jam.** `Options.MaxStaleAttempts` per bahasa per entri, bawaan `DefaultMaxStaleAttempts` = 30 (±1 jam pada interval RSS 2 menit), terpisah dari `MaxAttempts` (5) untuk kegagalan jaringan. Mencoba lagi hanya butuh satu request per entri per polling, sedangkan menyerah terlalu cepat berarti peringatan tidak pernah terbit. Setelah batas itu entri (atau terjemahannya) dilewati sampai entrinya di RSS berubah, dengan galat "menyerah setelah N dokumen basi".
3. **Identifier diperiksa setelah dokumen terbaca.** Dokumen basi yang tidak terbaca tetap ditolak seperti dokumen rusak. Dokumen lama pernah terbaca saat pesannya sendiri diproses, jadi kasus ini tidak diharapkan.
4. **Replay mengikuti Poller.** Saat RSS baru dibaca, state peringatan yang keluar dari RSS dilupakan (yang masih menunggu terjemahan diterbitkan dulu), sama dengan Poller yang melupakan entri yang hilang. Dokumen yang identifier-nya pernah ada di RSS tetapi tidak ada di RSS terakhir ditolak `ErrStale` ("sudah keluar dari RSS"). Yang belum pernah ada tetap `ErrNoContext`. Tanpa langkah pertama, terjemahan basi untuk pesan yang dulu terbit tanpa terjemahan menjadi revisi pesan lama, padahal revisi itu tidak pernah terbit saat polling (`TestReplayStaleDocuments`).
5. **Uji bolak-balik memodelkan file yang ditimpa.** Dunia sintetis mendapat operasi "file ditimpa": identifier baru, entri lama diganti entri baru di RSS, dan file masih berisi pesan lama untuk 0–3 request berikutnya per bahasa. Ada dua skenario bernama ("file ditimpa": pesan baru terbit setelah dokumennya benar dan terjemahan menyusul sebagai revisi; "file ditimpa menyerah": melewati batas, pesan baru tidak terbit) dan satu seed fuzz baru. Dengan Poller lama kedua skenario gagal: pesan lama terbit dua kali dan pesan baru tidak terbit.

## Konsekuensi

- Pesan baru terbit paling lambat satu polling setelah BMKG menimpa file, selama itu terjadi dalam ±1 jam.
- Dokumen basi tetap diarsipkan setiap kali diambil (satu objek per percobaan). Replay menolaknya dengan `ErrStale`, jadi angka `ditolak` di laporan replay bertambah tanpa ada beda pesan. `-compare` tetap satu lawan satu: selama dokumen basi, polling tidak menerbitkan apa pun dan replay menolak dokumennya.
- Pesan yang hilang karena perilaku lama (sebelum fix ini) tidak bisa dipulihkan dari arsip: dokumennya memang tidak pernah diambil. `cek-cap-basi.py` (titipan) mencari dokumen basi di arsip dan memeriksa apakah dokumen pesan barunya pernah diambil.
- Bila BMKG suatu saat mengubah skema sehingga `guid` ≠ `identifier` untuk semua pesan, Poller berhenti menerbitkan peringatan setelah ±1 jam, dan setiap polling mencatat log `record gagal diambil` dengan galat `dokumen CAP basi` untuk setiap entri. Itu lebih terlihat daripada sebelumnya (hanya replay yang gagal, ADR 0027), tetapi peringatan berhenti. `TestInspectRealPayloads` tetap mengunci asumsi `identifier` = `guid` pada rekaman asli.
- Kasus yang tidak dimodelkan: file ditimpa sementara entri lama masih ada di RSS (dua entri menautkan file yang sama). Poller menunggu dokumen yang cocok untuk masing-masing entri. Replay akan memperlakukan dokumen pesan lama sebagai dokumen utama baru untuk entri lama dan menerbitkan ulang isinya (ID sama, ditolak broker). Kasus ini belum pernah terlihat: di keempat dokumen 27 Sep/20 Okt, entri lamanya sudah keluar dari RSS.
