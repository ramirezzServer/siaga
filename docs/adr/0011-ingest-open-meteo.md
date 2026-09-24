# 0011. Ingest Open-Meteo: grid cuaca, kualitas udara CAMS, dan debit sungai

Tanggal: 2026-09-24 · Status: diterima

## Konteks

Fase 1d menambah tiga API Open-Meteo tanpa key (non-komersial, CC BY 4.0): Forecast (cuaca per jam), Air Quality (model CAMS), dan Flood (GloFAS). Dokumen arsitektur menetapkan grid 0,25° se-Jawa Barat tiap jam untuk cuaca dan udara, 38 titik pantau sungai tiap 6 jam, dan kuota 600/menit, 5.000/jam, 10.000/hari.

Rekaman asli 2026-09-24 (`services/ingest/internal/adapters/openmeteo/testdata`, `ambil-sampel-1d.sh`) menunjukkan:

- Satu request bisa memuat banyak lokasi (`latitude=a,b,…`); jawabannya daftar objek dengan urutan yang sama, atau satu objek untuk satu lokasi.
- **Kuota dihitung per lokasi, bukan per request.** Setelah enam request pencarian sel sungai berisi 98 lokasi (588 lokasi) dalam satu menit, request berikutnya ditolak `HTTP 429 Minutely API request limit exceeded` tanpa `Retry-After`. Request dengan lebih dari 10 variabel atau lebih dari 14 hari dihitung pecahan tambahan.
- Satuan tertulis di `hourly_units`/`daily_units` (`°C`, `μg/m³`, `m³/s`, `wmo code`); tidak ada nilai kosong di 80 titik grid.
- Sel yang dijawab bukan simpul 0,25°: cuaca `best_match` memakai sel sekitar 0,03° dari titik yang diminta, CAMS sekitar 0,05°, GloFAS pusat sel 0,05°.
- Dua request berjarak beberapa menit menghasilkan isi yang sama persis.
- Debit GloFAS musim kemarau sangat kecil (Citarum di Dayeuhkolot ±3 m³/s), dan sel di sekitar nama lokasi sering milik sungai lain.

## Keputusan

1. **Satu konektor per API, satu request untuk semua titik, satu event per titik.** `openmeteo-cuaca` → `raw.forecast.openmeteo` (`GridWeatherForecast`), `openmeteo-udara` → `raw.aq.openmeteo` (`AirQualityForecast`), `openmeteo-sungai` → `raw.flood.openmeteo` (`RiverDischargeForecast`). Ketiganya memakai use case `poll` yang sama dengan feed BMKG/USGS: payload diarsip utuh, ID record = ID titik, hanya titik yang isinya berubah yang terbit.
2. **Jendela waktu tetap per hari UTC.** Cuaca dan udara diminta `start_date` = hari ini sampai `end_date` = +3 hari (96 jam, `timezone=GMT`, `timeformat=unixtime`), bukan `forecast_hours` bergulir. Isi hanya berubah saat model diperbarui atau tanggal berganti, jadi polling tiap jam tidak menerbitkan ulang data yang sama. Debit diminta 4 hari lalu + 10 hari ke depan (14 hari, satu "API call" per titik) dengan statistik ensemble.
3. **Variabel dan satuan dikunci di domain.** `domain/series` menetapkan variabel, satuan, dan batas nilai per jenis data (≤ 10 variabel supaya satu lokasi = satu panggilan). Satuan yang berbeda atau variabel yang hilang menolak seluruh payload (`ErrStructure`); nilai di luar batas, sel yang terlalu jauh dari titik yang diminta, atau langkah di luar jendela menolak titik itu saja. Langkah yang semua nilainya kosong dibuang. `cell_selection=nearest` untuk grid, karena yang dipantau adalah model, bukan titik darat terdekat.
4. **Grid dari data wilayah, disematkan ke binary.** `make grid-list` menjalankan `import-regions -dry-run -grid-out`: simpul 0,25° yang selnya memuat paling sedikit satu titik batas kelurahan/desa provinsi. Hasilnya 70 simpul untuk Jawa Barat (dokumen arsitektur memperkirakan ±120), di `sitelist/data/grid025_32.txt`. Ingest tetap tidak membaca schema geo-processor.
5. **Titik sungai dipilih dengan aturan yang bisa diulang.** `docs/calibration/titik-sungai-32.csv` berisi 38 titik PRD dengan koordinat perkiraan dan radius pencarian. `make river-snap` meminta debit GloFAS untuk sel di sekitar tiap titik, lalu memilih sel alur utama terdekat: sel dengan debit rata-rata ≥ 30% debit terbesar di dalam radius, yang paling dekat dengan koordinat perkiraan. Memilih debit terbesar saja selalu menggeser titik ke hilir. Hasilnya `sitelist/data/rivers_32.csv` dan laporan `docs/calibration/titik-sungai.md` yang menandai pilihan meragukan (`tepi`, `kecil`, `hilir-lebih-kecil`, `sel-ganda`). Titik yang sudah diperiksa di peta ditulis dengan radius 0.
6. **Anggaran Open-Meteo.** Anggaran request `openmeteo` 10/menit (burst 3) mencegah putaran galat yang cepat; kuota per lokasi dijaga oleh jumlah titik: 70 × 24 × 2 + 38 × 4 = 3.512 lokasi/hari (35% kuota), paling banyak 178 lokasi dalam satu menit. `TestOpenMeteoQuota` gagal bila penambahan titik membuat polling rutin melewati 6.000 lokasi/hari. `river-snap` membatasi dirinya 400 lokasi/menit.

## Konsekuensi

- Sisa kuota harian (±6.500) cukup untuk `river-snap` (±800 lokasi) dan backfill historis bertahap, tetapi reanalisis GloFAS 1984–2022 untuk ambang persentil (±1.000 panggilan per titik) harus dicicil beberapa hari.
- Satu request gagal berarti semua titik jenis itu tertunda sampai polling berikutnya (runner memakai backoff). Karena jendela tetap per hari, data jam yang terlewat tetap terambil.
- `hazard.flood.*` belum diterbitkan: ambang banjir butuh titik sungai yang sudah diverifikasi dan persentil reanalisis.
- Model `best_match` bisa berganti model dasar tanpa pemberitahuan; nama model disimpan di setiap event dan baris `ts.*`, jadi pergantian eksplisit ke model tertentu (misal `ecmwf_ifs025`) cukup lewat konstanta tanpa mengubah skema.
