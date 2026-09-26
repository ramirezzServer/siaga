#!/usr/bin/env bash
# Membuat atau melengkapi secret produksi terenkripsi (SOPS + age, ADR 0017):
#
#   deploy/k8s/prod/secrets.enc.yaml   SopsSecret "siaga-secrets"; di cluster
#                                      didekripsi sops-secrets-operator menjadi
#                                      Secret biasa (siaga-db-*, siaga-garage,
#                                      siaga-ingest, siaga-otel-collector)
#
# Nilai yang sudah ada tidak pernah diubah; yang belum ada dibuat acak, key
# OpenAQ/FIRMS diambil dari .env, dan kredensial Grafana Cloud ditanyakan.
# Plaintext hanya lewat pipa ke sops, tidak pernah ditulis ke disk.
#
#   scripts/prod-secrets.sh             buat file / lengkapi nilai yang belum ada
#   scripts/prod-secrets.sh --grafana   ganti endpoint, instance ID, dan token Grafana Cloud
#
# Tanpa prompt (misal di skrip): isi GRAFANA_CLOUD_OTLP_ENDPOINT,
# GRAFANA_CLOUD_INSTANCE_ID, dan GRAFANA_CLOUD_TOKEN di environment.
# Kunci age: SOPS_AGE_KEY_FILE, bawaan ~/.config/sops/age/keys.txt (dibuat bila belum ada).
set -euo pipefail

FILE=deploy/k8s/prod/secrets.enc.yaml
KEY_FILE=${SOPS_AGE_KEY_FILE:-$HOME/.config/sops/age/keys.txt}
PLACEHOLDER=age1replace0with0your0public0key0000000000000000000000000000000

grafana=0
for arg in "$@"; do
  case "$arg" in
    --grafana) grafana=1 ;;
    -h | --help)
      sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "argumen tidak dikenal: $arg (lihat --help)" >&2
      exit 2
      ;;
  esac
done

for tool in sops age-keygen python3; do
  if ! command -v "$tool" >/dev/null; then
    echo "$tool belum terpasang (lihat docs/setup/secrets.md)" >&2
    exit 1
  fi
done
[[ -f .sops.yaml ]] || {
  echo "jalankan dari root repo (.sops.yaml tidak ditemukan)" >&2
  exit 1
}

# 1. Kunci age pribadi. Private key tidak pernah masuk repo.
if [[ ! -f "$KEY_FILE" ]]; then
  mkdir -p "$(dirname "$KEY_FILE")"
  (umask 077 && age-keygen -o "$KEY_FILE" 2>/dev/null)
  echo "Kunci age baru dibuat: $KEY_FILE"
  echo "  PENTING: simpan salinan isinya di password manager. Tanpa kunci ini"
  echo "  secret produksi tidak bisa dibuka atau diubah lagi."
fi
pub=$(age-keygen -y "$KEY_FILE")
export SOPS_AGE_KEY_FILE="$KEY_FILE"

# 2. Recipient di .sops.yaml.
if grep -q "$PLACEHOLDER" .sops.yaml; then
  sed -i "s/$PLACEHOLDER/$pub/" .sops.yaml
  echo ".sops.yaml: recipient diisi public key $pub"
elif ! grep -q "$pub" .sops.yaml; then
  echo "peringatan: public key $pub tidak ada di .sops.yaml; kunci ini tidak bisa membuka file yang dienkripsi" >&2
fi

# 3. Nilai lama (bila ada) didekripsi ke memori.
existing=""
if [[ -f "$FILE" ]]; then
  existing=$(sops decrypt --output-type json "$FILE")
fi

# 4. Susun plaintext (JSON) di Python, langsung dienkripsi sops lewat pipa.
out=$(mktemp "${FILE}.XXXXXX")
trap 'rm -f "$out"' EXIT
EXISTING="$existing" GRAFANA_PROMPT="$grafana" python3 - <<'PY' |
import base64
import getpass
import json
import os
import secrets
import sys

existing = os.environ.get("EXISTING", "").strip()
force_grafana = os.environ.get("GRAFANA_PROMPT") == "1"
old = {}
if existing:
    for t in json.loads(existing).get("spec", {}).get("secretTemplates", []):
        old[t["name"]] = t
report = []


def tty_input(prompt, secret=False):
    try:
        with open("/dev/tty", "r+") as tty:
            if secret:
                return getpass.getpass(prompt, stream=tty).strip()
            tty.write(prompt)
            tty.flush()
            return tty.readline().strip()
    except OSError:
        return ""  # tanpa terminal (CI, pipa): dianggap dilewati


def dotenv():
    values = {}
    try:
        with open(".env") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                values[k.strip()] = v.strip()
    except FileNotFoundError:
        pass
    return values


env = dotenv()


def value(secret_name, key, make, label=None):
    """Nilai lama bila ada, selain itu hasil make() (None = tidak diisi)."""
    cur = old.get(secret_name, {}).get("stringData", {}).get(key)
    if cur:
        return cur
    new = make()
    if new:
        report.append(f"{secret_name}.{key}: {label or 'dibuat acak'}")
    return new


def hex_(n):
    return lambda: secrets.token_hex(n)


templates = []

# Role database CloudNativePG (managed roles di postgres-cluster.yaml).
for svc in ["geo", "core", "alert", "ai", "tiles"]:
    name = f"siaga-db-{svc}"
    templates.append(
        {
            "name": name,
            "type": "kubernetes.io/basic-auth",
            "labels": {"cnpg.io/reload": "true"},
            "stringData": {
                "username": f"siaga_{svc}",
                "password": value(name, "password", hex_(24)),
            },
        }
    )

# Garage: secret RPC/admin/metrik dan access key bucket arsip (juga dipakai ingest).
garage = {
    "GARAGE_RPC_SECRET": value("siaga-garage", "GARAGE_RPC_SECRET", hex_(32)),
    "GARAGE_ADMIN_TOKEN": value("siaga-garage", "GARAGE_ADMIN_TOKEN", hex_(24)),
    "GARAGE_METRICS_TOKEN": value("siaga-garage", "GARAGE_METRICS_TOKEN", hex_(24)),
    "GARAGE_DEFAULT_ACCESS_KEY": value(
        "siaga-garage", "GARAGE_DEFAULT_ACCESS_KEY", lambda: "GK" + secrets.token_hex(12)
    ),
    "GARAGE_DEFAULT_SECRET_KEY": value("siaga-garage", "GARAGE_DEFAULT_SECRET_KEY", hex_(32)),
}
templates.append({"name": "siaga-garage", "stringData": garage})

# Key sumber data untuk ingest (opsional; kosong = konektornya tidak jalan).
ingest = {}
for key in ["OPENAQ_API_KEY", "FIRMS_MAP_KEY"]:
    v = value("siaga-ingest", key, lambda k=key: env.get(k, ""), "dari .env")
    if v:
        ingest[key] = v
    else:
        print(f"peringatan: {key} kosong (juga di .env); konektornya tidak jalan di produksi", file=sys.stderr)
if ingest:
    templates.append({"name": "siaga-ingest", "stringData": ingest})

# Tujuan OTLP OTel Collector (Grafana Cloud).
col_old = old.get("siaga-otel-collector", {}).get("stringData", {})
endpoint = col_old.get("OTLP_UPSTREAM_ENDPOINT", "")
auth = col_old.get("OTLP_UPSTREAM_AUTHORIZATION", "")
if force_grafana or not (endpoint and auth):
    g_endpoint = os.environ.get("GRAFANA_CLOUD_OTLP_ENDPOINT", "").strip()
    g_id = os.environ.get("GRAFANA_CLOUD_INSTANCE_ID", "").strip()
    g_token = os.environ.get("GRAFANA_CLOUD_TOKEN", "").strip()
    if not g_token:
        default = endpoint or (env.get("OTEL_EXPORTER_OTLP_ENDPOINT", "") if "grafana.net" in env.get("OTEL_EXPORTER_OTLP_ENDPOINT", "") else "")
        print(
            "\nKredensial Grafana Cloud untuk OTel Collector produksi.\n"
            "Buat token BARU khusus produksi (misal 'siaga-produksi') di Grafana Cloud Portal:\n"
            "stack → tile OpenTelemetry → Configure → Generate token. Enter kosong = lewati.",
            file=sys.stderr,
        )
        g_endpoint = tty_input(f"Endpoint OTLP [{default}]: ") or default
        g_id = tty_input("Instance ID (angka di halaman yang sama): ")
        g_token = tty_input("Token (glc_…, tidak ditampilkan): ", secret=True)
    if g_endpoint and g_id and g_token:
        endpoint = g_endpoint.rstrip("/")
        auth = "Basic " + base64.b64encode(f"{g_id}:{g_token}".encode()).decode()
        report.append("siaga-otel-collector: kredensial Grafana Cloud " + ("diganti" if col_old else "diisi"))
    elif not (endpoint and auth):
        print(
            "peringatan: kredensial Grafana Cloud belum diisi; Collector produksi tidak bisa start.\n"
            "  Jalankan lagi nanti: make secrets-grafana",
            file=sys.stderr,
        )
if endpoint and auth:
    templates.append(
        {
            "name": "siaga-otel-collector",
            "stringData": {"OTLP_UPSTREAM_ENDPOINT": endpoint, "OTLP_UPSTREAM_AUTHORIZATION": auth},
        }
    )

doc = {
    "apiVersion": "isindir.github.com/v1alpha3",
    "kind": "SopsSecret",
    "metadata": {"name": "siaga-secrets"},
    "spec": {"secretTemplates": templates},
}
json.dump(doc, sys.stdout)
for line in report:
    print("  " + line, file=sys.stderr)
if not report:
    print("  tidak ada nilai baru", file=sys.stderr)
PY
  sops encrypt --filename-override "$FILE" --input-type json --output-type yaml /dev/stdin >"$out"

# Hanya ganti file bila isi (bukan cuma IV/MAC hasil enkripsi ulang) berubah.
if [[ -f "$FILE" ]] && diff -q <(sops decrypt --output-type json "$FILE") <(sops decrypt --input-type yaml --output-type json "$out") >/dev/null; then
  echo "$FILE tidak berubah."
  exit 0
fi
mv "$out" "$FILE"
trap - EXIT
echo "$FILE ditulis (terenkripsi untuk recipient di .sops.yaml)."
echo "Commit $FILE dan .sops.yaml; private key tetap di $KEY_FILE."
