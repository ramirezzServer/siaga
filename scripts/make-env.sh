#!/usr/bin/env bash
# Membuat .env dari .env.example dengan rahasia acak. Bila .env sudah ada,
# variabel baru dari .env.example (misal setelah pull fase berikutnya)
# ditambahkan di akhir; nilai yang sudah ada tidak pernah diubah.
#
# Penanda nilai di .env.example:
#   ganti-saya         48 karakter heksadesimal (password)
#   ganti-saya-hex32   64 karakter heksadesimal (32 byte, misal rpc_secret Garage)
#   ganti-saya-gk      ID access key Garage: "GK" + 24 karakter heksadesimal
set -euo pipefail

value() {
  local line=$1
  case "$line" in
    *=ganti-saya-hex32) printf '%s=%s\n' "${line%%=*}" "$(openssl rand -hex 32)" ;;
    *=ganti-saya-gk) printf '%s=GK%s\n' "${line%%=*}" "$(openssl rand -hex 12)" ;;
    *=ganti-saya) printf '%s=%s\n' "${line%%=*}" "$(openssl rand -hex 24)" ;;
    *) printf '%s\n' "$line" ;;
  esac
}

if [[ ! -f .env ]]; then
  while IFS= read -r line; do value "$line"; done < .env.example > .env
  chmod 600 .env
  echo ".env dibuat dengan rahasia acak."
  exit 0
fi

added=()
block=""
while IFS= read -r line; do
  [[ "$line" =~ ^([A-Z][A-Z0-9_]*)= ]] || continue
  name=${BASH_REMATCH[1]}
  if ! grep -qE "^${name}=" .env; then
    block+="$(value "$line")"$'\n'
    added+=("$name")
  fi
done < .env.example
if ((${#added[@]} == 0)); then
  echo ".env sudah ada dan lengkap, dibiarkan."
  exit 0
fi
{
  printf '\n# Ditambahkan make env %s (variabel baru di .env.example)\n' "$(date -u +%Y-%m-%d)"
  printf '%s' "$block"
} >> .env
echo ".env: ditambahkan ${added[*]}."
