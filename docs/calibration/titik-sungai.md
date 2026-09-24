# Kalibrasi titik pantau sungai

Dibuat `make river-snap` pada 2026-09-24 13:05 UTC dari `docs/calibration/titik-sungai-32.csv`. Jangan diedit manual; ubah file sumber lalu jalankan ulang.

Setiap titik pantau PRD adalah nama lokasi. Alat ini meminta debit GloFAS v4 (Open-Meteo, grid 0,05°) harian 7 hari lalu sampai 7 hari ke depan untuk sel di sekitar koordinat perkiraan. Di dalam radius pencarian, sel alur utama adalah sel yang debit rata-ratanya paling sedikit 30% dari debit terbesar; dari sel itu dipilih yang paling dekat dengan koordinat perkiraan (memilih debit terbesar saja akan selalu menggeser titik ke hilir). Aturan ini bisa salah saat sungai lain yang lebih besar ada di dalam radius atau saat debit musim kemarau sangat kecil, jadi pilihan yang meragukan ditandai untuk diperiksa di peta (PRD, bagian Batas kemampuan data).

Tanda: `tepi` sel terpilih di tepi radius, `kecil` debit rata-rata < 0,5 m³/s, `hilir-lebih-kecil` debit lebih kecil dari titik hulu sebelumnya di sungai yang sama (wajar di hilir bendungan), `sel-ganda` sel dipakai titik lain, `manual` titik sudah diverifikasi (radius 0).

Setelah memeriksa satu titik di peta OSM, tulis koordinat sel yang benar di file sumber dengan radius 0 dan catatan asal verifikasinya.

| Titik                                        | Sungai       | Perkiraan         | Radius | Sel terpilih      | Geser (km) | Debit rata-rata (m³/s) | Tanda                       |
| -------------------------------------------- | ------------ | ----------------- | ------ | ----------------- | ---------- | ---------------------- | --------------------------- |
| Majalaya (`citarum-majalaya`)                | Citarum      | -7.0450, 107.7550 | 0,05°  | -7.0750, 107.7250 | 4,7        | 0,71                   | `tepi`                      |
| Dayeuhkolot (`citarum-dayeuhkolot`)          | Citarum      | -6.9870, 107.6250 | 0,05°  | -6.9750, 107.6250 | 1,3        | 3,37                   |                             |
| Nanjung (`citarum-nanjung`)                  | Citarum      | -6.9410, 107.5370 | 0,05°  | -6.9750, 107.5250 | 4,0        | 7,81                   | `tepi`                      |
| Hilir Jatiluhur (`citarum-hilir-jatiluhur`)  | Citarum      | -6.4500, 107.3600 | 0,10°  | -6.4750, 107.3250 | 4,8        | 73,15                  |                             |
| Karawang (`citarum-karawang`)                | Citarum      | -6.2950, 107.3000 | 0,10°  | -6.3250, 107.2750 | 4,3        | 72,35                  | `hilir-lebih-kecil`         |
| Muara (`citarum-muara`)                      | Citarum      | -5.9500, 107.0400 | 0,10°  | -5.9750, 107.0250 | 3,2        | 67,83                  | `hilir-lebih-kecil`         |
| Garut kota (`cimanuk-garut`)                 | Cimanuk      | -7.2100, 107.9000 | 0,10°  | -7.1750, 107.9250 | 4,8        | 1,27                   |                             |
| Hulu Jatigede (`cimanuk-hulu-jatigede`)      | Cimanuk      | -7.0200, 108.0300 | 0,10°  | -6.9750, 108.0250 | 5,0        | 1,40                   |                             |
| Hilir Jatigede (`cimanuk-hilir-jatigede`)    | Cimanuk      | -6.7800, 108.1300 | 0,10°  | -6.7750, 108.0750 | 6,1        | 0,64                   | `hilir-lebih-kecil`         |
| Jatibarang (`cimanuk-jatibarang`)            | Cimanuk      | -6.4700, 108.3100 | 0,10°  | -6.4250, 108.2750 | 6,3        | 0,37                   | `kecil` `hilir-lebih-kecil` |
| Muara (`cimanuk-muara`)                      | Cimanuk      | -6.2800, 108.3400 | 0,10°  | -6.3250, 108.3250 | 5,3        | 0,40                   | `kecil`                     |
| Cileungsi (`bekasi-cileungsi`)               | Cileungsi    | -6.4000, 106.9700 | 0,05°  | -6.4250, 106.9250 | 5,7        | 0,56                   | `tepi`                      |
| Cikeas (`bekasi-cikeas`)                     | Cikeas       | -6.4000, 106.9200 | 0,05°  | -6.4250, 106.8750 | 5,7        | 3,62                   | `tepi` `sel-ganda`          |
| Pertemuan Cileungsi-Cikeas (`bekasi-p2c`)    | Kali Bekasi  | -6.3100, 106.9700 | 0,10°  | -6.2750, 106.9750 | 3,9        | 4,56                   |                             |
| Bekasi kota (`bekasi-kota`)                  | Kali Bekasi  | -6.2350, 106.9900 | 0,10°  | -6.2250, 106.9750 | 2,0        | 7,31                   |                             |
| Hulu Salak (`cisadane-hulu`)                 | Cisadane     | -6.7000, 106.7600 | 0,10°  | -6.6750, 106.7750 | 3,2        | 1,12                   |                             |
| Kota Bogor (`cisadane-bogor`)                | Cisadane     | -6.5900, 106.7700 | 0,05°  | -6.5750, 106.7750 | 1,8        | 4,27                   |                             |
| Batas Banten (`cisadane-batas-banten`)       | Cisadane     | -6.3900, 106.6600 | 0,10°  | -6.4250, 106.6250 | 5,5        | 5,07                   |                             |
| Katulampa (`ciliwung-katulampa`)             | Ciliwung     | -6.6330, 106.8370 | 0,05°  | -6.6750, 106.8250 | 4,9        | 3,31                   | `tepi`                      |
| Kota Bogor (`ciliwung-bogor`)                | Ciliwung     | -6.5800, 106.8100 | 0,05°  | -6.5750, 106.8250 | 1,7        | 1,49                   | `hilir-lebih-kecil`         |
| Depok (`ciliwung-depok`)                     | Ciliwung     | -6.4000, 106.8300 | 0,10°  | -6.4250, 106.8750 | 5,7        | 3,62                   | `sel-ganda`                 |
| Kuningan (`cisanggarung-kuningan`)           | Cisanggarung | -7.0500, 108.7000 | 0,10°  | -6.9750, 108.6250 | 11,8       | 1,09                   | `tepi`                      |
| Cirebon timur (`cisanggarung-cirebon-timur`) | Cisanggarung | -6.9200, 108.7400 | 0,10°  | -6.9250, 108.7250 | 1,7        | 1,04                   | `hilir-lebih-kecil`         |
| Muara (`cisanggarung-muara`)                 | Cisanggarung | -6.8000, 108.8300 | 0,10°  | -6.8250, 108.8250 | 2,8        | 0,79                   | `hilir-lebih-kecil`         |
| Subang tengah (`cipunagara-subang`)          | Cipunagara   | -6.5200, 107.8500 | 0,10°  | -6.5250, 107.9250 | 8,3        | 0,41                   | `tepi` `kecil`              |
| Pamanukan (`cipunagara-pamanukan`)           | Cipunagara   | -6.2800, 107.8200 | 0,10°  | -6.3250, 107.7750 | 7,1        | 0,93                   |                             |
| Hilir (`cilamaya-hilir`)                     | Cilamaya     | -6.2500, 107.5700 | 0,10°  | -6.2750, 107.5750 | 2,8        | 1,38                   |                             |
| Tasikmalaya (`citanduy-tasikmalaya`)         | Citanduy     | -7.2200, 108.2800 | 0,10°  | -7.2750, 108.1750 | 13,1       | 3,57                   | `tepi`                      |
| Ciamis (`citanduy-ciamis`)                   | Citanduy     | -7.3300, 108.3700 | 0,10°  | -7.3750, 108.3750 | 5,0        | 5,49                   |                             |
| Kota Banjar (`citanduy-banjar`)              | Citanduy     | -7.3700, 108.5300 | 0,10°  | -7.3250, 108.5250 | 5,0        | 9,09                   |                             |
| Muara (`citanduy-muara`)                     | Citanduy     | -7.6300, 108.8000 | 0,10°  | -7.6750, 108.7750 | 5,7        | 10,73                  |                             |
| Tengah (`cimandiri-tengah`)                  | Cimandiri    | -7.0000, 106.7200 | 0,10°  | -7.0250, 106.7250 | 2,8        | 2,90                   |                             |
| Palabuhanratu (`cimandiri-palabuhanratu`)    | Cimandiri    | -6.9900, 106.5500 | 0,10°  | -7.0250, 106.5250 | 4,8        | 6,36                   |                             |
| Tengah (`ciwulan-tengah`)                    | Ciwulan      | -7.4500, 108.1000 | 0,10°  | -7.4750, 108.1250 | 3,9        | 4,12                   |                             |
| Muara (`ciwulan-muara`)                      | Ciwulan      | -7.7400, 108.0300 | 0,10°  | -7.7250, 108.0750 | 5,2        | 5,34                   |                             |
| Muara (`cilaki-muara`)                       | Cilaki       | -7.6600, 107.6300 | 0,10°  | -7.6750, 107.6750 | 5,2        | 1,03                   |                             |
| Muara (`cikaso-muara`)                       | Cikaso       | -7.4200, 106.8800 | 0,10°  | -7.3750, 106.8250 | 7,9        | 5,06                   |                             |
| Muara (`cibuni-muara`)                       | Cibuni       | -7.4400, 107.0200 | 0,10°  | -7.4250, 107.0750 | 6,3        | 2,74                   |                             |

Titik bertanda (selain `manual`) belum boleh dipakai untuk ambang banjir sebelum diverifikasi.
