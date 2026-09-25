# Perintah harian SIAGA. Jalankan `make help` untuk daftar lengkap.
# Semua target diasumsikan jalan di Linux/WSL2 dari root repo.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

COMPOSE     := docker compose -f deploy/compose/compose.lite.yaml --env-file .env
GO_MODULES  := libs/go/platform libs/go/contracts services/geo-processor services/ingest
PROVINCE    ?= 32

# DATABASE_URL untuk role siaga_geo, dibangun dari .env.
ifneq (,$(wildcard .env))
include .env
export
endif
GEO_DATABASE_URL ?= postgres://siaga_geo:$(SIAGA_GEO_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
# Resep yang memakai URL berisi password diawali @ supaya make tidak mencetaknya;
# gantinya dicetak versi tersamar ini.
GEO_DATABASE_URL_SAFE = postgres://siaga_geo:***@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)
# Arsip payload mentah ingest di Garage (ADR 0014), dan folder lokal untuk
# rekaman sampel (ingest-record) serta arsip fase 1a–1d sebelum Garage.
INGEST_ARCHIVE_URL ?= s3://$(or $(ARCHIVE_BUCKET),siaga-arsip)/raw?endpoint=http://127.0.0.1:$(or $(GARAGE_S3_PORT),3900)
LOCAL_ARCHIVE_DIR ?= $(CURDIR)/.cache/ingest-archive

.PHONY: help
help: ## Tampilkan daftar perintah
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

##@ Persiapan
.PHONY: doctor env deps hooks
doctor: ## Cek semua prasyarat terpasang
	@scripts/doctor.sh

env: ## Buat .env dengan rahasia acak, atau tambahkan variabel baru ke .env lama
	@scripts/make-env.sh

deps: ## Unduh dependensi Go dan Node, kunci versinya (go.sum, pnpm-lock.yaml)
	@for m in $(GO_MODULES); do (cd $$m && go mod tidy); done
	pnpm install

hooks: ## Pasang git hook (gitleaks, lint, commitlint)
	pnpm exec lefthook install

##@ Lingkungan lokal (Compose lite)
.PHONY: up down reset logs psql obs-up obs-down
up: env ## Jalankan PostgreSQL, NATS, Valkey, Garage, Mailpit
	$(COMPOSE) up -d --build --wait

down: ## Hentikan layanan, termasuk Grafana lokal (data tetap)
	$(COMPOSE) --profile obs down

reset: ## Hapus semua data lokal lalu mulai ulang dari nol
	$(COMPOSE) --profile obs down -v
	$(MAKE) up

obs-up: env ## Jalankan Grafana + Prometheus + Tempo + Loki lokal (~1 GB RAM), dashboard di http://127.0.0.1:3300
	$(COMPOSE) --profile obs up -d --wait lgtm
	@if [[ -z "$${OTEL_EXPORTER_OTLP_ENDPOINT:-}" ]]; then \
	  echo "Telemetri belum dikirim: isi OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 di .env, lalu jalankan ulang make ingest dan make geo."; \
	else echo "Telemetri dikirim ke $${OTEL_EXPORTER_OTLP_ENDPOINT}"; fi
	@echo "Grafana: http://127.0.0.1:3300 (dashboard SIAGA, Pipa data)"

obs-down: ## Hentikan Grafana lokal (data telemetri tetap)
	$(COMPOSE) --profile obs stop lgtm

logs: ## Ikuti log semua layanan
	$(COMPOSE) logs -f

psql: ## Buka psql sebagai superuser
	$(COMPOSE) exec postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

##@ Database
.PHONY: migrate migrate-down migrate-status regions-fetch regions-import seed adm4-list grid-list
# goose dijalankan dengan GOWORK=off: driver bawaannya memicu ambiguous import genproto di workspace mode.
migrate: ## Jalankan migrasi semua layanan
	@echo "goose up ($(GEO_DATABASE_URL_SAFE))"
	@cd services/geo-processor && GOWORK=off go tool goose -dir migrations -table ref.goose_db_version postgres "$(GEO_DATABASE_URL)" up

migrate-down: ## Mundurkan satu migrasi geo-processor
	@echo "goose down ($(GEO_DATABASE_URL_SAFE))"
	@cd services/geo-processor && GOWORK=off go tool goose -dir migrations -table ref.goose_db_version postgres "$(GEO_DATABASE_URL)" down

migrate-status: ## Status migrasi
	@cd services/geo-processor && GOWORK=off go tool goose -dir migrations -table ref.goose_db_version postgres "$(GEO_DATABASE_URL)" status

regions-fetch: ## Unduh data batas wilayah (PROVINCE=32)
	scripts/fetch-region-data.sh $(PROVINCE)

regions-import: regions-fetch ## Import batas wilayah ke ref.region
	@echo "import-regions provinsi $(PROVINCE) ($(GEO_DATABASE_URL_SAFE))"
	@cd services/geo-processor && DATABASE_URL="$(GEO_DATABASE_URL)" go run ./cmd/import-regions \
	  -source ../../.cache/wilayah_boundaries/db -province $(PROVINCE)

seed: migrate regions-import ## Migrasi + import wilayah

adm4-list: regions-fetch ## Bangun ulang daftar kode desa untuk sapuan prakiraan ingest (PROVINCE=32)
	@cd services/geo-processor && go run ./cmd/import-regions -dry-run \
	  -source ../../.cache/wilayah_boundaries/db -province $(PROVINCE) \
	  -adm4-out ../ingest/internal/adapters/regionlist/data/adm4_$(PROVINCE).txt

grid-list: regions-fetch ## Bangun ulang simpul grid 0,25° Open-Meteo ingest (PROVINCE=32)
	@cd services/geo-processor && go run ./cmd/import-regions -dry-run \
	  -source ../../.cache/wilayah_boundaries/db -province $(PROVINCE) \
	  -grid-out ../ingest/internal/adapters/sitelist/data/grid025_$(PROVINCE).txt

##@ Pipa data
.PHONY: ingest ingest-record geo calibrate-dedup river-snap archive-ls archive-verify archive-upload replay
ingest: ## Jalankan ingest (butuh `make up`); arsip ke Garage, status di http://127.0.0.1:8081/status
	cd services/ingest && INGEST_ARCHIVE_URL="$(INGEST_ARCHIVE_URL)" go run ./cmd/ingest

ingest-record: ## Rekam payload semua sumber sekali ke folder lokal, tanpa NATS
	cd services/ingest && INGEST_ARCHIVE_URL="$(LOCAL_ARCHIVE_DIR)" go run ./cmd/ingest -once -publish=false
	@echo "Arsip: $(LOCAL_ARCHIVE_DIR)"

archive-ls: ## Ringkasan arsip Garage per konektor (PREFIX=bmkg-autogempa/2026/09/)
	@cd services/ingest && INGEST_ARCHIVE_URL="$(INGEST_ARCHIVE_URL)" go run ./cmd/archive ls -prefix "$(PREFIX)"

archive-verify: ## Periksa setiap objek arsip: gzip utuh dan SHA-256 cocok dengan kunci
	@cd services/ingest && INGEST_ARCHIVE_URL="$(INGEST_ARCHIVE_URL)" go run ./cmd/archive verify -prefix "$(PREFIX)"

archive-upload: ## Salin arsip lokal (.cache/ingest-archive) ke Garage; aman diulang
	@if [[ -d "$(LOCAL_ARCHIVE_DIR)" ]]; then \
	  cd services/ingest && INGEST_ARCHIVE_URL="$(INGEST_ARCHIVE_URL)" go run ./cmd/archive cp -from "$(LOCAL_ARCHIVE_DIR)"; \
	else echo "Tidak ada arsip lokal di $(LOCAL_ARCHIVE_DIR)"; fi

# Contoh: make replay FROM=2026-09-24 TO=2026-09-25 CONNECTORS=bmkg-autogempa,usgs-2.5-day SPEED=60
replay: ## Putar ulang arsip Garage ke NATS (FROM, TO, CONNECTORS, SPEED; ARGS=-publish=false untuk cek saja)
	@cd services/ingest && INGEST_ARCHIVE_URL="$(INGEST_ARCHIVE_URL)" go run ./cmd/replay \
	  -from "$(FROM)" -to "$(TO)" -connectors "$(CONNECTORS)" -speed "$(or $(SPEED),0)" $(ARGS)

geo: ## Jalankan geo-processor (butuh `make up seed`); status di http://127.0.0.1:8082/status
	@echo "geo-processor ($(GEO_DATABASE_URL_SAFE))"
	@cd services/geo-processor && DATABASE_URL="$(GEO_DATABASE_URL)" go run ./cmd/geo-processor

river-snap: ## Pilih sel GloFAS untuk titik pantau sungai (±2 menit, ±800 lokasi Open-Meteo)
	go run ./services/ingest/cmd/river-snap -src docs/calibration/titik-sungai-32.csv \
	  -out services/ingest/internal/adapters/sitelist/data/rivers_32.csv -report docs/calibration/titik-sungai.md
	pnpm exec prettier --write --log-level warn docs/calibration/titik-sungai.md

CALIBRATION_DIR ?= $(CURDIR)/.cache/calibration
calibrate-dedup: ## Ukur ambang deduplikasi gempa dengan katalog BMKG + USGS historis
	scripts/fetch-calibration-data.sh
	cd services/geo-processor && go run ./cmd/calibrate-dedup \
	  -bmkg $(CALIBRATION_DIR)/bmkg/katalog_gempa/katalog_gempa.csv \
	  -usgs $(CALIBRATION_DIR)/usgs/rawdata/query_2008_2015.csv,$(CALIBRATION_DIR)/usgs/rawdata/query_2016_2023.csv \
	  -out ../../docs/calibration/dedup-gempa.md
	pnpm exec prettier --write --log-level warn docs/calibration/dedup-gempa.md

##@ Kualitas
.PHONY: gen lint lint-go test test-go test-integration fuzz check
gen: ## Generate ulang kode dari kontrak (proto + OpenAPI)
	pnpm run gen

lint: lint-go ## Semua lint
	pnpm run lint:proto
	pnpm run lint:openapi
	pnpm run format:check
	pnpm run lint

lint-go:
	@for m in $(GO_MODULES); do (cd $$m && golangci-lint run ./...); done

test: test-go ## Semua unit test
	pnpm run test

test-go:
	@for m in $(GO_MODULES); do (cd $$m && go test -race -count=1 -cover ./...); done

test-integration: ## Test integrasi (butuh `make up migrate`; paket dijalankan berurutan karena berbagi database)
	@echo "test integrasi geo-processor ($(GEO_DATABASE_URL_SAFE))"
	@cd services/geo-processor && SIAGA_TEST_DATABASE_URL="$(GEO_DATABASE_URL)" go test -race -count=1 -p 1 -tags integration ./...
	@echo "test integrasi ingest (Garage http://127.0.0.1:$(or $(GARAGE_S3_PORT),3900))"
	@cd services/ingest && SIAGA_TEST_S3_ENDPOINT="http://127.0.0.1:$(or $(GARAGE_S3_PORT),3900)" go test -race -count=1 -tags integration ./internal/adapters/s3archive/

fuzz: ## Fuzzing singkat semua target fuzz (30 detik per target)
	@cd services/geo-processor && for t in ./internal/domain/region:FuzzParseCode ./internal/domain/region:FuzzParseLatLngPath ./internal/adapters/cahyadsn:FuzzParseDump ./internal/domain/quake:FuzzClusteringOrderIndependent ./internal/domain/quake:FuzzClusteringInvariantsUnderCrowding ./internal/domain/quake:FuzzApplyOrderIndependent ./internal/domain/quake:FuzzLevelMonotone ./internal/domain/quake:FuzzRuleMatchSymmetric ./internal/app/quakes:FuzzServiceOrderIndependent ./internal/domain/weather:FuzzDeriveOrderIndependent ./internal/app/warnings:FuzzServiceOrderIndependent ./internal/domain/series:FuzzWeatherValidate; do \
	  go test $${t%%:*} -run='^$$' -fuzz="^$${t##*:}\$$" -fuzztime=30s || exit 1; done
	@cd services/ingest && for t in ./internal/domain/ratelimit:FuzzWindowBound ./internal/domain/ratelimit:FuzzPriorityHeadroom ./internal/domain/ratelimit:FuzzAvailable ./internal/domain/schedule:FuzzNextBounds ./internal/domain/forecast:FuzzOrder ./internal/domain/warning:FuzzParseReferences ./internal/adapters/bmkg:FuzzParse ./internal/adapters/bmkg:FuzzParseCAPDocuments ./internal/adapters/bmkg:FuzzParseForecastDocument ./internal/adapters/usgs:FuzzParse ./internal/adapters/openmeteo:FuzzParse ./internal/domain/airquality:FuzzClean ./internal/adapters/firms:FuzzParseFIRMS ./internal/adapters/openaq:FuzzParseOpenAQ ./internal/domain/archivekey:FuzzParse ./internal/app/replay:FuzzRunOrdered; do \
	  go test $${t%%:*} -run='^$$' -fuzz="^$${t##*:}\$$" -fuzztime=30s || exit 1; done
	@cd libs/go/platform && for t in ./otelx:FuzzValidTraceParent; do \
	  go test $${t%%:*} -run='^$$' -fuzz="^$${t##*:}\$$" -fuzztime=30s || exit 1; done

check: lint test ## Semua pemeriksaan sebelum push

##@ Kubernetes lokal (k3d + Tilt, profil full)
.PHONY: k3d-up k3d-down tilt
k3d-up: ## Buat cluster k3d "siaga"
	k3d cluster create --config deploy/k3d/cluster.yaml

k3d-down: ## Hapus cluster k3d
	k3d cluster delete siaga

tilt: ## Jalankan Tilt (dashboard di http://localhost:10350)
	tilt up
