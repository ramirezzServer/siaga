# Kalibrasi radius dirasakan gempa

Dihasilkan `make felt-radius` (ADR 0026); jangan diedit manual. Rumus: R = 10^(a·M − b) km (jarak hiposenter), R_permukaan = √(R² − h²).

## Bahan

- BMKG: 67 gempa dirasakan dari arsip `gempadirasakan`, 141 penyebutan wilayah (141 cocok, 138 berskala ≥ II), 0 bagian tidak terbaca; 65 gempa dengan wilayah dirasakan yang cocok dipakai untuk kalibrasi.
- Gazetir: 91599 nama Kemendagri (cahyadsn/wilayah `db/wilayah.sql`), 7837 batas provinsi/kab/kota/kecamatan (cahyadsn/wilayah_boundaries `db/{prov,kab,kec}`), 7 alias (`gazetir-alias.csv`). Desa/kelurahan memakai batas kecamatan induknya, jadi jaraknya batas bawah.
- DYFI USGS: 2027 kejadian, 10033 sel (0 mencurigakan, tidak dipakai). Sel CDI ≥ 2 dirasakan, CDI < 2 tidak dirasakan.

Cara cocok nama: nama 131, tanpa-spasi 1, kota-sebagai-kecamatan 1, nama+induk 3, alias 5.

## Rumus terpilih

Regresi kuantil τ = 0,90 pada log10 jarak hiposenter wilayah dirasakan terjauh per gempa BMKG (65 gempa), a dicari di [0,05; 1,00] langkah 0,005: **a = 0,350, b = -0,456**. Rumus layanan (`quake.DefaultPolicy`): **a = 0,35, b = -0,46**.

| Magnitudo                    | M3    | M4    | M5      | M6      | M7       |
| ---------------------------- | ----- | ----- | ------- | ------- | -------- |
| PRD (km)                     | 3     | 10    | 32      | 100     | 316      |
| Hasil fit (km)               | 32    | 72    | 161     | 360     | 806      |
| Layanan (km)                 | 32    | 72    | 162     | 363     | 813      |
| Bootstrap 5–95% (500 sampel) | 22–43 | 61–85 | 148–174 | 313–466 | 621–1272 |

Bootstrap kemiringan a: 0,295–0,425.

## Pembanding rumus

BMKG kejadian: bagian gempa yang wilayah dirasakan terjauhnya di dalam R. BMKG penyebutan: bagian penyebutan dirasakan di dalam R (bobot sama per gempa). DYFI: bobot sama per kejadian; "tidak dirasakan di luar" adalah satu-satunya ukuran radius yang kebesaran.

| Rumus        | a     | b      | R M5 (km) | BMKG kejadian | BMKG penyebutan | DYFI dirasakan di dalam | DYFI M ≥ 6 dirasakan di dalam | DYFI tidak dirasakan di luar |
| ------------ | ----- | ------ | --------- | ------------- | --------------- | ----------------------- | ----------------------------- | ---------------------------- |
| PRD awal     | 0,500 | 1,000  | 32        | 20,0%         | 24,7%           | 8,5%                    | 25,6%                         | 97,9%                        |
| Fit τ = 0,90 | 0,350 | -0,456 | 161       | 90,8%         | 93,8%           | 57,4%                   | 73,6%                         | 73,4%                        |
| Layanan      | 0,350 | -0,460 | 162       | 90,8%         | 94,0%           | 58,0%                   | 74,1%                         | 73,3%                        |
| Fit τ = 0,50 | 0,380 | 0,099  | 63        | 50,8%         | 59,2%           | 20,1%                   | 36,6%                         | 96,8%                        |
| Fit τ = 0,75 | 0,420 | -0,054 | 143       | 75,4%         | 85,6%           | 53,3%                   | 79,4%                         | 75,3%                        |

## Per magnitudo (rumus layanan)

| M   | Gempa BMKG | Terjauh di dalam R | Median terjauh (km) | Kejadian DYFI | Sel dirasakan di dalam R | Median sel dirasakan (km) | Sel tidak dirasakan di dalam R |
| --- | ---------- | ------------------ | ------------------- | ------------- | ------------------------ | ------------------------- | ------------------------------ |
| < 3 | 11         | 10/11 (90,9%)      | 8                   | 1             | –                        | –                         | 0/1 (0,0%)                     |
| 3–4 | 15         | 13/15 (86,7%)      | 15                  | 10            | 12/16 (75,0%)            | 27                        | 0/1 (0,0%)                     |
| 4–5 | 33         | 31/33 (93,9%)      | 31                  | 766           | 852/1648 (51,7%)         | 116                       | 22/122 (18,0%)                 |
| 5–6 | 6          | 5/6 (83,3%)        | 157                 | 951           | 2701/3793 (71,2%)        | 153                       | 36/157 (22,9%)                 |
| 6–7 | 0          | –                  | –                   | 249           | 2353/2897 (81,2%)        | 208                       | 48/117 (41,0%)                 |
| ≥ 7 | 0          | –                  | –                   | 50            | 1088/1203 (90,4%)        | 475                       | 43/78 (55,1%)                  |

## Gempa BMKG dengan wilayah dirasakan di luar R (rumus layanan)

| Gempa (UTC)      | M   | Kedalaman (km) | Wilayah terjauh | MMI    | Kode     | Jarak tepi (km) | Jarak hiposenter (km) | R (km) | R permukaan (km) |
| ---------------- | --- | -------------- | --------------- | ------ | -------- | --------------- | --------------------- | ------ | ---------------- |
| 2026-10-02 00:46 | 3,8 | 86             | Denpasar        | II     | 51.71    | 31              | 91                    | 62     | 0                |
| 2026-09-30 02:35 | 4,1 | 5              | Sigli           | II     | 11.07.09 | 107             | 107                   | 79     | 78               |
| 2026-11-06 17:38 | 3,3 | 5              | Bungku Morowali | II–III | 72.06.05 | 52              | 52                    | 41     | 41               |
| 2026-09-25 13:47 | 4,7 | 10             | Rangkasbitung   | II–III | 36.02.14 | 134             | 135                   | 127    | 127              |
| 2026-11-02 08:13 | 5,3 | 148            | Ternate         | II–III | 82.71    | 149             | 210                   | 207    | 144              |
| 2026-10-16 14:22 | 2,3 | 18             | Leuwiliang      | II     | 32.01.14 | 5               | 19                    | 18     | 4                |

## Nama yang tidak cocok

Tidak ada.

## Sumber

- BMKG, gempa dirasakan (`gempadirasakan.json`), https://data.bmkg.go.id/gempabumi/
- U.S. Geological Survey, Did You Feel It? (domain publik), https://earthquake.usgs.gov/data/dyfi/
- Nama dan batas wilayah: cahyadsn/wilayah dan cahyadsn/wilayah_boundaries (MIT), mengacu Kepmendagri No. 300.2.2-2430 Tahun 2025
