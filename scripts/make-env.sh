#!/usr/bin/env bash
# Membuat .env dari .env.example dengan password acak. Tidak menimpa .env yang sudah ada.
set -euo pipefail
if [[ -f .env ]]; then
  echo ".env sudah ada, dibiarkan."
  exit 0
fi
while IFS= read -r line; do
  if [[ "$line" == *"=ganti-saya" ]]; then
    printf '%s=%s\n' "${line%%=*}" "$(openssl rand -hex 24)"
  else
    printf '%s\n' "$line"
  fi
done < .env.example > .env
chmod 600 .env
echo ".env dibuat dengan password acak."
