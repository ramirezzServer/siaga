#!/usr/bin/env bash
# Membuat secret di cluster lokal bila belum ada: password role database (acak,
# hanya hidup di cluster) dan siaga-ingest (key OpenAQ/FIRMS dari .env bila ada).
# Tidak pernah menulis rahasia ke repo. Produksi memakai SOPS (fase 1e-2b-2).
set -euo pipefail
NS=siaga
kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"
for svc in geo core alert ai tiles; do
  name="siaga-db-$svc"
  if kubectl -n "$NS" get secret "$name" >/dev/null 2>&1; then
    continue
  fi
  kubectl -n "$NS" create secret generic "$name" \
    --type=kubernetes.io/basic-auth \
    --from-literal=username="siaga_$svc" \
    --from-literal=password="$(openssl rand -hex 24)" >/dev/null
  kubectl -n "$NS" label secret "$name" cnpg.io/reload=true >/dev/null
  echo "secret $name dibuat"
done

# Key sumber untuk ingest. Nilai dibaca dari .env tanpa menjalankan isinya.
if ! kubectl -n "$NS" get secret siaga-ingest >/dev/null 2>&1; then
  args=()
  if [[ -f .env ]]; then
    for key in OPENAQ_API_KEY FIRMS_MAP_KEY; do
      value=$(grep -E "^${key}=" .env | tail -n1 | cut -d= -f2- || true)
      if [[ -n "$value" ]]; then args+=("--from-literal=${key}=${value}"); fi
    done
  fi
  if ((${#args[@]} > 0)); then
    kubectl -n "$NS" create secret generic siaga-ingest "${args[@]}" >/dev/null
    echo "secret siaga-ingest dibuat (${#args[@]} key dari .env)"
  else
    echo "secret siaga-ingest tidak dibuat: OPENAQ_API_KEY/FIRMS_MAP_KEY kosong di .env (konektornya tidak jalan)"
  fi
fi
