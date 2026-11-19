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

## Build

```bash
go build ./...
go build -o bin/manager ./cmd/manager
go build -o bin/agent ./cmd/agent
go vet ./...
```

## Web dev

```bash
cd web && npm install && npm run dev   # dev server
npm --prefix web run build             # outputs web/dist/ (embedded by manager)
```

## Status

Skeleton only: no auth, job, or backup logic yet.
