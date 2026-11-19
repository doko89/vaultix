// Package scheduler holds the Manager job scheduler: queue definitions,
// River-shaped job args, worker scaffolding, recurring-schedule expansion,
// and retry/backoff policy mapping (PRD §7.9, §7.10).
//
// INTEGRATION NOTE (river): this package is intentionally dependency-free
// (no river import; go.mod is frozen for this track). The Args types already
// follow the River job-args convention (Kind() + JSON tags) and each worker's
// Work method maps 1:1 onto a river.Worker implementation. Integrator-2 adds
// github.com/riverqueue/river, replaces the in-process Runner with a
// river.Client adapter (see runner.go), and maps RetryPolicyFor onto
// river InsertOpts (MaxAttempts) and periodic-job definitions. No Redis is
// used anywhere; River is Postgres-backed.
package scheduler
