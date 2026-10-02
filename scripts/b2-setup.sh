#!/usr/bin/env bash
# Menyiapkan cadangan arsip payload mentah di Backblaze B2 (ADR 0022):
#
#   1. login sementara dengan master application key (isian terlihat dan
#      dikonfirmasi; tidak disimpan; sesi CLI di folder sementara yang dihapus
#      di akhir)
#   2. bucket privat dengan enkripsi SSE-B2, tanpa Object Lock
#   3. application key yang hanya berlaku untuk bucket itu dan awalan arsip/,
#      dengan hak listBuckets,listFiles,readFiles,writeFiles (tanpa deleteFiles:
#      key yang bocor tidak bisa menghapus cadangan)
#   4. ARCHIVE_BACKUP_URL dan ARCHIVE_BACKUP_S3_* ditulis ke .env tanpa
#      pernah dicetak ke layar
#
# Jalankan dari root repo setelah `make env`. Butuh CLI b2:
#   sudo apt install -y pipx && pipx install b2   (atau: uv tool install b2)
# Panduan lengkap: docs/setup/backblaze-b2.md
set -euo pipefail

B2=${B2:-b2}
KEY_NAME=${KEY_NAME:-siaga-cadangan-laptop}
PREFIX=arsip/
TARGET=${TARGET:-laptop}

if ! command -v "$B2" >/dev/null; then
  echo "CLI b2 belum terpasang: sudo apt install -y pipx && pipx install b2 (lihat docs/setup/backblaze-b2.md)" >&2
  exit 1
fi
[[ -f .env ]] || {
  echo "jalankan dari root repo setelah make env (.env tidak ditemukan)" >&2
  exit 1
}
if grep -qE '^ARCHIVE_BACKUP_S3_ACCESS_KEY_ID=.+' .env; then
  echo "ARCHIVE_BACKUP_S3_ACCESS_KEY_ID di .env sudah terisi; skrip berhenti supaya key lama tidak tertimpa." >&2
  echo "Untuk membuat ulang: kosongkan tiga baris ARCHIVE_BACKUP_* di .env, lalu jalankan lagi." >&2
  exit 1
fi

suggest="siaga-cadangan-$(openssl rand -hex 3)"
read -rp "Nama bucket (unik di seluruh B2, huruf kecil/angka/-) [$suggest]: " bucket
bucket=${bucket:-$suggest}
if [[ ! "$bucket" =~ ^[a-z0-9][a-z0-9-]{4,48}[a-z0-9]$ ]]; then
  echo "nama bucket tidak valid: $bucket" >&2
  exit 1
fi
echo "Master application key: halaman B2 → Application Keys → Generate New Master Application Key"
echo "(membuat master key baru membatalkan yang lama). Key ini hanya dipakai di skrip ini."
echo "Tempel satu per satu, lalu Enter. Isian terlihat supaya bisa dicek; spasi dan baris baru dibuang."

# clean membuang spasi, tab, CR, dan LF yang sering ikut tertempel dari browser.
clean() { printf '%s' "$1" | tr -d '[:space:]'; }
# mask menampilkan awal dan akhir key saja untuk konfirmasi.
mask() {
  local s=$1
  if ((${#s} <= 8)); then
    printf '%d karakter' "${#s}"
  else
    printf '%s…%s (%d karakter)' "${s:0:4}" "${s: -4}" "${#s}"
  fi
}
while :; do
  read -rp "keyID master          : " master_id
  read -rp "applicationKey master : " master_key
  master_id=$(clean "$master_id")
  master_key=$(clean "$master_key")
  echo
  echo "  keyID          : $master_id (${#master_id} karakter)"
  echo "  applicationKey : $(mask "$master_key")"
  [[ "$master_id" =~ ^[0-9a-f]{12}$|^[0-9a-f]{25}$ ]] ||
    echo "  peringatan: keyID biasanya 12 karakter heksadesimal (master) atau 25 karakter"
  ((${#master_key} >= 30)) ||
    echo "  peringatan: applicationKey biasanya 31 karakter; mungkin terpotong saat ditempel"
  read -rp "Sudah benar? [Y = lanjut / n = isi ulang / q = batal] " ok
  case "$ok" in
    [nN]*) continue ;;
    [qQ]*)
      echo "dibatalkan"
      exit 1
      ;;
    *) break ;;
  esac
done
echo "(Setelah selesai, jalankan 'clear' supaya key tidak tertinggal di layar.)"

session=$(mktemp -d)
trap 'rm -rf "$session"' EXIT
export B2_ACCOUNT_INFO="$session/akun.sqlite"

# Keluaran authorize memuat key dan token, jadi hanya s3endpoint yang diambil.
# Galat b2 (stderr) ditampilkan dengan key disamarkan.
if ! auth=$(B2_APPLICATION_KEY_ID="$master_id" B2_APPLICATION_KEY="$master_key" "$B2" account authorize 2>"$session/galat"); then
  err=$(<"$session/galat")
  err=${err//"$master_key"/***}
  echo "login B2 gagal: ${err:-tanpa pesan dari b2}" >&2
  echo "Periksa keyID dan applicationKey master (buat master key baru bila ragu), lalu jalankan lagi." >&2
  exit 1
fi
endpoint=$(AUTH="$auth" python3 -c 'import json, os; print(json.loads(os.environ["AUTH"])["s3endpoint"])' 2>/dev/null) || {
  echo "keluaran 'b2 account authorize' tidak dikenali (versi CLI b2: $("$B2" version 2>/dev/null || echo ?))" >&2
  exit 1
}
unset master_key auth
region=${endpoint#https://s3.}
region=${region%%.*}
if [[ ! "$region" =~ ^[a-z]+-[a-z]+-[0-9]+$ ]]; then
  echo "endpoint S3 tidak dikenali: $endpoint" >&2
  exit 1
fi

if "$B2" bucket get "$bucket" >/dev/null 2>&1; then
  echo "Bucket $bucket sudah ada di akun ini; dipakai apa adanya."
  create_bucket=0
else
  create_bucket=1
fi
echo
echo "Yang akan dibuat di akun B2 (region $region):"
((create_bucket)) && echo "  - bucket privat $bucket (SSE-B2, tanpa Object Lock)"
echo "  - application key $KEY_NAME: hanya bucket $bucket, awalan $PREFIX,"
echo "    hak listBuckets,listFiles,readFiles,writeFiles (tanpa deleteFiles)"
read -rp "Lanjut? [y/N] " ok
[[ "$ok" == [yYoO]* ]] || {
  echo "dibatalkan"
  exit 1
}

if ((create_bucket)); then
  "$B2" bucket create --default-server-side-encryption SSE-B2 "$bucket" allPrivate >/dev/null
  echo "bucket $bucket dibuat"
fi
created=$("$B2" key create --bucket "$bucket" --name-prefix "$PREFIX" "$KEY_NAME" listBuckets,listFiles,readFiles,writeFiles)
key_id=${created%% *}
app_key=${created#* }
"$B2" account clear >/dev/null 2>&1 || true
if [[ -z "$key_id" || -z "$app_key" || "$key_id" == "$app_key" ]]; then
  echo "keluaran b2 key create tidak dikenali; key mungkin sudah dibuat, hapus di halaman Application Keys lalu ulangi" >&2
  exit 1
fi

url="s3://$bucket/${PREFIX}${TARGET}/raw?endpoint=$endpoint&region=$region"
# .env diubah di tempat oleh Python; nilai lewat environment, bukan argumen
# (argumen proses terlihat di ps).
URL="$url" KEY_ID="$key_id" APP_KEY="$app_key" python3 - <<'PY'
import os
import re

values = {
    "ARCHIVE_BACKUP_URL": os.environ["URL"],
    "ARCHIVE_BACKUP_S3_ACCESS_KEY_ID": os.environ["KEY_ID"],
    "ARCHIVE_BACKUP_S3_SECRET_ACCESS_KEY": os.environ["APP_KEY"],
}
with open(".env") as f:
    lines = f.read().splitlines()
seen = set()
for i, line in enumerate(lines):
    m = re.match(r"^([A-Z0-9_]+)=", line)
    if m and m.group(1) in values:
        lines[i] = f"{m.group(1)}={values[m.group(1)]}"
        seen.add(m.group(1))
missing = [k for k in values if k not in seen]
if missing:
    lines.append("")
    lines.append("# Cadangan arsip Backblaze B2 (scripts/b2-setup.sh)")
    lines.extend(f"{k}={values[k]}" for k in missing)
tmp = ".env.b2-setup.tmp"
fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as f:
    f.write("\n".join(lines) + "\n")
os.replace(tmp, ".env")
PY
unset app_key
echo
echo ".env diisi: ARCHIVE_BACKUP_URL=$url"
echo "            ARCHIVE_BACKUP_S3_ACCESS_KEY_ID dan _SECRET_ACCESS_KEY (tidak ditampilkan)"
echo "Sesi CLI b2 sudah dihapus. Berikutnya: make archive-backup"
