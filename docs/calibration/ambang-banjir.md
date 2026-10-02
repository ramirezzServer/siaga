# Ambang banjir titik pantau sungai

Dibuat `make flood-threshold` pada 2026-10-02 02:13 UTC. Jangan diedit manual; ubah daftar titik atau kejadian lalu jalankan ulang (ADR 0020).

Ambang adalah persentil debit harian reanalisis GloFAS v4 (Open-Meteo `models=consolidated_v4`) 1997-01-01 sampai 2024-12-31 (10227 hari) di sel setiap titik pantau. Tingkat (PRD, Aturan bisnis): Info ≥ p80, Waspada ≥ p90, Siaga ≥ p98, Bahaya ≥ p99,5. Karena ambang dihitung dari hari kalender, rata-rata 73 hari per tahun di atas p80, 37 di atas p90, 7 di atas p98, dan 1,8 di atas p99,5 di setiap titik. Debit GloFAS di sungai kecil dan di hilir waduk jauh dari debit terukur, jadi ambang hanya bermakna terhadap debit model yang sama, bukan terhadap pengukuran lapangan.

Kolom rasio adalah p98 `seamless_v4` dibagi p98 reanalisis di periode tumpang tindih: di bawah 1 berarti data prakiraan dan intermediate cenderung lebih rendah dari klimatologi sel itu, jadi tingkat bisa terlambat naik.

| Titik                                        | Sungai       | Sel               | p50   | p80   | p90   | p98   | p99,5  | Median puncak tahunan | Puncak tertinggi    | Rasio p98 seamless |
| -------------------------------------------- | ------------ | ----------------- | ----- | ----- | ----- | ----- | ------ | --------------------- | ------------------- | ------------------ |
| Majalaya (`citarum-majalaya`)                | Citarum      | -7.0750, 107.7250 | 10,4  | 29,5  | 37,8  | 52,7  | 64,0   | 66,3                  | 114,9 (2022-10-23)  | 0,92               |
| Dayeuhkolot (`citarum-dayeuhkolot`)          | Citarum      | -6.9750, 107.6250 | 41,1  | 106,7 | 136,7 | 190,2 | 241,1  | 277,0                 | 411,1 (2019-12-31)  | 0,93               |
| Nanjung (`citarum-nanjung`)                  | Citarum      | -6.9750, 107.5250 | 95,8  | 215,8 | 271,9 | 378,7 | 479,7  | 534,2                 | 762,8 (2019-12-31)  | 0,87               |
| Hilir Jatiluhur (`citarum-hilir-jatiluhur`)  | Citarum      | -6.4250, 107.3250 | 200,8 | 441,6 | 592,5 | 711,8 | 1010,6 | 770,1                 | 1701,2 (2013-04-19) | 1,00               |
| Karawang (`citarum-karawang`)                | Citarum      | -6.3250, 107.2750 | 204,1 | 449,8 | 604,3 | 728,5 | 1031,8 | 788,1                 | 1740,8 (2013-04-19) | 1,00               |
| Muara (`citarum-muara`)                      | Citarum      | -5.9750, 107.0250 | 230,7 | 523,8 | 700,5 | 883,1 | 1198,1 | 972,9                 | 2040,7 (2013-04-19) | 1,01               |
| Garut kota (`cimanuk-garut`)                 | Cimanuk      | -7.1750, 107.9250 | 26,8  | 76,8  | 99,7  | 142,9 | 170,7  | 174,2                 | 291,4 (2021-11-18)  | 0,95               |
| Hulu Jatigede (`cimanuk-hulu-jatigede`)      | Cimanuk      | -6.9750, 108.0250 | 47,9  | 157,4 | 201,8 | 275,1 | 330,0  | 332,4                 | 628,5 (2021-11-18)  | 0,95               |
| Hilir Jatigede (`cimanuk-hilir-jatigede`)    | Cimanuk      | -6.7750, 108.0750 | 70,1  | 243,9 | 314,5 | 424,6 | 516,5  | 554,9                 | 947,4 (2021-11-18)  | 0,95               |
| Jatibarang (`cimanuk-jatibarang`)            | Cimanuk      | -6.4750, 108.2750 | 103,2 | 378,1 | 491,0 | 673,5 | 796,8  | 812,8                 | 1257,9 (2021-11-18) | 0,95               |
| Muara (`cimanuk-muara`)                      | Cimanuk      | -6.3750, 108.2250 | 99,7  | 380,9 | 496,7 | 682,9 | 806,2  | 821,3                 | 1232,1 (2021-11-18) | 0,94               |
| Cileungsi (`bekasi-cileungsi`)               | Cileungsi    | -6.4250, 107.0250 | 5,7   | 10,6  | 12,9  | 17,6  | 21,6   | 22,9                  | 57,9 (2020-01-25)   | 1,05               |
| Cikeas (`bekasi-cikeas`)                     | Cikeas       | -6.4250, 106.8750 | 16,1  | 28,6  | 34,6  | 47,0  | 57,9   | 59,6                  | 130,5 (2020-01-25)  | 1,04               |
| Pertemuan Cileungsi-Cikeas (`bekasi-p2c`)    | Kali Bekasi  | -6.3250, 106.9250 | 21,2  | 38,7  | 47,4  | 65,1  | 81,8   | 94,9                  | 233,4 (2020-01-25)  | 1,06               |
| Bekasi kota (`bekasi-kota`)                  | Kali Bekasi  | -6.1250, 106.9750 | 43,8  | 89,4  | 112,1 | 157,8 | 201,3  | 206,7                 | 624,2 (2020-01-25)  | 1,03               |
| Hulu Salak (`cisadane-hulu`)                 | Cisadane     | -6.7250, 106.7750 | 7,5   | 14,1  | 17,3  | 24,3  | 30,2   | 26,0                  | 45,1 (2013-12-23)   | 1,00               |
| Kota Bogor (`cisadane-bogor`)                | Cisadane     | -6.5750, 106.7250 | 18,5  | 33,1  | 40,6  | 56,1  | 69,2   | 71,3                  | 125,5 (2013-12-22)  | 0,94               |
| Batas Banten (`cisadane-batas-banten`)       | Cisadane     | -6.3750, 106.6250 | 60,1  | 110,4 | 133,3 | 178,5 | 217,8  | 198,9                 | 313,1 (2013-12-23)  | 1,01               |
| Katulampa (`ciliwung-katulampa`)             | Ciliwung     | -6.6750, 106.8250 | 10,6  | 18,0  | 21,7  | 29,8  | 37,5   | 41,3                  | 72,6 (2013-04-06)   | 1,01               |
| Kota Bogor (`ciliwung-bogor`)                | Ciliwung     | -6.5750, 106.7750 | 14,6  | 24,9  | 30,1  | 41,5  | 52,2   | 61,8                  | 113,8 (2013-12-22)  | 1,00               |
| Depok (`ciliwung-depok`)                     | Ciliwung     | -6.3750, 106.7750 | 20,3  | 35,7  | 44,0  | 61,4  | 80,7   | 92,2                  | 184,3 (2020-01-25)  | 1,01               |
| Kuningan (`cisanggarung-kuningan`)           | Cisanggarung | -6.9750, 108.6250 | 14,9  | 65,4  | 84,1  | 113,5 | 132,0  | 121,7                 | 176,0 (1998-12-21)  | 0,99               |
| Cirebon timur (`cisanggarung-cirebon-timur`) | Cisanggarung | -6.9250, 108.7250 | 22,3  | 91,7  | 117,3 | 157,1 | 182,5  | 169,0                 | 237,8 (1998-12-21)  | 0,98               |
| Muara (`cisanggarung-muara`)                 | Cisanggarung | -6.8250, 108.7750 | 22,4  | 95,2  | 121,7 | 163,1 | 190,4  | 177,4                 | 247,9 (1998-12-21)  | 0,99               |
| Subang tengah (`cipunagara-subang`)          | Cipunagara   | -6.5250, 107.8750 | 21,8  | 66,8  | 88,7  | 127,2 | 157,1  | 155,0                 | 316,7 (2020-01-25)  | 1,03               |
| Pamanukan (`cipunagara-pamanukan`)           | Cipunagara   | -6.2750, 107.7750 | 34,2  | 103,5 | 136,7 | 197,8 | 246,6  | 238,1                 | 511,9 (2020-01-26)  | 1,03               |
| Hilir (`cilamaya-hilir`)                     | Cilamaya     | -6.2250, 107.5750 | 6,3   | 12,3  | 15,5  | 21,7  | 28,2   | 22,0                  | 50,8 (2020-01-25)   | 1,02               |
| Tasikmalaya (`citanduy-tasikmalaya`)         | Citanduy     | -7.3250, 108.2250 | 29,8  | 77,0  | 93,8  | 120,3 | 142,8  | 136,1                 | 199,0 (1998-12-21)  | 0,98               |
| Ciamis (`citanduy-ciamis`)                   | Citanduy     | -7.3750, 108.3750 | 43,4  | 108,1 | 131,1 | 168,4 | 193,0  | 182,9                 | 267,5 (2010-06-08)  | 0,98               |
| Kota Banjar (`citanduy-banjar`)              | Citanduy     | -7.3250, 108.5250 | 87,3  | 246,7 | 305,9 | 393,0 | 441,6  | 426,6                 | 614,2 (1998-12-21)  | 0,99               |
| Muara (`citanduy-muara`)                     | Citanduy     | -7.6750, 108.7750 | 154,2 | 438,5 | 546,0 | 690,8 | 821,0  | 749,8                 | 1041,3 (2020-03-05) | 0,98               |
| Tengah (`cimandiri-tengah`)                  | Cimandiri    | -7.0250, 106.7250 | 28,9  | 56,7  | 69,6  | 102,4 | 133,2  | 114,1                 | 176,8 (2013-04-19)  | 0,94               |
| Palabuhanratu (`cimandiri-palabuhanratu`)    | Cimandiri    | -7.0250, 106.5250 | 93,8  | 182,7 | 222,9 | 328,6 | 418,6  | 333,8                 | 570,5 (2015-12-16)  | 0,96               |
| Tengah (`ciwulan-tengah`)                    | Ciwulan      | -7.4750, 108.1250 | 41,0  | 96,5  | 119,2 | 156,2 | 186,5  | 171,3                 | 269,4 (2011-05-14)  | 0,96               |
| Muara (`ciwulan-muara`)                      | Ciwulan      | -7.7750, 108.0750 | 58,6  | 140,5 | 175,8 | 234,5 | 289,9  | 247,8                 | 413,0 (2021-11-20)  | 0,98               |
| Muara (`cilaki-muara`)                       | Cilaki       | -7.6750, 107.6750 | 8,6   | 16,3  | 21,2  | 33,9  | 46,6   | 43,7                  | 90,4 (2003-10-31)   | 0,97               |
| Muara (`cikaso-muara`)                       | Cikaso       | -7.3750, 106.8250 | 88,1  | 152,5 | 183,9 | 259,6 | 327,9  | 287,1                 | 443,6 (2015-12-17)  | 0,96               |
| Muara (`cibuni-muara`)                       | Cibuni       | -7.4750, 107.0750 | 24,6  | 42,2  | 53,7  | 84,1  | 108,7  | 97,6                  | 163,4 (2022-11-14)  | 0,95               |

Satuan m³/s.

## Uji terhadap kejadian banjir tercatat

Kejadian dari `docs/calibration/banjir-tercatat.csv`. Kejadian **di luar sampel** tidak ikut dipakai menghitung ambang. Kejadian dengan model `seamless_v4` memakai data gabungan Open-Meteo (intermediate dan prakiraan), bukan reanalisis: debitnya bisa berbeda dari reanalisis di hari yang sama, jadi hasilnya indikatif.

| Kejadian                                                                                                                                                                     | Titik                   | Model             | Sampel          | Puncak (m³/s) | Tanggal puncak | Tingkat |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- | ----------------- | --------------- | ------------- | -------------- | ------- |
| [garut-2016](https://id.wikipedia.org/wiki/Cimanuk)                                                                                                                          | `cimanuk-garut`         | `consolidated_v4` | di dalam sampel | 115,7         | 2016-09-24     | Waspada |
| [garut-2016](https://id.wikipedia.org/wiki/Cimanuk)                                                                                                                          | `cimanuk-hulu-jatigede` | `consolidated_v4` | di dalam sampel | 161,2         | 2016-09-24     | Info    |
| [bekasi-2025](https://itb.ac.id/berita/dosen-meteorologi-itb-ungkap-fakta-banjir-bekasi-2025-tanggul-tak-lagi-cukup/62288)                                                   | `bekasi-cileungsi`      | `consolidated_v4` | di luar sampel  | 20,3          | 2025-03-04     | Siaga   |
| [bekasi-2025](https://itb.ac.id/berita/dosen-meteorologi-itb-ungkap-fakta-banjir-bekasi-2025-tanggul-tak-lagi-cukup/62288)                                                   | `bekasi-cikeas`         | `consolidated_v4` | di luar sampel  | 45,8          | 2025-02-28     | Waspada |
| [bekasi-2025](https://itb.ac.id/berita/dosen-meteorologi-itb-ungkap-fakta-banjir-bekasi-2025-tanggul-tak-lagi-cukup/62288)                                                   | `bekasi-p2c`            | `consolidated_v4` | di luar sampel  | 68,7          | 2025-03-03     | Siaga   |
| [bekasi-2025](https://itb.ac.id/berita/dosen-meteorologi-itb-ungkap-fakta-banjir-bekasi-2025-tanggul-tak-lagi-cukup/62288)                                                   | `bekasi-kota`           | `consolidated_v4` | di luar sampel  | 193,1         | 2025-03-04     | Siaga   |
| [pamanukan-2026](https://bandung.kompas.com/read/2026/01/30/100452978/darurat-banjir-subang-sungai-cipunagara-meluap-warga-pamanukan-takut-tanggul)                          | `cipunagara-subang`     | `seamless_v4`     | di luar sampel  | 122,9         | 2026-01-25     | Waspada |
| [pamanukan-2026](https://bandung.kompas.com/read/2026/01/30/100452978/darurat-banjir-subang-sungai-cipunagara-meluap-warga-pamanukan-takut-tanggul)                          | `cipunagara-pamanukan`  | `seamless_v4`     | di luar sampel  | 214,3         | 2026-01-24     | Siaga   |
| [bandung-2021](https://www.pikiran-rakyat.com/bandung-raya/pr-011955150/update-banjir-bandung-25-mei-2021-baleendah-tol-purbaleunyi-dan-4-titik-lainnya-masih-digenangi-air) | `citarum-majalaya`      | `consolidated_v4` | di dalam sampel | 90,8          | 2021-05-24     | Bahaya  |
| [bandung-2021](https://www.pikiran-rakyat.com/bandung-raya/pr-011955150/update-banjir-bandung-25-mei-2021-baleendah-tol-purbaleunyi-dan-4-titik-lainnya-masih-digenangi-air) | `citarum-dayeuhkolot`   | `consolidated_v4` | di dalam sampel | 320,4         | 2021-05-24     | Bahaya  |
| [bandung-2021](https://www.pikiran-rakyat.com/bandung-raya/pr-011955150/update-banjir-bandung-25-mei-2021-baleendah-tol-purbaleunyi-dan-4-titik-lainnya-masih-digenangi-air) | `citarum-nanjung`       | `consolidated_v4` | di dalam sampel | 625,6         | 2021-05-24     | Bahaya  |
