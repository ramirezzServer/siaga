# 0020. Ambang banjir persentil dan indeks hujan sub-DAS

Tanggal: 2026-10-01 · Status: diterima (butir 4 dilanjutkan ADR 0021)

## Konteks

PRD (Aturan bisnis) menetapkan tingkat banjir dari debit prakiraan per titik pantau: Info ≥ p80, Waspada ≥ p90, Siaga ≥ p98, Bahaya ≥ p99,5 klimatologi sel GloFAS yang sama, kedaluwarsa 24 jam sejak data terakhir di atas ambang, dan kalibrasi ulang tiap tahun. Tujuh sub-DAS Citarum Hulu terlalu kecil untuk sel GloFAS 5 km dan dipantau lewat indeks hujan (akumulasi 3, 6, dan 24 jam dibandingkan persentil historisnya), berlabel "indikasi potensi banjir". ADR 0011 dan 0019 merencanakan ambang dari reanalisis 1984–2022 untuk 38 sel terpilih (1e-3b-1).

Sebelum kode ditulis, sumber asli dicek 2026-10-01 dari browser di laptop pengguna (workspace asisten tidak menjangkau Open-Meteo, ±1.000 panggilan):

- **Reanalisis `consolidated_v4` berisi 1997-01-01 sampai 2025-05-31**, bukan 1984 sampai Juli 2022 seperti dokumentasi Open-Meteo. 1984–1996 kosong di semua tahun dan sel yang dicek; data berhenti tepat 2025-05-31.
- **`seamless_v4`** (bawaan API, dipakai konektor `openmeteo-sungai`) di masa lalu memakai data _intermediate_ GloFAS, bukan reanalisis. Di periode tumpang tindih 2022-08 sampai 2025-05, korelasi harian dengan reanalisis 0,90–0,96 dan p98 berbeda −16% sampai +1% (Citarum Nanjung paling rendah: 325 lawan 386 m³/s). Contoh harian: Bekasi 4 Maret 2025 = 193 m³/s di reanalisis, 130 di seamless.
- Statistik ensemble (`river_discharge_mean/median/max/min/p25/p75`) hanya ada untuk hari prakiraan (source `GloFasReader.swift`, 51 anggota). Untuk hari lampau dan reanalisis hanya ada `river_discharge`.
- Pratinjau ambang sel `bekasi-kota` 1997–2024: p80 89, p90 112, p98 158, p99,5 201 m³/s. Reanalisis 3–4 Maret 2025 (di luar sampel): 185 dan 193 → **Siaga** tepat di hari banjir. Pamanukan Januari 2026 (seamless, puncak 214 m³/s pada 24 Januari) melewati p98 sel itu (198) → **Siaga**.
- **ERA5-Land di Open-Meteo tidak berisi hujan** (`precipitation` null semua). Arsip **`ecmwf_ifs` (9 km) berisi hujan per jam sejak 2017-01-01** di sel yang sama dengan prakiraannya.
- `cell_selection` bawaan (`land`) memilih sel ECMWF IFS menurut kecocokan elevasi, bukan jarak: 64 dari 95 titik sampel jatuh ke sel terdekat. Dengan `cell_selection=nearest`, 100 dari 100 titik jatuh ke sel terdekat, dan pusat sel yang diminta dijawab sel itu sendiri, baik di prakiraan maupun arsip.
- Batas sub-DAS resmi tidak tersedia terbuka. HydroBASINS v1c level 12 (HydroSHEDS, boleh dipakai dengan atribusi) tersedia dan dicek isinya (shapefile + PDF dokumentasi, CRC cocok). Region `as` tidak menutup Jawa; Indonesia ada di region `au`.

## Keputusan

1. **Ambang debit = persentil debit harian reanalisis `consolidated_v4` 1997-01-01 sampai 2024-12-31** (10.227 hari, 28 tahun kalender penuh) di sel setiap titik pantau, dihitung `make flood-threshold` (`cmd/flood-threshold`, use case `app/floodthreshold`, fungsi murni `domain/threshold`). Persentil memakai interpolasi linear (metode 7 Hyndman–Fan, sama dengan NumPy). Ambang harus naik tegas dan positif; sel yang hampir selalu nol ditolak. Januari–Mei 2025 sengaja di luar periode supaya banjir Bekasi Maret 2025 menjadi uji di luar sampel. Keluaran `docs/calibration/ambang-banjir-32.csv` (dibaca geo-processor di 1e-3b-2b) dan laporan `ambang-banjir.md`.
2. **Pengambilan deret historis memakai pola river-snap** (ADR 0019) yang dipisah ke `app/reanalysis`: cache per sel di `.cache/<alat>/<model>-<variabel>-<dari>-<sampai>.json` (disimpan setelah tiap request), titik uji satu sel saat cache kosong (`ErrEmpty`), batas kuota berbobot (≤ 200 panggilan dan ≤ 400 ribu nilai per request, 400 panggilan per menit, `-max-calls`), `cell_selection=nearest`, dan pemeriksaan satuan, jumlah langkah, sel yang dijawab (≤ 0,001° dari yang diminta), serta kelengkapan (≥ 99% langkah berisi, `ErrCoverage`). Jalan pertama ±2.780 panggilan untuk klimatologi.
3. **Uji terhadap kejadian tercatat** dari `docs/calibration/banjir-tercatat.csv` (Garut 2016 di dalam sampel, Bekasi 2025 di luar sampel, Pamanukan 2026 dengan `seamless_v4`). Laporan mencantumkan puncak debit di jendela kejadian dan tingkat yang dicapai. Kejadian cekungan Bandung ditambahkan ke CSV setelah tanggal dan sumbernya pasti.
4. **Bias data pembanding diukur, belum dikoreksi.** `flood-threshold` juga meminta `seamless_v4` 2022-08-01..2024-12-31 (±240 panggilan) dan menulis rasio p98 seamless/reanalisis per titik (`rasio_p98_seamless`). Ambang tetap dari reanalisis, seperti GloFAS sendiri yang menghitung ambang periode ulang dari reanalisis. Koreksi (misal ambang × rasio untuk titik di bawah 0,9) diputuskan di 1e-3b-2b setelah rasio 38 titik terlihat. _Diputuskan di ADR 0021 butir 1: hanya rasio di bawah 0,90 yang dikoreksi (saat ini Nanjung), dan tiga titik hilir Jatiluhur dibatasi Siaga._
5. **Pembanding prakiraan (untuk geo-processor, 1e-3b-2b).** Untuk setiap hari prakiraan dari hari ini sampai +3 hari (tanggal UTC GloFAS):
   - tingkat dasar dari **median ensemble** (≥ 50% anggota);
   - bila **p75 ensemble** (≥ 25% anggota) mencapai tingkat lebih tinggi, tingkat naik **paling banyak satu**, dengan teks "kemungkinan";
   - **maksimum ensemble** tidak menentukan tingkat, hanya ditampilkan sebagai skenario terburuk (satu dari 51 anggota tidak boleh memicu peringatan; T3 nol alarm palsu berbahaya);
   - bila statistik ensemble kosong, dipakai `river_discharge` tanpa kenaikan dari p75.

   Tingkat kejadian adalah tingkat tertinggi di jendela itu.

6. **Tujuh sub-DAS = gabungan unit HydroBASINS level 12** di hulu Waduk Saguling (outlet unit 5120207670, luas hulu 2.205 km²; Walungan menyebut 2.123 km²). Unit dikelompokkan menurut topologi `NEXT_DOWN` dan dicocokkan dengan alur sungai OSM (Overpass). Daftar unit per sub-DAS ada di kepala `docs/calibration/sub-das-citarum-hulu.csv`; poligonnya tidak disimpan karena bisa dibangun ulang dari HydroBASINS.
7. **Indeks hujan dari ECMWF IFS 9 km**, satu model untuk klimatologi dan prakiraan. Setiap sub-DAS ditutup sel `ecmwf_ifs` dengan bobot luas (sampel kisi 0,01° ke sel terdekat; 47 sel unik, 79 pasangan sub-DAS–sel). Hujan rata-rata wilayah per jam → untuk setiap hari UTC, akumulasi 3, 6, dan 24 jam terbesar yang berakhir di hari itu → persentil 80/90/98/99,5 dari nilai harian itu, dihitung `make rain-threshold` dari arsip 2017–2024 (±980 panggilan). Ambang harian membuat frekuensinya sama dengan ambang debit (±73, 37, 7, dan 1,8 hari per tahun), sedangkan persentil per jam akan membuat Bahaya muncul puluhan jam setahun.
8. **Konektor hujan sub-DAS (1e-3b-2b)** meminta 47 sel `models=ecmwf_ifs&cell_selection=nearest` per jam, 1 hari lalu sampai 3 hari ke depan, tiap 3 jam: 47 × 8 = 376 panggilan per hari (ingest total ±3.900 dari 6.000 batas `TestOpenMeteoQuota`). Ini menyimpang dari dokumen arsitektur ("indeks hujan memakai data cuaca yang sama tanpa panggilan tambahan"): grid cuaca 0,25° (satu sel ±770 km²) terlalu kasar untuk sub-DAS 150–530 km², dan `best_match` bisa berganti model dasar.
9. **Indeks hujan paling tinggi Siaga.** Hujan saja tidak membuktikan debit, jadi tingkat indeks hujan dibatasi Siaga dan selalu berlabel "indikasi potensi banjir".
10. **`riversnap.ReanalysisStart` dikoreksi ke 1997-01-01.** Periode acuan river-snap (2020–2022) tidak berubah, jadi pilihan sel 1e-3b-1 tetap.

## Konsekuensi

- Satu hari kalibrasi penuh memakai ±4.000 panggilan (`flood-threshold` ±3.100, `rain-threshold` ±980) di atas ingest ±3.500, masih di bawah 10.000 per hari. Jalan ulang dari cache gratis; cache ±3 MB (debit) dan ±15 MB (hujan) di `.cache/`.
- Kalibrasi ulang tahunan berarti menggeser `-to` setelah Open-Meteo memperpanjang reanalisis. Akhir reanalisis tidak diumumkan, jadi titik uji dan `ErrCoverage` yang menjaganya.
- Ambang relatif terhadap model, bukan debit terukur: debit GloFAS di sungai kecil dan di hilir waduk jauh dari lapangan (catatan 1e-3b-1). Teks peringatan menyebut "debit model".
- Info (≥ p80) terjadi ±73 hari per tahun per titik, kebanyakan di musim hujan. Info tidak dikirim sebagai push (PRD), hanya tampil di peta.
- Batas sub-DAS sekasar unit level 12 (±10–220 km²): Cidurian, Cipamokolan, dan Cicadas masuk Citarik karena bermuara bersama Cikeruh di satu unit; sisi Waduk Saguling dibagi ke Cihaur (utara) dan Ciminyak (selatan). Untuk indeks hujan dari sel 9 km, ketelitian ini setara resolusi model. Peta sub-DAS di frontend (fase 2) perlu membangun poligon dari unit yang terdaftar.
- Logika batch dan kuota sekarang ada di `app/riversnap` dan `app/reanalysis`. river-snap bisa dipindah ke `app/reanalysis` saat diubah lagi.
- PRD bagian Sungai yang dipantau masih menyebut "reanalisis 1984–2022"; perlu diganti "reanalisis 1997–2024" (dokumen PRD ada di Project claude.ai, bukan di repo).
