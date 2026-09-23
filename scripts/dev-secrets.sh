#!/usr/bin/env bash
# Membuat secret password role database di cluster lokal bila belum ada.
# Password acak hanya hidup di cluster; tidak pernah ditulis ke repo.
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
