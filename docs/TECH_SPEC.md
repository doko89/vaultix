# Technical Specification (TECH_SPEC)
## Vaultix — Centralized Infrastructure Backup Platform

**Status:** Draft  
**Companion to:** `PRD.md` (§11 Recommended Technology Stack)  
**Purpose:** Fix the library choices up front so implementation starts fast and stays consistent. This list is the approved dependency set; adding anything outside it needs explicit approval.

---

## 1. Architecture Snapshot

- **Manager:** Go monolith with Clean Architecture layering (REST API, scheduler, job orchestration, metadata DB access). Serves the React build embedded in the binary (single deployable artifact).
- **Agent:** separate Go binary, installed as a systemd service on each target VM.
- **Metadata:** PostgreSQL (single database for Manager state, incl. job queue).
- **Data plane:** Agent uploads directly to S3-compatible storage; payloads never pass through the Manager.
- **Frontend:** React + TypeScript (`strict`), Tailwind CSS + shadcn/ui.

Suggested module path: `github.com/<org>/vaultix`.

### 1.1 Backend Layout (Clean Architecture)

Dependency rule: `domain` has zero external imports; `usecase` depends only on `domain`; `adapter` implements domain ports and depends inward only; `cmd` wires everything. No package outside `cmd` imports an adapter directly — only through its port interface.

```text
github.com/<org>/vaultix
├── cmd/
│   ├── manager/          # HTTP server + worker wiring (main.go)
│   └── agent/            # agent daemon wiring (main.go)
├── internal/
│   ├── domain/           # entities + port interfaces (agent, policy, job, snapshot, user, storage...)
│   ├── usecase/          # business rules, one package per feature
│   │   ├── agent/ policy/ job/ backup/ restore/
│   │   ├── storage/ retention/ schedule/ auth/ user/
│   │   └── notification/ audit/ discovery/
│   ├── adapter/          # implements domain ports; depends inward only
│   │   ├── http/         # chi router, handlers, DTOs, middleware, embedded SPA
│   │   ├── repo/         # sqlc + pgx repositories
│   │   ├── s3/           # object storage client
│   │   ├── oidc/         # OIDC relying-party client
│   │   ├── docker/       # Docker Engine client
│   │   └── scheduler/    # River workers + schedule definitions
│   └── agent/            # agent runtime: executor, uploader, heartbeat, discovery
├── db/
│   ├── migrations/       # goose migrations (embedded)
│   ├── queries/          # sqlc input (*.sql)
│   └── gen/              # sqlc generated code (do not edit)
├── deploy/               # compose files, systemd unit
└── web/                  # frontend source; dist/ embedded into manager binary (see §1.2)
```

### 1.2 Frontend Layout (Feature-Based)

Rule: each PRD §8 section is one feature. Features import only from `shared/`, never from each other — shared code goes in `shared/`.

```text
web/src/
├── app/                  # router, providers, global layout, main.tsx
├── shared/               # cross-feature code only
│   ├── ui/               # shadcn/ui components
│   ├── lib/              # API client, formatting, constants
│   ├── hooks/            # shared hooks
│   └── types/            # shared TS types
└── features/
    ├── dashboard/
    ├── agents/           # incl. resource discovery views
    ├── policies/         # incl. schedules views
    ├── jobs/
    ├── catalog/          # snapshots + manifests
    ├── restore/
    ├── storage/
    ├── credentials/
    ├── users/            # users, roles, OIDC identity links
    ├── notifications/
    ├── audit-logs/
    └── settings/         # global config + OIDC providers
```

Each feature owns `api.ts` (endpoints), `hooks.ts` (react-query), `components/`, `pages/` (or `routes.tsx`), and `types.ts`. A feature exposes only its route; no cross-feature imports.

Build output goes to `web/dist/`. The manager embeds it with `go:embed`: `adapter/http` serves `dist/` with SPA `index.html` fallback while `/api/*` routes take precedence. In development the frontend runs on the Vite dev server proxying `/api` to the manager; embedding applies to release builds only. `web/dist/` is git-ignored and produced by `npm run build` before `go build`.

---

## 2. Backend Libraries (Go)

| Area | Library (module) | Notes |
|---|---|---|
| HTTP routing | `github.com/go-chi/chi/v5` | Stdlib-compatible, middleware chain, easy testing |
| Request validation | `github.com/go-playground/validator/v10` | Validate all API DTOs at the boundary |
| DB driver | `github.com/jackc/pgx/v5` | Pooled connections; use `pgxpool` |
| Type-safe SQL | `sqlc` (`github.com/sqlc-dev/sqlc`) | Generate Go from SQL in `db/queries/` |
| Migrations | `github.com/pressly/goose/v3` | Embedded migrations in `db/migrations/` |
| OIDC / SSO | `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2` | Per PRD §7.1.1 (code + PKCE, JWKS) |
| S3-compatible storage | `github.com/aws/aws-sdk-go-v2` (`service/s3`) | Presigned URLs, multipart; endpoint override for MinIO etc. |
| Compression | `github.com/klauspost/compress` | zstd encoder/decoder (PRD default) |
| Job queue + scheduler | `github.com/riverqueue/river` | Postgres-backed: persistent schedules, retries, no Redis needed |
| CLI framework | `github.com/spf13/cobra` | `agent enroll/run/version`, `manager migrate/serve` |
| Config | `github.com/spf13/viper` | File + env + flags; agent needs config file support |
| Retry/backoff | `github.com/cenkalti/backoff/v5` | Heartbeat reconnect, upload retry, IdP calls |
| Docker Engine | `github.com/moby/moby/client` | Container/volume discovery and inspection |
| Logging | `log/slog` (stdlib) | Structured JSON logs, no extra dep |
| Metrics | `github.com/prometheus/client_golang` | Job duration, throughput, failures, storage usage |
| API contract (optional) | `github.com/oapi-codegen/oapi-codegen/v2` | Generate server stubs + TS client from OpenAPI |

MVP simplification: if River feels heavy on day one, start scheduling with `github.com/robfig/cron/v3` and migrate recurring execution to River before release.

## 3. Database Conventions

- One Postgres database; access only via `pgxpool` + `sqlc`-generated queries — no raw query strings in handlers.
- Migrations are forward-only in review; each migration must be reversible (`goose down` works).
- Use `timestamptz` for all timestamps, `gen_random_uuid()` for primary keys unless a natural key exists.
- Every external call (DB, S3, IdP) gets a context timeout; no unbounded queries.

## 4. Agent Specifics

- Commands: `agent enroll --manager <url> --token <token>`, `agent run`, `agent version`.
- Archive format: `archive/tar` (stdlib) piped into zstd; stream files, never load whole datasets into memory.
- Database tools shell out with context timeouts: `pg_dump`, `mysqldump`, `sqlcmd` — capture stderr into structured job errors.
- Heartbeat loop with exponential backoff (`cenkalti/backoff`); persist minimal local job state for restart recovery.
- systemd unit file shipped in `deploy/agent/vaultix-agent.service`.

## 5. Frontend Libraries

| Area | Library | Notes |
|---|---|---|
| Build | Vite + React + TypeScript | `strict`, `noUncheckedIndexedAccess` on; outputs to `web/dist/`, embedded into the manager binary |
| Components | shadcn/ui + Tailwind CSS | Per PRD §11; copy components into `web/src/shared/ui` |
| Server state | `@tanstack/react-query` | Jobs, agents, snapshots polling/caching |
| Routing | `react-router-dom` | One route per PRD §8 section |
| Forms + validation | `react-hook-form` + `zod` | Policy, storage, OIDC provider forms |
| API client | Typed `fetch` wrapper in `web/src/shared/lib/api.ts` | Switch to generated client if oapi-codegen is adopted |

## 6. Testing Libraries

- `github.com/stretchr/testify` — assertions, suites, mocks (mock only boundaries, never the unit under test).
- `github.com/testcontainers/testcontainers-go` (+ `postgres`, `minio` modules) — integration tests against real Postgres and S3.
- Gates before submit: `go vet ./...`, `golangci-lint run` (if configured), and the repo's typecheck/lint commands.

## 7. Bootstrap Commands

```bash
# backend (from repo root, after go mod init)
go get github.com/go-chi/chi/v5 \
  github.com/go-playground/validator/v10 \
  github.com/jackc/pgx/v5 \
  github.com/pressly/goose/v3 \
  github.com/coreos/go-oidc/v3 golang.org/x/oauth2 \
  github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/s3 \
  github.com/klauspost/compress \
  github.com/riverqueue/river \
  github.com/spf13/cobra github.com/spf13/viper \
  github.com/cenkalti/backoff/v5 \
  github.com/moby/moby/client \
  github.com/prometheus/client_golang \
  github.com/stretchr/testify

go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install github.com/pressly/goose/v3/cmd/goose@latest

# frontend (from web/)
npm i @tanstack/react-query react-router-dom react-hook-form zod
npx shadcn@latest init
```

Release build (frontend embedded into the manager binary):

```bash
npm --prefix web run build
go build -o bin/manager ./cmd/manager
```

## 8. Dependency Policy

1. Prefer stdlib (`slog`, `archive/tar`, `os/exec`, `net/http`) over a new package.
2. One abstraction used once gets inlined; no single-implementation interfaces.
3. Check that a package is maintained and licensed compatibly before adding; verify the real import path in the lockfile (no typosquats).
4. `go mod tidy` after every dependency change; commit `go.sum`.

## 9. Deferred (Post-MVP)

- gRPC streaming for Agent job delivery (start with HTTPS polling per PRD §6.3).
- SAML, SCIM, multi-provider SSO hardening (per PRD §12 exclusions).
- OpenAPI codegen adoption if the hand-written client drifts.

---

**End of document.**
