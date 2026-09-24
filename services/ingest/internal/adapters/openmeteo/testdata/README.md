# Payload Open-Meteo asli

Direkam 2026-09-24 sekitar 12.40 UTC dengan `ambil-sampel-1d.sh` (fase 1d), jendela 2026-09-24 sampai 2026-09-27. Data Open-Meteo berlisensi CC BY 4.0.

| File | Isi |
| --- | --- |
| `cuaca-1titik.json` | Prakiraan cuaca per jam satu titik (-7,0; 107,5), `cell_selection=nearest`, respons objek tunggal |
| `cuaca-grid-4.json` | Empat lokasi pertama dari request grid 80 titik (lintang -7,75; bujur 106,5–107,25) |
| `udara-grid-4.json` | Sama untuk kualitas udara CAMS global |
| `sungai-3.json` | Debit GloFAS harian (4 hari lalu + 10 hari ke depan, statistik ensemble) untuk titik perkiraan Majalaya, Dayeuhkolot, Nanjung |
| `galat-lintang.json`, `galat-variabel.json` | Isi respons HTTP 400 |
