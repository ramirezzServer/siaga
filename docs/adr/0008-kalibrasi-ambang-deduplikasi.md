# 0008. Ambang deduplikasi gempa hasil kalibrasi

Tanggal: 2026-09-24 · Status: diterima

## Konteks

PRD menetapkan ambang awal deduplikasi BMKG–USGS (≤ 90 dtk, ≤ 75 km, selisih magnitudo ≤ 0,7) dan menyatakan semua ambang dikalibrasi dengan data historis. Keputusan lanjutan PRD menunjuk katalog USGS Jawa Barat sebagai bahan kalibrasi. Target T4: presisi dan recall ≥ 99%.

Pasangan BMKG–USGS dibutuhkan untuk mengukur recall, jadi kalibrasi memakai dua katalog pada periode yang sama (2008-11 s.d. 2023-01, 5.526 gempa BMKG dan 819 gempa USGS di lintang −9..−5,5, bujur 105..109,5):

- Katalog RepoGempa BMKG versi CSV (kompilasi kekavigi/repo-gempa, CC BY 4.0; datanya milik BMKG).
- Keluaran layanan FDSN USGS (M ≥ 2,5; disimpan di horankev/quake_data).

Keduanya dikunci ke commit di `scripts/fetch-calibration-data.sh`, dan laporan dihasilkan oleh `make calibrate-dedup` memakai `quake.Rule.Match` yang sama dengan layanan. Hasil lengkap: [docs/calibration/dedup-gempa.md](../calibration/dedup-gempa.md). Angka yang sama didapat dari analisis independen di Python saat eksplorasi.

## Temuan

- Selisih waktu BMKG–USGS sangat kecil (p99 10,6 dtk), sedangkan selisih jarak besar (p95 61 km, p99 99 km) dan selisih magnitudo p99 0,8.
- Ambang PRD melewatkan 3,7% pasangan, hampir semuanya karena jarak (76–135 km) atau magnitudo (0,8–1,2).
- Jendela waktu 90 dtk tidak menambah recall, tetapi menjadi sumber utama gabungan salah dengan gempa susulan: 4,03 per 1.000 pada uji geser 2–10 menit, dibanding 1,71 untuk 30 dtk.

## Keputusan

- Ambang lintas sumber bawaan: **≤ 30 dtk, ≤ 100 km, selisih magnitudo ≤ 1,0**. Recall 98,5% (dari 96,3%) dan gabungan salah uji susulan turun 58% (4,03 → 1,71 per 1.000).
- Ambang revisi sumber sama: **≤ 10 dtk, ≤ 50 km, ≤ 1,0**. Di katalog BMKG hanya 13 pasangan dalam 14 tahun yang lolos, dan semuanya berselisih ≤ 4 detik: solusi ganda untuk gempa yang sama, bukan gempa berbeda.
- Ambang PRD tetap disimpan sebagai `quake.PRDInitialRule` untuk pembanding. Keduanya bisa diganti tanpa ubah kode lewat `QUAKE_DEDUP_CROSS_SOURCE` dan `QUAKE_DEDUP_REVISION` (format `30s,100,1.0`).
- Jendela 20 detik (recall 98,4%, gabungan salah 1,10) tidak dipilih walau sedikit lebih presisi: katalog berisi solusi final, sedangkan solusi otomatis real-time (terutama USGS menit-menit pertama) lebih meleset. Sisa 10 detik menjadi ruang aman.

## Konsekuensi

- Recall 98,5% masih di bawah target T4 99%. Sisa yang terlewat adalah pasangan dengan selisih jarak > 100 km atau magnitudo > 1,0; melonggarkan jarak ke 120 km hanya menambah 0,2 poin sambil menaikkan gabungan salah kebetulan. Kalibrasi berikutnya memakai arsip payload real-time ingest (fase 1e), yang mencerminkan solusi otomatis, bukan katalog final.
- Presisi diukur tidak langsung (uji geser waktu), karena kedua katalog tidak punya label pasangan resmi. Set berlabel manual untuk T4 disusun dari arsip replay di fase 1e.
- Katalog BMKG yang dipakai berhenti di Januari 2023. Katalog v2 (sampai 2026) hanya tersedia sebagai arsip HTML yang harus diolah skrip Python pihak ketiga; belum dipakai.
