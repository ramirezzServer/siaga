#!/usr/bin/env bash
# Membuat secret di cluster lokal (k3d) bila belum ada. Nama dan key sama
# dengan secret produksi (deploy/k8s/prod/secrets.enc.yaml, ADR 0017), tetapi
# nilainya hanya hidup di cluster lokal dan tidak pernah ditulis ke repo:
#
#   siaga-db-<svc>        password role database, acak
#   siaga-garage          secret Garage dan access key arsip, dari .env (sama dengan Compose)
#   siaga-ingest          key OpenAQ/FIRMS dari .env bila ada
#   siaga-otel-collector  tujuan OTLP Collector dari OTEL_EXPORTER_OTLP_* di .env
#
# Secret yang sudah ada dibiarkan; hapus dengan kubectl untuk membuat ulang.
set -euo pipefail
NS=siaga
kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"

# Nilai .env dibaca tanpa menjalankan isinya.
dotenv() {
  [[ -f .env ]] || return 0
  grep -E "^$1=" .env | tail -n1 | cut -d= -f2- || true
}
exists() { kubectl -n "$NS" get secret "$1" >/dev/null 2>&1; }

for svc in geo core alert ai tiles; do
  name="siaga-db-$svc"
  exists "$name" && continue
  kubectl -n "$NS" create secret generic "$name" \
    --type=kubernetes.io/basic-auth \
    --from-literal=username="siaga_$svc" \
    --from-literal=password="$(openssl rand -hex 24)" >/dev/null
  kubectl -n "$NS" label secret "$name" cnpg.io/reload=true >/dev/null
  echo "secret $name dibuat"
done

# Garage: nilai sama dengan Compose lite bila .env ada, supaya satu set
# kredensial arsip di laptop; tanpa .env dibuat acak.
if ! exists siaga-garage; then
  or_random() { # nilai, ukuran-hex, awalan
    if [[ -n "$1" && "$1" != ganti-saya* ]]; then printf '%s' "$1"; else printf '%s%s' "${3:-}" "$(openssl rand -hex "$2")"; fi
  }
  kubectl -n "$NS" create secret generic siaga-garage \
    --from-literal=GARAGE_RPC_SECRET="$(or_random "$(dotenv GARAGE_RPC_SECRET)" 32)" \
    --from-literal=GARAGE_ADMIN_TOKEN="$(or_random "$(dotenv GARAGE_ADMIN_TOKEN)" 24)" \
    --from-literal=GARAGE_METRICS_TOKEN="$(or_random "$(dotenv GARAGE_METRICS_TOKEN)" 24)" \
    --from-literal=GARAGE_DEFAULT_ACCESS_KEY="$(or_random "$(dotenv ARCHIVE_S3_ACCESS_KEY_ID)" 12 GK)" \
    --from-literal=GARAGE_DEFAULT_SECRET_KEY="$(or_random "$(dotenv ARCHIVE_S3_SECRET_ACCESS_KEY)" 32)" >/dev/null
  echo "secret siaga-garage dibuat"
fi

# Key sumber untuk ingest.
if ! exists siaga-ingest; then
  args=()
  for key in OPENAQ_API_KEY FIRMS_MAP_KEY; do
    value=$(dotenv "$key")
    if [[ -n "$value" ]]; then args+=("--from-literal=${key}=${value}"); fi
  done
  if ((${#args[@]} > 0)); then
    kubectl -n "$NS" create secret generic siaga-ingest "${args[@]}" >/dev/null
    echo "secret siaga-ingest dibuat (${#args[@]} key dari .env)"
  else
    echo "secret siaga-ingest tidak dibuat: OPENAQ_API_KEY/FIRMS_MAP_KEY kosong di .env (konektornya tidak jalan)"
  fi
fi

# Tujuan OTLP Collector. Grafana Cloud di .env dipakai apa adanya; Grafana
# lokal (127.0.0.1/localhost, make obs-up) diakses dari cluster lewat
# host.k3d.internal. Header .env berbentuk Authorization=Basic%20<base64>.
if ! exists siaga-otel-collector; then
  endpoint=$(dotenv OTEL_EXPORTER_OTLP_ENDPOINT)
  endpoint=${endpoint:-http://127.0.0.1:4318}
  endpoint=$(sed -E 's#^(https?://)(127\.0\.0\.1|localhost)([:/]|$)#\1host.k3d.internal\3#' <<<"$endpoint")
  auth=""
  headers=$(dotenv OTEL_EXPORTER_OTLP_HEADERS)
  IFS=',' read -ra pairs <<<"$headers"
  for pair in "${pairs[@]}"; do
    if [[ "${pair%%=*}" == Authorization ]]; then auth=${pair#*=}; fi
  done
  auth=${auth//%20/ }
  kubectl -n "$NS" create secret generic siaga-otel-collector \
    --from-literal=OTLP_UPSTREAM_ENDPOINT="$endpoint" \
    --from-literal=OTLP_UPSTREAM_AUTHORIZATION="$auth" >/dev/null
  echo "secret siaga-otel-collector dibuat (tujuan $endpoint)"
fi
