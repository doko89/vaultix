# Vaultix

Centralized infrastructure backup platform: a central Manager (web UI, API,
scheduling, job orchestration) plus a lightweight Agent on each VM that backs
up files, Docker resources, and databases to S3-compatible storage.

## Docs

- `PRD.md` — product requirements
- `docs/TECH_SPEC.md` — layout, libraries, bootstrap (authoritative)
- `docs/DESIGN.md` — design system (primary color red)

## Layout

Backend follows Clean Architecture (`cmd/`, `internal/domain|usecase|adapter`,
`db/`, `deploy/`); frontend lives in `web/` (feature-based). See TECH_SPEC 1.1
and 1.2 for the exact trees.

## Dev quickstart

Prereqs: Go toolchain, Node 20+, Docker. Install `goose`/`sqlc` per
TECH_SPEC section 7 when you need migrations or codegen.

```bash
make compose-up                    # postgres:5432, minio S3:9000 + console:9001, manager:8080
make migrate-up                    # goose up on db/migrations (DATABASE_URL overridable)
go run ./cmd/manager serve         # control plane (serve lands with backend team)
go run ./cmd/agent run             # per-VM executor (enroll/run land with backend team)
make web-dev                       # Vite dev server, proxies /api to the manager
```

Defaults are dev-only (see `deploy/docker-compose.yml` header for the prod
hardening checklist). Stop with `make compose-down` (volumes survive;
`docker compose -f deploy/docker-compose.yml down -v` wipes them).

## Build

```bash
go build ./...
go build -o bin/manager ./cmd/manager
go build -o bin/agent ./cmd/agent
go vet ./...
```

Release (manager embeds the web UI):

```bash
npm --prefix web run build
go build -o bin/manager ./cmd/manager
```

## Web dev

```bash
cd web && npm install && npm run dev   # dev server
npm --prefix web run build             # outputs web/dist/ (embedded by manager)
```

## Gates before submit

```bash
make vet && make lint && go test ./...   # Go gates (TECH_SPEC section 6)
npx --prefix web tsc -b && make web-build # web typecheck + build
```

CI (`.github/workflows/ci.yml`) runs the same gates plus a non-blocking
`sqlc diff` drift check when `sqlc` and a sqlc config are present.

## Status

Skeleton only: no auth, job, or backup logic yet.
