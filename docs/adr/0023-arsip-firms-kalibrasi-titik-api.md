# 0023. Arsip FIRMS untuk kalibrasi titik api

Tanggal: 2026-10-20, diperbarui 2026-11-08 · Status: diterima; aturan tingkat dan saringan akhir diputuskan di 1e-3c-1b (bagian Keputusan 1e-3c-1b)

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

## Keputusan 1e-3c-1b (2026-11-08)

Arsip asli diambil di Windows dengan `fire-threshold.exe` (WSL tidak menjangkau FIRMS lewat IPv4): 3.055 potongan, 82.290 deteksi, 0 baris ditolak, 45 kali menunggu kuota. Rentang: SP sampai 30 Jun 2026 (MODIS 31 Jul), lalu NRT sampai 19 Okt; NOAA-21 punya SP sejak Mar 2023. Cache dikemas ke titipan (`arsip-firms.tgz`) dan dianalisis dengan `-offline` di workspace asisten: 3.047 potongan, 81.404 deteksi (8 potongan NRT terbaru tidak di-cache).

**Saringan awal terlalu agresif.** Aturan butir 5 menemukan 197 sumber yang menyaring 34.852 deteksi (43%). Dua masalah:

- **Type 1 bukan kawah.** Di Jawa Barat FIRMS memberi type 1 (gunung api) pada kebakaran hutan di lereng gunung api di tahun kering (2015, 2019, 2023), bukan panas kawah. Akibatnya puncak Ciremai menjadi "sumber tetap" dan 112 deteksi kebakaran Ciremai Agustus 2019 tersaring. Kawah Jawa Barat (Papandayan, Guntur, Kamojang) tidak pernah terdeteksi sebagai panas tetap di arsip.
- **Kebakaran musiman berulang ikut tersaring.** ±60 sumber di kebun tebu Majalengka–Indramayu dan perbukitan Cirebon–Kuningan menyala bertahun-tahun di 6+ bulan berbeda dengan 20–60% deteksi malam (lolos syarat lama ≥ 20%), tetapi 85–97% deteksinya di Juli–Oktober. Industri dan suar gas menyala sepanjang tahun: 40–70% di Juli–Oktober (lebih tinggi dari 4/12 karena langit kemarau lebih cerah).

**Saringan akhir** (`firecluster.DefaultStaticRule`):

1. Tidak musiman = ≤ 80% deteksi di Juli–Oktober (UTC), atau ≥ 90% deteksi malam. Pengecualian malam untuk suar kecil yang hanya terlihat VIIRS di malam cerah kemarau (contoh: sumur AKP-001 dekat Balongan, 99% malam, 89% Juli–Oktober sejak 2020).
2. Persisten = ≥ 12 hari, ≥ 3 tahun, ≥ 6 bulan berbeda, ≥ 50% deteksi malam (naik dari 20%), dan tidak musiman.
3. Type FIRMS = ≥ 3 deteksi SP dengan ≥ 50% type 2 (sumber tetap darat) atau 3 (lepas pantai), ≥ 3 tahun, dan tidak musiman. Type 1 tidak dihitung.

Hasil: **139 sumber, 30.660 dari 81.404 deteksi (37,7%)**, 0 deteksi kebakaran tercatat tersaring. Sumber terbesar dicek dengan OSM (API peta per kotak, lewat browser bawaan; Overpass sedang menolak 429/504) dan citra satelit untuk yang tidak ada di OSM:

- Terkonfirmasi OSM: Kilang Pertamina RU VI Balongan (flare, cerobong), Indocement Citeureup (PLTG, pabrik semen) dan Palimanan, PLTGU Muara Tawar, PLTU Banten Lontar, stasiun pengumpul Pertamina Tambun (TBN) dan Kilang LPG, Stasiun Pengumpul Subang, kawasan industri Cikande, Jababeka, Purwakarta (Asahimas Flat Glass), dan Dayeuhkolot.
- Tidak ada di OSM, dikonfirmasi citra: sumber terbesar (-6.548,108.094; 4.251 deteksi, 81% malam) adalah fasilitas Pertamina ABG di Jatimunggul, Indramayu; -6.285,107.879 adalah Pertamina EP Asset 3 SP JAS di Pusakaratu, Subang; -6.372,108.357 sumur AKP-001.
- Tanpa objek OSM karena di laut: anjungan lepas pantai (type 3, 85–97% malam).
- Beberapa sumber di Tangerang (Banten, di luar Jawa Barat) tidak bisa dicek karena kotaknya terlalu padat untuk API peta OSM; letaknya di kawasan industri.

**Aturan tingkat** (`firecluster.SelectedRule`, varian V4 di laporan): **Info** untuk setiap kejadian; **Waspada** bila ada deteksi keyakinan tinggi atau kejadian terlihat di ≥ 2 lintasan (wahana + waktu lintas) dalam 24 jam; **Siaga** bila ≥ 5 deteksi dalam 24 jam; dinilai tanpa S-NPP. Bahaya tetap tidak dipakai sendiri.

| Varian (tanpa S-NPP kecuali V1)        | Waspada 2018–2026 | Siaga 2018–2026 | Kebakaran tercatat Siaga (dari 7 yang terdeteksi) |
| -------------------------------------- | ----------------: | --------------: | ------------------------------------------------: |
| V1 PRD dengan S-NPP                    |               309 |             841 |                                                 7 |
| V2 PRD                                 |               269 |             375 |                                                 7 |
| V3 Waspada nominal, Siaga ≥ 3 lintasan |            13.648 |             348 |                                                 7 |
| **V4 terpilih**                        |         **1.820** |         **375** |                                             **7** |
| V5 V4 tanpa MODIS                      |             1.459 |             308 |                                                 7 |

Alasan:

- **Waspada PRD hampir tidak pernah terpenuhi.** Keyakinan tinggi VIIRS hanya piksel jenuh (±1% deteksi), jadi kebakaran yang bertahan dari siang ke malam atau terlihat dua wahana tetap Info. Lintasan kedua dalam 24 jam adalah konfirmasi yang wajar: api nyata, bukan kilau sesaat. Waspada mulai keyakinan nominal (V3) membuat ±86% kejadian Waspada (±3.000 pada 2023) sehingga push kehilangan makna.
- **Siaga dihitung dalam deteksi, bukan lintasan.** Tanpa MODIS (Terra dan Aqua sudah jauh melewati masa tugasnya), Siaga ≥ 3 lintasan menurunkan Guntur 2019 dan Papandayan 2023 menjadi Waspada, sedangkan ≥ 5 deteksi tetap Siaga untuk ketujuh kebakaran. Aturan harus tetap jalan bila hanya NOAA-20 dan NOAA-21 yang tersisa.
- **Tanpa S-NPP** angka Siaga turun dari 841 ke 375 (2018–2026), tetapi tidak satu pun kebakaran tercatat yang turun. Papandayan Okt 2019 (1,5 ha) tetap Info dengan satu deteksi; Ciremai Sep 2019 tidak terlihat satelit mana pun.
- Volume V4 di tahun terkering (2023): 601 Waspada dan 124 Siaga untuk seluruh kotak Jawa Barat dan sekitarnya; pengguna hanya menerima push untuk kejadian ≤ 10 km dari lokasi tersimpan, dan Info tidak di-push.

**`hazard.fire.*` di geo-processor:**

1. **Aturan disalin, bukan diimpor.** `domain/fire` geo-processor menulis ulang pengelompokan dan `Evaluate` firecluster. Kecocokannya diuji dengan berkas acuan bersama: deteksi asli 18 Agu–7 Sep 2023 (2.171 deteksi, kebakaran Sarimukti dan Ciremai) di `internal/app/fires/testdata`. Test ingest `TestGoldenForGeoProcessor` menghitung kejadian acuan dengan firecluster (711 kejadian: 528 Info, 155 Waspada, 28 Siaga); test geo-processor memutar deteksi yang sama satu per satu lewat use case (memori dan PostgreSQL asli) dan harus menghasilkan kejadian, tingkat, puncak, dan waktu capai yang sama persis. `make fire-threshold` memperbarui berkas acuan.
2. **Saringan disematkan sebagai data**: `sumber-panas-tetap-32.csv` (kini dengan komentar periode arsip dan jumlah sumber) disalin ke `internal/adapters/calibration/data` lewat `make calibration-copy`, diperiksa sama persis dengan `docs/calibration`, dan diselaraskan ke `ref.fire_static_source` saat start (sama dengan ambang banjir, ADR 0021).
3. **Pengelompokan bertahap**: deteksi masuk kejadian aktif yang punya anggota ≤ 2 km dan ≤ 24 jam; tanpa itu, deteksi membuka kejadian baru (ID dari ID deteksi pembuka). Deteksi yang menyambung dua kejadian aktif menggabungkannya: yang deteksi pertamanya lebih tua bertahan, yang lain berakhir `merged`. Hasilnya sama dengan pengelompokan berantai sekaligus selama tidak ada kejadian yang kedaluwarsa di tengah (diuji fuzz urutan kedatangan).
4. **Tingkat = tingkat tertinggi selama kejadian aktif**, dihitung ulang dari semua anggota setiap ada deteksi baru (sama dengan laporan kalibrasi). Tingkat tidak pernah turun; alert-engine hanya mem-push kenaikan.
5. **Kedaluwarsa 24 jam setelah deteksi terakhir diterima** (`last_fetched_at`, bukan waktu lintas), konsisten dengan banjir (waktu terima keluaran). Dengan keterlambatan NRT ±3 jam, deteksi yang berselang ≤ 24 jam dari anggota terakhir selalu tiba sebelum kejadian berakhir. Deteksi lama (replay) membuka lalu langsung mengakhiri kejadian, sama dengan banjir.
6. **Consumer terpisah** `geo-processor-hazard-fire` di `raw.fire.firms`, di samping penyimpanan `ts.hotspot`. Deteksi S-NPP dan deteksi di sumber tetap tetap disimpan di `ts.hotspot` tetapi tidak dinilai. Migrasi 00008: `ref.fire_static_source`, `hazard.fire`, `hazard.fire_detection` (ID deteksi diperiksa sama dengan isinya, satu deteksi satu kejadian).
7. **Belum ada area dan wilayah terdampak.** Pesan membawa semua deteksi anggota (`FireDetail.detections`); aturan PRD ≤ 10 km dihitung alert-engine (fase 3).
8. **Konektor `firms-viirs-snpp-nrt` dibuang dari polling** (`fire.PolledProducts`). Parser S-NPP tetap ada supaya payload lama di arsip bisa di-replay.

Konsekuensi tambahan:

- Kalibrasi ulang tahunan: `make fire-threshold` (ulang dari cache gratis), tinjau laporan dan saringan, commit; berkas acuan geo-processor ikut diperbarui otomatis, dan test geo-processor gagal bila salinan saringan lupa disalin.
- Kebakaran lahan pertanian (tebu, jerami) kini menjadi kejadian Info/Waspada, bukan disaring. Itu disengaja: asapnya nyata, dan teks kejadian menyebut bahwa deteksi satelit bisa juga pembakaran lahan.
- Bila MODIS berhenti, hitungan Siaga turun ±20% (V5). Tinjau ulang ambang Siaga bila itu terjadi.
