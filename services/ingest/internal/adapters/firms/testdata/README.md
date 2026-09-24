# Fixture FIRMS

- `jabar-*-2026-09-24.csv`: rekaman asli 2026-09-24 17.04 UTC (`ambil-sampel-1d-2.sh`), kotak Jawa Barat, jendela 2 hari, keempat produk NRT. Kode satelit: `N` (SNPP), `N20`, `N21`, `Terra`, `Aqua`; `acq_time` tanpa nol di depan (misal `550`).
- `kalimantan-*-2026-09-24-300baris.csv`: 300 baris pertama rekaman Kalimantan 1 hari (musim kemarau), untuk volume dan variasi nilai.
- `key-salah.txt`: isi asli jawaban FIRMS untuk MAP_KEY salah (HTTP 400). Galat parameter lain juga HTTP 400 dengan teks pendek (`Invalid day range. Expects [1..5].`, `Invalid source.`), jadi ditangani `httpfetch` sebagai `StatusError` sebelum sampai ke parser.
- `viirs-sintetis.csv`, `modis-sintetis.csv`, `kosong.csv`: ditulis tangan untuk kasus galat per baris (duplikat, keyakinan tak dikenal, nilai kosong) dan payload tanpa deteksi.
