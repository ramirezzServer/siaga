# 0003. Batas wilayah dari cahyadsn/wilayah_boundaries

Tanggal: 2026-09-23 · Status: diterima

## Konteks

SIAGA mengunci semua data ke kode wilayah Kemendagri sampai tingkat desa/kelurahan (adm4). Butuh poligon batas yang bebas dipakai.

## Keputusan

Pakai `cahyadsn/wilayah_boundaries` (MIT, mengacu Kepmendagri No. 300.2.2-2430 Tahun 2025) pada commit terkunci `3c5a7960` (2026-08-20). Data diunduh sparse (±49 MB untuk Jawa Barat) oleh `scripts/fetch-region-data.sh`, tidak di-commit. Importer Go memvalidasi setiap baris dengan aturan domain dan menolak seluruh import bila ada satu baris bermasalah (bisa dilonggarkan dengan `-max-skipped`).

Hasil import pertama untuk Jawa Barat: 6.612 wilayah (1 provinsi, 18 kabupaten, 9 kota, 627 kecamatan, 646 kelurahan, 5.311 desa), semua geometri valid, tanpa perbaikan.

## Konsekuensi

Poligon disederhanakan oleh penyusun dataset, jadi batas antarwilayah bisa meleset puluhan meter. Cukup untuk peringatan tingkat kelurahan, tidak untuk keperluan legal. Kode yang hilang dari sumber (misal karena pemekaran) dilaporkan sebagai "stale" dan tidak dihapus otomatis.
