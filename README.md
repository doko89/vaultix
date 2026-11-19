# Vaultix

Centralized infrastructure backup platform: a central Manager (web UI, API,
scheduling, job orchestration) plus a lightweight Agent on each VM that backs
up files, Docker resources, and databases to S3-compatible storage.

## Quick setup

One-shot installers from the latest GitHub Release (pinned `releases/latest/download`
URLs — no floating branch):

**1. Manager (server VM).** Installs postgres + manager via Docker Compose into
`/opt/vaultix`, generates secrets unless provided, waits for `/healthz`:

```bash
curl -fsSL https://github.com/doko89/vaultix/releases/latest/download/server.sh \
  | sudo bash -s -- --port 8080
```

No env is required (`POSTGRES_PASSWORD` / `VAULTIX_CRYPTO_KEY` are generated when
absent; pass `--version vX.Y.Z` / `--dir DIR` to override). The script prints the
exact `seed-admin` and enroll-token commands when it finishes. Plain HTTP without
TLS — local/lab use; see the prod-hardening checklist at the top of the downloaded
compose file before exposing anything.

**2. Agent (each worker VM).** Installs the agent binary to `/usr/local/bin`,
redeems the enroll token, writes config + systemd unit, starts the service:

```bash
curl -fsSL https://github.com/doko89/vaultix/releases/latest/download/worker.sh \
  | sudo MANAGER_URL=http://<manager-host>:8080 VAULTIX_ENROLL_TOKEN=<token> bash -s --
```

Required: `MANAGER_URL` (or `--manager`) and `VAULTIX_ENROLL_TOKEN` (or `--token`),
the single-use token from step 2 of the manager installer's output. Check with
`systemctl status vaultix-agent` and `journalctl -u vaultix-agent -f`.

**3. CLI (your workstation).** Download `vaultix-cli-linux-amd64` (or `-arm64`)
from the latest release, mint a key with your login session, then verify it:

```bash
curl -fsSL -o vaultix-cli https://github.com/doko89/vaultix/releases/latest/download/vaultix-cli-linux-amd64
chmod +x vaultix-cli
curl -s -b vaultix_session=<session-cookie> -X POST http://<manager-host>:8080/api/api-keys \
  -H 'Content-Type: application/json' \
  -d '{"name":"laptop","scopes":["read","write"]}'   # prints the key ONCE
./vaultix-cli login --manager http://<manager-host>:8080 --api-key <key>
./vaultix-cli agents list
```

Full manual path (migrate → seed admin → login → enroll → first backup → restore)
is documented in [E2E bootstrap](#e2e-bootstrap) below; dev-stack equivalents live
under [Dev quickstart](#dev-quickstart).

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
make migrate-up                    # goose-compatible up on db/migrations (DATABASE_URL overridable)
go run ./cmd/manager serve         # control plane (auto-applies migrations on boot)
go run ./cmd/agent run             # per-VM executor (enroll first, see E2E bootstrap)
make web-dev                       # Vite dev server, proxies /api to the manager
```

The manager container also auto-migrates on boot, so `make migrate-up` is
only needed for host-side runs. Stop with `make compose-down` (volumes
survive; `docker compose -f deploy/docker-compose.yml --profile dev down -v`
wipes them).

If `make compose-up` fails pulling MinIO images, your registry is hitting
the outage noted in `deploy/docker-compose.yml` (observed 2026-11-19) — use
the hermetic E2E stack instead: `make e2e-up` (manager :8081, S3Mock :9090,
postgres :5433, isolated under project `vaultix-e2e`).

## Local endpoints and credentials — LOCAL DEV ONLY

> **WARNING: every credential below is a published dev default. It is safe
> only on your own machine behind `localhost`. NEVER reuse these values —
> especially `VAULTIX_CRYPTO_KEY` — in staging, production, or any shared
> environment. See the prod-hardening checklist at the top of
> `deploy/docker-compose.yml` before exposing anything.**

| Service              | Dev endpoint              | E2E endpoint (`make e2e-up`) |
| -------------------- | ------------------------- | ---------------------------- |
| Manager API + web UI | http://localhost:8080     | http://localhost:8081        |
| Manager health       | http://localhost:8080/healthz | http://localhost:8081/healthz |
| S3 API               | http://localhost:9000 (MinIO) | http://localhost:9090 (S3Mock, no console) |
| S3 console           | http://localhost:9001 (MinIO) | — (use `aws --endpoint-url http://localhost:9090 s3 ls`) |
| Postgres             | localhost:5432            | localhost:5433               |

| Credential                    | Value                              | Env override            |
| ----------------------------- | ---------------------------------- | ----------------------- |
| Postgres user / password / db | `vaultix` / `vaultix-dev-only` / `vaultix` | `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` |
| S3 access / secret key        | `vaultix` / `vaultix-dev-only-change-me` | `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` |
| S3 bucket                     | `vaultix-backups`                  | `S3_BUCKET`             |
| Manager crypto key (32 chars) | `dev-only-vaultix-crypto-key-0000` | `VAULTIX_CRYPTO_KEY`    |

## E2E bootstrap

End-to-end path: stack → migrate → seed admin → enroll agent → first backup
→ restore. Needs `curl` and `jq`. Point `MGR` at your stack (dev `:8080`,
e2e `:8081`):

```bash
MGR=http://localhost:8080   # http://localhost:8081 after `make e2e-up`
```

**1. Boot the stack.**

```bash
make e2e-up                      # hermetic: postgres + s3mock + bucket + manager
# or: make compose-up            # dev: postgres + minio + bucket + manager
curl -s $MGR/healthz             # {"status":"ok",...} once the manager is up
```

**2. Migrate.** The container already ran `migrate up` on boot; re-run
explicitly any time with:

```bash
docker compose -f deploy/docker-compose.yml --profile dev exec manager /usr/local/bin/manager migrate up
# e2e project variant: add `-p vaultix-e2e` and swap `--profile dev` for `--profile e2e`
# (full binary path: `exec` bypasses the image ENTRYPOINT)
```

(`manager migrate` also has `down` and `status`; host-side runs use
`VAULTIX_DATABASE_URL=... go run ./cmd/manager migrate up`.)

**3. Seed the admin user.** `manager seed-admin` creates the first local
administrator (refuses when any user exists unless `--force` is passed).
Credentials come from `VAULTIX_ADMIN_USER` / `VAULTIX_ADMIN_PASSWORD`
(email defaults to `<user>@localhost`, overridable via
`VAULTIX_ADMIN_EMAIL`) or the equivalent `--username` / `--password` /
`--email` flags.

```bash
VAULTIX_ADMIN_USER=admin VAULTIX_ADMIN_PASSWORD='change-me-strong' make seed-admin
# e2e project variant:
docker compose -f deploy/docker-compose.yml -p vaultix-e2e --profile e2e exec \
  -e VAULTIX_ADMIN_USER=admin -e VAULTIX_ADMIN_PASSWORD='change-me-strong' \
  manager /usr/local/bin/manager seed-admin
```

**4. Log in (session for the gated APIs).** `/api/policies`, `/api/jobs`,
`/api/restore`, and friends require the `vaultix_session` cookie; the login
below captures it from the `Set-Cookie` header (the cookie is `Secure`, so
plain-HTTP clients must send it back as an explicit header). Mutating calls
also need `-H 'X-Requested-With: XMLHttpRequest'` (CSRF rule).

```bash
curl -s -D /tmp/vaultix-headers.txt -o /dev/null -X POST $MGR/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"admin@localhost","password":"change-me-strong"}'
# (identifier is the admin email: seed-admin defaults it to
# <user>@localhost, and usernames persist as the email address)
S=$(grep -i '^set-cookie: vaultix_session=' /tmp/vaultix-headers.txt | sed 's/.*vaultix_session=\([^;]*\).*/\1/')
AUTH="Cookie: vaultix_session=$S"
curl -s $MGR/api/auth/me -H "$AUTH"   # your user + roles
```

**5. Enroll an agent.** Issue a token, redeem it for agent credentials,
persist them with the agent CLI, then run the daemon:

```bash
ENROLL=$(curl -s -X POST $MGR/api/agents/enroll-tokens | jq -r .token)
JOIN=$(curl -s -X POST $MGR/api/agent/enroll -H 'Content-Type: application/json' \
  -d "{\"token\":\"$ENROLL\",\"hostname\":\"e2e-vm\"}")
AGENT_ID=$(echo "$JOIN" | jq -r .agent_id)
AGENT_TOKEN=$(echo "$JOIN" | jq -r .token)
export VAULTIX_AGENT_CONFIG=/tmp/vaultix-e2e-agent.yaml   # keeps e2e hermetic
go run ./cmd/agent enroll --manager $MGR --token "$AGENT_TOKEN" --agent-id "$AGENT_ID"
go run ./cmd/agent run        # heartbeat loop; Ctrl-C to stop
```

> The agent client speaks the manager's agent protocol at `/api/agent/*`
> (heartbeat, poll, report, grants, restores); there are no `/api/v1`
> paths left on either side, so `agent run` heartbeats, job pickup, and
> reporting work end to end.

**6. Run the first backup.** Create a policy (enabled by default), attach a
schedule, trigger a manual run, then watch the job:

```bash
POLICY_ID=$(curl -s -X POST $MGR/api/policies -H "$AUTH" \
  -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \
  -d "{\"name\":\"e2e-files\",\"agent_ids\":[\"$AGENT_ID\"],
       \"retention\":{\"keep_last_n\":3,\"keep_days\":7,\"keep_minimum\":1},
       \"targets\":[{\"type\":\"host_filesystem\",\"selector\":\"/var/log\"}]}" \
  | jq -r .policy.id)
SCHED_ID=$(curl -s -X POST $MGR/api/schedules -H "$AUTH" \
  -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \
  -d "{\"policy_id\":\"$POLICY_ID\",\"kind\":\"daily\",\"time_of_day\":\"02:00\",\"timezone\":\"UTC\"}" \
  | jq -r .id)
curl -s -X POST $MGR/api/schedules/$SCHED_ID/run -H "$AUTH" \
  -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \
  -d '{"agent_id":""}' | jq .            # 202 + jobs ("" fans out to all agents)
curl -s "$MGR/api/jobs/" -H "$AUTH" | jq .   # job states incl. the new run
```

The agent picks the job up on its next poll and reports back; poll
`GET /api/jobs/{id}` until `state` is `succeeded`.

**7. Restore from the snapshot.** Once a backup has produced a snapshot,
find it, then walk the plan → confirm → execute flow (each step is
explicit by design):

```bash
SNAP_ID=$(curl -s "$MGR/api/catalog?restorable_only=true" -H "$AUTH" | jq -r '.items[0].id')
PLAN_ID=$(curl -s -X POST $MGR/api/restore/plan -H "$AUTH" \
  -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \
  -d "{\"kind\":\"file\",\"snapshot_id\":\"$SNAP_ID\",\"requested_by\":\"admin\"}" \
  | jq -r .id)
CONFIRM_TOKEN=$(curl -s -X POST $MGR/api/restore/confirm -H "$AUTH" \
  -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \
  -d "{\"plan_id\":\"$PLAN_ID\",\"actor\":\"admin\"}" \
  | jq -r .token)
curl -s -X POST $MGR/api/restore/execute -H "$AUTH" \
  -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \
  -d "{\"token\":\"$CONFIRM_TOKEN\",\"actor\":\"admin\"}" | jq .
```

Tear down with `make e2e-down` (dev: `make compose-down`); append `-v` to
the underlying `down` to wipe volumes. Follow logs with `make e2e-logs`.

## Build

```bash
go build ./...
go build -o bin/manager ./cmd/manager
go build -o bin/agent ./cmd/agent
go vet ./...
```

Release (manager serves the web UI from `./web/dist`, so the UI must be
built first — the release Dockerfile does both stages automatically):

```bash
make release-build               # npm --prefix web run build + go build
# docker image equivalent:
docker build -f deploy/manager.Dockerfile -t vaultix-manager .
```

## Web dev

```bash
cd web && npm install && npm run dev   # dev server
npm --prefix web run build             # outputs web/dist/ (served by manager)
```

## Gates before submit

```bash
make vet && make lint && go test ./...   # Go gates (TECH_SPEC section 6)
npm --prefix web run typecheck && make web-build # web typecheck + build
```

CI (`.github/workflows/ci.yml`) runs the same gates plus a non-blocking
pinned-`sqlc` drift check (`generate` + `git diff` on `db/gen`) and
`docker compose config` validation for the dev and e2e profiles.

## Status

Manager boots (API + scheduler + migrations); agent enrollment,
heartbeat, backup/restore execution, and reporting work end to end; the
web UI wires all features to the manager API. E2E bootstrap is
documented above.
