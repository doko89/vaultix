// Package job implements backup-job orchestration (PRD §7.10, §10.1).
//
// The Service in this package is the single owner of the 8-state job
// lifecycle (queued → assigned → running → uploading → verifying →
// succeeded / failed / cancelled). Every transition is validated against
// the domain state machine and persisted through domain ports, so an
// illegal transition is rejected and incomplete uploads are never marked
// successful.
//
// The Service exposes two faces on the same type:
//
//  1. UI-facing methods used by the Manager REST API and scheduler:
//     EnqueueManual, EnqueueScheduled, Assign, Transition, UpdateProgress,
//     RecordEncryptionStatus, RecordError, Retry, Cancel, Get, List,
//     RetryHistory, CountActive.
//
//  2. Agent-facing queue methods, wired by the agents track behind its own
//     HTTP/gRPC handlers:
//
//     PollJobs(ctx, agentID string, capabilities []string) ([]*domain.BackupJob, error)
//     ReportResult(ctx, agentID, jobID string, outcome Outcome) (*domain.BackupJob, error)
//
// PollJobs assigns the oldest due queued jobs to a healthy agent and
// returns them; ReportResult validates the agent's report (unknown job and
// agent mismatch are rejected) and advances the job. See their method docs
// for the exact contract the agents track can rely on.
//
// INTEGRATION NOTE (agents track): depend on PollJobs/ReportResult only;
// do not reach into UI methods from agent handlers. Agent capability
// advertisement stays a []string so the agents track can pass heartbeated
// capabilities straight through.
//
// INTEGRATION NOTE (repo adapter): BackupJobRepository.UpdateJob must
// persist the full job row including the new state; ListJobs should return
// oldest-first within a state so PollJobs ordering is stable even before
// the service-side sort. Push the date bounds in job.Filter down to SQL
// when convenient; the service applies them in memory regardless.
//
// INTEGRATION NOTE (scheduler track): use EnqueueScheduled and CountActive
// for due-schedule claiming and overlap/concurrency guards.
package job
