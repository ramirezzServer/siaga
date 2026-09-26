#!/usr/bin/env bash
# Memastikan setiap file secret *.enc.yaml di deploy/ benar-benar terenkripsi
# SOPS sebelum di-commit atau di-deploy: ada metadata sops dengan recipient age
# dan MAC, dan setiap nilai di bawah data:/stringData: berbentuk ENC[...].
# Tidak butuh kunci age, jadi aman dijalankan di CI.
set -euo pipefail
shopt -s globstar nullglob

files=(deploy/**/*.enc.yaml deploy/**/*.enc.yml)
if ((${#files[@]} == 0)); then
  echo "sops-check: tidak ada file *.enc.yaml"
  exit 0
fi

fail=0
for f in "${files[@]}"; do
  problems=$(awk '
    function indent(s) { match(s, /^ */); return RLENGTH }
    /^sops:/ { sops = 1 }
    sops && /^[[:space:]]+(- )?recipient: age1/ { age = 1 }
    sops && /^[[:space:]]+mac: ENC\[/ { mac = 1 }
    {
      if (block && NF > 0 && indent($0) <= block_indent) block = 0
      if (block && $0 ~ /:[[:space:]]*[^[:space:]]/) {
        value = $0; sub(/^[^:]*:[[:space:]]*/, "", value)
        if (value !~ /^ENC\[AES256_GCM,/) { printf "  baris %d: nilai tidak terenkripsi\n", NR; bad = 1 }
      }
      if ($0 ~ /^[[:space:]]*(data|stringData):[[:space:]]*$/) { block = 1; block_indent = indent($0); blocks++ }
    }
    END {
      if (!sops) print "  metadata sops tidak ada"
      if (!age) print "  recipient age tidak ada"
      if (!mac) print "  MAC tidak ada"
      if (!blocks) print "  tidak ada blok data/stringData"
    }
  ' "$f")
  if [[ -n "$problems" ]]; then
    echo "✗ $f"
    echo "$problems"
    fail=1
  else
    echo "✓ $f"
  fi
done
exit "$fail"
