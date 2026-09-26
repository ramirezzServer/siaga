# syntax=docker/dockerfile:1.7
# Image layanan Go SIAGA (ingest, geo-processor). Satu Dockerfile untuk semua
# layanan; layanan dan binary dipilih lewat build arg. Lihat docs/adr/0016.
#
#   docker buildx build -f deploy/images/go/Dockerfile --target ingest \
#     --build-arg SERVICE=ingest --build-arg BINARIES="ingest archive replay" \
#     --build-arg VERSION=$(git describe --always --dirty) -t siaga-ingest .
#
# Biasanya lewat `make images` (arsitektur mesin sendiri) atau job CI `images`.
#
# Build berjalan di platform mesin build dan meng-cross-compile ke TARGETARCH,
# jadi image arm64 (VM Oracle) dibangun tanpa emulasi QEMU.
ARG GO_VERSION=1.27.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build
ARG SERVICE
ARG BINARIES
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
# Tiap modul dibangun sendiri (GOWORK=off): go.mod layanan sudah punya replace
# ke libs/go, dan driver bawaan goose memicu ambiguous import di workspace mode.
ENV CGO_ENABLED=0 GOWORK=off GOFLAGS=-trimpath

# Lapisan dependensi: hanya go.mod/go.sum, supaya cache tidak rusak tiap kode berubah.
COPY libs/go/contracts/go.mod libs/go/contracts/go.sum /src/libs/go/contracts/
COPY libs/go/platform/go.mod libs/go/platform/go.sum /src/libs/go/platform/
COPY services/${SERVICE}/go.mod services/${SERVICE}/go.sum /src/services/${SERVICE}/
WORKDIR /src/services/${SERVICE}
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY libs/go/ /src/libs/go/
COPY services/${SERVICE}/ /src/services/${SERVICE}/
# Untuk geo-processor ikut dibangun goose (versi terkunci di go.mod lewat
# directive tool) dan disalin folder migrasinya, dipakai Job migrasi sebelum deploy.
# BINARIES sengaja tidak dikutip: daftar nama dipisah spasi.
# hadolint ignore=SC2086
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eu; \
    test -n "${BINARIES}" || { echo "BINARIES kosong" >&2; exit 1; }; \
    mkdir -p /out/bin /out/migrations; \
    for b in ${BINARIES}; do \
      GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build \
        -ldflags "-s -w -X main.version=${VERSION}" -o "/out/bin/$b" "./cmd/$b"; \
    done; \
    if [ -d migrations ]; then \
      goose_version="$(go list -m -f '{{.Version}}' github.com/pressly/goose/v3)"; \
      GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build \
        -tags "no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb" \
        -ldflags "-s -w -X main.version=${goose_version}" -o /out/bin/goose github.com/pressly/goose/v3/cmd/goose; \
      cp migrations/*.sql /out/migrations/; \
    fi

# distroless static: tanpa shell dan paket, sudah membawa sertifikat CA, tzdata,
# dan user nonroot (UID 65532). ENTRYPOINT per layanan ada di target di bawah
# (exec form tidak bisa memakai ARG), jadi build selalu dengan --target.
FROM gcr.io/distroless/static-debian13:nonroot AS runtime
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/ramirezzServer/siaga" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/bin/ /usr/local/bin/
COPY --from=build /out/migrations/ /migrations/
USER 65532:65532

FROM runtime AS ingest
LABEL org.opencontainers.image.title="siaga-ingest" \
      org.opencontainers.image.description="SIAGA ingest: polling sumber data bahaya, arsip payload mentah, dan replay"
ENTRYPOINT ["/usr/local/bin/ingest"]

FROM runtime AS geo-processor
LABEL org.opencontainers.image.title="siaga-geo-processor" \
      org.opencontainers.image.description="SIAGA geo-processor: kejadian bahaya, deret waktu, migrasi (goose), dan import wilayah"
ENTRYPOINT ["/usr/local/bin/geo-processor"]
