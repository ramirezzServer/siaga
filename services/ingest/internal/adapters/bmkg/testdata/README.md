# Rekaman payload BMKG

Diambil dari `https://data.bmkg.go.id/DataMKG/TEWS/` pada 2026-09-24. Sumber: BMKG.

- `autogempa.json`: payload utuh.
- `gempaterkini.json`, `gempadirasakan.json`: dua entri pertama dari 15, sisanya dipotong supaya test mudah dibaca. Strukturnya tidak diubah.

Rekaman utuh untuk uji replay dibuat dengan `make ingest-record` dan disimpan di arsip ingest, bukan di sini.
