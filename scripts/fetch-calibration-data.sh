#!/usr/bin/env bash
# Mengunduh katalog historis untuk kalibrasi deduplikasi gempa (make calibrate-dedup).
# Kedua sumber dikunci ke commit supaya laporan kalibrasi bisa diulang persis:
#   - BMKG: katalog RepoGempa versi CSV lama (2008-11 s.d. 2023-01) dari
#     kekavigi/repo-gempa, kompilasi CC BY 4.0; data gempanya milik BMKG.
#   - USGS: keluaran layanan FDSN USGS (format CSV, M >= 2,5, kotak Indonesia)
#     yang disimpan di horankev/quake_data; data USGS berstatus domain publik.
# Hanya file yang dibutuhkan yang diunduh (sparse + partial clone).
set -euo pipefail

DEST="${DEST:-.cache/calibration}"
BMKG_REPO="https://github.com/kekavigi/repo-gempa.git"
BMKG_COMMIT="539ed6e461c409da1475781fd57bbc6deaef66ef"
BMKG_SHA256="fb8afdcbc824e502c3b9901ab1a1a7b3c85f6cbe6c3d73fb7432c88c5c7929af"
USGS_REPO="https://github.com/horankev/quake_data.git"
USGS_COMMIT="902191fb81b02430f21e2d13bfc5108087ff80a8"

fetch() { # fetch <repo> <commit> <dir> <path...>
  local repo=$1 commit=$2 dir=$3
  shift 3
  if [[ -f "$dir/.commit" && "$(cat "$dir/.commit")" == "$commit" ]]; then
    return
  fi
  rm -rf "$dir"
  mkdir -p "$dir"
  git -C "$dir" init -q
  git -C "$dir" remote add origin "$repo"
  git -C "$dir" sparse-checkout set --no-cone "$@"
  git -C "$dir" fetch -q --depth 1 --filter=blob:none origin "$commit"
  git -C "$dir" checkout -q FETCH_HEAD
  echo "$commit" > "$dir/.commit"
}

fetch "$BMKG_REPO" "$BMKG_COMMIT" "$DEST/bmkg" /katalog_gempa/katalog_gempa.csv
fetch "$USGS_REPO" "$USGS_COMMIT" "$DEST/usgs" /rawdata/query_2008_2015.csv /rawdata/query_2016_2023.csv

echo "$BMKG_SHA256  $DEST/bmkg/katalog_gempa/katalog_gempa.csv" | sha256sum -c --quiet -
echo "Katalog kalibrasi siap di $DEST"
