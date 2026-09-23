#!/usr/bin/env bash
# Khusus Docker Compose: aktifkan login role layanan dengan password dari environment.
# Di Kubernetes, CloudNativePG melakukan hal yang sama lewat spec.managed.roles.
set -euo pipefail

declare -A roles=(
  [siaga_geo]="${SIAGA_GEO_PASSWORD:-}"
  [siaga_core]="${SIAGA_CORE_PASSWORD:-}"
  [siaga_alert]="${SIAGA_ALERT_PASSWORD:-}"
  [siaga_ai]="${SIAGA_AI_PASSWORD:-}"
  [siaga_tiles]="${SIAGA_TILES_PASSWORD:-}"
)

for role in "${!roles[@]}"; do
  password="${roles[$role]}"
  if [[ -z "$password" ]]; then
    echo "900_login_roles: password untuk $role kosong; jalankan 'make env' dulu" >&2
    exit 1
  fi
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -v role="$role" -v password="$password" <<'SQL'
ALTER ROLE :"role" LOGIN PASSWORD :'password';
SQL
done
