# Deduplikasi gempa BMKG–USGS terhadap set berlabel (PRD T4)

Dihasilkan otomatis oleh `make dedup-labels`. Jangan diedit manual; jalankan ulang perintahnya. Metode di ADR 0024, keputusan ambang di ADR 0025.

Set berlabel: `dedup-berlabel.csv` (sha256 502259b1a448), `dedup-arsip.csv` (sha256 21196e4871e5). Label ISC berasal dari pengelompokan kejadian ISC Bulletin (International Seismological Centre, On-line Bulletin, https://doi.org/10.31905/D808B830): origin DJA (BMKG) dan NEIC (USGS) di kejadian ISC yang sama adalah pasangan. Label arsip disusun manual dari arsip payload real-time ingest.

Untuk setiap origin USGS: **positif** bila set berisi origin BMKG berlabel sama. **Pasangan**: origin BMKG dengan skor `quake.Rule.Match` terbaik yang lolos ambang lintas sumber. **Simulasi**: semua origin diputar lewat `quake.Settle` (sama dengan geo-processor) menurut urutan kedatangan (BMKG 3 menit, USGS 20 menit setelah kejadian), dengan ambang revisi 10s, 50 km, 1,0; pasangannya adalah origin BMKG yang berlaku di kejadian akhir. TP = pasangan berlabel sama, FP = pasangan berlabel lain (gabungan salah), FN = positif yang bukan TP. Presisi = TP/(TP+FP), recall = TP/positif.

## Basis `isc`

|                     |                                  |
| ------------------- | -------------------------------- |
| Periode origin      | 2021-01-01 s.d. 2024-10-31 (UTC) |
| Origin BMKG         | 6677                             |
| Origin USGS         | 8524                             |
| Origin USGS positif | 6513                             |
| Label (kejadian)    | 8688                             |

Selisih origin USGS ke origin BMKG berlabel sama:

| Selisih              | p50  | p90  | p95  | p99  | maks  |
| -------------------- | ---- | ---- | ---- | ---- | ----- |
| Waktu (detik)        | 1,4  | 4,2  | 5,5  | 10,2 | 34,3  |
| Jarak episenter (km) | 15,3 | 37,2 | 49,3 | 89,7 | 274,6 |
| Magnitudo            | 0,21 | 0,52 | 0,62 | 0,85 | 1,45  |

### semua (8524 origin USGS, 6513 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN    | Simulasi: recall | presisi | TP/FP/FN    |
| ------------------------------------------------ | ---------------- | ------- | ----------- | ---------------- | ------- | ----------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 99,7%            | 99,9%   | 6493/7/20   | 99,7%            | 99,9%   | 6492/4/21   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 98,9%            | 99,8%   | 6444/14/69  | 98,9%            | 99,9%   | 6443/6/70   |
| PRD awal: 1m30s, 75 km, 0,7                      | 95,8%            | 99,5%   | 6239/32/274 | 95,7%            | 99,7%   | 6235/16/278 |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 99,5%            | 99,8%   | 6479/14/34  | 99,5%            | 99,9%   | 6478/6/35   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 99,7%            | 99,9%   | 6492/7/21   | 99,7%            | 99,9%   | 6491/4/22   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,6%            | 99,9%   | 6484/4/29   | 99,6%            | 100,0%  | 6484/3/29   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,7%            | 99,8%   | 6494/12/19  | 99,7%            | 99,9%   | 6493/6/20   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,7%            | 99,8%   | 6494/14/19  | 99,7%            | 99,9%   | 6493/6/20   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 99,5%            | 99,9%   | 6478/7/35   | 99,4%            | 99,9%   | 6477/4/36   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 99,8%            | 99,9%   | 6500/7/13   | 99,8%            | 99,9%   | 6499/4/14   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 99,4%            | 99,9%   | 6472/7/41   | 99,4%            | 99,9%   | 6471/4/42   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 99,8%            | 99,9%   | 6497/8/16   | 99,7%            | 99,9%   | 6496/4/17   |

### USGS M ≥ 4,3 (6354 origin USGS, 5147 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN    | Simulasi: recall | presisi | TP/FP/FN    |
| ------------------------------------------------ | ---------------- | ------- | ----------- | ---------------- | ------- | ----------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 99,9%            | 99,9%   | 5140/7/7    | 99,8%            | 99,9%   | 5139/4/8    |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 99,2%            | 99,7%   | 5107/13/40  | 99,2%            | 99,9%   | 5106/5/41   |
| PRD awal: 1m30s, 75 km, 0,7                      | 96,1%            | 99,5%   | 4944/27/203 | 96,0%            | 99,7%   | 4940/13/207 |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 99,7%            | 99,7%   | 5131/13/16  | 99,7%            | 99,9%   | 5130/5/17   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 99,8%            | 99,9%   | 5139/7/8    | 99,8%            | 99,9%   | 5138/4/9    |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,7%            | 99,9%   | 5134/4/13   | 99,7%            | 99,9%   | 5134/3/13   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,9%            | 99,8%   | 5140/11/7   | 99,8%            | 99,9%   | 5139/5/8    |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,9%            | 99,7%   | 5140/13/7   | 99,8%            | 99,9%   | 5139/5/8    |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 99,7%            | 99,9%   | 5131/7/16   | 99,7%            | 99,9%   | 5130/4/17   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 99,9%            | 99,9%   | 5142/7/5    | 99,9%            | 99,9%   | 5141/4/6    |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 99,6%            | 99,9%   | 5124/7/23   | 99,5%            | 99,9%   | 5123/4/24   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 99,9%            | 99,8%   | 5142/8/5    | 99,9%            | 99,9%   | 5141/4/6    |

### USGS M ≥ 4,5 (3723 origin USGS, 3236 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN    | Simulasi: recall | presisi | TP/FP/FN   |
| ------------------------------------------------ | ---------------- | ------- | ----------- | ---------------- | ------- | ---------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 99,9%            | 99,8%   | 3232/7/4    | 99,8%            | 99,9%   | 3231/4/5   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 99,2%            | 99,6%   | 3210/12/26  | 99,2%            | 99,9%   | 3209/4/27  |
| PRD awal: 1m30s, 75 km, 0,7                      | 96,4%            | 99,4%   | 3120/19/116 | 96,3%            | 99,7%   | 3117/9/119 |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 99,8%            | 99,6%   | 3228/12/8   | 99,7%            | 99,9%   | 3227/4/9   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 99,8%            | 99,8%   | 3231/7/5    | 99,8%            | 99,9%   | 3230/4/6   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,8%            | 99,9%   | 3231/4/5    | 99,8%            | 99,9%   | 3231/3/5   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,9%            | 99,7%   | 3232/10/4   | 99,8%            | 99,9%   | 3231/4/5   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,9%            | 99,6%   | 3232/12/4   | 99,8%            | 99,9%   | 3231/4/5   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 99,8%            | 99,8%   | 3228/7/8    | 99,7%            | 99,9%   | 3227/4/9   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 99,9%            | 99,8%   | 3232/7/4    | 99,8%            | 99,9%   | 3231/4/5   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 99,5%            | 99,8%   | 3221/7/15   | 99,5%            | 99,9%   | 3220/4/16  |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 99,9%            | 99,8%   | 3234/8/2    | 99,9%            | 99,9%   | 3233/4/3   |

### Jawa Barat (224 origin USGS, 201 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ------------------------------------------------ | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| PRD awal: 1m30s, 75 km, 0,7                      | 96,0%            | 100,0%  | 193/0/8  | 96,0%            | 100,0%  | 193/0/8  |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 201/0/0  | 100,0%           | 100,0%  | 201/0/0  |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |

### Gabungan salah lain

Kejadian simulasi berisi dua origin satu sumber dari label berbeda (lewat ambang revisi), dan kecocokan per 1.000 origin USGS setelah waktunya digeser ±2–10 menit. Set berlabel hanya memuat origin BMKG di sekitar origin USGS, jadi angka geser adalah batas bawah.

| Ambang                                           | BMKG–BMKG | USGS–USGS | Geser ±2–10 mnt /1.000 |
| ------------------------------------------------ | --------- | --------- | ---------------------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 0         | 3         | 2,52                   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 0         | 3         | 3,32                   |
| PRD awal: 1m30s, 75 km, 0,7                      | 0         | 3         | 7,17                   |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 0         | 3         | 3,66                   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 0         | 3         | 2,48                   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 0         | 3         | 1,83                   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 0         | 3         | 3,07                   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 0         | 3         | 3,82                   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 0         | 3         | 2,44                   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 0         | 3         | 2,63                   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 0         | 3         | 2,31                   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 0         | 3         | 2,66                   |

### Menurut tipe magnitudo BMKG (ambang bawaan (ADR 0025))

| Tipe | Positif | Recall pasangan | Selisih M p50 | p95  | p99  | maks |
| ---- | ------- | --------------- | ------------- | ---- | ---- | ---- |
| MLv  | 5333    | 99,6%           | 0,23          | 0,64 | 0,85 | 1,45 |
| Mw   | 858     | 100,0%          | 0,11          | 0,41 | 0,56 | 0,90 |
| Mwp  | 265     | 100,0%          | 0,23          | 0,74 | 1,19 | 1,24 |
| mb   | 35      | 100,0%          | 0,29          | 0,53 | 0,85 | 0,85 |
| Mjma | 22      | 100,0%          | 0,11          | 0,29 | 0,31 | 0,31 |

### Salah menurut ambang bawaan (ADR 0025) (cara pasangan, 22 dari 27 ditampilkan)

Semua 7 FP ditampilkan; 7 di antaranya memilih origin BMKG yang label ISC-nya punya origin USGS sendiri (kolom terakhir), jadi gempa lain yang berdekatan, bukan satu gempa yang dipecah ISC.

| USGS      | Waktu (UTC)         | M   | Jenis | Ke BMKG berlabel sama (dtk, km, ΔM) | Pasangan terpilih                             | USGS label pasangan (dtk) |
| --------- | ------------------- | --- | ----- | ----------------------------------- | --------------------------------------------- | ------------------------- |
| 615858317 | 2021-01-30 05:17:24 | 5,3 | FP    | –                                   | 619150271 (label 619781857; 5,7, 30,0, 0,34)  | 615858316 (9,5)           |
| 615861473 | 2021-02-21 18:52:12 | 4,2 | FN    | 21,0, 195,1, 0,11 (MLv)             | –                                             | –                         |
| 616568277 | 2021-03-26 09:04:09 | 4,4 | FN    | 4,5, 213,2, 0,68 (MLv)              | –                                             | –                         |
| 616574113 | 2021-05-16 09:39:38 | 4,1 | FN    | 26,1, 240,6, 1,29 (MLv)             | –                                             | –                         |
| 616575812 | 2021-05-31 12:17:06 | 4,4 | FN    | 11,2, 160,1, 0,19 (MLv)             | –                                             | –                         |
| 616578800 | 2021-06-15 10:11:11 | 4,0 | FN    | 1,8, 176,5, 0,16 (MLv)              | –                                             | –                         |
| 616580778 | 2021-06-28 15:06:38 | 4,2 | FN    | 13,7, 175,1, 0,39 (MLv)             | –                                             | –                         |
| 616580877 | 2021-06-29 14:30:02 | 4,6 | FP    | –                                   | 619766137 (label 620786324; 8,0, 47,2, 0,28)  | 616580876 (9,2)           |
| 616588251 | 2021-08-11 23:34:27 | 4,4 | FN    | 16,6, 164,4, 0,38 (MLv)             | –                                             | –                         |
| 616588310 | 2021-08-12 08:17:30 | 4,5 | FN    | 21,9, 202,4, 0,89 (MLv)             | –                                             | –                         |
| 616590146 | 2021-08-20 17:47:26 | 4,2 | FN    | 3,3, 32,2, 1,45 (MLv)               | –                                             | –                         |
| 616654082 | 2021-11-01 18:38:08 | 4,7 | FP    | –                                   | 620388169 (label 621252388; 10,0, 9,5, 0,39)  | 616654081 (8,5)           |
| 616661640 | 2021-12-29 18:47:20 | 4,1 | FN    | 0,2, 17,7, 1,41 (MLv)               | –                                             | –                         |
| 618802402 | 2022-03-26 13:16:53 | 4,7 | FP    | –                                   | 630973925 (label 622180388; 13,5, 15,2, 0,40) | 618802401 (13,3)          |
| 618803643 | 2022-04-05 12:26:57 | 4,2 | FN    | 9,5, 208,0, 0,52 (MLv)              | –                                             | –                         |
| 618803727 | 2022-04-06 06:07:11 | 4,8 | FN    | 28,9, 221,5, 1,30 (MLv)             | –                                             | –                         |
| 619008467 | 2022-06-14 03:54:45 | 4,1 | FN    | 20,9, 174,6, 0,21 (MLv)             | –                                             | –                         |
| 619582977 | 2022-10-08 17:34:31 | 5,1 | FN    | 4,5, 41,7, 1,39 (MLv)               | –                                             | –                         |
| 619768922 | 2022-12-24 20:57:17 | 4,7 | FP    | –                                   | 631702791 (label 625550147; 17,5, 6,0, 0,36)  | 619768923 (17,1)          |
| 620013698 | 2023-01-24 02:50:57 | 4,2 | FN    | 14,4, 167,4, 0,04 (MLv)             | –                                             | –                         |
| 630902344 | 2023-07-05 12:07:12 | 5,1 | FP    | –                                   | 631709056 (label 626349475; 19,4, 7,5, 0,45)  | 630902343 (19,6)          |
| 630902813 | 2023-07-10 01:19:22 | 4,7 | FP    | –                                   | 631709168 (label 626366433; 15,9, 36,0, 0,22) | 630902812 (15,0)          |

## Basis `arsip`

|                     |                                  |
| ------------------- | -------------------------------- |
| Periode origin      | 2026-09-24 s.d. 2026-10-20 (UTC) |
| Origin BMKG         | 10                               |
| Origin USGS         | 10                               |
| Origin USGS positif | 10                               |
| Label (kejadian)    | 10                               |

Selisih origin USGS ke origin BMKG berlabel sama:

| Selisih              | p50  | p90  | p95  | p99  | maks |
| -------------------- | ---- | ---- | ---- | ---- | ---- |
| Waktu (detik)        | 2,0  | 3,6  | 3,8  | 3,8  | 3,8  |
| Jarak episenter (km) | 13,2 | 38,3 | 38,4 | 38,4 | 38,4 |
| Magnitudo            | 0,10 | 0,40 | 0,40 | 0,40 | 0,40 |

### semua (10 origin USGS, 10 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ------------------------------------------------ | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| PRD awal: 1m30s, 75 km, 0,7                      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |

### USGS M ≥ 4,3 (10 origin USGS, 10 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ------------------------------------------------ | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| PRD awal: 1m30s, 75 km, 0,7                      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |

### USGS M ≥ 4,5 (10 origin USGS, 10 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ------------------------------------------------ | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| PRD awal: 1m30s, 75 km, 0,7                      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |

### Jawa Barat (1 origin USGS, 1 positif)

| Ambang (waktu, jarak, magnitudo)                 | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ------------------------------------------------ | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| PRD awal: 1m30s, 75 km, 0,7                      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |

### Gabungan salah lain

Kejadian simulasi berisi dua origin satu sumber dari label berbeda (lewat ambang revisi), dan kecocokan per 1.000 origin USGS setelah waktunya digeser ±2–10 menit. Set berlabel hanya memuat origin BMKG di sekitar origin USGS, jadi angka geser adalah batas bawah.

| Ambang                                           | BMKG–BMKG | USGS–USGS | Geser ±2–10 mnt /1.000 |
| ------------------------------------------------ | --------- | --------- | ---------------------- |
| bawaan (ADR 0025): 20s, 150 km, 1,3; M ≥ 6: 1m0s | 0         | 0         | 0,00                   |
| bawaan lama (ADR 0008): 30s, 100 km, 1,0         | 0         | 0         | 0,00                   |
| PRD awal: 1m30s, 75 km, 0,7                      | 0         | 0         | 0,00                   |
| kandidat 1e-3c-2a: 30s, 120 km, 1,3              | 0         | 0         | 0,00                   |
| tanpa jendela M ≥ 6: 20s, 150 km, 1,3            | 0         | 0         | 0,00                   |
| waktu 15 dtk: 15s, 150 km, 1,3; M ≥ 6: 1m0s      | 0         | 0         | 0,00                   |
| waktu 25 dtk: 25s, 150 km, 1,3; M ≥ 6: 1m0s      | 0         | 0         | 0,00                   |
| waktu 30 dtk: 30s, 150 km, 1,3; M ≥ 6: 1m0s      | 0         | 0         | 0,00                   |
| jarak 120 km: 20s, 120 km, 1,3; M ≥ 6: 1m0s      | 0         | 0         | 0,00                   |
| jarak 200 km: 20s, 200 km, 1,3; M ≥ 6: 1m0s      | 0         | 0         | 0,00                   |
| magnitudo 1,0: 20s, 150 km, 1,0; M ≥ 6: 1m0s     | 0         | 0         | 0,00                   |
| magnitudo 1,5: 20s, 150 km, 1,5; M ≥ 6: 1m0s     | 0         | 0         | 0,00                   |

### Menurut tipe magnitudo BMKG (ambang bawaan (ADR 0025))

| Tipe            | Positif | Recall pasangan | Selisih M p50 | p95  | p99  | maks |
| --------------- | ------- | --------------- | ------------- | ---- | ---- | ---- |
| (tidak disebut) | 10      | 100,0%          | 0,10          | 0,40 | 0,40 | 0,40 |
