package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// BackupExecutor is the narrow seam to the jobs track. The backup-execution
// worker calls it when a schedule fires.
//
// INTEGRATION NOTE (jobs track): *job.Service from internal/usecase/job
// (parallel track, not in this worktree) already has exactly this method —
// EnqueueScheduled(ctx, agentID, policyID, scheduleID) (*domain.BackupJob,
// error) — so serve.go passes it directly with no adapter.
type BackupExecutor interface {
	EnqueueScheduled(ctx context.Context, agentID, policyID, scheduleID string) (*domain.BackupJob, error)
}

// RetentionEnforcer is the narrow seam to the recovery track. The retention
// worker calls it when a retention pass is due.
//
// INTEGRATION NOTE (recovery track): *retention.Service (parallel track)
// exposes Evaluate + Apply rather than one call, so integrator-2 writes a
// small adapter: load the policy's retention rule via the repos track,
// Evaluate the deletion plan, then Apply it (with the recovery-point floor
// the retention service enforces).
type RetentionEnforcer interface {
	EnforceRetention(ctx context.Context, policyID string) error
}

// BackupExecutionWorker dispatches one schedule-triggered backup. Under
// River it becomes river.Worker[BackupExecutionArgs] with the same body.
type BackupExecutionWorker struct {
	Executor BackupExecutor
	Log      *slog.Logger
}

// Kind mirrors BackupExecutionArgs.Kind for registry symmetry.
func (BackupExecutionWorker) Kind() string { return BackupExecutionArgs{}.Kind() }

// Work dispatches the backup via the jobs service.
func (w BackupExecutionWorker) Work(ctx context.Context, args BackupExecutionArgs) error {
	if w.Executor == nil {
		return fmt.Errorf("scheduler: BackupExecutor is not wired")
	}
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	job, err := w.Executor.EnqueueScheduled(ctx, args.AgentID, args.PolicyID, args.ScheduleID)
	if err != nil {
		return fmt.Errorf("scheduler: enqueue scheduled backup (policy %s): %w", args.PolicyID, err)
	}
	log.Info("scheduled backup dispatched",
		"job_id", job.ID, "agent_id", args.AgentID, "policy_id", args.PolicyID, "schedule_id", args.ScheduleID)
	return nil
}

// RetentionWorker runs one retention pass. Under River it becomes
// river.Worker[RetentionArgs] with the same body.
type RetentionWorker struct {
	Enforcer RetentionEnforcer
	Log      *slog.Logger
}

// Kind mirrors RetentionArgs.Kind for registry symmetry.
func (RetentionWorker) Kind() string { return RetentionArgs{}.Kind() }

// Work enforces retention for the policy.
func (w RetentionWorker) Work(ctx context.Context, args RetentionArgs) error {
	if w.Enforcer == nil {
		return fmt.Errorf("scheduler: RetentionEnforcer is not wired")
	}
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	if err := w.Enforcer.EnforceRetention(ctx, args.PolicyID); err != nil {
		return fmt.Errorf("scheduler: enforce retention (policy %s): %w", args.PolicyID, err)
	}
	log.Info("retention pass completed",
		"policy_id", args.PolicyID, "trigger", args.Trigger, "elapsed", time.Since(start).String())
	return nil
}
