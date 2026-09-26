#!/usr/bin/env bash
# Uji asap image layanan Go: setiap binary bisa dijalankan di image distroless,
# user bukan root, dan folder migrasi ikut di image geo-processor.
# Pemakaian: scripts/image-smoke.sh [--ingest <image>] [--geo <image>]
# (minimal satu; job CI menguji tiap layanan di job matrix sendiri).
set -euo pipefail
ingest="" geo=""
while (($#)); do
  case "$1" in
    --ingest) ingest="${2:?}"; shift 2 ;;
    --geo) geo="${2:?}"; shift 2 ;;
    *) echo "argumen tidak dikenal: $1" >&2; exit 2 ;;
  esac
done
[[ -n "$ingest$geo" ]] || { echo "pemakaian: $0 [--ingest <image>] [--geo <image>]" >&2; exit 2; }
fail=0

# expect <deskripsi> <pola keluaran> <image> <entrypoint> [argumen...]
# Kode keluar diabaikan (misal -h), yang dicek isi keluarannya.
expect() {
  local desc="$1" pattern="$2" image="$3" entry="$4"
  shift 4
  local out
  out=$(docker run --rm --network none --read-only --entrypoint "$entry" "$image" "$@" 2>&1 || true)
  if grep -qE -- "$pattern" <<<"$out"; then
    echo "ok    $desc"
  else
    echo "GAGAL $desc: keluaran tidak memuat /$pattern/" >&2
    sed 's/^/      /' <<<"$out" | head -20 >&2
    fail=1
  fi
}

if [[ -n "$ingest" ]]; then
  expect "ingest -h"                 '-once'        "$ingest" /usr/local/bin/ingest -h
  expect "archive tanpa subperintah" 'verify'       "$ingest" /usr/local/bin/archive
  expect "replay -h"                 '-connectors'  "$ingest" /usr/local/bin/replay -h
fi
if [[ -n "$geo" ]]; then
  expect "geo-processor tanpa env"   'DATABASE_URL' "$geo"    /usr/local/bin/geo-processor
  expect "import-regions -h"         '-province'    "$geo"    /usr/local/bin/import-regions -h
  expect "goose -version"            'v3\.[0-9]+'   "$geo"    /usr/local/bin/goose -version
fi

for image in $ingest $geo; do
  user=$(docker image inspect -f '{{.Config.User}}' "$image")
  if [[ "$user" == "65532:65532" ]]; then
    echo "ok    user $user ($image)"
  else
    echo "GAGAL user image $image = '$user', harus 65532:65532" >&2
    fail=1
  fi
done

if [[ -z "$geo" ]]; then exit "$fail"; fi

# Folder migrasi di image harus sama persis dengan repo.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cid=$(docker create "$geo")
docker cp -q "$cid:/migrations" "$tmp/migrations"
docker rm -f "$cid" >/dev/null
root=$(git rev-parse --show-toplevel)
if diff -r "$root/services/geo-processor/migrations" "$tmp/migrations" >/dev/null; then
  echo "ok    /migrations ($(find "$tmp/migrations" -name '*.sql' | wc -l) file)"
else
  echo "GAGAL /migrations di image berbeda dengan services/geo-processor/migrations" >&2
  fail=1
fi
exit "$fail"
