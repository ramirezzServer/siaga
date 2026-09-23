# SIAGA

Platform ketahanan wilayah Jawa Barat: peringatan dini bencana (gempa, cuaca ekstrem, banjir, titik api) dan pemantauan serta prediksi kualitas udara (NAPAS) dalam satu peta real-time. Proyek portofolio independen, bukan layanan resmi BMKG, BNPB, atau BPBD. Selaras dengan SDGs 3, 11, dan 13.

> Status: **Fase 0 (fondasi)**. Belum ada aplikasi yang bisa dibuka di browser; yang sudah ada adalah database, kontrak, importer wilayah, dan CI.

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
make up               # PostgreSQL + NATS + Valkey + Mailpit (profil lite)
make seed             # migrasi + import 6.612 wilayah Jawa Barat
make check            # lint + test
```

Profil full (Kubernetes lokal, sama dengan produksi): `make k3d-up && make tilt`.

## Struktur

```text
contracts/          Kontrak: proto (event NATS) dan OpenAPI (API publik)
libs/go/            Library Go bersama (platform, contracts hasil generate)
packages/           Paket TypeScript (contracts, api-client)
services/           Layanan; sekarang baru geo-processor (importer wilayah)
infra/db/bootstrap  SQL awal database: ekstensi, role, schema
deploy/             Image, Compose, k3d, manifest Kubernetes
scripts/            Skrip pendukung
docs/               ADR, panduan setup, handoff
```

## Sumber data dan atribusi

Batas wilayah: [cahyadsn/wilayah_boundaries](https://github.com/cahyadsn/wilayah_boundaries) (MIT), mengacu Kepmendagri 2025. Sumber data bahaya (BMKG, USGS, Open-Meteo, OpenAQ, NASA FIRMS, OpenStreetMap) ditambahkan mulai fase 1 dengan atribusi di setiap tampilan.

## Lisensi

MIT
