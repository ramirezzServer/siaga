# Ambang indeks hujan sub-DAS Citarum Hulu

Dibuat `make rain-threshold` pada 2026-10-01 17:44 UTC. Jangan diedit manual; ubah daftar sel lalu jalankan ulang (ADR 0020).

Hujan per jam model ECMWF IFS 9 km (arsip Open-Meteo `models=ecmwf_ifs`, `cell_selection=nearest`) 2017-01-01 sampai 2024-12-31 dirata-rata per sub-DAS dengan bobot luas (`docs/calibration/sub-das-citarum-hulu.csv`). Untuk setiap hari UTC diambil akumulasi 3, 6, dan 24 jam terbesar yang berakhir di hari itu; ambang adalah persentil nilai harian itu, jadi frekuensinya sama dengan ambang debit (73, 37, 7, dan 1,8 hari per tahun). Hasilnya berlabel "indikasi potensi banjir", bukan pengukuran debit (PRD).

| Sub-DAS     | Jendela | p50 | p80  | p90  | p98  | p99,5 | Hari berisi |
| ----------- | ------- | --- | ---- | ---- | ---- | ----- | ----------- |
| Cirasea     | 3 jam   | 1,9 | 6,7  | 10,3 | 17,6 | 24,4  | 2922        |
| Cirasea     | 6 jam   | 2,8 | 9,1  | 14,3 | 25,6 | 37,9  | 2922        |
| Cirasea     | 24 jam  | 6,2 | 17,4 | 24,7 | 45,7 | 66,7  | 2922        |
| Cisangkuy   | 3 jam   | 1,9 | 7,0  | 10,3 | 20,0 | 30,2  | 2922        |
| Cisangkuy   | 6 jam   | 2,9 | 9,8  | 14,9 | 28,5 | 44,4  | 2922        |
| Cisangkuy   | 24 jam  | 6,7 | 18,3 | 26,7 | 52,5 | 73,1  | 2922        |
| Ciwidey     | 3 jam   | 1,6 | 5,4  | 8,0  | 17,0 | 27,8  | 2922        |
| Ciwidey     | 6 jam   | 2,4 | 7,8  | 11,4 | 24,2 | 38,7  | 2922        |
| Ciwidey     | 24 jam  | 5,0 | 14,8 | 21,8 | 47,2 | 68,5  | 2922        |
| Citarik     | 3 jam   | 1,7 | 5,6  | 8,5  | 15,4 | 23,6  | 2922        |
| Citarik     | 6 jam   | 2,5 | 7,8  | 11,5 | 21,5 | 38,0  | 2922        |
| Citarik     | 24 jam  | 5,4 | 13,9 | 20,9 | 42,3 | 63,3  | 2922        |
| Ciminyak    | 3 jam   | 1,7 | 5,9  | 9,1  | 17,8 | 26,6  | 2922        |
| Ciminyak    | 6 jam   | 2,5 | 8,1  | 12,5 | 25,1 | 40,6  | 2922        |
| Ciminyak    | 24 jam  | 5,8 | 15,8 | 23,4 | 44,5 | 71,5  | 2922        |
| Cihaur      | 3 jam   | 2,1 | 6,9  | 10,2 | 17,4 | 27,2  | 2922        |
| Cihaur      | 6 jam   | 3,0 | 9,3  | 14,2 | 24,7 | 39,4  | 2922        |
| Cihaur      | 24 jam  | 7,3 | 17,1 | 24,7 | 46,7 | 76,2  | 2922        |
| Cikapundung | 3 jam   | 1,7 | 6,1  | 9,6  | 17,5 | 29,4  | 2922        |
| Cikapundung | 6 jam   | 2,6 | 8,4  | 13,1 | 25,7 | 40,6  | 2922        |
| Cikapundung | 24 jam  | 5,8 | 15,4 | 23,2 | 48,7 | 76,7  | 2922        |

Satuan mm.

## Hari dengan hujan 24 jam terbesar

Bahan pencocokan dengan berita banjir cekungan Bandung: bila hari-hari ini tidak pernah tercatat banjir, ambang perlu ditinjau.

| Sub-DAS     | Hari (akumulasi 24 jam, mm)                                                                       |
| ----------- | ------------------------------------------------------------------------------------------------- |
| Cirasea     | 2019-02-09 (115,6), 2019-02-08 (109,6), 2021-12-18 (92,0), 2021-12-17 (89,5), 2021-01-10 (88,0)   |
| Cisangkuy   | 2017-10-01 (92,6), 2019-02-09 (91,6), 2018-12-04 (91,5), 2021-12-18 (91,4), 2021-12-17 (90,5)     |
| Ciwidey     | 2021-05-25 (89,0), 2021-05-24 (88,9), 2021-12-23 (86,5), 2017-10-01 (80,0), 2017-02-21 (78,0)     |
| Citarik     | 2021-05-25 (125,5), 2021-05-24 (123,7), 2020-01-25 (83,6), 2017-10-01 (82,9), 2017-09-30 (78,8)   |
| Ciminyak    | 2021-05-25 (102,1), 2021-05-24 (101,7), 2020-01-09 (91,5), 2020-01-10 (90,6), 2019-05-10 (90,4)   |
| Cihaur      | 2017-09-30 (99,1), 2017-09-29 (95,1), 2019-12-06 (91,9), 2020-01-18 (90,0), 2021-05-25 (86,7)     |
| Cikapundung | 2019-12-06 (130,3), 2019-12-05 (122,2), 2021-04-01 (106,8), 2021-03-31 (102,4), 2019-05-09 (93,9) |
