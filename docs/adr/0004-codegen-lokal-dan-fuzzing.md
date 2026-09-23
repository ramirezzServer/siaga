# 0004. Codegen lokal dan fuzzing bawaan Go

Tanggal: 2026-09-23 · Status: diterima

## Keputusan

- Plugin `buf generate` dijalankan lokal (protoc-gen-go lewat `go tool`, protoc-gen-es dari npm), bukan remote plugin Buf Schema Registry. Generate bisa diulang persis tanpa jaringan dan tanpa akun.
- Property-based testing di Go memakai fuzzing bawaan (`testing.F`) alih-alih library luar. Seed corpus jalan di setiap `go test`; fuzzing singkat jalan di CI. Ini mengganti rencana awal di dokumen arsitektur yang menyebut `rapid`.
- Klien Dart di-generate di CI dari OpenAPI (`openapi-generator` dart-dio) dan dianalisis `dart analyze`. Belum di-commit sampai app Flutter dimulai di fase 6.

## Konsekuensi

Satu dependensi luar lebih sedikit di Go. Fuzzing menemukan input yang membuat panik, bukan hanya pelanggaran properti, dan korpus kegagalan otomatis tersimpan di `testdata/fuzz` untuk regresi.
