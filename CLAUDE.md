# SIAGA — konteks untuk asisten AI

Baca `docs/HANDOFF.md` dulu: di sana status terakhir dan langkah berikutnya.

## Aturan proyek

- Bahasa: komentar kode, pesan error, dokumen, dan commit body dalam bahasa Indonesia. Identifier kode dalam bahasa Inggris.
- Biaya operasional Rp0: jangan menambah layanan atau API berbayar.
- Arsitektur hexagonal per layanan: `internal/domain` (fungsi murni, tanpa I/O), `internal/app` (use case), `internal/ports` (interface), `internal/adapters` (implementasi), `cmd` (merakit dependensi saja).
- Invarian dijaga dua kali: di kode domain dan di constraint PostgreSQL.
- Waktu disimpan UTC (`timestamptz`), ditampilkan WIB.
- Satu schema database per layanan, satu role pemilik per schema. Tidak ada layanan yang menulis ke schema milik layanan lain.
- Kode hasil generate (`**/gen/**`) tidak diedit manual; ubah kontrak lalu `make gen`.
- Secret tidak pernah di-commit. Lokal: `.env` (dibuat `make env`) dan secret acak di cluster. Produksi: SOPS + age.
- Conventional Commits dengan scope dari `commitlint.config.mjs`.

## Perintah

- `make check` sebelum commit; `make test-integration` butuh `make up migrate`.
- Test fuzz: `make fuzz`. Property test memakai fuzzing bawaan Go (`testing.F`), bukan library luar.

## Gerbang kualitas

- Go: golangci-lint (config per modul), `go test -race`, coverage domain + app >= 85%.
- TypeScript: `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, ESLint `strictTypeChecked`.
- Proto: `buf lint` STANDARD, `buf breaking` WIRE_JSON. OpenAPI: Redocly `recommended`.
