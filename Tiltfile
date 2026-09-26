# Profil "full": lingkungan Kubernetes lokal yang identik dengan produksi.
# Prasyarat: make k3d-up. Jalankan: tilt up
load("ext://helm_resource", "helm_repo", "helm_resource")

allow_k8s_contexts("k3d-siaga")
# Registry lokal k3d terdeteksi otomatis lewat ConfigMap local-registry-hosting.

# Secret lokal (password database acak, Garage dan key sumber dari .env, tujuan
# OTLP Collector); nama sama dengan secret SOPS produksi. Lihat scripts/dev-secrets.sh.
local_resource("dev-secrets", cmd="scripts/dev-secrets.sh", labels=["platform"])

# Operator CloudNativePG.
helm_repo("cnpg-repo", "https://cloudnative-pg.github.io/charts", labels=["platform"])
helm_resource(
    "cnpg-operator",
    "cnpg-repo/cloudnative-pg",
    namespace="cnpg-system",
    flags=["--create-namespace", "--wait"],
    resource_deps=["cnpg-repo"],
    labels=["platform"],
)

# NATS JetStream.
helm_repo("nats-repo", "https://nats-io.github.io/k8s/helm/charts/", labels=["platform"])
helm_resource(
    "nats",
    "nats-repo/nats",
    namespace="siaga",
    flags=["--values=deploy/k8s/base/nats-values.yaml", "--wait"],
    resource_deps=["nats-repo", "dev-secrets"],
    port_forwards=["4222:4222"],
    labels=["platform"],
)

# Image PostgreSQL kustom; Tilt menyuntikkan tag hasil build ke spec.imageName.
docker_build("siaga-postgres", ".", dockerfile="deploy/images/postgres/Dockerfile")
k8s_kind("Cluster", api_version="postgresql.cnpg.io/v1", image_json_path="{.spec.imageName}")

k8s_yaml(kustomize("deploy/k8s/local", flags=["--load-restrictor=LoadRestrictionsNone"]))
k8s_resource("siaga-db", resource_deps=["cnpg-operator", "dev-secrets"], labels=["data"])
k8s_resource("valkey", port_forwards=["6379:6379"], labels=["data"])
k8s_resource("mailpit", port_forwards=["8025:8025"], labels=["tools"])

# Garage (arsip payload mentah) dan OTel Collector (telemetri ke Grafana Cloud
# atau Grafana lokal, sesuai OTEL_EXPORTER_OTLP_* di .env). S3 Garage cluster
# di localhost:13900 (3900 dipakai Garage Compose lite).
k8s_resource("garage", resource_deps=["dev-secrets"], port_forwards=["13900:3900"], labels=["data"])
k8s_resource("otel-collector", resource_deps=["dev-secrets"], labels=["platform"])

# Port-forward ke primary PostgreSQL di localhost:15432 (5432 dipakai Compose lite).
local_resource(
    "db-port-forward",
    serve_cmd="kubectl -n siaga wait --for=condition=Ready cluster/siaga-db --timeout=300s && kubectl -n siaga port-forward svc/siaga-db-rw 15432:5432",
    resource_deps=["siaga-db"],
    labels=["data"],
)

# Image layanan Go (deploy/images/go/Dockerfile, sama dengan job CI images).
docker_build(
    "siaga-geo-processor",
    ".",
    dockerfile="deploy/images/go/Dockerfile",
    target="geo-processor",
    build_args={"SERVICE": "geo-processor", "BINARIES": "geo-processor import-regions", "VERSION": "tilt"},
    only=["libs/go", "services/geo-processor"],
)
docker_build(
    "siaga-ingest",
    ".",
    dockerfile="deploy/images/go/Dockerfile",
    target="ingest",
    build_args={"SERVICE": "ingest", "BINARIES": "ingest archive replay", "VERSION": "tilt"},
    only=["libs/go", "services/ingest"],
)

# Migrasi sebagai Job (sama dengan produksi, hook PreSync Argo CD), lalu import
# wilayah lewat port-forward karena data batas wilayah diunduh di laptop.
k8s_resource("geo-processor-migrate", resource_deps=["siaga-db", "dev-secrets"], labels=["pipa-data"])
k8s_resource(
    "geo-processor",
    resource_deps=["geo-processor-migrate", "nats", "otel-collector"],
    port_forwards=["18082:8082"],
    labels=["pipa-data"],
)
# ingest tidak otomatis jalan: bila `make ingest` (Compose) juga hidup, kuota
# sumber terpakai dua kali. Nyalakan dari dashboard Tilt bila perlu.
k8s_resource(
    "ingest",
    resource_deps=["nats", "garage", "otel-collector", "dev-secrets"],
    port_forwards=["18081:8081"],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
    labels=["pipa-data"],
)
local_resource(
    "regions-import",
    cmd="scripts/fetch-region-data.sh 32 && cd services/geo-processor && DATABASE_URL=\"$(../../scripts/k8s-db-url.sh geo)\" go run ./cmd/import-regions -source ../../.cache/wilayah_boundaries/db -province 32",
    deps=["services/geo-processor/internal", "services/geo-processor/cmd"],
    resource_deps=["geo-processor-migrate", "db-port-forward"],
    labels=["data"],
)
