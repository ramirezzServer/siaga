# 0005. Go 1.26 sebagai versi minimum

Tanggal: 2026-09-24 · Status: diterima

## Konteks

goose v3.28.0, runner migrasi yang dipakai lewat `go tool goose`, mensyaratkan Go 1.26. Fondasi fase 0 menulis `go 1.24.0` di `go.work` dan semua `go.mod`, sehingga `go mod tidy` menaikkan `go.mod` geo-processor sendiri dan workspace menjadi tidak konsisten. Go 1.24 dan 1.25 juga sudah tidak menerima patch keamanan sejak Go 1.27 rilis.

## Keputusan

- Versi minimum bahasa: `go 1.26.0` di `go.work` dan semua `go.mod`.
- Toolchain pengembangan dan CI: `toolchain go1.27.1` di `go.work`, dibaca oleh `actions/setup-go`.
- golangci-lint v2.13.2 di lokal dan CI. Versi 2.5.0 di-build dengan Go 1.25 dan tidak bisa menganalisis kode Go 1.26.
- protoc-gen-go dijalankan dengan `GOWORK=off go -C libs/go/contracts tool protoc-gen-go`, karena flag `-modfile` tidak boleh dipakai dalam workspace mode.

## Konsekuensi

Kontributor butuh Go 1.26 atau lebih baru; `make doctor` memeriksanya. Kenaikan toolchain berikutnya cukup mengubah baris `toolchain` di `go.work`.
