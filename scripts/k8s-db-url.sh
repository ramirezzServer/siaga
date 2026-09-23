#!/usr/bin/env bash
# Mencetak DATABASE_URL role layanan (default geo) untuk port-forward lokal di 15432.
set -euo pipefail
svc="${1:-geo}"
pw=$(kubectl -n siaga get secret "siaga-db-$svc" -o jsonpath='{.data.password}' | base64 -d)
printf 'postgres://siaga_%s:%s@localhost:15432/siaga?sslmode=disable\n' "$svc" "$pw"
