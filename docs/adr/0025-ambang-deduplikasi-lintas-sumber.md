# 0025. Ambang deduplikasi lintas sumber dari set berlabel ISC

Tanggal: 2026-11-22 · Status: diterima; menggantikan ambang lintas sumber ADR 0008 (ambang revisi ADR 0008 tetap)

## Konteks

ADR 0024 menyusun set berlabel T4 dari ISC Bulletin dan menunda keputusan ambang sampai laporan dari data asli ada. `make dedup-labels` dijalankan penuh di WSL 2026-11-09: 200 minggu (0 diambil ulang), 331,5 MB QuakeML, 44.528 kejadian ISC, 8.524 origin USGS M ≥ 4,0 (6.513 berpasangan BMKG), 6.677 origin BMKG, 429 pasangan negatif, 1.664 origin tanpa magnitudo dilewati. Cache ISC dikemas ke titipan (`cache-isc.tar`, sha256 37a8317f…); jalan ulang `-offline` di workspace asisten menghasilkan CSV yang identik byte per byte.

Ambang ADR 0008 (30 dtk, 100 km, 1,0) di set itu: recall 98,9%, presisi 99,8% (simulasi 99,9%); USGS M ≥ 4,3 99,2%/99,7%; Jawa Barat 99,0%/100%; arsip real-time 10/10. Kandidat terbaik dari sembilan ambang laporan 1e-3c-2a adalah 30 dtk/120 km/1,3: recall 99,5% (M ≥ 4,3 99,7%), FP tetap 14, tetapi uji geser naik 3,32 → 3,66 per 1.000.

Yang diperiksa di 1e-3c-2c, dari cache ISC (semua origin per kejadian, bukan hanya baris CSV):

1. **14 FP ambang lama semuanya gempa lain.** Origin USGS-nya ada di kejadian ISC tanpa origin DJA, dan origin BMKG yang terpilih ada di kejadian ISC lain yang dilokasikan sendiri oleh ISC, IDC, dan biasanya ISC-EHB, 5,7–27,3 dtk lebih awal. Pada 12 dari 14, kejadian BMKG itu juga punya origin NEIC sendiri: gempa kembar yang dilaporkan USGS dua-duanya, BMKG hanya yang pertama (misalnya 2021-01-30 M 4,5 lalu M 5,3 9,5 dtk kemudian, 2022-12-24 M 5,0 dan M 4,7 berselang 17 dtk). Dua sisanya: 2023-03-21 (ISC dan IDC melokasikan gempa 19:05:46 terpisah dari 19:06:12, jadi gempa lain) dan 2023-08-06 (kejadian ISC hanya berisi origin DJA, 21,5 dtk lebih awal dan 3 km dari NEIC; satu-satunya yang mungkin satu gempa yang tidak digabung ISC). FP hanya bergantung pada jendela waktu: jarak dan magnitudo yang lebih longgar tidak menambah FP.
2. **3 gabungan USGS–USGS juga gempa kembar nyata**: 2021-01-30 (9,5 dtk, 20 km), 2021-06-29 (9,2 dtk, 16 km), 2021-11-01 (8,5 dtk, 4 km). Masing-masing dua ID NEIC berbeda di dua kejadian ISC dengan solusi ISC/IDC sendiri. Gabungan ini lewat ambang revisi (10 dtk, 50 km), jadi tidak dipengaruhi ambang lintas sumber.
3. **1.664 origin tanpa magnitudo**: 1.663 DJA (1.661 tanpa magnitudo sama sekali, 2 hanya ML) dan 1 NEIC (hanya ML 3,9, di bawah M 4,0). **Tidak ada origin NEIC M ≥ 4 yang terbuang.** Origin DJA tanpa magnitudo hampir semuanya dari Jul 2021–Des 2023, dan 1.572 di antaranya kejadian ISC yang hanya berisi origin DJA itu. 30 berada di kejadian dengan NEIC M ≥ 4 (27 dalam ≤ 6 dtk dan ≤ 76 km), jadi 30 origin USGS itu dihitung bukan positif; tidak satu pun menjadi FP. Feed BMKG selalu membawa magnitudo, jadi melewatinya tetap benar; recall dihitung atas 6.513 positif, bukan 6.543.
4. **Kisi 36 ambang** (20/25/30 dtk × 100/120/150/200 km × 1,0/1,3/1,5) di set ISC:
   - FP turun hanya dengan jendela waktu: 30 dtk 14, 25 dtk 12, 20 dtk 7, 15 dtk 4.
   - Positif dengan selisih jarak > 100 km: 47, > 120 km: 30, > 150 km: 15, > 200 km: 6. Selisih magnitudo > 1,0: 27, > 1,3: 4, > 1,5: 0. Selisih waktu > 20 dtk: 8, > 25 dtk: 3, > 30 dtk: 1.
   - 20 dtk/150 km/1,3 mengungguli ambang lama dan kandidat 1e-3c-2a di setiap ukuran: recall 99,7%, FP 7, geser 2,48.
5. **Gempa besar paling berbeda waktunya.** Dari 67 positif USGS M ≥ 6, selisih waktu terbesar 22,4 dtk (M 7,1 Mindanao 2021-08-11), lalu 18,1 dan 17,9 dtk (M 7,3 Laut Banda 2021-12-29, M 7,3 Laut Flores 2021-12-14). Jendela 20 dtk saja melewatkan M 7,1 itu dan menyisakan ±2 dtk untuk dua M 7,3; padahal kejadian ganda paling merugikan justru untuk gempa besar (dua peringatan Bahaya untuk satu gempa). Set tidak memuat M ≥ 8.
6. **Katalog ADR 0008 dihitung ulang** (RepoGempa × USGS Jawa Barat 2008–2023, independen dari ISC) dengan ambang yang sama.

## Keputusan

1. **`quake.DefaultCrossSourceRule` = ≤ 20 dtk (≤ 60 dtk bila salah satu origin M ≥ 6), ≤ 150 km, selisih magnitudo ≤ 1,3.**

   | Ukuran                                       | ADR 0008 (30 dtk, 100 km, 1,0) | Kandidat 1e-3c-2a (30 dtk, 120 km, 1,3) | Bawaan baru     |
   | -------------------------------------------- | ------------------------------ | --------------------------------------- | --------------- |
   | ISC semua: recall / presisi (pasangan)       | 98,9% / 99,8%                  | 99,5% / 99,8%                           | 99,7% / 99,9%   |
   | ISC semua: TP/FP/FN                          | 6444/14/69                     | 6479/14/34                              | 6493/7/20       |
   | ISC semua, simulasi `Settle`                 | 98,9% / 99,9%                  | 99,5% / 99,9%                           | 99,7% / 99,9%   |
   | ISC USGS M ≥ 4,3                             | 99,2% / 99,7%                  | 99,7% / 99,7%                           | 99,9% / 99,9%   |
   | ISC Jawa Barat                               | 99,0% / 100%                   | 99,5% / 100%                            | 99,5% / 100%    |
   | ISC geser ±2–10 mnt /1.000                   | 3,32                           | 3,66                                    | 2,52            |
   | Gabungan BMKG–BMKG / USGS–USGS (simulasi)    | 0 / 3                          | 0 / 3                                   | 0 / 3           |
   | Katalog Jawa Barat: recall                   | 98,5% (663/673)                | 99,0% (666/673)                         | 99,1% (667/673) |
   | Katalog: gabungan salah /1.000 (jam / menit) | 0,20 / 1,71                    | 0,31 / 2,32                             | 0,20 / 1,71     |
   | Arsip real-time (10 pasangan)                | 10/10                          | 10/10                                   | 10/10           |

   Sisa 20 FN: 15 karena jarak > 150 km (6 juga waktu), 4 karena selisih magnitudo > 1,3, 1 karena waktu saja. Sisa 7 FP semuanya gempa kembar yang USGS laporkan dua-duanya (kolom terakhir tabel contoh di `dedup-berlabel.md`).

2. **Jendela gempa besar.** `quake.Rule` mendapat `LargeMagnitude` dan `LargeTimeDelta`: bila magnitudo salah satu origin ≥ `LargeMagnitude`, jendela waktu dan pembagi skor waktu memakai `LargeTimeDelta`. `Rule.Window()` (jendela terlebar) dipakai untuk mencari kandidat di geo-processor dan alat kalibrasi. Keduanya nol berarti tidak ada jendela khusus (ambang revisi, PRD, dan ADR 0008 tetap begitu). Validasi: `LargeMagnitude` di (0, 10], `LargeTimeDelta` di (`MaxTimeDelta`, 10m]. `QUAKE_DEDUP_CROSS_SOURCE` menerima dua bagian opsional: `20s,150,1.3,6,60s`. 60 dtk dipilih ±2,7 kali selisih terbesar M ≥ 6 yang teramati; jendela ini hampir tidak menambah geser (2,48 → 2,52) karena gempa M ≥ 6 jarang dan selisih magnitudo ≤ 1,3 tetap berlaku.
3. **Kandidat 1e-3c-2a tidak dipilih**: recall lebih rendah, FP dua kali lipat, dan geser serta gabungan salah katalog lebih tinggi daripada bawaan baru.
4. **Ambang revisi tetap 10 dtk, 50 km, 1,0.** Ketiga gabungan USGS–USGS (8,5–9,5 dtk) adalah gempa kembar, tetapi set ISC hanya memuat satu origin NEIC per kejadian, jadi tidak ada data tentang USGS yang mengganti ID untuk gempa yang sama. Ditinjau lagi bila arsip real-time memuat penggantian ID USGS (misalnya USGS–USGS hanya digabung lewat `AlternateIDs`).
5. **Origin DJA tanpa magnitudo tetap dilewati** (ADR 0024 butir 3).
6. Ambang lama disimpan sebagai `quake.ADR0008CrossSourceRule` untuk pembanding. Laporan `dedup-berlabel.md` kini membandingkan bawaan dengan ambang lama, PRD, kandidat 1e-3c-2a, tanpa jendela M ≥ 6, dan satu parameter digeser per baris (waktu 15/25/30 dtk, jarak 120/200 km, magnitudo 1,0/1,5); tabel contoh memuat semua FP beserta origin USGS milik label pasangannya. `dedup-gempa.md` menambah bawaan baru, 20 dtk/150 km/1,3, 30 dtk/120 km/1,3, 25 dtk/150 km/1,3, dan 20 dtk/200 km/1,3.

## Konsekuensi

- T4 terpenuhi di set berlabel: presisi dan recall ≥ 99% di semua subset, cara pasangan maupun simulasi `Settle`.
- **Jendela 20 dtk diukur dari solusi final.** ADR 0008 menolak 20 dtk karena khawatir solusi otomatis USGS menit-menit pertama lebih meleset; ADR 0024 menemukan USGS Indonesia sudah `reviewed` saat terbit, feed BMKG tidak pernah mengubah nilai antar-payload, dan 10 pasangan arsip berselisih ≤ 3,8 dtk. p99 selisih waktu di set ISC 10,2 dtk, jadi masih ada ruang ±2 kali. Bila pasangan arsip real-time M < 6 berselisih > 15 dtk, ambang ditinjau ulang; `dedup-arsip.csv` terus ditambah selama ingest jalan.
- 150 km tidak menambah gabungan kebetulan di katalog (0,20 per 1.000, sama dengan ambang lama).
- Gempa ≥ M 8 belum pernah diukur; jendela 60 dtk adalah perkiraan. Bila gempa besar nyata menghasilkan dua kejadian, `QUAKE_DEDUP_CROSS_SOURCE` bisa dilebarkan tanpa ubah kode.
- Gempa kembar yang berselang < 20 dtk (< 60 dtk untuk M ≥ 6) di lokasi yang sama tetap menjadi satu kejadian bila BMKG hanya melaporkan satu; kejadian itu tetap mewakili wilayah dan waktu yang sama.
- PRD (bagian deduplikasi dan T4) diselaraskan dengan ambang ini.
