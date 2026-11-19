# Vaultix DB — migrations + queries

Layout: `migrations/` (goose, embedded by the manager), `queries/` (sqlc input,
one file per aggregate), `gen/` (sqlc output, do not edit).

## Migrate

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
export GOOSE_DRIVER=postgres
export GOOSE_DBSTRING='postgres://user:pass@localhost:5432/vaultix?sslmode=disable'
goose -dir db/migrations up        # apply
goose -dir db/migrations down      # roll back one (every migration is reversible)
goose -dir db/migrations status    # show applied versions
```

Conventions (TECH_SPEC §3): `timestamptz` everywhere, `gen_random_uuid()` PKs,
access only via `pgxpool` + `db/gen` — no raw SQL in handlers.

## Generate

Requires the pinned sqlc (never add sqlc to go.mod):

```bash
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 generate
```

Config: `sqlc.yaml` at repo root (pgx/v5 → `db/gen`). Generated code needs
`github.com/jackc/pgx/v5` in go.mod — owned by the backend track, not this one.

## Notes

- Seed: migration `00002` inserts the `administrator` / `operator` / `readonly` roles.
- Jobs reference `(policy_id, policy_version)`; `backup_policy_versions.config`
  is the immutable config snapshot for that execution.
- `oidc_providers.client_secret_encrypted`, `secret_profiles.secret_encrypted`,
  and credential fields in `storage_destinations.auth` must never leave the
  server (no read API, no logs). `GetProviderSecret` is for token exchange only.
- `audit_logs` is append-only by design (no update/delete queries).
- Verified 2026-11-19: `sqlc generate` clean; goose `up` → 23 tables,
  `down-to 1` → 0, `up` → 23 on scratch Postgres 16; `go vet ./...` clean
  (with transient pgx dep; go.mod untouched).
