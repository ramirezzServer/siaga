#!/usr/bin/env bash
# Menarik image yang gagal ditarik node k3d (ErrImagePull/ImagePullBackOff)
# lewat Docker host, mengimpornya ke cluster, lalu menghapus pod yang
# tertahan supaya controller-nya langsung membuat ulang dengan image lokal.
#
# Di Docker Desktop + WSL2, DNS dari dalam container node k3d kadang
# putus-sambung (lookup registry-1.docker.io gagal) walau Docker host bisa
# menarik image yang sama. Aman diulang; pod yang dihapus hanya pod yang
# belum pernah jalan karena image-nya belum ada.
set -euo pipefail
CLUSTER=${CLUSTER:-siaga}

stuck() { # "namespace pod image" per baris untuk container yang menunggu image
  kubectl get pods -A -o json | python3 -c '
import json, sys
for p in json.load(sys.stdin)["items"]:
    st = p.get("status", {})
    for cs in st.get("initContainerStatuses", []) + st.get("containerStatuses", []):
        reason = ((cs.get("state") or {}).get("waiting") or {}).get("reason")
        if reason in ("ErrImagePull", "ImagePullBackOff"):
            print(p["metadata"]["namespace"], p["metadata"]["name"], cs["image"])
'
}

mapfile -t rows < <(stuck)
if ((${#rows[@]} == 0)); then
  echo "Tidak ada pod yang tertahan menarik image."
  exit 0
fi
mapfile -t images < <(printf '%s\n' "${rows[@]}" | awk '{print $3}' | sort -u)
echo "Image yang gagal ditarik node k3d:"
printf '  %s\n' "${images[@]}"

for img in "${images[@]}"; do
  for i in 1 2 3 4 5; do
    if docker pull -q "$img"; then break; fi
    if ((i == 5)); then
      echo "docker pull $img gagal 5 kali; cek koneksi Docker Desktop lalu ulangi." >&2
      exit 1
    fi
    sleep $((i * 5))
  done
done

# `k3d image import` gagal di Docker Desktop dengan containerd image store
# ("content digest … not found"): docker save menyertakan indeks
# multi-platform, padahal hanya layer satu platform yang ditarik. Jadi
# ekspor hanya platform node lalu impor langsung dengan ctr di setiap node.
arch=$(docker info --format '{{.Architecture}}')
case "$arch" in
  x86_64 | amd64) platform=linux/amd64 ;;
  aarch64 | arm64) platform=linux/arm64 ;;
  *) platform="linux/$arch" ;;
esac
mapfile -t nodes < <(docker ps --format '{{.Names}}' | grep -E "^k3d-$CLUSTER-(server|agent)-[0-9]+$")
if ((${#nodes[@]} == 0)); then
  echo "Node k3d-$CLUSTER-server-* tidak ditemukan; cluster jalan?" >&2
  exit 1
fi
for node in "${nodes[@]}"; do
  echo "impor ke $node ($platform)"
  docker save --platform "$platform" "${images[@]}" |
    docker exec -i "$node" ctr -n k8s.io images import --platform "$platform" -
  for img in "${images[@]}"; do
    ref=$img
    [[ $ref == */* ]] || ref="library/$ref"
    [[ ${ref%%/*} == *.* ]] || ref="docker.io/$ref"
    if ! docker exec "$node" ctr -n k8s.io images ls -q | grep -qxF "$ref"; then
      echo "$ref belum ada di $node setelah impor." >&2
      exit 1
    fi
  done
done

printf '%s\n' "${rows[@]}" | awk '{print $1, $2}' | sort -u | while read -r ns pod; do
  kubectl -n "$ns" delete pod "$pod" --wait=false
done
echo "Selesai. Cek lagi: kubectl get pods -A"
