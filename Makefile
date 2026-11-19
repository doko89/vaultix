# Vaultix root Makefile. Dev compose stack: deploy/docker-compose.yml.
# Tool installs (goose, sqlc): see docs/TECH_SPEC.md section 7.

GO ?= go
COMPOSE ?= docker compose -f deploy/docker-compose.yml
DATABASE_URL ?= postgres://vaultix:vaultix-dev-only@localhost:5432/vaultix?sslmode=disable
MIGRATIONS_DIR ?= db/migrations
GOOSE ?= goose
# E2E stack: isolated project (-p), S3Mock backend, host ports shifted by one
# so e2e runs alongside dev. VAULTIX_S3_ENDPOINT reselects the manager's
# backend; the compose file defaults it to MinIO (dev).
E2E_PROJECT ?= vaultix-e2e
E2E_MANAGER_PORT ?= 8081
E2E_POSTGRES_PORT ?= 5433
E2E_ENV ?= VAULTIX_S3_ENDPOINT=http://s3mock:9090 MANAGER_PORT=$(E2E_MANAGER_PORT) POSTGRES_PORT=$(E2E_POSTGRES_PORT)

.PHONY: help build test vet lint migrate-up migrate-down sqlc-generate web-build web-dev compose-up compose-down e2e-up e2e-down e2e-logs seed-admin release-build

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

compose-up: ## Start dev stack (postgres, minio, manager :8080)
	$(COMPOSE) --profile dev up -d

compose-down: ## Stop dev stack (keeps volumes; add -v to wipe)
	$(COMPOSE) --profile dev down

e2e-up: ## Start hermetic e2e stack (postgres, s3mock, manager :8081)
	$(E2E_ENV) $(COMPOSE) -p $(E2E_PROJECT) --profile e2e up -d

e2e-down: ## Stop e2e stack (keeps volumes; use `... down -v` to wipe)
	$(COMPOSE) -p $(E2E_PROJECT) --profile e2e down

e2e-logs: ## Follow e2e stack logs
	$(COMPOSE) -p $(E2E_PROJECT) logs -f --tail=200

seed-admin: ## Create/update admin user in the running manager container
	@echo "target: dev manager (for e2e use the -p variant in README)"
	@echo "needs the jobproto track's 'manager seed-admin' (VAULTIX_ADMIN_USER/PASSWORD); fails until it lands"
	$(COMPOSE) --profile dev exec \
		-e VAULTIX_ADMIN_USER=$${VAULTIX_ADMIN_USER:-admin} \
		-e VAULTIX_ADMIN_PASSWORD=$${VAULTIX_ADMIN_PASSWORD:?set VAULTIX_ADMIN_PASSWORD} \
		manager /usr/local/bin/manager seed-admin

release-build: ## Release manager binary with embedded UI (web build + go build)
	npm --prefix web run build
	CGO_ENABLED=0 $(GO) build -o bin/manager ./cmd/manager
