# 0002. Satu image PostgreSQL untuk Compose dan CloudNativePG

Tanggal: 2026-09-23 · Status: diterima

## Konteks

SIAGA butuh PostGIS, TimescaleDB, pgvector, dan h3-pg dalam satu database. Image CloudNativePG resmi tidak membawa TimescaleDB, sedangkan image TimescaleDB resmi berbeda basis dengan image CNPG.

## Keputusan

Image sendiri di `deploy/images/postgres`, berbasis `postgres:17-bookworm` resmi. PostGIS, pgvector, dan h3 dari repo PGDG yang sudah ada di image resmi; TimescaleDB dari repo apt Timescale. Di CloudNativePG, `postgresUID`/`postgresGID` diset 999 agar cocok dengan user image resmi. SQL bootstrap (`infra/db/bootstrap`) dipakai dua jalur: `docker-entrypoint-initdb.d` untuk Compose dan `postInitApplicationSQLRefs` untuk CNPG.

TimescaleDB memakai edisi Community (lisensi Timescale License). Fitur kompresi dan continuous aggregate boleh dipakai gratis selama SIAGA tidak menjual TimescaleDB sebagai layanan database.

## Konsekuensi

Satu Dockerfile, perilaku sama di lokal dan produksi. Versi ekstensi mengikuti paket apt terbaru saat build; untuk rilis produksi, image di-tag dan dipindai Trivy di CI. Kompatibilitas CNPG dengan image berbasis resmi diverifikasi saat pertama kali menjalankan profil full; bila bermasalah, alternatifnya basis `ghcr.io/cloudnative-pg/postgresql` ditambah paket yang sama.

Hasil verifikasi profil full (1f-2, 2026-11-23, operator CNPG 1.30.2): image ini jalan di CNPG tanpa perubahan (bootstrap, migrasi, dan 6.612 baris `ref.region` lulus). Yang gagal adalah cara menunjuk image: CNPG membaca versi mayor PostgreSQL dari tag `spec.imageName`, sehingga webhook menolak tag hasil build Tilt (`tilt-<hash>`), dan tag `sha-<commit>` dari CI akan ditolak dengan cara yang sama. Cluster kini memakai `imageCatalogRef` ke `ImageCatalog` `siaga-postgres` (`deploy/k8s/base/postgres-image-catalog.yaml`): versi mayor (17) ditulis eksplisit di katalog, tag image bebas, dan Tilt atau pipeline CD cukup mengganti image di katalog. Saat image di katalog berganti, CNPG melakukan rolling update instance. Menaikkan versi mayor tetap keputusan terpisah (entri katalog baru + `major` di Cluster), bukan efek samping ganti tag.
