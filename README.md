# SIAGA

Platform ketahanan wilayah Jawa Barat: peringatan dini bencana (gempa, cuaca ekstrem, banjir, titik api) dan pemantauan serta prediksi kualitas udara (NAPAS) dalam satu peta real-time. Proyek portofolio independen, bukan layanan resmi BMKG, BNPB, atau BPBD. Selaras dengan SDGs 3, 11, dan 13.

> Status: **Fase 1 (pipa data), irisan 1d-2**. Belum ada aplikasi yang bisa dibuka di browser. Yang sudah jalan: ingest menarik data gempa BMKG dan USGS, peringatan dini cuaca BMKG (CAP), prakiraan cuaca BMKG per kelurahan/desa Jawa Barat, prakiraan cuaca dan kualitas udara (CAMS) per simpul grid 0,25° dan debit 38 titik pantau sungai (GloFAS) dari Open-Meteo, nilai sensor stasiun kualitas udara OpenAQ, dan titik panas satelit NASA FIRMS, lalu menerbitkannya ke NATS JetStream. geo-processor menggabungkan laporan gempa kedua sumber menjadi satu kejadian (deduplikasi terkalibrasi), menyusun rantai pesan CAP menjadi satu kejadian cuaca, memperkirakan kelurahan/desa terdampak, menerbitkan `hazard.quake.*` serta `hazard.weather.*`, dan menyimpan semua prakiraan, nilai sensor, dan titik panas ke hypertable TimescaleDB schema `ts`.

## Dokumen

- [Arsitektur sistem](https://claude.ai/code/artifact/e641835c-1ce9-4898-822c-3bc4fb4ca343)
- [Product Requirements Document](https://claude.ai/code/artifact/bd22d1a6-1c06-46d0-8c33-e615c8098be0)
- [Keputusan arsitektur (ADR)](docs/adr/)
- [Kontrak event dan subjek NATS](docs/events.md)
- [Status dan langkah berikutnya](docs/HANDOFF.md)

## Mulai cepat

Butuh Linux atau WSL2 (Windows). Panduan Windows: [docs/setup/windows-wsl2.md](docs/setup/windows-wsl2.md).

```bash
make doctor           # cek prasyarat
make deps             # unduh dependensi, kunci versi
make up               # PostgreSQL + NATS + Valkey + Garage + Mailpit (profil lite)
make seed             # migrasi + import 6.612 wilayah Jawa Barat
make adm4-list        # (opsional) bangun ulang daftar kode desa untuk sapuan prakiraan
make grid-list        # (opsional) bangun ulang simpul grid 0,25° Open-Meteo
make ingest           # tarik BMKG, USGS, Open-Meteo, OpenAQ, FIRMS ke NATS, arsip mentah ke Garage (status: :8081/status)
make geo              # gempa + cuaca → hazard.*, deret waktu → ts.* (status: :8082/status)
make check            # lint + test
make calibrate-dedup  # ukur ambang deduplikasi dengan katalog historis BMKG + USGS
make river-snap       # pilih sel GloFAS untuk 38 titik pantau sungai (±800 lokasi Open-Meteo)
make archive-ls       # isi arsip payload mentah per konektor; archive-verify memeriksa integritasnya
make replay FROM=2026-09-24 TO=2026-09-25   # putar ulang arsip ke NATS, urut waktu ambil asli
```

OpenAQ dan NASA FIRMS butuh key gratis: isi `OPENAQ_API_KEY` (daftar di explore.openaq.org) dan `FIRMS_MAP_KEY` (firms.modaps.eosdis.nasa.gov/api/map_key) di `.env`. Tanpa key, ingest tetap jalan tanpa kedua konektor itu.

Payload mentah setiap sumber diarsipkan ke Garage (bucket `siaga-arsip`, awalan `raw/`) dan bisa diputar ulang untuk uji replay atau memulihkan stream NATS (ADR 0014). Arsip lokal dari sebelum fase 1e dipindahkan dengan `make archive-upload`.

Profil full (Kubernetes lokal, sama dengan produksi): `make k3d-up && make tilt`.

## Struktur

```text
contracts/          Kontrak: proto (event NATS) dan OpenAPI (API publik)
libs/go/            Library Go bersama (platform, contracts hasil generate)
packages/           Paket TypeScript (contracts, api-client)
services/           Layanan: ingest (pengambil data sumber), geo-processor (wilayah, kejadian bahaya, deret waktu)
infra/db/bootstrap  SQL awal database: ekstensi, role, schema
deploy/             Image, Compose, k3d, manifest Kubernetes
scripts/            Skrip pendukung
docs/               ADR, kalibrasi, panduan setup, handoff
```

## Sumber data dan atribusi

Batas wilayah: [cahyadsn/wilayah_boundaries](https://github.com/cahyadsn/wilayah_boundaries) (MIT), mengacu Kepmendagri 2025. Data gempa, peringatan dini cuaca, dan prakiraan cuaca: BMKG (wajib dicantumkan); gempa pembanding: USGS (domain publik). Prakiraan cuaca grid, kualitas udara (CAMS), dan debit sungai (GloFAS): [Open-Meteo](https://open-meteo.com/) (CC BY 4.0, pemakaian non-komersial). Kalibrasi deduplikasi memakai katalog RepoGempa BMKG yang dikompilasi [kekavigi/repo-gempa](https://github.com/kekavigi/repo-gempa) (kompilasi CC BY 4.0) dan keluaran FDSN USGS yang disimpan di [horankev/quake_data](https://github.com/horankev/quake_data); keduanya hanya diunduh saat kalibrasi, tidak disimpan di repo ini. Sumber lain (OpenAQ, NASA FIRMS, OpenStreetMap) ditambahkan sepanjang fase 1 dengan atribusi di setiap tampilan.

## Lisensi

MIT
