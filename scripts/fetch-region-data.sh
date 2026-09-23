#!/usr/bin/env bash
# Mengunduh dataset batas wilayah cahyadsn/wilayah_boundaries (MIT) pada commit terkunci,
# hanya folder yang dibutuhkan (sparse + partial clone, tidak mengunduh ~470 MB penuh).
set -euo pipefail

REPO_URL="https://github.com/cahyadsn/wilayah_boundaries.git"
# Commit 2026-08-20. Ganti hanya lewat PR, lalu jalankan ulang import dan tinjau laporan "stale".
COMMIT="3c5a7960f4d227a38489094d84252bdbb3f978a5"
PROVINCE="${1:-32}"
DEST="${DEST:-.cache/wilayah_boundaries}"

if [[ ! "$PROVINCE" =~ ^[0-9]{2}$ ]]; then
  echo "kode provinsi harus 2 digit, contoh: 32" >&2
  exit 2
fi

if [[ -f "$DEST/.source-version" && "$(cat "$DEST/.source-version")" == "$COMMIT" && -d "$DEST/db/kel/$PROVINCE" ]]; then
  echo "Data sudah ada di $DEST (commit ${COMMIT:0:12})"
  exit 0
fi

rm -rf "$DEST"
mkdir -p "$DEST"
git -C "$DEST" init -q
git -C "$DEST" remote add origin "$REPO_URL"
git -C "$DEST" sparse-checkout set db/prov db/kab db/kec "db/kel/$PROVINCE"
git -C "$DEST" fetch -q --depth 1 --filter=blob:none origin "$COMMIT"
git -C "$DEST" checkout -q FETCH_HEAD
echo "$COMMIT" > "$DEST/.source-version"
echo "Data provinsi $PROVINCE siap di $DEST ($(du -sh "$DEST/db" | cut -f1))"
