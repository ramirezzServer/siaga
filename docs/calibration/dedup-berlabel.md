# Deduplikasi gempa BMKG–USGS terhadap set berlabel (PRD T4)

Dihasilkan otomatis oleh `make dedup-labels`. Jangan diedit manual; jalankan ulang perintahnya. Keputusan di ADR 0024.

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

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN    | Simulasi: recall | presisi | TP/FP/FN    |
| ---------------------------------------- | ---------------- | ------- | ----------- | ---------------- | ------- | ----------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 98,9%            | 99,8%   | 6444/14/69  | 98,9%            | 99,9%   | 6443/6/70   |
| PRD awal: 1m30s, 75 km, 0,7              | 95,8%            | 99,5%   | 6239/32/274 | 95,7%            | 99,7%   | 6235/16/278 |
| jarak 120 km: 30s, 120 km, 1,0           | 99,2%            | 99,8%   | 6461/14/52  | 99,2%            | 99,9%   | 6460/6/53   |
| jarak 150 km: 30s, 150 km, 1,0           | 99,4%            | 99,8%   | 6473/14/40  | 99,4%            | 99,9%   | 6472/6/41   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 98,9%            | 99,9%   | 6442/7/71   | 98,9%            | 99,9%   | 6441/4/72   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 98,9%            | 99,6%   | 6444/26/69  | 98,9%            | 99,8%   | 6441/12/72  |
| magnitudo 0,7: 30s, 100 km, 0,7          | 96,6%            | 99,8%   | 6294/13/219 | 96,6%            | 99,9%   | 6293/9/220  |
| magnitudo 1,3: 30s, 100 km, 1,3          | 99,2%            | 99,8%   | 6462/14/51  | 99,2%            | 99,9%   | 6461/6/52   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 99,5%            | 99,8%   | 6479/14/34  | 99,5%            | 99,9%   | 6478/6/35   |

### USGS M ≥ 4,3 (6354 origin USGS, 5147 positif)

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN    | Simulasi: recall | presisi | TP/FP/FN    |
| ---------------------------------------- | ---------------- | ------- | ----------- | ---------------- | ------- | ----------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 99,2%            | 99,7%   | 5107/13/40  | 99,2%            | 99,9%   | 5106/5/41   |
| PRD awal: 1m30s, 75 km, 0,7              | 96,1%            | 99,5%   | 4944/27/203 | 96,0%            | 99,7%   | 4940/13/207 |
| jarak 120 km: 30s, 120 km, 1,0           | 99,4%            | 99,7%   | 5117/13/30  | 99,4%            | 99,9%   | 5116/5/31   |
| jarak 150 km: 30s, 150 km, 1,0           | 99,6%            | 99,7%   | 5124/13/23  | 99,5%            | 99,9%   | 5123/5/24   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 99,2%            | 99,9%   | 5106/7/41   | 99,2%            | 99,9%   | 5105/4/42   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 99,2%            | 99,6%   | 5107/22/40  | 99,2%            | 99,8%   | 5104/9/43   |
| magnitudo 0,7: 30s, 100 km, 0,7          | 96,8%            | 99,8%   | 4980/12/167 | 96,7%            | 99,8%   | 4979/8/168  |
| magnitudo 1,3: 30s, 100 km, 1,3          | 99,5%            | 99,7%   | 5121/13/26  | 99,5%            | 99,9%   | 5120/5/27   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 99,7%            | 99,7%   | 5131/13/16  | 99,7%            | 99,9%   | 5130/5/17   |

### USGS M ≥ 4,5 (3723 origin USGS, 3236 positif)

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN    | Simulasi: recall | presisi | TP/FP/FN   |
| ---------------------------------------- | ---------------- | ------- | ----------- | ---------------- | ------- | ---------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 99,2%            | 99,6%   | 3210/12/26  | 99,2%            | 99,9%   | 3209/4/27  |
| PRD awal: 1m30s, 75 km, 0,7              | 96,4%            | 99,4%   | 3120/19/116 | 96,3%            | 99,7%   | 3117/9/119 |
| jarak 120 km: 30s, 120 km, 1,0           | 99,4%            | 99,6%   | 3218/12/18  | 99,4%            | 99,9%   | 3217/4/19  |
| jarak 150 km: 30s, 150 km, 1,0           | 99,5%            | 99,6%   | 3221/12/15  | 99,5%            | 99,9%   | 3220/4/16  |
| waktu 20 dtk: 20s, 100 km, 1,0           | 99,2%            | 99,8%   | 3209/7/27   | 99,1%            | 99,9%   | 3208/4/28  |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 99,2%            | 99,5%   | 3210/16/26  | 99,1%            | 99,8%   | 3208/5/28  |
| magnitudo 0,7: 30s, 100 km, 0,7          | 97,0%            | 99,7%   | 3139/11/97  | 97,0%            | 99,8%   | 3138/7/98  |
| magnitudo 1,3: 30s, 100 km, 1,3          | 99,5%            | 99,6%   | 3220/12/16  | 99,5%            | 99,9%   | 3219/4/17  |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 99,8%            | 99,6%   | 3228/12/8   | 99,7%            | 99,9%   | 3227/4/9   |

### Jawa Barat (224 origin USGS, 201 positif)

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ---------------------------------------- | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| PRD awal: 1m30s, 75 km, 0,7              | 96,0%            | 100,0%  | 193/0/8  | 96,0%            | 100,0%  | 193/0/8  |
| jarak 120 km: 30s, 120 km, 1,0           | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| jarak 150 km: 30s, 150 km, 1,0           | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| waktu 20 dtk: 20s, 100 km, 1,0           | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 99,0%            | 100,0%  | 199/0/2  | 99,0%            | 100,0%  | 199/0/2  |
| magnitudo 0,7: 30s, 100 km, 0,7          | 97,0%            | 100,0%  | 195/0/6  | 97,0%            | 100,0%  | 195/0/6  |
| magnitudo 1,3: 30s, 100 km, 1,3          | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 99,5%            | 100,0%  | 200/0/1  | 99,5%            | 100,0%  | 200/0/1  |

### Gabungan salah lain

Kejadian simulasi berisi dua origin satu sumber dari label berbeda (lewat ambang revisi), dan kecocokan per 1.000 origin USGS setelah waktunya digeser ±2–10 menit. Set berlabel hanya memuat origin BMKG di sekitar origin USGS, jadi angka geser adalah batas bawah.

| Ambang                                   | BMKG–BMKG | USGS–USGS | Geser ±2–10 mnt /1.000 |
| ---------------------------------------- | --------- | --------- | ---------------------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 0         | 3         | 3,32                   |
| PRD awal: 1m30s, 75 km, 0,7              | 0         | 3         | 7,17                   |
| jarak 120 km: 30s, 120 km, 1,0           | 0         | 3         | 3,37                   |
| jarak 150 km: 30s, 150 km, 1,0           | 0         | 3         | 3,47                   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 0         | 3         | 2,17                   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 0         | 3         | 5,98                   |
| magnitudo 0,7: 30s, 100 km, 0,7          | 0         | 3         | 2,73                   |
| magnitudo 1,3: 30s, 100 km, 1,3          | 0         | 3         | 3,60                   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 0         | 3         | 3,66                   |

### Menurut tipe magnitudo BMKG (ambang bawaan (ADR 0008))

| Tipe | Positif | Recall pasangan | Selisih M p50 | p95  | p99  | maks |
| ---- | ------- | --------------- | ------------- | ---- | ---- | ---- |
| MLv  | 5333    | 98,9%           | 0,23          | 0,64 | 0,85 | 1,45 |
| Mw   | 858     | 99,9%           | 0,11          | 0,41 | 0,56 | 0,90 |
| Mwp  | 265     | 97,4%           | 0,23          | 0,74 | 1,19 | 1,24 |
| mb   | 35      | 100,0%          | 0,29          | 0,53 | 0,85 | 0,85 |
| Mjma | 22      | 100,0%          | 0,11          | 0,29 | 0,31 | 0,31 |

### Salah menurut ambang bawaan (ADR 0008) (cara pasangan, 15 dari 83 ditampilkan)

| USGS      | Waktu (UTC)         | M   | Jenis | Ke BMKG berlabel sama (dtk, km, ΔM) | Pasangan terpilih                            |
| --------- | ------------------- | --- | ----- | ----------------------------------- | -------------------------------------------- |
| 615856423 | 2021-01-14 05:08:33 | 4,1 | FN    | 1,3, 102,9, 0,07 (MLv)              | –                                            |
| 615858317 | 2021-01-30 05:17:24 | 5,3 | FP    | –                                   | 619150271 (label 619781857; 5,7, 30,0, 0,34) |
| 615858462 | 2021-01-31 14:53:34 | 4,4 | FN    | 13,5, 125,2, 0,42 (MLv)             | –                                            |
| 615858544 | 2021-02-01 05:31:49 | 4,5 | FN    | 13,1, 119,4, 0,22 (MLv)             | –                                            |
| 615860735 | 2021-02-16 08:42:11 | 5,1 | FN    | 0,0, 12,2, 1,01 (Mwp)               | –                                            |
| 615861372 | 2021-02-21 00:07:40 | 4,3 | FN    | 9,1, 90,5, 1,07 (MLv)               | –                                            |
| 615861473 | 2021-02-21 18:52:12 | 4,2 | FN    | 21,0, 195,1, 0,11 (MLv)             | –                                            |
| 615861881 | 2021-02-25 03:30:39 | 4,2 | FN    | 3,0, 33,3, 1,03 (MLv)               | –                                            |
| 615862330 | 2021-02-28 22:51:23 | 4,5 | FN    | 9,3, 100,3, 0,82 (MLv)              | –                                            |
| 616565740 | 2021-03-07 04:43:25 | 4,7 | FN    | 0,0, 9,6, 1,24 (Mwp)                | –                                            |
| 616566718 | 2021-03-13 14:29:15 | 4,7 | FN    | 1,6, 9,5, 1,19 (Mwp)                | –                                            |
| 616568277 | 2021-03-26 09:04:09 | 4,4 | FN    | 4,5, 213,2, 0,68 (MLv)              | –                                            |
| 616568733 | 2021-03-30 16:08:21 | 4,9 | FN    | 9,7, 87,2, 1,03 (MLv)               | –                                            |
| 616570156 | 2021-04-09 04:58:56 | 4,1 | FN    | 11,0, 115,4, 0,19 (MLv)             | –                                            |
| 616574113 | 2021-05-16 09:39:38 | 4,1 | FN    | 26,1, 240,6, 1,29 (MLv)             | –                                            |

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

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ---------------------------------------- | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| PRD awal: 1m30s, 75 km, 0,7              | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 120 km: 30s, 120 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 150 km: 30s, 150 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 0,7: 30s, 100 km, 0,7          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,3: 30s, 100 km, 1,3          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |

### USGS M ≥ 4,3 (10 origin USGS, 10 positif)

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ---------------------------------------- | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| PRD awal: 1m30s, 75 km, 0,7              | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 120 km: 30s, 120 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 150 km: 30s, 150 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 0,7: 30s, 100 km, 0,7          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,3: 30s, 100 km, 1,3          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |

### USGS M ≥ 4,5 (10 origin USGS, 10 positif)

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ---------------------------------------- | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| PRD awal: 1m30s, 75 km, 0,7              | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 120 km: 30s, 120 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| jarak 150 km: 30s, 150 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 0,7: 30s, 100 km, 0,7          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| magnitudo 1,3: 30s, 100 km, 1,3          | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 100,0%           | 100,0%  | 10/0/0   | 100,0%           | 100,0%  | 10/0/0   |

### Jawa Barat (1 origin USGS, 1 positif)

| Ambang (waktu, jarak, magnitudo)         | Pasangan: recall | presisi | TP/FP/FN | Simulasi: recall | presisi | TP/FP/FN |
| ---------------------------------------- | ---------------- | ------- | -------- | ---------------- | ------- | -------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| PRD awal: 1m30s, 75 km, 0,7              | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| jarak 120 km: 30s, 120 km, 1,0           | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| jarak 150 km: 30s, 150 km, 1,0           | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| waktu 20 dtk: 20s, 100 km, 1,0           | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| magnitudo 0,7: 30s, 100 km, 0,7          | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| magnitudo 1,3: 30s, 100 km, 1,3          | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 100,0%           | 100,0%  | 1/0/0    | 100,0%           | 100,0%  | 1/0/0    |

### Gabungan salah lain

Kejadian simulasi berisi dua origin satu sumber dari label berbeda (lewat ambang revisi), dan kecocokan per 1.000 origin USGS setelah waktunya digeser ±2–10 menit. Set berlabel hanya memuat origin BMKG di sekitar origin USGS, jadi angka geser adalah batas bawah.

| Ambang                                   | BMKG–BMKG | USGS–USGS | Geser ±2–10 mnt /1.000 |
| ---------------------------------------- | --------- | --------- | ---------------------- |
| bawaan (ADR 0008): 30s, 100 km, 1,0      | 0         | 0         | 0,00                   |
| PRD awal: 1m30s, 75 km, 0,7              | 0         | 0         | 0,00                   |
| jarak 120 km: 30s, 120 km, 1,0           | 0         | 0         | 0,00                   |
| jarak 150 km: 30s, 150 km, 1,0           | 0         | 0         | 0,00                   |
| waktu 20 dtk: 20s, 100 km, 1,0           | 0         | 0         | 0,00                   |
| waktu 60 dtk: 1m0s, 100 km, 1,0          | 0         | 0         | 0,00                   |
| magnitudo 0,7: 30s, 100 km, 0,7          | 0         | 0         | 0,00                   |
| magnitudo 1,3: 30s, 100 km, 1,3          | 0         | 0         | 0,00                   |
| 120 km + magnitudo 1,3: 30s, 120 km, 1,3 | 0         | 0         | 0,00                   |

### Menurut tipe magnitudo BMKG (ambang bawaan (ADR 0008))

| Tipe            | Positif | Recall pasangan | Selisih M p50 | p95  | p99  | maks |
| --------------- | ------- | --------------- | ------------- | ---- | ---- | ---- |
| (tidak disebut) | 10      | 100,0%          | 0,10          | 0,40 | 0,40 | 0,40 |
