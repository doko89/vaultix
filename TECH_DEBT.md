# Tech Debt — Phase 2 Integration

Locked decision at Phase 2 integration: the tracks' tested stdlib
equivalents stay as-is. The following production-grade replacements
were deliberately NOT added to `go.mod`:

- `river` (job queue) — replaced by in-process scheduler runner in
  `internal/adapter/scheduler` + `usecase/job` service.
- `goose` (migrations) — replaced by migrate runner in `cmd/manager`.
- `prometheus` client — replaced by metrics exposition in the http root.
- `go-oidc` — replaced by OIDC RP in `internal/adapter/oidc`.
- `aws-sdk-go` — replaced by stdlib SigV4 S3 client in
  `internal/adapter/s3`.

Revisit when durability, multi-replica operation, or richer provider
coverage is required.

---

# Deploy / E2E notes (appended with feat/p3-deploy)

- MinIO images unpullable (2026-11-19, verified): Docker Hub answers "pull
  access denied" for minio/minio (+mc) :latest and pinned RELEASE tags;
  quay.io answers 401. Dev profile keeps the pinned tags as defaults
  (`MINIO_IMAGE`/`MC_IMAGE` overridable); the e2e profile runs S3Mock
  (adobe/s3mock:5.2.3, pull-verified) instead. Revisit when MinIO restores
  registry access; then consider retiring the S3Mock backend.
- Compose `depends_on` on an inactive-profile service is a validation error
  (verified: "depends on undefined service"), so the manager depends only on
  postgres and selects its S3 backend via `VAULTIX_S3_ENDPOINT`. Safe
  because the S3 registry resolves lazily; do not add profile-scoped
  `depends_on` back without splitting the manager service per profile.
- No `.dockerignore` at repo root (out of scope for the deploy track): the
  manager Dockerfile uses stage-scoped COPYs so a dirty checkout cannot
  break the build, but adding one would shrink build contexts.
- `manager seed-admin` (jobproto track) is the documented bootstrap step 3;
  the README marks it pending and `make seed-admin` fails loudly until it
  lands.
- S3Mock has no in-container healthcheck (verified: no curl/wget in the
  image); readiness is enforced by the s3-init retry loop instead.
- Volumes lost their fixed `vaultix-*` names so `-p vaultix-e2e` isolates
  data per project; pre-existing `vaultix-pgdata`/`vaultix-minio-data`
  volumes are orphaned but intact (`docker volume ls | grep vaultix`).
- Agent/manager protocol paths diverged (verified live 2026-11-19):
  `internal/agent/client.go` calls `/api/v1/agents/*` but the manager
  serves `/api/agent/*` (heartbeat 404s, job pickup impossible). Agent
  track owns the fix; E2E backup execution is blocked on it.
