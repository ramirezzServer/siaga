#!/usr/bin/env bash
# Pemakaian memori (working set) dan CPU per pod di cluster k3d, untuk
# memperkirakan ukuran VM produksi. cluster.yaml mematikan metrics-server
# (meniru k3s produksi yang ramping), jadi `kubectl top` tidak jalan; angka
# diambil langsung dari endpoint /metrics/resource kubelet lewat API server,
# dua kali dengan jeda INTERVAL detik untuk menghitung CPU.
#
# Memori node = cgroup seluruh node k3d: pod + k3s (API server, kubelet,
# containerd, Traefik, CoreDNS) — angka terdekat dengan kebutuhan VM.
#
# Pemakaian: scripts/k3d-usage.sh            (make k3d-usage)
#            INTERVAL=30 scripts/k3d-usage.sh
set -euo pipefail
INTERVAL=${INTERVAL:-15}

node=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

kubectl get --raw "/api/v1/nodes/$node/proxy/metrics/resource" >"$tmp/a"
sleep "$INTERVAL"
kubectl get --raw "/api/v1/nodes/$node/proxy/metrics/resource" >"$tmp/b"

python3 - "$tmp/a" "$tmp/b" "$node" <<'PY'
import re
import sys

LINE = re.compile(r'^(\w+)(?:\{([^}]*)\})?\s+(\S+)(?:\s+(\d+))?$')
LABEL = re.compile(r'(\w+)="([^"]*)"')


def parse(path):
    out = {}
    for raw in open(path, encoding="utf-8"):
        m = LINE.match(raw.strip())
        if not m:
            continue
        name, labels, value, ts = m.groups()
        lab = dict(LABEL.findall(labels or ""))
        key = (name, lab.get("namespace", ""), lab.get("pod", ""))
        out[key] = (float(value), int(ts) / 1000 if ts else None)
    return out


a, b, node = parse(sys.argv[1]), parse(sys.argv[2]), sys.argv[3]


def cpu_milli(name, ns="", pod=""):
    k = (name, ns, pod)
    if k not in a or k not in b or a[k][1] is None or b[k][1] is None or b[k][1] <= a[k][1]:
        return None
    return (b[k][0] - a[k][0]) / (b[k][1] - a[k][1]) * 1000


MiB = 1024 * 1024
rows = []
for (name, ns, pod), (value, _) in b.items():
    if name != "pod_memory_working_set_bytes":
        continue
    rows.append((ns, pod, value / MiB, cpu_milli("pod_cpu_usage_seconds_total", ns, pod)))
rows.sort(key=lambda r: (r[0], -r[2]))

print(f"{'NAMESPACE':<14} {'POD':<44} {'MEM MiB':>8} {'CPU m':>7}")
per_ns = {}
for ns, pod, mem, cpu in rows:
    c = "-" if cpu is None else f"{cpu:.0f}"
    print(f"{ns:<14} {pod[:44]:<44} {mem:>8.1f} {c:>7}")
    t = per_ns.setdefault(ns, [0.0, 0.0])
    t[0] += mem
    t[1] += cpu or 0
print()
print(f"{'NAMESPACE':<14} {'TOTAL MEM MiB':>14} {'CPU m':>7}")
for ns, (mem, cpu) in sorted(per_ns.items()):
    print(f"{ns:<14} {mem:>14.1f} {cpu:>7.0f}")
pods_mem = sum(r[2] for r in rows)
pods_cpu = sum(r[3] or 0 for r in rows)
print(f"{'semua pod':<14} {pods_mem:>14.1f} {pods_cpu:>7.0f}")

node_mem = b.get(("node_memory_working_set_bytes", "", ""))
node_cpu = cpu_milli("node_cpu_usage_seconds_total")
if node_mem:
    c = "-" if node_cpu is None else f"{node_cpu:.0f}"
    print(f"{'node ' + node:<30} memori {node_mem[0] / MiB:.0f} MiB, CPU {c} m"
          f" (pod + k3s; selisih dengan semua pod ±{node_mem[0] / MiB - pods_mem:.0f} MiB)")
PY

echo
echo "Container k3d (docker stats, termasuk load balancer dan registry):"
mapfile -t containers < <(docker ps --filter 'name=^k3d-siaga' --format '{{.Names}}' 2>/dev/null || true)
if ((${#containers[@]} > 0)); then
  docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}' "${containers[@]}"
else
  echo "(tidak ada container k3d-siaga* atau docker tidak bisa dihubungi)"
fi
