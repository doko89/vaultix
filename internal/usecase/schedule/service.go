package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Service owns schedule rules. It programs against domain ports only.
//
// policies is required (manual triggers and dispatch pin the policy's
// current version and fan out to its agents). Limits configure the
// per-agent and global concurrency caps (PRD §10.3); 0 = unlimited.
type Service struct {
	schedules domain.ScheduleRepository
	policies  domain.PolicyRepository
	clock     domain.Clock

	perAgentLimit int
	globalLimit   int
}

// Option customizes a Service.
type Option func(*Service)

// WithClock overrides the clock (tests).
func WithClock(c domain.Clock) Option {
	return func(s *Service) { s.clock = c }
}

// WithLimits sets the per-agent and global concurrency caps (0 = unlimited).
func WithLimits(perAgent, global int) Option {
	return func(s *Service) { s.perAgentLimit, s.globalLimit = perAgent, global }
}

// NewService builds a schedule Service.
func NewService(schedules domain.ScheduleRepository, policies domain.PolicyRepository, opts ...Option) *Service {
	s := &Service{schedules: schedules, policies: policies, clock: domain.SystemClock{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateInput carries the schedule definition. MaxConcurrency nil defaults
// to 1 (overlap prevention); explicit 0 means unlimited.
type CreateInput struct {
	PolicyID       string
	Kind           domain.ScheduleKind
	TimeOfDay      string
	Weekday        *time.Weekday
	IntervalHours  int
	CronExpr       string
	RunAt          *time.Time
	Timezone       string
	MaxConcurrency *int
}

// UpdateInput replaces the timing definition. Pause state is preserved
// (use Pause/Resume); NextRunAt is recomputed.
type UpdateInput struct {
	Kind           domain.ScheduleKind
	TimeOfDay      string
	Weekday        *time.Weekday
	IntervalHours  int
	CronExpr       string
	RunAt          *time.Time
	Timezone       string
	MaxConcurrency *int
}

// ManualTrigger runs a schedule on demand through a JobEnqueuer.
// AgentID "" fans out to every agent assigned to the policy.
// A nil Gate skips admission (tests); production MUST pass a real gate.
type ManualTrigger struct {
	ScheduleID string
	AgentID    string
	CreatedBy  string
	Enqueuer   JobEnqueuer
	Gate       ConcurrencyGate
}

// DispatchReport summarizes one scheduler tick.
type DispatchReport struct {
	Fired   []string
	Skipped []SkippedSchedule
	Failed  []FailedSchedule
}

// SkippedSchedule records an admission denial or inactive schedule.
type SkippedSchedule struct {
	ScheduleID string
	Reason     string
}

// FailedSchedule records a runner or persistence failure.
type FailedSchedule struct {
	ScheduleID string
	Err        error
}

// Create validates the definition, computes NextRunAt, and persists.
func (s *Service) Create(ctx context.Context, in CreateInput) (*domain.Schedule, error) {
	if _, err := s.policies.GetPolicy(ctx, in.PolicyID); err != nil {
		return nil, err
	}
	sched := &domain.Schedule{
		ID:            newID("sch"),
		PolicyID:      in.PolicyID,
		Kind:          in.Kind,
		TimeOfDay:     in.TimeOfDay,
		Weekday:       in.Weekday,
		IntervalHours: in.IntervalHours,
		CronExpr:      in.CronExpr,
		RunAt:         in.RunAt,
		Timezone:      in.Timezone,
		CreatedAt:     s.clock.Now(),
		UpdatedAt:     s.clock.Now(),
	}
	sched.MaxConcurrency = concurrencyOrDefault(in.MaxConcurrency)
	if err := sched.Validate(); err != nil {
		return nil, err
	}
	if sched.Kind == domain.ScheduleCron {
		if _, err := ParseCron(sched.CronExpr); err != nil {
			return nil, err
		}
	}
	sched.NextRunAt = NextRun(*sched, s.clock.Now())
	if sched.Active() && sched.NextRunAt == nil {
		return nil, fmt.Errorf("%w: schedule has no future firing (check run_at/cron)", domain.ErrValidation)
	}
	if err := s.schedules.CreateSchedule(ctx, sched); err != nil {
		return nil, err
	}
	return sched, nil
}

// Get returns one schedule by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.Schedule, error) {
	return s.schedules.GetSchedule(ctx, id)
}

// List returns all schedules for a policy.
func (s *Service) List(ctx context.Context, policyID string) ([]domain.Schedule, error) {
	return s.schedules.ListSchedules(ctx, policyID)
}

// Update replaces the timing definition and recomputes NextRunAt.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (*domain.Schedule, error) {
	sched, err := s.schedules.GetSchedule(ctx, id)
	if err != nil {
		return nil, err
	}
	sched.Kind = in.Kind
	sched.TimeOfDay = in.TimeOfDay
	sched.Weekday = in.Weekday
	sched.IntervalHours = in.IntervalHours
	sched.CronExpr = in.CronExpr
	sched.RunAt = in.RunAt
	sched.Timezone = in.Timezone
	sched.MaxConcurrency = concurrencyOrDefault(in.MaxConcurrency)
	sched.UpdatedAt = s.clock.Now()
	if err := sched.Validate(); err != nil {
		return nil, err
	}
	if sched.Kind == domain.ScheduleCron {
		if _, err := ParseCron(sched.CronExpr); err != nil {
			return nil, err
		}
	}
	sched.NextRunAt = NextRun(*sched, s.clock.Now())
	if sched.Active() && sched.NextRunAt == nil {
		return nil, fmt.Errorf("%w: schedule has no future firing (check run_at/cron)", domain.ErrValidation)
	}
	if err := s.schedules.UpdateSchedule(ctx, sched); err != nil {
		return nil, err
	}
	return sched, nil
}

// Delete removes a schedule.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.schedules.DeleteSchedule(ctx, id)
}

// Pause suspends automatic firing; the definition and NextRunAt are kept.
func (s *Service) Pause(ctx context.Context, id string) (*domain.Schedule, error) {
	sched, err := s.schedules.GetSchedule(ctx, id)
	if err != nil {
		return nil, err
	}
	sched.Pause()
	sched.UpdatedAt = s.clock.Now()
	if err := s.schedules.UpdateSchedule(ctx, sched); err != nil {
		return nil, err
	}
	return sched, nil
}

// Resume re-enables firing and recomputes NextRunAt from now, so a stale
// pre-pause instant never fires a catch-up burst.
func (s *Service) Resume(ctx context.Context, id string) (*domain.Schedule, error) {
	sched, err := s.schedules.GetSchedule(ctx, id)
	if err != nil {
		return nil, err
	}
	sched.Resume()
	sched.NextRunAt = NextRun(*sched, s.clock.Now())
	sched.UpdatedAt = s.clock.Now()
	if err := s.schedules.UpdateSchedule(ctx, sched); err != nil {
		return nil, err
	}
	return sched, nil
}

// TriggerManual enqueues one job per target agent through q, pinning the
// policy's current version. A paused schedule MAY be triggered manually
// (pause suspends automatic firing, not explicit runs); a disabled schedule
// may not. Admission denials return domain.ErrConflict.
func (s *Service) TriggerManual(ctx context.Context, in ManualTrigger) ([]*domain.BackupJob, error) {
	if in.Enqueuer == nil {
		return nil, fmt.Errorf("%w: manual trigger requires a JobEnqueuer", domain.ErrValidation)
	}
	sched, err := s.schedules.GetSchedule(ctx, in.ScheduleID)
	if err != nil {
		return nil, err
	}
	if sched.Kind == domain.ScheduleDisabled {
		return nil, fmt.Errorf("%w: schedule is disabled", domain.ErrConflict)
	}
	p, err := s.policies.GetPolicy(ctx, sched.PolicyID)
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		return nil, fmt.Errorf("%w: policy %q is disabled", domain.ErrConflict, p.ID)
	}
	if p.CurrentVersion <= 0 {
		return nil, fmt.Errorf("%w: policy %q has no published version", domain.ErrConflict, p.ID)
	}
	agents := p.AgentIDs
	if in.AgentID != "" {
		found := false
		for _, a := range agents {
			if a == in.AgentID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: agent %q is not assigned to policy %q",
				domain.ErrValidation, in.AgentID, p.ID)
		}
		agents = []string{in.AgentID}
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("%w: policy %q has no assigned agents", domain.ErrConflict, p.ID)
	}
	counts, err := s.liveCounts(ctx, in.Gate)
	if err != nil {
		return nil, err
	}
	// Manual runs bypass the paused flag (explicit user action) but still
	// honor overlap prevention and concurrency caps.
	probe := *sched
	probe.Paused = false
	if d := Admit(AdmitInput{
		Schedule: probe, PolicyID: p.ID, AgentIDs: agents,
		PerAgentLimit: s.perAgentLimit, GlobalLimit: s.globalLimit, Live: counts,
	}); !d.Admit {
		return nil, fmt.Errorf("%w: %s", domain.ErrConflict, d.Reason)
	}
	var jobs []*domain.BackupJob
	for _, a := range agents {
		j, err := in.Enqueuer.EnqueueJob(ctx, EnqueueRequest{
			PolicyID:      p.ID,
			PolicyVersion: p.CurrentVersion,
			ScheduleID:    sched.ID,
			AgentID:       a,
			CreatedBy:     in.CreatedBy,
		})
		if err != nil {
			return jobs, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// DispatchDue fires every schedule due at `now` through the RecurringRunner.
// One-time schedules that fire are retired to disabled; recurring schedules
// advance NextRunAt only after the runner succeeds. Denied or inactive
// schedules are reported as skipped; runner failures are reported per
// schedule without aborting the tick.
func (s *Service) DispatchDue(ctx context.Context, now time.Time, r RecurringRunner, gate ConcurrencyGate) (DispatchReport, error) {
	var rep DispatchReport
	if r == nil {
		return rep, fmt.Errorf("%w: dispatch requires a RecurringRunner", domain.ErrValidation)
	}
	due, err := s.schedules.DueSchedules(ctx, now, 500)
	if err != nil {
		return rep, err
	}
	counts, err := s.liveCounts(ctx, gate)
	if err != nil {
		return rep, err
	}
	for i := range due {
		sched := due[i]
		if !sched.Active() {
			rep.Skipped = append(rep.Skipped, SkippedSchedule{ScheduleID: sched.ID, Reason: "schedule is paused or disabled"})
			continue
		}
		p, err := s.policies.GetPolicy(ctx, sched.PolicyID)
		if err != nil {
			rep.Failed = append(rep.Failed, FailedSchedule{ScheduleID: sched.ID, Err: err})
			continue
		}
		if !p.Enabled || p.CurrentVersion <= 0 || len(p.AgentIDs) == 0 {
			rep.Skipped = append(rep.Skipped, SkippedSchedule{ScheduleID: sched.ID, Reason: "policy is disabled, unversioned, or has no agents"})
			continue
		}
		if d := Admit(AdmitInput{
			Schedule: sched, PolicyID: p.ID, AgentIDs: p.AgentIDs,
			PerAgentLimit: s.perAgentLimit, GlobalLimit: s.globalLimit, Live: counts,
		}); !d.Admit {
			rep.Skipped = append(rep.Skipped, SkippedSchedule{ScheduleID: sched.ID, Reason: d.Reason})
			continue
		}
		dueAt := sched.NextRunAt
		fire := Fire{
			Schedule:      sched,
			PolicyID:      p.ID,
			PolicyVersion: p.CurrentVersion,
			AgentIDs:      append([]string{}, p.AgentIDs...),
			DueAt:         now,
		}
		if dueAt != nil {
			fire.DueAt = *dueAt
		}
		if err := r.RunScheduled(ctx, fire); err != nil {
			rep.Failed = append(rep.Failed, FailedSchedule{ScheduleID: sched.ID, Err: err})
			continue
		}
		// Runner accepted the firing: retire one-time schedules, advance
		// recurring ones. A nil next run (cron without match) retires the
		// schedule rather than spinning the tick forever.
		if sched.Kind == domain.ScheduleOneTime {
			sched.Kind = domain.ScheduleDisabled
			sched.NextRunAt = nil
		} else if next := NextRun(sched, now); next == nil {
			sched.Kind = domain.ScheduleDisabled
			sched.NextRunAt = nil
		} else {
			sched.NextRunAt = next
		}
		sched.UpdatedAt = s.clock.Now()
		if err := s.schedules.UpdateSchedule(ctx, &sched); err != nil {
			rep.Failed = append(rep.Failed, FailedSchedule{ScheduleID: sched.ID, Err: err})
			continue
		}
		rep.Fired = append(rep.Fired, sched.ID)
	}
	return rep, nil
}

// liveCounts snapshots load through the gate; a nil gate means no load.
func (s *Service) liveCounts(ctx context.Context, gate ConcurrencyGate) (Counts, error) {
	if gate == nil {
		return Counts{}, nil
	}
	return gate.ActiveCounts(ctx)
}

// concurrencyOrDefault applies DefaultMaxConcurrency when the caller leaves
// the cap unset; an explicit 0 means unlimited.
func concurrencyOrDefault(v *int) int {
	if v == nil {
		return DefaultMaxConcurrency
	}
	return *v
}

// newID mints a prefixed random ID (stdlib only; 128 bits of entropy).
func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
