package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---- Integration seams (narrow, locally defined) ----

// JobEnqueuer creates one backup job for a manual trigger.
//
// INTEGRATION NOTE: the jobs track owns the real implementation (backed by
// domain.BackupJobRepository). It MUST pin req.PolicyVersion on the created
// job, stamp Trigger manual, and return the persisted job. Until it lands,
// wire a stub that returns an explicit "not implemented" error.
type JobEnqueuer interface {
	EnqueueJob(ctx context.Context, req EnqueueRequest) (*domain.BackupJob, error)
}

// EnqueueRequest describes one manual job to create.
type EnqueueRequest struct {
	PolicyID      string
	PolicyVersion int // pinned immutable version
	ScheduleID    string
	AgentID       string
	CreatedBy     string
}

// RecurringRunner executes one due recurring firing.
//
// INTEGRATION NOTE: the scheduler track owns the real implementation (it
// fans out to the jobs track to create one job per agent, honoring the
// pinned version and the frozen firing definition). DispatchDue calls it
// once per admitted due schedule; only a nil error advances NextRunAt.
type RecurringRunner interface {
	RunScheduled(ctx context.Context, fire Fire) error
}

// Fire is one admitted recurring firing.
type Fire struct {
	// Schedule is the frozen firing definition. The jobs track MUST record
	// it on each created job (PRD §7.9: record the schedule definition
	// used for each job).
	Schedule      domain.Schedule
	PolicyID      string
	PolicyVersion int // pinned immutable version
	AgentIDs      []string
	DueAt         time.Time
}

// ConcurrencyGate reports live (non-terminal) job load for admission.
//
// INTEGRATION NOTE: the jobs track owns the real implementation (counted
// from domain.BackupJobRepository by state). Admission treats a nil gate as
// "no live load" (admits everything the schedule allows); production
// dispatch MUST pass a real gate or overlap prevention is inert.
type ConcurrencyGate interface {
	ActiveCounts(ctx context.Context) (Counts, error)
}

// Counts is a point-in-time load snapshot.
type Counts struct {
	Global    int
	PerAgent  map[string]int
	PerPolicy map[string]int // active jobs per policy = per protected resource
}

// ActiveForPolicy returns the active count guarding one protected resource.
func (c Counts) ActiveForPolicy(policyID string) int {
	return c.PerPolicy[policyID]
}

// ActiveForAgent returns the active count for one agent.
func (c Counts) ActiveForAgent(agentID string) int {
	return c.PerAgent[agentID]
}

// ---- Admission (pure; overlap prevention + concurrency limits) ----

// AdmitInput feeds the admission decision.
type AdmitInput struct {
	Schedule domain.Schedule // MaxConcurrency caps this protected resource; 0 = unlimited
	PolicyID string
	AgentIDs []string
	// PerAgentLimit / GlobalLimit are the configured caps (PRD §10.3);
	// 0 = unlimited.
	PerAgentLimit int
	GlobalLimit   int
	Live          Counts
}

// AdmitDecision is the admission verdict with a human-readable reason.
type AdmitDecision struct {
	Admit  bool
	Reason string
}

// Admit decides whether a firing may proceed. Denials wrap
// domain.ErrConflict so HTTP maps them to 409 (overlap / at capacity).
func Admit(in AdmitInput) AdmitDecision {
	if !in.Schedule.Active() {
		return AdmitDecision{Reason: "schedule is paused or disabled"}
	}
	if in.Schedule.MaxConcurrency > 0 &&
		in.Live.ActiveForPolicy(in.PolicyID) >= in.Schedule.MaxConcurrency {
		return AdmitDecision{Reason: fmt.Sprintf(
			"protected resource at capacity: policy %q has %d active job(s), limit %d",
			in.PolicyID, in.Live.ActiveForPolicy(in.PolicyID), in.Schedule.MaxConcurrency)}
	}
	if in.PerAgentLimit > 0 {
		for _, a := range in.AgentIDs {
			if n := in.Live.ActiveForAgent(a); n >= in.PerAgentLimit {
				return AdmitDecision{Reason: fmt.Sprintf(
					"agent %q at capacity: %d active job(s), limit %d", a, n, in.PerAgentLimit)}
			}
		}
	}
	if in.GlobalLimit > 0 && in.Live.Global >= in.GlobalLimit {
		return AdmitDecision{Reason: fmt.Sprintf(
			"global capacity reached: %d active job(s), limit %d", in.Live.Global, in.GlobalLimit)}
	}
	return AdmitDecision{Admit: true, Reason: "admitted"}
}

// DefaultMaxConcurrency is applied when a schedule is created without an
// explicit cap: 1 prevents unintended overlap for the same protected
// resource (PRD §7.9). Explicit 0 means unlimited.
const DefaultMaxConcurrency = 1
