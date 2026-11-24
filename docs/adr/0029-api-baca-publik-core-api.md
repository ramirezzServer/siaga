# 0029. API baca publik: core-api dimulai di Fase 2 dengan modul baca-saja

Tanggal: 2026-11-24 · Status: diterima

## Konteks

Fase 2 (R1 Peta Publik) butuh REST untuk feed kejadian, detail kejadian, kelurahan/desa terdampak, dan pencarian wilayah (US-01–03, US-05–07). Menurut dokumen arsitektur, `web` dan `mobile` mengambil data itu lewat core-api (NestJS, Fastify, Drizzle, Zod), tetapi core-api baru dijadwalkan di Fase 3 bersama akun dan peringatan personal. Kontrak `contracts/openapi/core-api.yaml` sejak Fase 0 hanya berisi `/healthz` dan wilayah, tanpa server.

Data yang dibaca semuanya milik geo-processor (skema `hazard`, `ref`, `ts`, ADR 0007 dan seterusnya). Migrasi geo-processor sudah memberi `siaga_core` `USAGE` + `SELECT` pada tabel dan view yang dibaca layanan lain, dan role itu tidak punya hak tulis di sana.

Pilihan yang dipertimbangkan:

1. **core-api dimulai sekarang, hanya modul publik baca-saja.** Layanan ini memang akan dibangun; Fase 3 tinggal menambah auth, skema `core`, dan modul akun.
2. Route handler Next.js langsung ke PostgreSQL. Cepat, tetapi web memegang kredensial database, kontrak OpenAPI terlewati, dan klien Dart Fase 6 tidak bisa memakainya.
3. Layanan Go baca-saja terpisah. Layanan ke-11 yang nanti tumpang tindih dengan core-api.

## Keputusan

1. **Pilihan 1.** `services/core-api` (paket pnpm `@siaga/core-api`, NestJS 12 + Fastify 5, ESM) dengan modul `health`, `regions`, dan `hazards`. Struktur hexagonal sama dengan layanan Go: per modul `domain` (fungsi murni: saringan feed, kursor, pemetaan ke DTO), `app` (use case), `ports`, `adapters` (PostgreSQL), dan `http` (controller Nest). `main.ts` hanya merakit dependensi; `createApp(deps)` dipakai bersama oleh test.
2. **Endpoint** (kontrak 0.2.0): `GET /v1/hazards` (feed: `kind` berulang, `status=active|all`, `at` untuk putar ulang, `since`/`until`, `min_level`, `bbox`, `drill`, kursor keyset `occurred_at`+`id`), `GET /v1/hazards/{id}` (detail per jenis: gempa dengan laporan tiap sumber, cuaca dengan teks asli BMKG id/en, banjir dengan ambang dan penilaian per hari, titik api; `include=area` untuk GeoJSON), `GET /v1/hazards/{id}/regions` (estimasi kelurahan/desa terdampak dengan nama kecamatan dan kabupaten/kota, `basis` felt_radius/area_overlap), `GET /v1/regions` dan `/v1/regions/{code}` (sudah ada di kontrak Fase 0), `/healthz` (liveness, tanpa database) dan `/readyz` (readiness, `SELECT 1`).
3. **Kejadian `merged` tidak pernah muncul di feed** (US-05). Detailnya tetap bisa dibuka (tautan lama) dengan `merged_into` menunjuk kejadian yang bertahan. `status=active` berarti `status = 'active'` dan `expires_at` belum lewat pada jam aplikasi, jadi kejadian yang kedaluwarsa hilang dari feed walau geo-processor belum sempat menandainya. `at` memilih kejadian yang tampil pada saat itu (`detected_at <= at` dan belum berakhir) dari keadaan terakhirnya; perubahan tingkat di tengah jalan tidak direkam, jadi putar ulang memakai tingkat terakhir.
4. **Batas akses dijaga dua kali.** Lapis pertama grant yang sudah ada: `siaga_core` hanya `SELECT` di `hazard`/`ref`/`ts` (outbox tidak terbaca). Lapis kedua sesi: pool core-api memasang `default_transaction_read_only=on`, `statement_timeout` (bawaan 5 dtk), dan `TimeZone=UTC` untuk setiap koneksi. Detail yang butuh beberapa query dibaca dalam satu transaksi `REPEATABLE READ READ ONLY`. Test integrasi memastikan kedua lapis menolak tulis (25006 dan 42501). Skema `core` belum dipakai sampai Fase 3.
5. **Skema milik geo-processor tidak didefinisikan ulang di Drizzle.** Query baca ditulis sebagai SQL berparameter lewat `pg` (driver yang juga dipakai Drizzle), dan setiap baris divalidasi Zod sebelum masuk domain; baris yang tidak sesuai menjadi galat `RowShapeError` (500), bukan data yang terpotong diam-diam. Salinan skema kedua di TypeScript akan melenceng dari migrasi Go tanpa ketahuan. Drizzle (dan drizzle-kit) dipakai mulai Fase 3 untuk skema `core` milik core-api sendiri.
6. **Kontrak adalah sumber kebenaran respons.** DTO di domain bertipe dari `@siaga/api-client` (hasil generate kontrak), jadi respons yang menyimpang gagal saat `tsc`. Test unit dan integrasi memvalidasi setiap body terhadap `components.schemas` dengan Ajv (OpenAPI 3.1 = JSON Schema 2020-12), dan memastikan setiap operasi GET di kontrak punya rute. `HazardDetail` ditulis lengkap tanpa `allOf` supaya generator klien Dart tidak perlu komposisi; test menjaga kolomnya tetap sama dengan `HazardSummary`.
7. **Galat dalam Problem Details** (RFC 9457, `application/problem+json`): 400 `/problems/invalid-parameter` dengan `errors[]` per parameter (pesan Zod berbahasa Indonesia), 404 `/problems/not-found`, 503 `/problems/unavailable` dengan `Retry-After` untuk database yang tidak terjangkau atau lambat (kode koneksi Node, 57014, 57P0x, 53300, 08xxx), 500 `/problems/internal` tanpa pesan internal. Izin ditolak (42501) sengaja 500: itu bug konfigurasi, bukan gangguan sementara.
8. **Cache dan CORS.** `Cache-Control` publik singkat untuk data bahaya (feed dan detail 15 dtk, wilayah terdampak 60 dtk), panjang untuk wilayah (1 jam daftar, 1 hari detail); Problem selalu `no-store`. CORS hanya untuk origin di `CORS_ORIGINS` (web di Vercel nanti), metode GET/HEAD/OPTIONS.
9. **Jam disuntikkan.** "Aktif" dihitung terhadap `Clock`, sama dengan aturan domain Go. Test integrasi memaku jam ke 2001 dan memakai konvensi data uji geo-processor (tahun 2001, provinsi 99), jadi bisa dijalankan di database lokal yang berisi data nyata.

## Konsekuensi

- Peta publik dan klien Dart bisa dibangun di atas kontrak yang sama sejak Fase 2. Fase 3 menambah auth (Keycloak), skema `core` dengan Drizzle, dan modul akun ke layanan yang sama tanpa membuang kode.
- core-api menambah satu proses Node: RSS ±120 MiB di workspace asisten setelah 200 permintaan feed (perkiraan Rencana Fase 2 untuk tiga layanan baru tetap 0,3–0,5 GiB); angka cluster diukur di 2a-3.
- `make core` menjalankan core-api lokal di port 8083 (butuh `make up seed`); `make test-integration` kini juga menjalankan test integrasi core-api (role `siaga_geo` untuk menulis data uji, `siaga_core` untuk membaca). Job CI `database` menjalankannya di image PostgreSQL SIAGA.
- Belum di irisan ini: status sumber data yang tahan restart (2a-2; temuan 1f-1: status yang tampil ke publik tidak boleh berasal dari gauge di memori ingest), serta image, manifest Kubernetes, Tilt, NetworkPolicy, dan OpenTelemetry core-api (2a-3). Sampai 2a-3 core-api hanya jalan lewat `make core`.
- Query poligon `include=area` dan `ST_AsGeoJSON` per permintaan cukup untuk detail; lapisan peta tetap lewat vector tiles Martin (2b), bukan API ini.
