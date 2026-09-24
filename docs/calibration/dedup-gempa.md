# Kalibrasi deduplikasi gempa BMKG–USGS

Dihasilkan otomatis oleh `make calibrate-dedup`. Jangan diedit manual; jalankan ulang perintahnya.

## Data

|                                              |                                                                             |
| -------------------------------------------- | --------------------------------------------------------------------------- |
| Wilayah                                      | lintang -9,00..-5,50, bujur 105,00..109,50 (Jawa Barat dan laut sekitarnya) |
| Periode                                      | 2008-11-01 s.d. 2023-01-27 (UTC)                                            |
| Gempa BMKG                                   | 5526                                                                        |
| Gempa USGS                                   | 819                                                                         |
| USGS dengan kandidat BMKG (≤ 5m0s, ≤ 300 km) | 673                                                                         |

Sumber BMKG: `katalog_gempa.csv` (sha256 fb8afdcbc824). Sumber USGS: `query_2008_2015.csv` (sha256 33fcd3a57bdc), `query_2016_2023.csv` (sha256 f67c4bd8abb1).

## Selisih pasangan terbaik BMKG–USGS

| Selisih              | p50  | p90  | p95  | p99  | maks  |
| -------------------- | ---- | ---- | ---- | ---- | ----- |
| Waktu (detik)        | 1,5  | 3,8  | 4,9  | 10,6 | 103,1 |
| Jarak episenter (km) | 24,5 | 52,7 | 61,4 | 99,4 | 264,6 |
| Magnitudo            | 0,20 | 0,50 | 0,60 | 0,80 | 1,20  |

## Ambang

Recall dihitung atas gempa USGS yang punya kandidat BMKG. Gabungan salah adalah kecocokan per 1.000 gempa USGS setelah waktunya digeser: ±1–13 jam mengukur kebetulan murni, ±2–10 menit mengukur gempa susulan yang berdekatan (paling berisiko). Pasangan sumber sama adalah dua gempa berbeda di satu katalog yang lolos ambang.

| Ambang (waktu, jarak, magnitudo) | Recall          | Gabungan salah /1.000 (jam) | Gabungan salah /1.000 (menit) | Pasangan BMKG–BMKG | Pasangan USGS–USGS |
| -------------------------------- | --------------- | --------------------------- | ----------------------------- | ------------------ | ------------------ |
| 1m30s, 75 km, 0,7                | 96,3% (648/673) | 0,31                        | 4,03                          | 43                 | 2                  |
| 30s, 100 km, 1,0                 | 98,5% (663/673) | 0,20                        | 1,71                          | 17                 | 1                  |
| 30s, 75 km, 0,7                  | 96,1% (647/673) | 0,10                        | 1,47                          | 17                 | 0                  |
| 30s, 100 km, 0,7                 | 97,6% (657/673) | 0,10                        | 1,47                          | 17                 | 1                  |
| 20s, 100 km, 1,0                 | 98,4% (662/673) | 0,10                        | 1,10                          | 16                 | 0                  |
| 1m0s, 100 km, 1,0                | 98,8% (665/673) | 0,31                        | 4,15                          | 30                 | 3                  |
| 30s, 120 km, 1,0                 | 98,7% (664/673) | 0,31                        | 1,71                          | 17                 | 1                  |
| 10s, 50 km, 1,0                  | 87,5% (589/673) | 0,00                        | 0,61                          | 13                 | 0                  |

## Contoh gempa yang terlewat ambang 1m30s, 75 km, 0,7

| USGS       | Waktu (UTC)         | M   | Selisih waktu (dtk) | Jarak (km) | Selisih M |
| ---------- | ------------------- | --- | ------------------- | ---------- | --------- |
| usp000gnrm | 2008-11-15 13:00:48 | 4,5 | 1,5                 | 75,0       | 0,70      |
| usp000gs98 | 2009-01-06 14:36:11 | 4,1 | 1,8                 | 117,1      | 0,60      |
| usp000gsq3 | 2009-01-16 20:40:17 | 4,3 | 11,8                | 134,5      | 0,30      |
| usp000j78x | 2011-08-30 09:56:01 | 4,2 | 3,4                 | 80,0       | 0,50      |
| usc000lvpm | 2013-12-19 22:56:54 | 4,3 | 37,0                | 99,4       | 0,40      |
| usc000rad7 | 2014-05-14 16:52:10 | 4,7 | 2,0                 | 41,5       | 0,80      |
| usc000thsh | 2015-01-09 19:01:16 | 4,7 | 1,0                 | 9,5        | 0,80      |
| usb000tiyh | 2015-01-18 02:53:33 | 4,1 | 0,3                 | 65,9       | 0,80      |
| usc000tnpy | 2015-02-02 13:41:10 | 4,1 | 10,6                | 57,9       | 1,10      |
| us200041s2 | 2015-11-03 21:43:41 | 4,7 | 1,6                 | 27,8       | 1,20      |
| us10004wbu | 2016-03-05 15:18:27 | 4,3 | 6,9                 | 89,8       | 0,00      |
| us100078ry | 2016-11-09 04:05:41 | 4,5 | 2,7                 | 78,0       | 0,10      |
| us10008gm9 | 2017-04-05 08:20:49 | 4,0 | 0,5                 | 95,1       | 0,70      |
| us1000e7zr | 2018-05-15 16:41:22 | 4,2 | 7,4                 | 84,2       | 0,10      |
| us70003yhr | 2019-06-04 16:53:11 | 4,4 | 4,4                 | 132,8      | 1,10      |
