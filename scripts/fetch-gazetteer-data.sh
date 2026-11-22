#!/usr/bin/env bash
# Mengunduh daftar kode dan nama wilayah Kemendagri seluruh Indonesia (provinsi
# sampai desa/kelurahan) dari cahyadsn/wilayah (MIT) pada commit terkunci, untuk
# mencocokkan nama wilayah di kolom "Dirasakan" BMKG (make felt-radius, ADR 0026).
# Batas wilayah (provinsi, kab/kota, kecamatan) diambil scripts/fetch-region-data.sh.
set -euo pipefail

REPO_URL="https://github.com/cahyadsn/wilayah.git"
# Commit 2026-11-10. Ganti hanya bersama laporan radius-dirasakan.md yang dihasilkan ulang.
COMMIT="c2eff0881e3c75b8822f8c84ea9d23affc11aadb"
SHA256="c4c3396d9380d4edee072af1d9dff83573b574d7cd00a6562cf82e200e954031"
DEST="${DEST:-.cache/wilayah}"

if [[ -f "$DEST/.source-version" && "$(cat "$DEST/.source-version")" == "$COMMIT" && -f "$DEST/db/wilayah.sql" ]]; then
  echo "$SHA256  $DEST/db/wilayah.sql" | sha256sum -c --quiet -
  echo "Nama wilayah sudah ada di $DEST (commit ${COMMIT:0:12})"
  exit 0
fi

rm -rf "$DEST"
mkdir -p "$DEST"
git -C "$DEST" init -q
git -C "$DEST" remote add origin "$REPO_URL"
git -C "$DEST" sparse-checkout set --no-cone /db/wilayah.sql
git -C "$DEST" fetch -q --depth 1 --filter=blob:none origin "$COMMIT"
git -C "$DEST" checkout -q FETCH_HEAD
echo "$SHA256  $DEST/db/wilayah.sql" | sha256sum -c --quiet -
echo "$COMMIT" > "$DEST/.source-version"
echo "Nama wilayah siap di $DEST/db/wilayah.sql"
