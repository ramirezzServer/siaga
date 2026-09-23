#!/usr/bin/env bash
# Memeriksa prasyarat pengembangan. Tidak memasang apa pun; hanya melaporkan.
set -uo pipefail

ok=0; missing=0
check() { # nama, perintah-versi, petunjuk, wajib(1)/opsional(0)
  local name="$1" cmd="$2" hint="$3" required="$4"
  if out=$(eval "$cmd" 2>/dev/null | head -1) && [[ -n "$out" ]]; then
    printf '  \033[32m✓\033[0m %-14s %s\n' "$name" "$out"; ok=$((ok+1))
  elif [[ "$required" == 1 ]]; then
    printf '  \033[31m✗\033[0m %-14s belum ada — %s\n' "$name" "$hint"; missing=$((missing+1))
  else
    printf '  \033[33m-\033[0m %-14s opsional — %s\n' "$name" "$hint"
  fi
}

if grep -qi microsoft /proc/version 2>/dev/null; then
  echo "Lingkungan: WSL2"
  case "$PWD" in /mnt/*) echo "  ! Repo ada di drive Windows ($PWD). Pindahkan ke ~/ di WSL agar build dan file watcher cepat." ;; esac
fi

echo "Wajib (profil lite):"
check docker   "docker version --format '{{.Server.Version}}'" "Docker Desktop + WSL integration (docs/setup/windows-wsl2.md)" 1
check compose  "docker compose version --short" "ikut Docker Desktop" 1
check git      "git --version" "sudo apt install git" 1
check make     "make --version" "sudo apt install make" 1
check go       "go version" "https://go.dev/dl (>= 1.24)" 1
check node     "node --version" "nvm install 22" 1
check pnpm     "pnpm --version" "corepack enable" 1
check openssl  "openssl version" "sudo apt install openssl" 1
check golangci "golangci-lint version" "https://golangci-lint.run/welcome/install/" 1
check gitleaks "gitleaks version" "https://github.com/gitleaks/gitleaks#installing" 1

echo "Profil full (k3d + Tilt):"
check k3d      "k3d version" "https://k3d.io" 0
check kubectl  "kubectl version --client -o yaml | grep gitVersion" "https://kubernetes.io/docs/tasks/tools/" 0
check helm     "helm version --short" "https://helm.sh/docs/intro/install/" 0
check tilt     "tilt version" "https://docs.tilt.dev/install.html" 0

echo "Secret produksi:"
check sops     "sops --version" "https://github.com/getsops/sops/releases" 0
check age      "age --version" "sudo apt install age" 0

echo
if (( missing > 0 )); then
  echo "$missing alat wajib belum terpasang."
  exit 1
fi
echo "Semua alat wajib siap."
