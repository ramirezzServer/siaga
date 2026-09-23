# Perintah harian SIAGA. Jalankan `make help` untuk daftar lengkap.
# Semua target diasumsikan jalan di Linux/WSL2 dari root repo.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

COMPOSE     := docker compose -f deploy/compose/compose.lite.yaml --env-file .env
GO_MODULES  := libs/go/platform libs/go/contracts services/geo-processor
PROVINCE    ?= 32

# DATABASE_URL untuk role siaga_geo, dibangun dari .env.
ifneq (,$(wildcard .env))
include .env
export
endif
GEO_DATABASE_URL ?= postgres://siaga_geo:$(SIAGA_GEO_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable

.PHONY: help
help: ## Tampilkan daftar perintah
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

##@ Persiapan
.PHONY: doctor env deps hooks
doctor: ## Cek semua prasyarat terpasang
	@scripts/doctor.sh

env: ## Buat .env dengan password acak (sekali saja)
	@scripts/make-env.sh

deps: ## Unduh dependensi Go dan Node, kunci versinya (go.sum, pnpm-lock.yaml)
	@for m in $(GO_MODULES); do (cd $$m && go mod tidy); done
	pnpm install

hooks: ## Pasang git hook (gitleaks, lint, commitlint)
	pnpm exec lefthook install

##@ Lingkungan lokal (Compose lite)
.PHONY: up down reset logs psql
up: env ## Jalankan PostgreSQL, NATS, Valkey, Mailpit
	$(COMPOSE) up -d --build --wait

down: ## Hentikan layanan (data tetap)
	$(COMPOSE) down

reset: ## Hapus semua data lokal lalu mulai ulang dari nol
	$(COMPOSE) down -v
	$(MAKE) up

logs: ## Ikuti log semua layanan
	$(COMPOSE) logs -f

psql: ## Buka psql sebagai superuser
	$(COMPOSE) exec postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

##@ Database
.PHONY: migrate migrate-down migrate-status regions-fetch regions-import seed
migrate: ## Jalankan migrasi semua layanan
	cd services/geo-processor && go tool goose -dir migrations -table ref.goose_db_version postgres "$(GEO_DATABASE_URL)" up

migrate-down: ## Mundurkan satu migrasi geo-processor
	cd services/geo-processor && go tool goose -dir migrations -table ref.goose_db_version postgres "$(GEO_DATABASE_URL)" down

migrate-status: ## Status migrasi
	cd services/geo-processor && go tool goose -dir migrations -table ref.goose_db_version postgres "$(GEO_DATABASE_URL)" status

regions-fetch: ## Unduh data batas wilayah (PROVINCE=32)
	scripts/fetch-region-data.sh $(PROVINCE)

regions-import: regions-fetch ## Import batas wilayah ke ref.region
	cd services/geo-processor && DATABASE_URL="$(GEO_DATABASE_URL)" go run ./cmd/import-regions \
	  -source ../../.cache/wilayah_boundaries/db -province $(PROVINCE)

seed: migrate regions-import ## Migrasi + import wilayah

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

test-integration: ## Test integrasi (butuh `make up migrate`)
	cd services/geo-processor && SIAGA_TEST_DATABASE_URL="$(GEO_DATABASE_URL)" go test -race -count=1 -tags integration ./...

fuzz: ## Fuzzing singkat semua target fuzz (30 detik per target)
	@cd services/geo-processor && for t in FuzzParseCode FuzzParseLatLngPath; do go test ./internal/domain/region -run=^$$ -fuzz=$$t -fuzztime=30s; done
	@cd services/geo-processor && go test ./internal/adapters/cahyadsn -run=^$$ -fuzz=FuzzParseDump -fuzztime=30s

check: lint test ## Semua pemeriksaan sebelum push

##@ Kubernetes lokal (k3d + Tilt, profil full)
.PHONY: k3d-up k3d-down tilt
k3d-up: ## Buat cluster k3d "siaga"
	k3d cluster create --config deploy/k3d/cluster.yaml

k3d-down: ## Hapus cluster k3d
	k3d cluster delete siaga

tilt: ## Jalankan Tilt (dashboard di http://localhost:10350)
	tilt up
