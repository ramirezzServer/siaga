# Cadangan arsip ke Backblaze B2

Arsip payload mentah di Garage dikemas setiap hari ke Backblaze B2: satu bundel `tar.zst` per konektor per hari UTC, ditemani indeks berisi SHA-256 setiap payload (ADR 0022). Setelah objek tercatat di cadangan, `archive prune` boleh menghapusnya dari Garage sesuai aturan retensi.

B2 dipilih karena bisa dibuat tanpa kartu kredit: 10 GB pertama gratis dan API kompatibel S3. Proyeksi pemakaian ±2 GB per tahun bila ingest jalan 24 jam.

## Sekali: bucket, key, dan `.env`

1. **Batas biaya.** Masuk ke web B2 → **Caps & Alerts**. Isi keempat cap harian (penyimpanan, unduhan, Class B, Class C) dengan `$0` dan nyalakan alert email. Dengan cap $0, B2 menolak request yang melewati jatah gratis (galat 403) alih-alih menagih.

2. **CLI b2** di WSL (sekali):

   ```bash
   sudo apt install -y pipx && pipx ensurepath   # sekali; buka terminal baru setelahnya
   pipx install b2                                # atau: uv tool install b2
   b2 version
   ```

3. **Master application key.** Web B2 → **Application Keys** → **Generate New Master Application Key**. keyID dan applicationKey hanya ditampilkan sekali; biarkan tab itu terbuka untuk langkah berikutnya. Key ini tidak disimpan di repo atau `.env`.

4. **Buat bucket dan key cadangan** dari root repo:

   ```bash
   make env            # menambah ARCHIVE_BACKUP_* (kosong) ke .env lama
   make backup-setup   # scripts/b2-setup.sh
   ```

   Skrip menanyakan nama bucket (bawaan `siaga-cadangan-<acak>`, harus unik di seluruh B2), keyID master, dan applicationKey master (tidak tampil di layar). Sebelum membuat apa pun ia menampilkan rencananya dan meminta konfirmasi. Yang dibuat:

   - bucket **privat** dengan enkripsi SSE-B2, tanpa Object Lock;
   - application key `siaga-cadangan-laptop` yang hanya berlaku untuk bucket itu dan awalan `arsip/`, dengan hak `listBuckets,listFiles,readFiles,writeFiles`. Tanpa `deleteFiles`: key yang bocor tidak bisa menghapus cadangan.

   Key baru langsung ditulis ke `.env` (`ARCHIVE_BACKUP_URL`, `ARCHIVE_BACKUP_S3_ACCESS_KEY_ID`, `ARCHIVE_BACKUP_S3_SECRET_ACCESS_KEY`) tanpa dicetak, lalu sesi CLI dihapus. Simpan master key di password manager atau buat ulang di web B2 (membuat ulang master key tidak membatalkan key cadangan).

   Opsi akses di form web B2 (Read Only / Write Only / Read and Write) tidak bisa membuat kombinasi hak ini, karena itu key dibuat lewat CLI.

5. **Cadangan pertama dan pemeriksaan:**

   ```bash
   make archive-backup          # semua hari UTC yang sudah ditutup; ±1 menit untuk seminggu
   make archive-backup-verify   # unduh bundel dua hari terakhir, periksa setiap payload
   make archive-prune           # HANYA menghitung: apa yang akan dihapus dari Garage
   ```

   Periksa keluaran `archive-prune`: kolom "belum dicadangkan" harus 0, dan yang akan dihapus hanya `bmkg-prakiraan` (lebih tua dari 14 hari), `usgs-2.5-day` dan `openmeteo-cuaca/udara` (30 hari), `openmeteo-sungai/hujan` (90 hari). Arsip dimulai 24 Sep 2026, jadi prakiraan baru mulai kedaluwarsa 9 Okt dan USGS/Open-Meteo 25 Okt; sebelum itu daftarnya kosong. Bila sesuai: `make archive-prune ARGS=-apply`.

## Setiap memulai sesi

```bash
make archive-maintain   # backup → backup-verify → prune -apply
```

Ketiganya aman diulang dan mengejar hari yang tertinggal selama laptop mati. Di cluster produksi urutan yang sama dijalankan CronJob `archive-maintenance` setiap 01.20 UTC (08.20 WIB) dengan Secret `siaga-arsip-cadangan` dari `make secrets-prod` (awalan `arsip/produksi/`).

## Memulihkan

```bash
# Ke folder (misal untuk replay hari lama)
make archive-restore FROM=2026-09-26 TO=2026-09-27 CONNECTORS=bmkg-prakiraan INTO=/tmp/pulih
INGEST_ARCHIVE_URL=/tmp/pulih make replay FROM=2026-09-26 TO=2026-09-28 ARGS="-publish=false -strict"

# Kembali ke Garage (kunci yang sudah ada dilewati)
make archive-restore FROM=2026-09-26
```

Uji pulih sebulan sekali dengan perintah pertama di atas: pemulihan menulis kunci dan isi gzip yang sama byte per byte, jadi replay `-strict` harus lolos tanpa payload rusak.

## Mengganti key atau penyedia

- **Rotasi key:** hapus key `siaga-cadangan-laptop` di web B2 → Application Keys, kosongkan tiga baris `ARCHIVE_BACKUP_*` di `.env`, lalu `make backup-setup` lagi (pilih bucket yang sama).
- **Pindah ke penyedia S3 lain** (misal Oracle Object Storage di fase 2): pulihkan semua hari ke folder (`make archive-restore FROM=2026-09-24 INTO=/tmp/semua`), lalu `cd services/ingest && go run ./cmd/archive backup -archive /tmp/semua -backup '<URL baru>'` dengan kredensial baru di `ARCHIVE_BACKUP_S3_*`.

## Masalah umum

| Gejala                                         | Penyebab                                                                                                                               |
| ---------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| `kredensial cadangan belum diisi`              | `make backup-setup` belum dijalankan, atau `.env` lama belum mendapat variabel baru (`make env`)                                       |
| `AccessDenied` / 403 saat backup               | Awalan URL bukan di bawah `arsip/` (key dibatasi ke awalan itu), atau key sudah dihapus                                                |
| `SignatureDoesNotMatch`                        | `region=` di URL tidak sama dengan endpoint (misal endpoint `us-west-004` tetapi region lain)                                          |
| Galat setelah lama berjalan dengan pesan cap   | Cap harian di Caps & Alerts tercapai; periksa pemakaian di web B2                                                                      |
| `archive-prune` melaporkan "belum dicadangkan" | Objek itu baru muncul setelah cadangan hari itu dibuat; `make archive-backup` menambah bagian baru, lalu prune berikutnya menghapusnya |
