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
- [resolved at phase-3 integration] `manager seed-admin` landed with the
  jobproto track; README step 3 and `make seed-admin` are live.
- S3Mock has no in-container healthcheck (verified: no curl/wget in the
  image); readiness is enforced by the s3-init retry loop instead.
- Volumes lost their fixed `vaultix-*` names so `-p vaultix-e2e` isolates
  data per project; pre-existing `vaultix-pgdata`/`vaultix-minio-data`
  volumes are orphaned but intact (`docker volume ls | grep vaultix`).
- [resolved at phase-3 integration] Agent/manager protocol paths are
  aligned at `/api/agent/*` (verified: zero `/api/v1` references in the
  agent client); heartbeat, poll, report, grants, and restores reconcile,
  including the completion-manifest shape and restore report states.

---

# Deferred acceptance findings (integrator-4, fix batch, 2026-11-19)

Logged at the fix-batch integration (merges of feat/fix-tls,
feat/fix-multipart, feat/fix-dbcreds, feat/fix-filerestore). Deferred =
accepted behavior gap, no fix in this batch. Revisit per item below.

- F4 — In-flight job cancel is serially blocked (deferred 2026-11-19):
  cancelling a running job waits behind the executor's serial loop, so
  the cancel acknowledgement lands late for long-running backups. The
  409 state guard preserves correctness (no double-apply, no lost
  terminal state), so the gap is latency-only. Revisit when adding
  cooperative cancel / mid-run checkpointing to the agent executor.
- F6 — OIDC callback needs `?provider=` hand-embedded (deferred
  2026-11-19): the callback URL works only when the caller
  hand-embeds the `?provider=` query param; the RP does not derive or
  persist the provider across the round-trip on its own. Revisit when
  hardening OIDC (state/nonce store, multi-provider routing).
- F7 — Failed success-reports are never retried (deferred 2026-11-19):
  if the agent's terminal success report POST fails (manager down,
  network blip), the job stays non-terminal server-side even though
  the backup bytes are in storage; nothing re-sends the report.
  Revisit when adding report retry / at-least-once delivery with
  idempotent completion handling.
- F8 — Cosmetics (deferred 2026-11-19): (a) doubled `agent:` prefix in
  some log/error strings; (b) presigned URLs leak into job error
  text surfaced via the API; (c) a `manifest.json` sidecar lands in
  the restore destination alongside restored data. All cosmetic /
  hygiene; (b) has a mild secret-exposure flavor — scrub URLs from
  errors before any externally-visible deployment.
- NEW (integrator-4 live finding, 2026-11-19): the backup verify gate
  does not fall back to the default destination — a policy/job with
  empty `storage_id` uploads fine (grants resolve the default) but
  verify-then-complete fails with `head_error (s3: destination id is
  required)`, so the job can never succeed. Workaround (used by this
  re-verification): always set `storage_id` explicitly on policies.
  Fix direction: resolve the default destination in
  `CompleteJobFlat`/verify like `destinationForJob` already does for
  grants, or reject empty-`storage_id` policies at the boundary.
- FIXED live (integrator-4, 2026-11-19, in the `chore: integrator-4
  merge fallout and tech-debt log` commit): selective restores
  failed on the `manifest.json` metadata sidecar
  (`selective restore (N path(s)) needs a file/volume snapshot, got
  "file"`), because the agent tried to selective-extract it. The
  agent now drops manifest objects before grants/download when paths
  are requested (`isManifestObject` in `internal/agent/restore.go`,
  covered by `TestRestoreExecuteSelectiveSkipsManifestObject`).
  Full-scope restores still materialize the sidecar (F8c above).
