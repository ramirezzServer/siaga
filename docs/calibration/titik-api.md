# Kalibrasi titik api dari arsip FIRMS

Dibuat `make fire-threshold` pada 2026-11-08 21.18 WIB (ADR 0023). Jangan diedit manual.

Kotak 106.3,-7.9,108.9,-5.7, deteksi 2012-01-10 10.23 s.d. 2026-10-11 03.44 WIB. Arsip standard processing (SP) dipakai selama tersedia, lalu near real-time (NRT).

| Wahana  | Produk             | Dari       | Sampai     |
| ------- | ------------------ | ---------- | ---------- |
| S-NPP   | `VIIRS_SNPP_SP`    | 2012-01-20 | 2026-06-30 |
| S-NPP   | `VIIRS_SNPP_NRT`   | 2026-07-01 | 2026-10-20 |
| NOAA-20 | `VIIRS_NOAA20_SP`  | 2018-04-01 | 2026-06-30 |
| NOAA-20 | `VIIRS_NOAA20_NRT` | 2026-07-01 | 2026-10-20 |
| NOAA-21 | `VIIRS_NOAA21_SP`  | 2023-03-01 | 2026-06-30 |
| NOAA-21 | `VIIRS_NOAA21_NRT` | 2026-07-01 | 2026-10-20 |
| MODIS   | `MODIS_SP`         | 2012-01-01 | 2026-07-31 |
| MODIS   | `MODIS_NRT`        | 2026-08-01 | 2026-10-20 |

## Inventaris deteksi

Keyakinan VIIRS: rendah (glint/anomali < 15 K), nominal, tinggi (piksel jenuh). MODIS dipetakan dari persen (< 30, 30–79, ≥ 80). Kolom type hanya ada di SP: 0 vegetasi, 1 gunung api, 2 sumber tetap lain, 3 lepas pantai.

| Wahana  | Tahun | Deteksi | Rendah | Nominal | Tinggi | Malam | type 0 | type 1 | type 2 | type 3 | tanpa type |
| ------- | ----: | ------: | -----: | ------: | -----: | ----: | -----: | -----: | -----: | -----: | ---------: |
| S-NPP   |  2012 |    4463 |    423 |    3982 |     58 |   38% |   3334 |      0 |    960 |    169 |          0 |
| S-NPP   |  2013 |    2441 |    162 |    2266 |     13 |   50% |   1426 |      0 |    856 |    159 |          0 |
| S-NPP   |  2014 |    2960 |    252 |    2686 |     22 |   46% |   2061 |      0 |    560 |    339 |          0 |
| S-NPP   |  2015 |    4918 |    602 |    4225 |     91 |   35% |   4184 |     21 |    535 |    178 |          0 |
| S-NPP   |  2016 |     785 |     49 |     733 |      3 |   55% |    530 |      0 |    198 |     57 |          0 |
| S-NPP   |  2017 |    1938 |    207 |    1709 |     22 |   36% |   1623 |      0 |    211 |    104 |          0 |
| S-NPP   |  2018 |    3622 |    355 |    3214 |     53 |   40% |   3257 |      0 |    144 |    221 |          0 |
| S-NPP   |  2019 |    5337 |    538 |    4729 |     70 |   37% |   4788 |     19 |    255 |    275 |          0 |
| S-NPP   |  2020 |    1253 |    113 |    1136 |      4 |   44% |   1008 |      0 |    163 |     82 |          0 |
| S-NPP   |  2021 |    1525 |    107 |    1413 |      5 |   59% |   1285 |      0 |    175 |     65 |          0 |
| S-NPP   |  2022 |     989 |     39 |     948 |      2 |   62% |    798 |      0 |    155 |     36 |          0 |
| S-NPP   |  2023 |    3783 |    488 |    3249 |     46 |   33% |   3134 |      1 |    565 |     83 |          0 |
| S-NPP   |  2024 |    2229 |    235 |    1980 |     14 |   45% |   1652 |      0 |    537 |     40 |          0 |
| S-NPP   |  2025 |    1018 |     59 |     956 |      3 |   56% |    572 |      0 |    399 |     47 |          0 |
| S-NPP   |  2026 |    2776 |    262 |    2490 |     24 |   33% |    248 |      0 |    158 |     15 |       2355 |
| NOAA-20 |  2018 |    3598 |    373 |    3183 |     42 |   39% |   3022 |      0 |    370 |    206 |          0 |
| NOAA-20 |  2019 |    5626 |    625 |    4943 |     58 |   38% |   4840 |     13 |    482 |    291 |          0 |
| NOAA-20 |  2020 |    1399 |    148 |    1246 |      5 |   42% |   1110 |      0 |    202 |     87 |          0 |
| NOAA-20 |  2021 |    1611 |     92 |    1517 |      2 |   61% |   1312 |      0 |    219 |     80 |          0 |
| NOAA-20 |  2022 |    1119 |     52 |    1061 |      6 |   62% |    749 |      0 |    307 |     63 |          0 |
| NOAA-20 |  2023 |    4024 |    610 |    3377 |     37 |   33% |   3367 |      1 |    573 |     83 |          0 |
| NOAA-20 |  2024 |    2268 |    230 |    2024 |     14 |   42% |   1609 |      0 |    608 |     51 |          0 |
| NOAA-20 |  2025 |    1100 |     53 |    1042 |      5 |   63% |    605 |      0 |    445 |     50 |          0 |
| NOAA-20 |  2026 |    3245 |    388 |    2822 |     35 |   33% |    339 |      0 |    174 |     18 |       2714 |
| NOAA-21 |  2023 |    3835 |    497 |    3304 |     34 |   33% |   3129 |      2 |    629 |     75 |          0 |
| NOAA-21 |  2024 |    2302 |    218 |    2065 |     19 |   45% |   1556 |      0 |    688 |     58 |          0 |
| NOAA-21 |  2025 |    1132 |     77 |    1052 |      3 |   58% |    659 |      0 |    416 |     57 |          0 |
| NOAA-21 |  2026 |    3165 |    399 |    2732 |     34 |   32% |    256 |      0 |    180 |     30 |       2699 |
| MODIS   |  2012 |     716 |     59 |     586 |     71 |    8% |    428 |      0 |    285 |      3 |          0 |
| MODIS   |  2013 |     450 |     54 |     376 |     20 |    6% |    137 |      0 |    312 |      1 |          0 |
| MODIS   |  2014 |     508 |     57 |     418 |     33 |    5% |    214 |      0 |    286 |      8 |          0 |
| MODIS   |  2015 |     790 |     64 |     638 |     88 |    8% |    462 |     31 |    293 |      4 |          0 |
| MODIS   |  2016 |     243 |     30 |     211 |      2 |    2% |     51 |      0 |    189 |      3 |          0 |
| MODIS   |  2017 |     419 |     24 |     358 |     37 |    6% |    200 |      0 |    219 |      0 |          0 |
| MODIS   |  2018 |     596 |     53 |     500 |     43 |    5% |    334 |      5 |    254 |      3 |          0 |
| MODIS   |  2019 |     749 |     52 |     618 |     79 |   11% |    487 |     17 |    239 |      6 |          0 |
| MODIS   |  2020 |     283 |     22 |     253 |      8 |    4% |     92 |      0 |    191 |      0 |          0 |
| MODIS   |  2021 |     298 |     30 |     263 |      5 |    4% |     68 |      0 |    227 |      3 |          0 |
| MODIS   |  2022 |     267 |     24 |     235 |      8 |    3% |     92 |      0 |    175 |      0 |          0 |
| MODIS   |  2023 |     603 |     38 |     514 |     51 |    6% |    331 |      4 |    261 |      7 |          0 |
| MODIS   |  2024 |     400 |     32 |     349 |     19 |   10% |    207 |      5 |    187 |      1 |          0 |
| MODIS   |  2025 |     257 |     22 |     229 |      6 |    5% |     84 |      0 |    173 |      0 |          0 |
| MODIS   |  2026 |     364 |     14 |     317 |     33 |   14% |     58 |      0 |     66 |      0 |        240 |

## Sebaran bulanan (di luar sumber panas tetap, WIB)

| Jan | Feb | Mar | Apr | Mei | Jun | Jul | Agu | Sep | Okt | Nov | Des |
| --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: |
|  0% |  1% |  1% |  1% |  1% |  3% | 11% | 27% | 33% | 17% |  3% |  1% |

## Sumber panas tetap

Sel 0.005° dinilai bersama 8 tetangganya. Tidak musiman: ≤ 80% deteksi di Jul–Okt, atau ≥ 90% deteksi malam. Persisten: ≥ 12 hari, ≥ 3 tahun, ≥ 6 bulan kalender berbeda, ≥ 50% deteksi malam, dan tidak musiman. Type FIRMS: ≥ 3 deteksi SP, ≥ 50% di antaranya type 2/3 (sumber tetap darat, lepas pantai), ≥ 3 tahun, dan tidak musiman; type 1 (gunung api) tidak dihitung karena di Jawa Barat dipakai FIRMS untuk kebakaran lereng gunung api. Sel kandidat yang bersebelahan digabung; jari-jari = anggota terjauh + 0.50 km (minimal 0.75 km).

**139 sumber**, menyaring 30660 dari 81404 deteksi (38%). Daftar lengkap di `sumber-panas-tetap-32.csv`, peta di `sumber-panas-tetap-32.geojson` (bisa dibuka di GitHub atau geojson.io).

| Pusat              | Jari-jari | Alasan     | Deteksi | Hari | Tahun | Bulan | type 2/3 | Malam | Jul–Okt | FRP rata | Pertama    | Terakhir   |
| ------------------ | --------: | ---------- | ------: | ---: | ----: | ----: | -------: | ----: | ------: | -------: | ---------- | ---------- |
| -6.54807,108.09425 |   2.14 km | keduanya   |    4251 | 1396 |    14 |    12 |     3105 |   81% |     57% |   2.3 MW | 2012-08-13 | 2026-10-11 |
| -6.37815,108.39848 |   2.43 km | keduanya   |    2572 | 1387 |    15 |    12 |     2386 |   75% |     46% |   3.4 MW | 2012-01-21 | 2026-10-03 |
| -6.09473,106.67117 |   6.63 km | keduanya   |    1778 |  823 |    15 |    12 |     1148 |   88% |     58% |   1.9 MW | 2012-03-03 | 2026-10-11 |
| -7.04984,107.74665 |   2.91 km | type-firms |    1438 |  856 |    15 |    12 |      925 |    0% |     41% |   7.7 MW | 2012-01-17 | 2026-10-09 |
| -6.19560,106.55256 |   4.26 km | keduanya   |    1146 |  725 |    15 |    12 |      358 |   94% |     55% |   1.4 MW | 2012-02-13 | 2026-10-05 |
| -6.47934,106.89820 |   1.98 km | keduanya   |    1117 |  823 |    15 |    12 |      849 |  100% |     61% |   1.3 MW | 2012-03-13 | 2026-10-11 |
| -6.28496,107.87892 |   2.69 km | persisten  |     975 |  555 |    10 |    12 |      145 |   95% |     69% |   1.0 MW | 2014-07-28 | 2026-08-29 |
| -6.15034,106.52391 |   5.89 km | keduanya   |     889 |  496 |    15 |    12 |      131 |   97% |     55% |   1.4 MW | 2012-05-13 | 2026-10-10 |
| -6.09939,107.03124 |   2.60 km | keduanya   |     724 |  471 |     9 |    12 |      479 |   95% |     57% |   1.0 MW | 2012-02-07 | 2023-12-17 |
| -6.18231,106.35335 |   4.11 km | keduanya   |     637 |  371 |    12 |    12 |      277 |   97% |     59% |   1.1 MW | 2012-07-27 | 2026-10-10 |
| -6.96087,107.80038 |   3.67 km | type-firms |     607 |  467 |    15 |    12 |      494 |    1% |     43% |   9.8 MW | 2012-01-19 | 2026-10-10 |
| -6.70573,108.40021 |   1.86 km | keduanya   |     518 |  411 |    15 |    12 |      260 |   93% |     58% |   1.2 MW | 2012-02-15 | 2026-10-05 |
| -5.89447,107.49470 |   1.08 km | keduanya   |     507 |  373 |    15 |    12 |      457 |   85% |     56% |   2.7 MW | 2012-03-06 | 2026-10-10 |
| -6.09903,106.54828 |   1.79 km | keduanya   |     496 |  333 |    13 |    12 |      175 |   80% |     62% |   2.5 MW | 2012-06-23 | 2026-10-05 |
| -6.13484,107.02846 |   1.61 km | persisten  |     455 |  299 |    15 |    12 |      185 |   82% |     35% |   1.6 MW | 2012-02-21 | 2026-01-12 |
| -6.46069,107.21170 |   1.73 km | persisten  |     454 |  376 |    13 |    12 |      171 |   99% |     61% |   1.0 MW | 2014-05-06 | 2026-10-10 |
| -6.18590,106.31385 |   2.81 km | keduanya   |     440 |  341 |    13 |    12 |      206 |   93% |     49% |   1.3 MW | 2014-04-08 | 2026-09-29 |
| -6.08713,106.99945 |   1.74 km | type-firms |     412 |  372 |    15 |    12 |      347 |   19% |     55% |   3.2 MW | 2012-02-23 | 2026-06-18 |
| -6.07541,107.09756 |   1.45 km | keduanya   |     361 |  247 |     9 |    12 |      194 |   96% |     53% |   1.0 MW | 2012-02-06 | 2026-05-30 |
| -5.91531,107.73090 |   1.38 km | keduanya   |     352 |  289 |    15 |    12 |      347 |   94% |     52% |   2.6 MW | 2012-11-28 | 2026-10-05 |
| -5.91027,107.92763 |   1.02 km | keduanya   |     338 |  302 |    14 |    12 |      326 |   97% |     49% |   2.4 MW | 2012-02-06 | 2026-10-11 |
| -7.00780,107.74454 |   2.10 km | type-firms |     334 |  299 |    15 |    12 |      201 |    0% |     40% |   6.3 MW | 2012-01-17 | 2026-09-02 |
| -6.09537,108.14059 |   1.47 km | keduanya   |     324 |  269 |    15 |    12 |      322 |   90% |     41% |   2.5 MW | 2012-05-13 | 2026-09-26 |
| -6.10156,107.83319 |   0.99 km | keduanya   |     301 |  236 |     5 |    12 |      301 |   97% |     64% |   2.4 MW | 2015-08-04 | 2019-11-08 |
| -6.42054,106.44107 |   1.55 km | keduanya   |     289 |  195 |     7 |    12 |      126 |   99% |     60% |   0.7 MW | 2012-04-11 | 2026-09-17 |
| -6.15993,106.74319 |   1.94 km | keduanya   |     276 |  176 |    10 |    12 |       99 |   99% |     67% |   1.4 MW | 2012-07-16 | 2024-08-10 |
| -6.04698,107.52452 |   1.37 km | keduanya   |     266 |  225 |    12 |    12 |      264 |   92% |     39% |   2.7 MW | 2012-04-16 | 2026-07-16 |
| -6.21196,106.50014 |   3.14 km | keduanya   |     264 |  196 |    15 |    12 |       72 |   89% |     50% |   1.5 MW | 2012-03-24 | 2026-10-05 |
| -6.30151,108.63311 |   1.02 km | keduanya   |     260 |  228 |    15 |    12 |      258 |   90% |     41% |   2.7 MW | 2012-02-28 | 2026-07-12 |
| -6.44960,107.45422 |   1.80 km | type-firms |     242 |  207 |    15 |    12 |      167 |    0% |     32% |   8.1 MW | 2012-02-08 | 2026-09-16 |
| -6.86677,107.49789 |   1.47 km | type-firms |     239 |  200 |    15 |    12 |      137 |    0% |     46% |   6.2 MW | 2012-01-19 | 2026-09-21 |
| -6.08195,107.04388 |   1.43 km | persisten  |     233 |  167 |    14 |    11 |        0 |   93% |     47% |   1.2 MW | 2012-06-03 | 2026-09-27 |
| -6.19628,106.46516 |   4.59 km | persisten  |     208 |  152 |    14 |    12 |        7 |   85% |     57% |   1.8 MW | 2012-03-16 | 2026-09-12 |
| -6.37233,108.35733 |   1.46 km | persisten  |     200 |   99 |     3 |     6 |        0 |  100% |     86% |   1.2 MW | 2020-07-17 | 2026-10-11 |
| -6.29575,107.95901 |   0.93 km | keduanya   |     196 |  167 |     6 |    12 |      167 |   98% |     66% |   0.8 MW | 2012-06-15 | 2026-08-13 |
| -6.50088,108.43284 |   1.72 km | persisten  |     179 |  156 |    14 |    12 |       81 |   99% |     73% |   0.8 MW | 2012-04-20 | 2026-07-31 |
| -6.41348,107.40369 |   2.35 km | keduanya   |     149 |  120 |    15 |    12 |       41 |   49% |     45% |   4.1 MW | 2012-01-19 | 2026-09-15 |
| -6.93182,107.69662 |   1.73 km | type-firms |     145 |  119 |    15 |    11 |       69 |    0% |     54% |   5.0 MW | 2012-04-23 | 2026-09-29 |
| -6.23678,106.54324 |   2.30 km | persisten  |     144 |  106 |    13 |    12 |        2 |   95% |     47% |   1.2 MW | 2012-06-14 | 2026-06-07 |
| -6.28753,106.52304 |   1.54 km | keduanya   |     135 |  104 |     5 |    12 |       92 |   99% |     58% |   0.8 MW | 2018-03-31 | 2023-09-29 |

## Kejadian per varian aturan

Deteksi dikelompokkan menjadi kejadian bila berjarak ≤ jarak sambung dan berselang ≤ jeda dari anggota lain (berantai). Tingkat dinilai di setiap deteksi atas jendela 24 jam: Info bila ada deteksi, Waspada bila ada deteksi dengan keyakinan minimal aturan (atau terlihat di cukup banyak lintasan), Siaga bila hitungan ≥ ambang. Lintasan = wahana + waktu lintas berbeda. Tahun menurut awal kejadian (WIB). Varian bertanda * dipakai `hazard.fire.*`.

| Varian                                                                       | Wahana dibuang | Saringan | Sambung         | Waspada mulai          | Siaga        | Deteksi dinilai | Kejadian |  Info | Waspada | Siaga |
| ---------------------------------------------------------------------------- | -------------- | -------- | --------------- | ---------------------- | ------------ | --------------: | -------: | ----: | ------: | ----: |
| PRD                                                                          | -              | ya       | 2.0 km / 24 jam | high                   | ≥ 5 deteksi  |           50744 |    31206 | 29649 |     525 |  1032 |
| PRD tanpa S-NPP                                                              | S-NPP          | ya       | 2.0 km / 24 jam | high                   | ≥ 5 deteksi  |           25760 |    17386 | 16563 |     433 |   390 |
| Waspada nominal, Siaga ≥ 3 lintasan, tanpa S-NPP                             | S-NPP          | ya       | 2.0 km / 24 jam | nominal                | ≥ 3 lintasan |           25760 |    17386 |  2026 |   15006 |   354 |
| Terpilih: Waspada tinggi atau ≥ 2 lintasan, Siaga ≥ 5 deteksi, tanpa S-NPP * | S-NPP          | ya       | 2.0 km / 24 jam | high atau ≥ 2 lintasan | ≥ 5 deteksi  |           25760 |    17386 | 14990 |    2006 |   390 |
| Terpilih tanpa MODIS                                                         | S-NPP, MODIS   | ya       | 2.0 km / 24 jam | high atau ≥ 2 lintasan | ≥ 5 deteksi  |           21612 |    14754 | 12987 |    1459 |   308 |
| Terpilih tanpa saringan                                                      | S-NPP          | tidak    | 2.0 km / 24 jam | high atau ≥ 2 lintasan | ≥ 5 deteksi  |           41367 |    25983 | 21810 |    3601 |   572 |

Kejadian Waspada / Siaga per tahun:

| Tahun |       V1 |       V2 |         V3 |        V4 |        V5 |        V6 |
| ----: | -------: | -------: | ---------: | --------: | --------: | --------: |
|  2012 |  67 / 53 |   54 / 4 |    357 / 0 |    58 / 4 |     0 / 0 |    72 / 4 |
|  2013 |  17 / 20 |   11 / 0 |    153 / 0 |    14 / 0 |     0 / 0 |    30 / 1 |
|  2014 |  30 / 17 |   20 / 0 |    203 / 0 |    26 / 0 |     0 / 0 |    37 / 0 |
|  2015 |  71 / 69 |  48 / 11 |    390 / 5 |   54 / 11 |     0 / 0 |   73 / 11 |
|  2016 |    3 / 4 |    2 / 0 |     72 / 0 |     3 / 0 |     0 / 0 |     9 / 0 |
|  2017 |  28 / 28 |   29 / 0 |    183 / 1 |    31 / 0 |     0 / 0 |    37 / 0 |
|  2018 | 45 / 127 |  36 / 48 |  1592 / 35 |  182 / 48 |  120 / 33 |  343 / 61 |
|  2019 | 79 / 207 |  68 / 76 |  2602 / 63 |  297 / 76 |  171 / 56 |  527 / 91 |
|  2020 |  10 / 26 |   8 / 12 |    618 / 4 |   46 / 12 |   27 / 12 |  115 / 16 |
|  2021 |   8 / 13 |    5 / 4 |    483 / 6 |    34 / 4 |    24 / 3 |  166 / 12 |
|  2022 |    8 / 9 |    7 / 3 |    320 / 0 |    18 / 3 |     6 / 1 |   122 / 6 |
|  2023 | 66 / 228 | 58 / 124 | 2963 / 134 | 601 / 124 | 545 / 107 | 891 / 163 |
|  2024 |  29 / 66 |  25 / 32 |  1648 / 42 |  200 / 32 |  170 / 26 |  418 / 72 |
|  2025 |   7 / 19 |    6 / 6 |    620 / 3 |    57 / 6 |    48 / 5 |  195 / 31 |
|  2026 | 57 / 146 |  56 / 70 |  2802 / 61 |  385 / 70 |  348 / 65 | 566 / 104 |

V1 = PRD. V2 = PRD tanpa S-NPP. V3 = Waspada nominal, Siaga ≥ 3 lintasan, tanpa S-NPP. V4 = Terpilih: Waspada tinggi atau ≥ 2 lintasan, Siaga ≥ 5 deteksi, tanpa S-NPP. V5 = Terpilih tanpa MODIS. V6 = Terpilih tanpa saringan.

## 25 kejadian terbesar (Terpilih: Waspada tinggi atau ≥ 2 lintasan, Siaga ≥ 5 deteksi, tanpa S-NPP)

| Mulai (WIB)      | Selesai          | Pusat            | Deteksi | Puncak 24 jam | Tingkat | Wahana                  |
| ---------------- | ---------------- | ---------------- | ------: | ------------: | ------- | ----------------------- |
| 2019-10-21 13.10 | 2019-10-23 00.32 | -6.8576,108.3795 |      47 |            41 | Siaga   | MODIS, NOAA-20          |
| 2019-08-08 00.57 | 2019-08-12 01.22 | -6.8948,108.4029 |      63 |            38 | Siaga   | MODIS, NOAA-20          |
| 2024-08-20 01.42 | 2024-08-21 09.06 | -7.1546,107.8499 |      35 |            34 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2018-09-30 12.44 | 2018-10-05 00.14 | -6.8264,108.4200 |      54 |            31 | Siaga   | MODIS, NOAA-20          |
| 2019-10-21 13.47 | 2019-10-22 13.53 | -6.5062,107.2539 |      26 |            25 | Siaga   | MODIS, NOAA-20          |
| 2019-10-21 13.47 | 2019-10-23 13.09 | -6.7795,107.9470 |      30 |            22 | Siaga   | MODIS, NOAA-20          |
| 2019-10-24 12.50 | 2019-10-27 00.57 | -7.4818,107.7285 |      23 |            20 | Siaga   | MODIS, NOAA-20          |
| 2026-08-09 00.22 | 2026-08-09 15.24 | -7.0800,106.7640 |      19 |            19 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2018-10-07 13.29 | 2018-10-12 01.22 | -6.8508,108.4192 |      30 |            18 | Siaga   | MODIS, NOAA-20          |
| 2018-09-28 00.45 | 2018-09-28 13.35 | -7.1631,107.8558 |      18 |            18 | Siaga   | MODIS, NOAA-20          |
| 2023-10-23 00.39 | 2023-10-23 01.56 | -7.3291,107.7226 |      18 |            18 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2026-09-02 13.03 | 2026-09-03 00.52 | -6.7784,108.0894 |      18 |            18 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2023-07-28 01.11 | 2023-07-31 13.14 | -6.5796,108.1437 |      24 |            16 | Siaga   | NOAA-20, NOAA-21        |
| 2023-10-27 01.04 | 2023-10-28 13.46 | -7.3328,107.7206 |      21 |            16 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2018-09-10 10.55 | 2018-09-11 13.41 | -6.5938,108.2024 |      18 |            16 | Siaga   | MODIS, NOAA-20          |
| 2023-10-28 13.22 | 2023-10-30 01.48 | -6.3799,107.4883 |      18 |            15 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2019-10-05 01.10 | 2019-10-06 00.51 | -7.4902,107.7367 |      15 |            15 | Siaga   | MODIS, NOAA-20          |
| 2023-08-25 13.23 | 2023-08-26 00.50 | -6.8188,108.4148 |      14 |            14 | Siaga   | NOAA-20, NOAA-21        |
| 2023-09-29 12.26 | 2023-09-29 14.07 | -6.6659,108.1682 |      14 |            14 | Siaga   | NOAA-20, NOAA-21        |
| 2023-08-20 13.17 | 2023-08-25 13.23 | -6.8010,107.3481 |      33 |            13 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2026-09-05 12.53 | 2026-09-08 12.53 | -6.6032,108.0926 |      19 |            13 | Siaga   | NOAA-20, NOAA-21        |
| 2018-08-03 12.32 | 2018-08-04 01.16 | -6.6279,108.0735 |      13 |            13 | Siaga   | MODIS, NOAA-20          |
| 2026-09-14 12.38 | 2026-09-14 15.18 | -7.1745,107.8665 |      13 |            13 | Siaga   | MODIS, NOAA-20, NOAA-21 |
| 2019-10-04 14.06 | 2019-10-07 10.05 | -6.8375,108.3952 |      17 |            12 | Siaga   | MODIS, NOAA-20          |
| 2018-08-20 01.16 | 2018-08-22 13.17 | -6.5582,108.2146 |      16 |            12 | Siaga   | MODIS, NOAA-20          |

## Uji kebakaran tercatat

Kejadian dihitung bila punya deteksi di dalam radius dari sehari sebelum tanggal mulai sampai dua hari setelah tanggal selesai (WIB). Isi sel: tingkat tertinggi, deteksi di radius, deteksi pertama; "tersaring" adalah deteksi di radius yang jatuh di sumber panas tetap.

| Kebakaran                                                                                                                                        | Tanggal               |     Luas | V1                                           | V2                                          | V3                                           | V4                                          | V5                                          | V6                                          |
| ------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------- | -------: | -------------------------------------------- | ------------------------------------------- | -------------------------------------------- | ------------------------------------------- | ------------------------------------------- | ------------------------------------------- |
| [ciremai-2019-08](https://kabar24.bisnis.com/read/20190808/79/1134397/gunung-ciremai-kebakaran-belum-semua-pendaki-turun)                        | 2019-08-07 s.d. 08-10 |   308 ha | Siaga, 136 deteksi, pertama 2019-08-08 00.57 | Siaga, 63 deteksi, pertama 2019-08-08 00.57 | Siaga, 63 deteksi, pertama 2019-08-08 00.57  | Siaga, 63 deteksi, pertama 2019-08-08 00.57 | Siaga, 50 deteksi, pertama 2019-08-08 00.57 | Siaga, 63 deteksi, pertama 2019-08-08 00.57 |
| [ciremai-2019-09](https://news.republika.co.id/berita/pxd74h414/puncak-gunung-ciremai-kembali-terbakar)                                          | 2019-09-05 s.d. 09-07 |        - | tidak terdeteksi                             | tidak terdeteksi                            | tidak terdeteksi                             | tidak terdeteksi                            | tidak terdeteksi                            | tidak terdeteksi                            |
| [ciremai-2019-10](https://news.republika.co.id/berita/q00l46382/kebakaran-di-gunung-ciremai-berhasil-dipadamkan)                                 | 2019-10-21 s.d. 10-26 |   190 ha | Siaga, 92 deteksi, pertama 2019-10-21 12.57  | Siaga, 52 deteksi, pertama 2019-10-21 13.10 | Siaga, 52 deteksi, pertama 2019-10-21 13.10  | Siaga, 52 deteksi, pertama 2019-10-21 13.10 | Siaga, 45 deteksi, pertama 2019-10-21 13.47 | Siaga, 52 deteksi, pertama 2019-10-21 13.10 |
| [papandayan-2019-10](https://news.republika.co.id/berita/pyzlsx430/hutan-gunung-papandayan-garut-terbakar)                                       | 2019-10-06 s.d. 10-07 |   1.5 ha | Info, 1 deteksi, pertama 2019-10-05 10.17    | Info, 1 deteksi, pertama 2019-10-05 10.17   | Waspada, 1 deteksi, pertama 2019-10-05 10.17 | Info, 1 deteksi, pertama 2019-10-05 10.17   | tidak terdeteksi                            | Info, 1 deteksi, pertama 2019-10-05 10.17   |
| [guntur-2019-10](https://news.republika.co.id/berita/pzxacn463/gunung-guntur-terbakar-pendakian-ditutup)                                         | 2019-10-24 s.d. 10-26 |        - | Siaga, 31 deteksi, pertama 2019-10-24 12.50  | Siaga, 13 deteksi, pertama 2019-10-24 12.50 | Siaga, 13 deteksi, pertama 2019-10-24 12.50  | Siaga, 13 deteksi, pertama 2019-10-24 12.50 | Siaga, 12 deteksi, pertama 2019-10-24 12.50 | Siaga, 13 deteksi, pertama 2019-10-24 12.50 |
| [sarimukti-2023-08](https://www.medcom.id/nasional/daerah/Obz0Ex7K-api-belum-padam-total-area-tpa-sarimukti-yang-terbakar-mencapai-19-5-hektare) | 2023-08-19 s.d. 08-31 |  19.5 ha | Siaga, 55 deteksi, pertama 2023-08-20 13.17  | Siaga, 34 deteksi, pertama 2023-08-20 13.17 | Siaga, 34 deteksi, pertama 2023-08-20 13.17  | Siaga, 34 deteksi, pertama 2023-08-20 13.17 | Siaga, 28 deteksi, pertama 2023-08-20 13.17 | Siaga, 34 deteksi, pertama 2023-08-20 13.17 |
| [ciremai-2023-08](https://www.detik.com/jabar/berita/d-6898508/bpbd-sebut-kebakaran-hutan-gunung-ciremai-mulai-terkendali)                       | 2023-08-25 s.d. 08-28 | 125.5 ha | Siaga, 25 deteksi, pertama 2023-08-25 12.33  | Siaga, 17 deteksi, pertama 2023-08-25 13.23 | Siaga, 17 deteksi, pertama 2023-08-25 13.23  | Siaga, 17 deteksi, pertama 2023-08-25 13.23 | Siaga, 17 deteksi, pertama 2023-08-25 13.23 | Siaga, 17 deteksi, pertama 2023-08-25 13.23 |
| [papandayan-2023-10](https://jabar.antaranews.com/berita/476268/bbksda-tutup-sebagian-kawasan-wisata-gunung-papandayan-dampak-kebakaran)         | 2023-10-22 s.d. 10-23 |        - | Siaga, 28 deteksi, pertama 2023-10-23 00.39  | Siaga, 18 deteksi, pertama 2023-10-23 00.39 | Siaga, 18 deteksi, pertama 2023-10-23 00.39  | Siaga, 18 deteksi, pertama 2023-10-23 00.39 | Siaga, 17 deteksi, pertama 2023-10-23 00.39 | Siaga, 18 deteksi, pertama 2023-10-23 00.39 |
| [guntur-2024-08](https://jabar.antaranews.com/berita/536951/kebakaran-hutan-di-gunung-guntur-sudah-padam)                                        | 2024-08-19 s.d. 08-22 |        - | Siaga, 51 deteksi, pertama 2024-08-20 01.42  | Siaga, 35 deteksi, pertama 2024-08-20 01.42 | Siaga, 35 deteksi, pertama 2024-08-20 01.42  | Siaga, 35 deteksi, pertama 2024-08-20 01.42 | Siaga, 29 deteksi, pertama 2024-08-20 01.42 | Siaga, 35 deteksi, pertama 2024-08-20 01.42 |
