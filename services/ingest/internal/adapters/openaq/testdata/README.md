# Fixture OpenAQ v3

- `*-2026-09-24.json`: rekaman asli 2026-09-24 17.03 UTC (`ambil-sampel-1d-2.sh`) untuk kotak Jawa Barat. `locations` berisi 31 lokasi; hanya `6455006` ("BMKG 1", Jakarta) dan `6539694` ("Griya Tugu Asri", Depok) yang melapor dalam 48 jam, keduanya AirGradient dengan PM2,5 sebagai satu-satunya parameter SIAGA. `latest-1563313` adalah stasiun lama dengan nilai 2024–2025 (semua basi).
- `tanpa-key.json`: jawaban asli HTTP 401 tanpa header `X-API-Key`.
- `locations-sintetis.json`, `latest-2178-sintetis.json`: ditulis tangan untuk kasus yang tidak ada di rekaman: stasiun monitor dengan ozon dalam ppm, lokasi bergerak, lokasi tanpa koordinat, sensor basi di stasiun aktif.
