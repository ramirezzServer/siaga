# Kalibrasi titik pantau sungai

Dibuat `make river-snap` pada 2026-09-30 11:35 UTC dari `docs/calibration/titik-sungai-32.csv`. Jangan diedit manual; ubah file sumber lalu jalankan ulang.

Setiap titik pantau PRD adalah nama lokasi. Alat ini merata-rata debit harian reanalisis GloFAS v4 (Open-Meteo `models=consolidated_v4`, grid 0,05°) periode acuan 2020-07-01 sampai 2022-06-30 (730 hari) untuk sel di sekitar koordinat perkiraan. Debit rata-rata periode acuan hampir sebanding dengan luas daerah tangkapan di hulu sel, jadi pilihan sel tidak berubah dengan hujan beberapa hari terakhir; reanalisis juga tetap, jadi jalan ulang memberi hasil yang sama (ADR 0019). Di dalam radius pencarian, sel alur utama adalah sel yang debit rata-ratanya paling sedikit 30% dari debit terbesar; dari sel itu dipilih yang paling dekat dengan koordinat perkiraan (memilih debit terbesar saja akan selalu menggeser titik ke hilir). Sel yang hari berisinya kurang dari 90% periode acuan tidak dipakai. Aturan ini bisa salah saat sungai lain yang lebih besar ada di dalam radius, jadi pilihan yang meragukan ditandai untuk diperiksa di peta (PRD, bagian Batas kemampuan data).

Tanda: `tepi` sel terpilih di tepi radius, `kecil` debit rata-rata < 0,5 m³/s, `hilir-lebih-kecil` debit lebih kecil dari titik hulu sebelumnya di sungai yang sama (wajar di hilir bendungan), `sel-ganda` sel dipakai titik lain, `manual` titik sudah diverifikasi (radius 0).

Setelah memeriksa satu titik di peta OSM, tulis koordinat sel yang benar di file sumber dengan radius 0 dan catatan asal verifikasinya.

| Titik                                        | Sungai       | Perkiraan         | Radius | Sel terpilih      | Geser (km) | Debit rata-rata periode acuan (m³/s) | Tanda    |
| -------------------------------------------- | ------------ | ----------------- | ------ | ----------------- | ---------- | ------------------------------------ | -------- |
| Majalaya (`citarum-majalaya`)                | Citarum      | -7.0750, 107.7250 | 0,00°  | -7.0750, 107.7250 | 0,0        | 18,21                                | `manual` |
| Dayeuhkolot (`citarum-dayeuhkolot`)          | Citarum      | -6.9870, 107.6250 | 0,05°  | -6.9750, 107.6250 | 1,3        | 64,25                                |          |
| Nanjung (`citarum-nanjung`)                  | Citarum      | -6.9750, 107.5250 | 0,00°  | -6.9750, 107.5250 | 0,0        | 131,52                               | `manual` |
| Hilir Jatiluhur (`citarum-hilir-jatiluhur`)  | Citarum      | -6.4500, 107.3600 | 0,10°  | -6.4250, 107.3250 | 4,8        | 278,80                               |          |
| Karawang (`citarum-karawang`)                | Citarum      | -6.2950, 107.3000 | 0,10°  | -6.3250, 107.2750 | 4,3        | 283,49                               |          |
| Muara (`citarum-muara`)                      | Citarum      | -5.9500, 107.0400 | 0,10°  | -5.9750, 107.0250 | 3,2        | 322,05                               |          |
| Garut kota (`cimanuk-garut`)                 | Cimanuk      | -7.2100, 107.9000 | 0,10°  | -7.1750, 107.9250 | 4,8        | 47,61                                |          |
| Hulu Jatigede (`cimanuk-hulu-jatigede`)      | Cimanuk      | -7.0200, 108.0300 | 0,10°  | -6.9750, 108.0250 | 5,0        | 91,68                                |          |
| Hilir Jatigede (`cimanuk-hilir-jatigede`)    | Cimanuk      | -6.7800, 108.1300 | 0,10°  | -6.7750, 108.0750 | 6,1        | 137,29                               |          |
| Jatibarang (`cimanuk-jatibarang`)            | Cimanuk      | -6.4700, 108.3100 | 0,10°  | -6.4750, 108.2750 | 3,9        | 203,81                               |          |
| Muara (`cimanuk-muara`)                      | Cimanuk      | -6.3750, 108.2250 | 0,00°  | -6.3750, 108.2250 | 0,0        | 204,08                               | `manual` |
| Cileungsi (`bekasi-cileungsi`)               | Cileungsi    | -6.4250, 107.0250 | 0,00°  | -6.4250, 107.0250 | 0,0        | 6,66                                 | `manual` |
| Cikeas (`bekasi-cikeas`)                     | Cikeas       | -6.4250, 106.8750 | 0,00°  | -6.4250, 106.8750 | 0,0        | 18,05                                | `manual` |
| Pertemuan Cileungsi-Cikeas (`bekasi-p2c`)    | Kali Bekasi  | -6.3250, 106.9250 | 0,00°  | -6.3250, 106.9250 | 0,0        | 24,41                                | `manual` |
| Bekasi kota (`bekasi-kota`)                  | Kali Bekasi  | -6.1250, 106.9750 | 0,00°  | -6.1250, 106.9750 | 0,0        | 53,93                                | `manual` |
| Hulu Salak (`cisadane-hulu`)                 | Cisadane     | -6.7000, 106.7600 | 0,10°  | -6.7250, 106.7750 | 3,2        | 7,96                                 |          |
| Kota Bogor (`cisadane-bogor`)                | Cisadane     | -6.5750, 106.7250 | 0,00°  | -6.5750, 106.7250 | 0,0        | 19,42                                | `manual` |
| Batas Banten (`cisadane-batas-banten`)       | Cisadane     | -6.3900, 106.6600 | 0,10°  | -6.3750, 106.6250 | 4,2        | 61,24                                |          |
| Katulampa (`ciliwung-katulampa`)             | Ciliwung     | -6.6750, 106.8250 | 0,00°  | -6.6750, 106.8250 | 0,0        | 11,34                                | `manual` |
| Kota Bogor (`ciliwung-bogor`)                | Ciliwung     | -6.5750, 106.7750 | 0,00°  | -6.5750, 106.7750 | 0,0        | 15,73                                | `manual` |
| Depok (`ciliwung-depok`)                     | Ciliwung     | -6.3750, 106.7750 | 0,00°  | -6.3750, 106.7750 | 0,0        | 22,52                                | `manual` |
| Kuningan (`cisanggarung-kuningan`)           | Cisanggarung | -6.9750, 108.6250 | 0,00°  | -6.9750, 108.6250 | 0,0        | 34,92                                | `manual` |
| Cirebon timur (`cisanggarung-cirebon-timur`) | Cisanggarung | -6.9200, 108.7400 | 0,10°  | -6.9250, 108.7250 | 1,7        | 48,74                                |          |
| Muara (`cisanggarung-muara`)                 | Cisanggarung | -6.8000, 108.8300 | 0,10°  | -6.8250, 108.7750 | 6,7        | 50,13                                |          |
| Subang tengah (`cipunagara-subang`)          | Cipunagara   | -6.5200, 107.8500 | 0,10°  | -6.5250, 107.8750 | 2,8        | 36,19                                |          |
| Pamanukan (`cipunagara-pamanukan`)           | Cipunagara   | -6.2800, 107.8200 | 0,10°  | -6.2750, 107.7750 | 5,0        | 55,46                                |          |
| Hilir (`cilamaya-hilir`)                     | Cilamaya     | -6.2250, 107.5750 | 0,00°  | -6.2250, 107.5750 | 0,0        | 8,03                                 | `manual` |
| Tasikmalaya (`citanduy-tasikmalaya`)         | Citanduy     | -7.3250, 108.2250 | 0,00°  | -7.3250, 108.2250 | 0,0        | 44,88                                | `manual` |
| Ciamis (`citanduy-ciamis`)                   | Citanduy     | -7.3300, 108.3700 | 0,10°  | -7.3750, 108.3750 | 5,0        | 64,10                                |          |
| Kota Banjar (`citanduy-banjar`)              | Citanduy     | -7.3700, 108.5300 | 0,10°  | -7.3250, 108.5250 | 5,0        | 143,62                               |          |
| Muara (`citanduy-muara`)                     | Citanduy     | -7.6300, 108.8000 | 0,10°  | -7.6750, 108.7750 | 5,7        | 260,74                               |          |
| Tengah (`cimandiri-tengah`)                  | Cimandiri    | -7.0000, 106.7200 | 0,10°  | -7.0250, 106.7250 | 2,8        | 32,10                                |          |
| Palabuhanratu (`cimandiri-palabuhanratu`)    | Cimandiri    | -6.9900, 106.5500 | 0,10°  | -7.0250, 106.5250 | 4,8        | 101,17                               |          |
| Tengah (`ciwulan-tengah`)                    | Ciwulan      | -7.4500, 108.1000 | 0,10°  | -7.4750, 108.1250 | 3,9        | 61,15                                |          |
| Muara (`ciwulan-muara`)                      | Ciwulan      | -7.7400, 108.0300 | 0,10°  | -7.7750, 108.0750 | 6,3        | 89,75                                |          |
| Muara (`cilaki-muara`)                       | Cilaki       | -7.6600, 107.6300 | 0,10°  | -7.6750, 107.6750 | 5,2        | 11,29                                |          |
| Muara (`cikaso-muara`)                       | Cikaso       | -7.4200, 106.8800 | 0,10°  | -7.3750, 106.8250 | 7,9        | 97,08                                |          |
| Muara (`cibuni-muara`)                       | Cibuni       | -7.4400, 107.0200 | 0,10°  | -7.4750, 107.0750 | 7,2        | 28,17                                |          |
