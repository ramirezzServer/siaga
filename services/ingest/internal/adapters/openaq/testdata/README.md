# Fixture OpenAQ v3

- `*-2026-09-24.json`: rekaman asli 2026-09-24 17.03 UTC (`ambil-sampel-1d-2.sh`) untuk kotak Jawa Barat. `locations` berisi 31 lokasi; hanya `6455006` ("BMKG 1", Jakarta) dan `6539694` ("Griya Tugu Asri", Depok) yang melapor dalam 48 jam, keduanya AirGradient dengan PM2,5 sebagai satu-satunya parameter SIAGA. `latest-1563313` adalah stasiun lama dengan nilai 2024–2025 (semua basi).
- `tanpa-key.json`: jawaban asli HTTP 401 tanpa header `X-API-Key`.
- `locations-sintetis.json`, `latest-2178-sintetis.json`: ditulis tangan untuk kasus yang tidak ada di rekaman: stasiun monitor dengan ozon dalam ppm, lokasi bergerak, lokasi tanpa koordinat, sensor basi di stasiun aktif.
- `measurements-17620437-2026-09-26.json`, `measurements-17000275-2026-09-26.json`: rekaman asli 2026-09-28 13.29 UTC (`ambil-sampel-openaq-jam.sh`) nilai mentah PM2,5 "Griya Tugu Asri" dan "BMKG 1" untuk 26 Sep 09.00–19.00 UTC, sekitar celah jaringan 26 Sep. Dibandingkan dengan `ts.aq_observation` untuk rentang yang sama: `observed_at` live = akhir periode, dan nilainya sama persis dengan `/measurements` (jam 19.00 sensor 17620437: 117,6; `/hours` menulis 118,0). Baris live yang tidak ada: 14.00 UTC (celah).
- `hours-halaman-2026-09-26.json`: halaman 2 `/v3/sensors/17620437/hours` dengan `limit=3`; `meta.found` berupa teks `">3"`.
