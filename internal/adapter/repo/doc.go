// Package repo implements the domain repository ports on pgxpool +
// sqlc-generated queries (db/gen). It is the only place besides db/ that
// knows SQL; usecases program against domain ports, and cmd wires this
// package exactly once.
//
// # Constructor pattern
//
// There is a single constructor, New, which takes the shared *pgxpool.Pool
// and returns *Repos exposing one field per port implementation:
//
//	repos := repo.New(pool)
//	var users domain.UserRepository = repos.Users
//
// There is intentionally no per-repository exported constructor: cmd wiring
// stays one line, and tests either use pure mapping helpers (no DB) or
// build the whole bundle against a test database. Concrete-only stores
// without a domain port (policy targets, retention rules) hang off the same
// bundle: PolicyRepo.ReplaceTargets/ListTargets and RetentionStore. A later
// domain change should promote those to ports; until then usecases in other
// workstreams can declare narrow interfaces matching them.
//
// # Conventions
//
// Every DB call runs under a context timeout (DefaultQueryTimeout) unless
// the caller already set a deadline. Multi-statement mutations run in a
// pgx transaction. IDs are UUID strings; empty means "let the database
// generate one on create" or NULL for optional references. Errors map to
// the domain sentinels (ErrNotFound, ErrConflict, ErrValidation) and never
// carry secret material: password hashes, token hashes, ciphertext, and
// presigned data stay out of error strings and logs.
//
// # Schema friction (read before wiring)
//
// The MVP schema predates parts of the domain model, so a few mappings are
// lossy or encoded; each is documented at the mapping site:
//
//   - User.Username is the email address; LocalLoginDisabled is not persisted.
//   - BackupPolicy.AgentIDs persists the first entry only (single agent_id column).
//   - Policy versions freeze targets/schedule/retention/compression/encryption
//     into backup_policy_versions.config as JSON.
//   - Schedule kinds cron/disabled and daily/weekly clocks are encoded into
//     schedules.cron_expression by an internal codec (see schedule_repo.go).
//   - BackupJob trigger/storage/retry/creator extras live in
//     backup_jobs.target_resources as a JSON object; BackupJob.SnapshotID is
//     derived via reverse lookup, as is Snapshot.ManifestID.
//   - Snapshot.TargetType is stored in snapshots.storage_prefix so catalog
//     filtering keeps working; Snapshot.BytesOriginal is not persisted.
//   - SnapshotObject.Kind is not persisted; checksum "alg:hex" splits across
//     checksum/checksum_alg.
//   - SecretProfile.Kind round-trips via the description column because the
//     owner_type check constraint has no encryption/generic values.
//   - Notification event types derive from RefType; RefID must be a UUID.
//   - Audit outcome "ok" maps to "success" (the DB check value).
//   - AgentCredential requires an agent: unclaimed enrollment tokens cannot
//     be stored (agent_credentials.agent_id is NOT NULL).
//   - Stacks have no tables in scope (migrations are frozen), so StackRepo
//     is a thread-safe in-memory store until a stacks migration lands.
package repo
