# 0023. Arsip FIRMS untuk kalibrasi titik api

Tanggal: 2026-10-20 · Status: diterima (ambang dan saringan akhir diputuskan setelah data asli, di fase 1e-3c-1b)

## Konteks

PRD (Aturan bisnis) menetapkan tingkat titik api dalam 10 km dari lokasi: Info untuk deteksi keyakinan rendah, Waspada bila ada ≥ 1 deteksi keyakinan tinggi dalam 24 jam, Siaga bila ≥ 5 deteksi dalam 24 jam, Bahaya tidak dipakai sendiri, kedaluwarsa 24 jam sejak data terakhir di atas ambang. Keputusan lanjutan PRD: kalibrasi dari arsip NASA FIRMS VIIRS sejak tersedia. ADR 0013 menunda `hazard.fire.*` sampai arsip ini ada. Fase 1e-3c (ADR 0018) berisi arsip FIRMS, set berlabel T4, radius dirasakan, dan replay CAP. Keempatnya tidak saling bergantung, jadi dipecah: **1e-3c-1a** arsip FIRMS dan laporan kalibrasi (ADR ini), **1e-3c-1b** `hazard.fire.*` di geo-processor dengan aturan yang dipilih dari laporan, **1e-3c-2** set berlabel T4 dan radius dirasakan, **1e-3c-3** replay CAP.

Yang dicek 2026-10-20 sebelum kode ditulis (dokumen FIRMS lewat WebFetch dan browser bawaan):

- **Keyakinan VIIRS tinggi hanya untuk piksel jenuh** (siang atau malam). Rendah = sun glint atau anomali < 15 K, nominal = anomali > 15 K tanpa glint. Jadi aturan Waspada PRD ("≥ 1 keyakinan tinggi") untuk VIIRS jarang terpenuhi; perlu diukur berapa kali setahun, bukan diasumsikan.
- **Kolom `type` hanya ada di arsip standard processing (SP)**: 0 vegetasi, 1 gunung api, 2 sumber tetap lain (industri), 3 lepas pantai. NRT yang dipolling ingest tidak membawanya, jadi sumber tetap harus diturunkan dari arsip dan disaring sendiri. Rekaman 24 Sep 2026 (1d-2) sudah memperlihatkan deteksi MODIS di kawasan industri Bandung selatan.
- **Pengiriman data S-NPP berhenti 2 November 2026** (pemberitahuan di halaman FIRMS). Konektor `firms-viirs-snpp-nrt` akan kosong; aturan yang menghitung deteksi tidak boleh bergantung pada tiga wahana VIIRS sekaligus.
- Area API: `/api/area/csv/KEY/PRODUK/KOTAK/HARI/TANGGAL`, 1–5 hari per request, produk SP `VIIRS_SNPP_SP`, `VIIRS_NOAA20_SP`, `VIIRS_NOAA21_SP`, `MODIS_SP`; ketersediaan per produk di `/api/data_availability/csv/KEY/ALL`. Batas MAP_KEY 5.000 transaksi per 10 menit; request yang lebih besar bisa dihitung lebih dari satu (cara hitungnya tidak dijelaskan). Status pemakaian di `/mapserver/mapkey_status/?MAP_KEY=` (format jawaban tidak didokumentasikan; key salah atau kuota habis dijawab HTTP 403 dengan teks yang sama). Key salah di Area API dijawab HTTP 400 `Invalid MAP_KEY.`.
- Arsip tahunan per negara tidak dipakai: satu berkas VIIRS seluruh negara ±370 MB per tahun, sedangkan kotak Jawa Barat hanya ribuan baris per tahun.
- Workspace asisten tidak menjangkau FIRMS, dan MAP_KEY hanya ada di `.env` laptop. Data asli harus diambil di WSL.

## Keputusan

1. **`make fire-threshold`** (`cmd/fire-threshold`) mengambil arsip lalu menulis bahan kalibrasi; alat ini tidak memutuskan ambang. Keluaran: `docs/calibration/titik-api.md` (laporan), `sumber-panas-tetap-32.csv` dan `.geojson` (saringan usulan, GeoJSON bisa dilihat di GitHub). Masukan uji: `docs/calibration/kebakaran-tercatat.csv`.
2. **SP selama tersedia, lalu NRT** per wahana (S-NPP, NOAA-20, NOAA-21, MODIS), kotak yang sama dengan polling (`INGEST_FIRMS_BBOX`), bawaan 2012-01-01 sampai kemarin. Rentang dari jawaban ketersediaan, bukan tanggal yang ditulis di kode.
3. **Cache per potongan lima hari** di `.cache/fire-archive/<produk>/<tahun>/<tanggal>.d<hari>.csv`, isi CSV FIRMS apa adanya. Potongan mengikuti grid tetap sejak 2000-01-01 sehingga kuncinya sama antar-jalan; potongan SP dan NRT yang berakhir lebih dari 7 hari lalu final dan disimpan, NRT yang lebih baru diambil ulang. Jalan yang terputus dilanjutkan dari cache; `-offline` hanya memakai cache, `-plan` hanya mencetak rencana.
4. **Hemat kuota MAP_KEY**: request berurutan, status MAP_KEY dibaca sebelum request pertama dan setiap 20 request; bila pemakaian ≥ 60% batas (atau status 403), tunggu satu menit lalu baca lagi (maks 30 kali). Galat 403/429/5xx diulang dengan jeda 1, 2, 4, 8, 10 menit (maks 6 kali); 400/401 berhenti dengan galat key. Isi setiap potongan diperiksa `firms.ParseArchive` sebelum disimpan. Key ditandai rahasia di setiap request, jadi tidak muncul di log maupun galat.
5. **Sumber panas tetap** (domain `firecluster`, nilai awal untuk dikalibrasi): sel 0,005° dinilai bersama 8 tetangganya; tetap bila ≥ 12 hari, ≥ 3 tahun, ≥ 6 bulan kalender berbeda, dan ≥ 20% deteksi malam (persisten), atau ≥ 3 deteksi SP dengan ≥ 50% type 1/2 (FIRMS). Sel kandidat yang bersebelahan digabung, jari-jari = anggota terjauh + 0,5 km, minimal 0,75 km. Syarat malam memisahkan nyala industri dan suar gas (siang dan malam) dari pembakaran jerami dan tebu yang juga berulang tiap tahun tetapi siang.
6. **Kejadian** = deteksi yang berjarak ≤ 2 km dan berselang ≤ 24 jam dari anggota lain, berantai (kebakaran berhari-hari tetap satu kejadian selama tidak ada jeda > 24 jam, sama dengan kedaluwarsa PRD). Tingkat dinilai di setiap deteksi atas jendela 24 jam ke belakang.
7. **Lima varian dibandingkan** di laporan, karena pilihan akhirnya bergantung pada data: PRD apa adanya; PRD tanpa S-NPP; PRD tanpa saringan sumber tetap; Waspada mulai keyakinan nominal; Waspada nominal + Siaga ≥ 3 lintasan (wahana + waktu berbeda) tanpa S-NPP. Laporan memuat inventaris per wahana per tahun, sebaran bulanan, daftar sumber tetap, kejadian Waspada/Siaga per tahun, 25 kejadian terbesar, dan uji kebakaran tercatat.
8. **Kebakaran tercatat** dari berita (9 kejadian 2019–2024: Ciremai 2019 tiga kali dan 2023, Papandayan 2019 dan 2023, Guntur 2019 dan 2024, TPA Sarimukti 2023), titik perkiraan dengan radius longgar; jendela uji sehari sebelum mulai sampai dua hari setelah selesai (WIB).
9. **Cache dibawa ke workspace asisten** setelah jalan pertama (`kemas-arsip-firms.sh` di titipan) supaya aturan bisa disetel ulang dengan `-offline` tanpa kuota dan tanpa bolak-balik ke laptop.

## Konsekuensi

- Jalan pertama ±2.900 request (S-NPP ±1.000, MODIS ±1.000, NOAA-20 ±620, NOAA-21 ±200) dan bisa makan satu sampai dua jam; ingest boleh tetap jalan karena alat berhenti di 60% kuota.
- `domain/firecluster` adalah pustaka kalibrasi ingest. Saat `hazard.fire.*` dibuat (1e-3c-1b), aturan terpilih ditulis ulang di geo-processor dengan test yang mencocokkan hasilnya, seperti indeks hujan (ADR 0021); saringan sumber tetap disematkan sebagai data seperti ambang banjir.
- `fire.Detection` mendapat `ValidateArchive` (tanpa batas umur) dan produk SP; validasi polling tidak berubah.
- Celah 3–19 Okt 2026 (ingest mati) untuk titik api tertutup oleh arsip NRT alat ini, tetapi hanya untuk kalibrasi; `ts.hotspot` tidak diisi ulang.
- S-NPP berhenti 2 Nov 2026: konektor NRT-nya kosong setelah itu. Dibuang di 1e-3c-1b bersama keputusan aturan tanpa S-NPP.
