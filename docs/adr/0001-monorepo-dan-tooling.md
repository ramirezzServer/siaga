# 0001. Monorepo dengan tooling per bahasa

Tanggal: 2026-09-23 · Status: diterima

## Konteks

SIAGA terdiri dari layanan Go, TypeScript, Python, dan Dart yang berbagi kontrak (proto dan OpenAPI). Dikerjakan solo.

## Keputusan

Satu repo. Tiap ekosistem memakai alat bawaannya: Go workspace (`go.work`), pnpm + Turborepo untuk TypeScript, `uv` untuk Python (mulai fase 4), Melos untuk Flutter (fase 6), `buf` untuk proto. `Makefile` jadi satu pintu perintah. Alat CLI (buf, protoc-gen-es, redocly, prettier) dikunci sebagai devDependency npm; alat Go (goose, protoc-gen-go) dikunci lewat direktif `tool` di `go.mod`.

## Konsekuensi

Perubahan kontrak dan semua pemakainya masuk dalam satu PR, dan versi alat sama di laptop dan CI. Harga yang dibayar: CI lebih kompleks dan butuh beberapa toolchain.
