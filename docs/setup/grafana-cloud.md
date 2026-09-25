# Grafana Cloud (free tier)

Dokumen arsitektur memakai Grafana Cloud free tier untuk metrik, log, dan trace produksi. Untuk pengembangan harian cukup Grafana lokal (`make obs-up`); langkah di bawah dipakai bila ingin data laptop atau demo tampil di Grafana Cloud. Keputusan dan nama metrik ada di ADR 0015.

## 1. Buat stack dan token

1. Daftar akun gratis di grafana.com (tanpa kartu kredit), lalu buat stack. Pilih region terdekat dari laptop atau server.
2. Di Grafana Cloud Portal, buka stack, lalu pada tile **OpenTelemetry** klik **Configure**.
3. Buat token baru (misal bernama `siaga-laptop`). Halaman itu menampilkan endpoint OTLP (`https://otlp-gateway-<region>.grafana.net/otlp`) dan nilai `OTEL_EXPORTER_OTLP_HEADERS` yang sudah jadi (`Authorization=Basic <base64 instance-ID:token>`).

Token adalah rahasia: simpan hanya di `.env` (tidak di-commit). Di cluster produksi token disimpan sebagai Secret SOPS dan dipakai OTel Collector (fase 1e-2b), bukan oleh layanan langsung.

## 2. Isi `.env`

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic%20<base64>
```

Spasi setelah `Basic` ditulis `%20` (nilai header OTLP di environment memakai percent-encoding; SDK Go membacanya kembali menjadi spasi). Endpoint diakhiri `/otlp` tanpa `/v1/...`; SDK menambahkan path per sinyal.

Jalankan ulang `make ingest` dan `make geo`. Log start menunjukkan `"trace":true,"metrics":true,"logs":true`. Galat pengiriman (token salah, jaringan putus) dicatat paling sering sekali per menit dengan pesan `galat OpenTelemetry`.

## 3. Impor dashboard

1. Di Grafana Cloud: **Dashboards → New → Import**, unggah `deploy/observability/grafana/dashboards/siaga-pipa-data.json`.
2. Dashboard memakai variabel sumber data (`Metrik`, `Trace`, `Log`); pilih sumber data `grafanacloud-…-prom`, `…-traces`, dan `…-logs` milik stack.

Nama metrik sama di Grafana lokal dan Grafana Cloud (keduanya menerjemahkan nama OTLP ke Prometheus dengan akhiran satuan, misal `siaga_ingest_polls_total`), dan label `job` bernilai `siaga/ingest` atau `siaga/geo-processor`.

## Batas free tier

Semua sinyal SIAGA fase 1 jauh di bawah batas free tier: uji lokal dengan 10 konektor dan 8 consumer menghasilkan sekitar 1.000 seri metrik aktif (sebagian besar bucket histogram), dan trace hanya dibuat per polling, per kode sapuan, dan per pesan. Bila trace sapuan prakiraan (±6.000 kode per 6 jam) terlalu banyak, turunkan sampling:

```bash
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.2
```

Sampling berbasis parent menjaga satu gempa tetap satu trace utuh: keputusan diambil sekali di polling ingest dan diikuti geo-processor lewat flag di `traceparent`.
