# Vaultix root Makefile. Dev compose stack: deploy/docker-compose.yml.
# Tool installs (goose, sqlc): see docs/TECH_SPEC.md section 7.

GO ?= go
COMPOSE ?= docker compose -f deploy/docker-compose.yml
DATABASE_URL ?= postgres://vaultix:vaultix-dev-only@localhost:5432/vaultix?sslmode=disable
MIGRATIONS_DIR ?= db/migrations
GOOSE ?= goose

.PHONY: help build test vet lint migrate-up migrate-down sqlc-generate web-build web-dev compose-up compose-down

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS=":.*## "} {printf "  %-14s %s\n", $$1, $$2}'

build: ## Build manager + agent binaries into bin/
	$(GO) build -o bin/manager ./cmd/manager
	$(GO) build -o bin/agent ./cmd/agent

test: ## Run Go tests
	$(GO) test ./...

vet: ## go vet (submit gate)
	$(GO) vet ./...

lint: ## golangci-lint (submit gate; install per TECH_SPEC section 6)
	@command -v golangci-lint >/dev/null || (echo "golangci-lint not installed" && exit 1)
	golangci-lint run ./...

migrate-up: ## Apply DB migrations (goose up)
	@command -v $(GOOSE) >/dev/null || (echo "goose not installed: go install github.com/pressly/goose/v3/cmd/goose@latest" && exit 1)
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up

migrate-down: ## Roll back one DB migration (goose down)
	@command -v $(GOOSE) >/dev/null || (echo "goose not installed: go install github.com/pressly/goose/v3/cmd/goose@latest" && exit 1)
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" down

sqlc-generate: ## Regenerate type-safe SQL from db/queries
	@command -v sqlc >/dev/null || (echo "sqlc not installed: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest" && exit 1)
	sqlc generate

web-build: ## Build web UI into web/dist (embedded by manager release binary)
	npm --prefix web run build

web-dev: ## Run Vite dev server (proxies /api to manager)
	npm --prefix web run dev

compose-up: ## Start dev stack (postgres, minio, manager)
	$(COMPOSE) up -d

compose-down: ## Stop dev stack (keeps volumes; add -v to wipe)
	$(COMPOSE) down
