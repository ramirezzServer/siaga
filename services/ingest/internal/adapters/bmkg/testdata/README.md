# Rekaman payload BMKG

Semua file di sini adalah payload asli BMKG, tidak diubah kecuali yang disebut. Sumber: BMKG.

## Gempa (2026-09-24)

Diambil dari `https://data.bmkg.go.id/DataMKG/TEWS/`.

- `autogempa.json`: payload utuh.
- `gempaterkini.json`, `gempadirasakan.json`: dua entri pertama dari 15, sisanya dipotong supaya test mudah dibaca. Strukturnya tidak diubah.

## Peringatan dini cuaca (2026-09-24 07.19 UTC)

Diambil dari `https://www.bmkg.go.id/alerts/nowcast/`. Saat direkam tidak ada peringatan untuk Jawa Barat; test untuk Jawa Barat, `Update`, dan `Cancel` memakai dokumen sintetis yang ditulis di kode test dan ditandai begitu.

- `nowcast-rss-id.xml`: RSS nasional bahasa Indonesia, utuh (11 peringatan).
- `cap-id-CGT20260924002_alert.xml`, `cap-en-…`: peringatan Gorontalo, 4 poligon, versi Indonesia dan Inggris.
- `cap-id-CSU20260924001_alert.xml`, `cap-en-…`: peringatan Sumatera Utara, 150 poligon dan 3.072 titik; dokumen terbesar di rekaman.

## Prakiraan cuaca adm4 (2026-09-24 07.20 UTC)

Diambil dari `https://api.bmkg.go.id/publik/prakiraan-cuaca?adm4=<kode>`.

- `prakiraan-32.73.01.1001.json` (Sukarasa, Kota Bandung), `prakiraan-32.04.05.2001.json` (Kab. Bandung), `prakiraan-32.18.01.2001.json` (Kab. Pangandaran): payload utuh, 20 langkah per 3 jam.
- `prakiraan-404.json`: jawaban untuk kode yang tidak dikenal BMKG (HTTP 404).

Rekaman utuh untuk uji replay dibuat dengan `make ingest-record` dan disimpan di arsip ingest, bukan di sini.
