package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// CancelNotifier tells an agent that one of its jobs was cancelled.
//
// INTEGRATION NOTE: the agents track implements this (e.g. flag the job so
// the next agent poll/heartbeat sees the cancellation, or push over an
// active stream). Cancel treats notification as best-effort: a notification
// error never fails the cancellation because the persisted cancelled state
// is the source of truth and the agent revalidates on ReportResult.
type CancelNotifier interface {
	NotifyCancel(ctx context.Context, agentID, jobID string) error
}

type noopCancelNotifier struct{}

func (noopCancelNotifier) NotifyCancel(context.Context, string, string) error { return nil }

// NoopCancelNotifier is the default CancelNotifier until the agents track
// wires a real one.
func NoopCancelNotifier() CancelNotifier { return noopCancelNotifier{} }

// ConcurrencyLimits caps how many active (non-terminal) jobs may exist.
// Zero means unlimited on that dimension. Limits are enforced in
// EnqueueManual, EnqueueScheduled (global + per-agent) and PollJobs
// (per-agent skip), and are observable through CountActive so the
// scheduler track can apply overlap guards before enqueueing.
type ConcurrencyLimits struct {
	MaxPerAgent int
	MaxGlobal   int
}

// Deps wires the Service. Jobs, Agents and Policies are domain ports
// implemented by adapters; Clock, Cancels, IDGenerator and PollBatchSize
// have production defaults when left zero.
type Deps struct {
	Jobs     domain.BackupJobRepository
	Agents   domain.AgentRepository
	Policies domain.PolicyRepository

	Clock         domain.Clock
	Cancels       CancelNotifier
	Limits        ConcurrencyLimits
	IDGenerator   func() string
	PollBatchSize int
}

// Filter narrows job listings: the embedded domain.JobFilter (agent, policy,
// state) is pushed to the repository, while Since/Until bound CreatedAt and
// are applied in memory.
//
// INTEGRATION NOTE (repo adapter): push Since/Until into the SQL query when
// convenient; until then Total on a date-bounded page reflects the
// pre-date-filter count.
type Filter struct {
	domain.JobFilter
	Since *time.Time
	Until *time.Time
}

// Outcome is an agent's execution report consumed by ReportResult. A zero
// State means "progress update only" (no transition); any other State must
// be a legal transition from the job's current state.
type Outcome struct {
	State domain.JobState

	Progress        *float64
	BytesRead       *uint64
	BytesCompressed *uint64
	BytesUploaded   *uint64

	Encrypted *bool
	KeyRef    string

	// SnapshotID registers produced artifacts; it never marks a job
	// successful by itself (only the verifying → succeeded transition,
	// after integrity checks, does that).
	SnapshotID string

	ErrorCode    string
	ErrorMessage string
}

// ProgressUpdate carries measurable execution progress (PRD §7.10).
type ProgressUpdate struct {
	Progress        *float64 // 0..1
	BytesRead       *uint64
	BytesCompressed *uint64
	BytesUploaded   *uint64
}

// Service owns the backup-job lifecycle. See package doc for the UI-facing
// vs agent-facing method split.
type Service struct {
	jobs     domain.BackupJobRepository
	agents   domain.AgentRepository
	policies domain.PolicyRepository

	clock         domain.Clock
	cancels       CancelNotifier
	limits        ConcurrencyLimits
	idgen         func() string
	pollBatchSize int
}

// DefaultPollBatchSize caps how many jobs one PollJobs call assigns.
const DefaultPollBatchSize = 10

// NewService builds a Service, filling production defaults for optional deps.
func NewService(d Deps) *Service {
	s := &Service{
		jobs:          d.Jobs,
		agents:        d.Agents,
		policies:      d.Policies,
		clock:         d.Clock,
		cancels:       d.Cancels,
		limits:        d.Limits,
		idgen:         d.IDGenerator,
		pollBatchSize: d.PollBatchSize,
	}
	if s.clock == nil {
		s.clock = domain.SystemClock{}
	}
	if s.cancels == nil {
		s.cancels = NoopCancelNotifier()
	}
	if s.idgen == nil {
		s.idgen = defaultID
	}
	if s.pollBatchSize <= 0 {
		s.pollBatchSize = DefaultPollBatchSize
	}
	return s
}

func defaultID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to timestamp hex; uniqueness still holds in practice.
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(b[:])
}

// ---- Enqueue ----

// EnqueueManual starts a manual backup job for an agent under a policy,
// pinning the policy's current version so later policy edits cannot rewrite
// this job's configuration (PRD §7.4). The job starts queued.
func (s *Service) EnqueueManual(ctx context.Context, agentID, policyID, createdBy string) (*domain.BackupJob, error) {
	return s.enqueue(ctx, agentID, policyID, domain.JobTriggerManual, "", createdBy)
}

// EnqueueScheduled starts a job for a due schedule. scheduleID records which
// schedule definition fired; the frozen policy version carries the exact
// configuration the job executes (PRD §7.4, §7.9).
func (s *Service) EnqueueScheduled(ctx context.Context, agentID, policyID, scheduleID string) (*domain.BackupJob, error) {
	if scheduleID == "" {
		return nil, fmt.Errorf("job: schedule id is required: %w", domain.ErrValidation)
	}
	return s.enqueue(ctx, agentID, policyID, domain.JobTriggerSchedule, scheduleID, "")
}

func (s *Service) enqueue(ctx context.Context, agentID, policyID string, trigger domain.JobTrigger, scheduleID, createdBy string) (*domain.BackupJob, error) {
	if agentID == "" || policyID == "" {
		return nil, fmt.Errorf("job: agent id and policy id are required: %w", domain.ErrValidation)
	}
	agent, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent.Disabled {
		return nil, fmt.Errorf("job: agent %s is disabled: %w", agentID, domain.ErrConflict)
	}
	policy, err := s.policies.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("job: policy %s is disabled: %w", policyID, domain.ErrConflict)
	}
	version, err := s.policies.GetVersion(ctx, policyID, policy.CurrentVersion)
	if err != nil {
		return nil, fmt.Errorf("job: policy %s has no published version %d: %w", policyID, policy.CurrentVersion, domain.ErrConflict)
	}
	if err := s.checkEnqueueLimits(ctx, agentID); err != nil {
		return nil, err
	}

	storageID := version.StorageID
	if storageID == "" {
		storageID = policy.StorageID
	}
	now := s.clock.Now()
	j := &domain.BackupJob{
		ID:            s.idgen(),
		AgentID:       agentID,
		PolicyID:      policyID,
		PolicyVersion: policy.CurrentVersion,
		ScheduleID:    scheduleID,
		Trigger:       trigger,
		State:         domain.JobQueued,
		Encrypted:     version.Encryption.Enabled,
		KeyRef:        version.Encryption.KeyRef,
		StorageID:     storageID,
		Attempt:       1,
		CreatedBy:     createdBy,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobs.CreateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) checkEnqueueLimits(ctx context.Context, agentID string) error {
	if s.limits.MaxGlobal > 0 {
		n, err := s.countActive(ctx, "")
		if err != nil {
			return err
		}
		if n >= s.limits.MaxGlobal {
			return fmt.Errorf("job: global concurrency limit (%d) reached: %w", s.limits.MaxGlobal, domain.ErrConflict)
		}
	}
	if s.limits.MaxPerAgent > 0 {
		n, err := s.countActive(ctx, agentID)
		if err != nil {
			return err
		}
		if n >= s.limits.MaxPerAgent {
			return fmt.Errorf("job: agent %s concurrency limit (%d) reached: %w", agentID, s.limits.MaxPerAgent, domain.ErrConflict)
		}
	}
	return nil
}

// ---- Transitions ----

// Assign moves a queued job to assigned for the given agent (manager-side
// assignment; agents normally receive jobs through PollJobs).
func (s *Service) Assign(ctx context.Context, jobID, agentID string) (*domain.BackupJob, error) {
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.State != domain.JobQueued {
		return nil, fmt.Errorf("job: assign requires queued state, job %s is %s: %w", jobID, j.State, domain.ErrConflict)
	}
	agent, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent.Disabled {
		return nil, fmt.Errorf("job: agent %s is disabled: %w", agentID, domain.ErrConflict)
	}
	j.AgentID = agentID
	if err := domain.TransitionTo(&j.State, domain.JobAssigned); err != nil {
		return nil, err
	}
	j.UpdatedAt = s.clock.Now()
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// Transition advances a job to the next state, persisting the change.
// Illegal transitions are rejected with an ErrConflict-wrapping error and
// nothing is persisted.
func (s *Service) Transition(ctx context.Context, jobID string, next domain.JobState) (*domain.BackupJob, error) {
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if err := s.applyTransition(j, next); err != nil {
		return nil, err
	}
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// applyTransition validates and stamps a state change in memory.
func (s *Service) applyTransition(j *domain.BackupJob, next domain.JobState) error {
	if !j.State.CanTransitionTo(next) {
		return fmt.Errorf("job: illegal job transition %s -> %s: %w", j.State, next, domain.ErrConflict)
	}
	now := s.clock.Now()
	if next == domain.JobRunning && j.StartedAt == nil {
		j.StartedAt = &now
	}
	if next.Terminal() && j.FinishedAt == nil {
		j.FinishedAt = &now
	}
	j.State = next
	j.UpdatedAt = now
	return nil
}

// UpdateProgress records measurable progress and byte counters. It never
// changes state: completion is reported through ReportResult/Transition so
// progress alone cannot fabricate success.
func (s *Service) UpdateProgress(ctx context.Context, jobID string, up ProgressUpdate) (*domain.BackupJob, error) {
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.State.Terminal() {
		return nil, fmt.Errorf("job: cannot update progress of terminal job %s (%s): %w", jobID, j.State, domain.ErrConflict)
	}
	if err := checkProgress(up.Progress); err != nil {
		return nil, err
	}
	if up.Progress != nil {
		j.Progress = up.Progress
	}
	if up.BytesRead != nil {
		j.BytesRead = up.BytesRead
	}
	if up.BytesCompressed != nil {
		j.BytesCompressed = up.BytesCompressed
	}
	if up.BytesUploaded != nil {
		j.BytesUploaded = up.BytesUploaded
	}
	j.UpdatedAt = s.clock.Now()
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func checkProgress(p *float64) error {
	if p != nil && (*p < 0 || *p > 1) {
		return fmt.Errorf("job: progress must be within 0..1: %w", domain.ErrValidation)
	}
	return nil
}

// RecordEncryptionStatus records whether the job's artifacts are encrypted
// and, when enabled, the key reference needed for restore (PRD §7.10, §7.11).
func (s *Service) RecordEncryptionStatus(ctx context.Context, jobID string, encrypted bool, keyRef string) (*domain.BackupJob, error) {
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.State.Terminal() {
		return nil, fmt.Errorf("job: cannot record encryption of terminal job %s (%s): %w", jobID, j.State, domain.ErrConflict)
	}
	if encrypted && keyRef == "" {
		return nil, fmt.Errorf("job: key reference is required when encryption is enabled: %w", domain.ErrValidation)
	}
	j.Encrypted = encrypted
	j.KeyRef = keyRef
	j.UpdatedAt = s.clock.Now()
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// RecordError attaches structured error details to a job without changing
// its state; terminal failure itself is a transition (to failed) so the
// state machine stays the single source of truth.
func (s *Service) RecordError(ctx context.Context, jobID, code, message string) (*domain.BackupJob, error) {
	if code == "" && message == "" {
		return nil, fmt.Errorf("job: error code or message is required: %w", domain.ErrValidation)
	}
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	j.ErrorCode = code
	j.ErrorMessage = message
	j.UpdatedAt = s.clock.Now()
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// Retry creates a clean new attempt for a failed job, linked through RetryOf.
// The failed job is left untouched (its snapshot refs, errors and timestamps
// stay exactly as recorded), so a retry can never fabricate or rewrite the
// failed attempt's outcome — only a fresh execution reaching verifying →
// succeeded produces a successful snapshot.
func (s *Service) Retry(ctx context.Context, jobID, createdBy string) (*domain.BackupJob, error) {
	old, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if old.State != domain.JobFailed {
		return nil, fmt.Errorf("job: retry requires failed state, job %s is %s: %w", jobID, old.State, domain.ErrConflict)
	}
	if err := s.checkEnqueueLimits(ctx, old.AgentID); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	attempt := old.Attempt + 1
	if attempt < 1 {
		attempt = 1
	}
	j := &domain.BackupJob{
		ID:            s.idgen(),
		AgentID:       old.AgentID,
		PolicyID:      old.PolicyID,
		PolicyVersion: old.PolicyVersion,
		ScheduleID:    old.ScheduleID,
		Trigger:       domain.JobTriggerRetry,
		State:         domain.JobQueued,
		Encrypted:     old.Encrypted,
		KeyRef:        old.KeyRef,
		StorageID:     old.StorageID,
		RetryOf:       old.ID,
		Attempt:       attempt,
		CreatedBy:     createdBy,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobs.CreateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// Cancel cancels an active job (queued, assigned, running, uploading, or
// verifying, per the domain transition table). Terminal jobs are rejected.
// The agent holding the job is notified best-effort through CancelNotifier;
// notification failure never fails the cancellation.
func (s *Service) Cancel(ctx context.Context, jobID string) (*domain.BackupJob, error) {
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	switch j.State {
	case domain.JobQueued, domain.JobAssigned, domain.JobRunning, domain.JobUploading, domain.JobVerifying:
	default:
		return nil, fmt.Errorf("job: cancel requires an active job, job %s is %s: %w", jobID, j.State, domain.ErrConflict)
	}
	if err := s.applyTransition(j, domain.JobCancelled); err != nil {
		return nil, err
	}
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	// Best-effort: the persisted cancelled state is authoritative; the
	// agent revalidates on its next PollJobs/ReportResult.
	_ = s.cancels.NotifyCancel(ctx, j.AgentID, j.ID)
	return j, nil
}

// ---- Reads ----

// Get returns one job by id.
func (s *Service) Get(ctx context.Context, jobID string) (*domain.BackupJob, error) {
	return s.jobs.GetJob(ctx, jobID)
}

// List returns a page of jobs filtered by agent, policy, state and creation
// date (all bounds optional).
func (s *Service) List(ctx context.Context, opts domain.ListOptions, f Filter) (domain.ListResult[domain.BackupJob], error) {
	res, err := s.jobs.ListJobs(ctx, opts, f.JobFilter)
	if err != nil {
		return domain.ListResult[domain.BackupJob]{}, err
	}
	if f.Since == nil && f.Until == nil {
		return res, nil
	}
	kept := res.Items[:0]
	for _, j := range res.Items {
		if f.Since != nil && j.CreatedAt.Before(*f.Since) {
			continue
		}
		if f.Until != nil && !j.CreatedAt.Before(*f.Until) {
			continue
		}
		kept = append(kept, j)
	}
	res.Items = kept
	return res, nil
}

// RetryHistory returns the job plus its retry ancestors, oldest first, by
// following RetryOf links. The walk is cycle-safe and depth-bounded.
func (s *Service) RetryHistory(ctx context.Context, jobID string) ([]*domain.BackupJob, error) {
	const maxDepth = 100
	var chain []*domain.BackupJob
	seen := map[string]bool{}
	for id := jobID; id != "" && len(chain) < maxDepth; {
		if seen[id] {
			break
		}
		seen[id] = true
		j, err := s.jobs.GetJob(ctx, id)
		if err != nil {
			return nil, err
		}
		chain = append(chain, j)
		id = j.RetryOf
	}
	for i, k := 0, len(chain)-1; i < k; i, k = i+1, k-1 {
		chain[i], chain[k] = chain[k], chain[i]
	}
	return chain, nil
}

// CountActive reports how many non-terminal jobs an agent currently holds;
// an empty agentID counts globally. It is the enforcement hook behind the
// concurrency limits (and is usable by the scheduler track for overlap
// guards).
//
// INTEGRATION NOTE (repo adapter): replace the paged scan with a COUNT
// query when job volume grows; the semantics stay identical.
func (s *Service) CountActive(ctx context.Context, agentID string) (int, error) {
	return s.countActive(ctx, agentID)
}

func (s *Service) countActive(ctx context.Context, agentID string) (int, error) {
	const pageSize = 500
	filter := domain.JobFilter{AgentID: agentID}
	n := 0
	for offset := 0; ; offset += pageSize {
		res, err := s.jobs.ListJobs(ctx, domain.ListOptions{Limit: pageSize, Offset: offset}, filter)
		if err != nil {
			return 0, err
		}
		for _, j := range res.Items {
			if !j.State.Terminal() {
				n++
			}
		}
		if len(res.Items) < pageSize {
			return n, nil
		}
	}
}

// ---- Agent-facing queue ----

// PollJobs assigns due queued jobs to a healthy agent, oldest first, and
// returns the newly assigned jobs.
//
// Contract for the agents track:
//   - Unknown or disabled agents get an empty (non-nil) list and no error;
//     agent authentication/authorization happens in the agents track before
//     calling here.
//   - Jobs under missing or disabled policies are skipped, never assigned.
//   - When the agent is already at its per-agent concurrency limit, the
//     result is empty.
//   - At most the configured batch size is assigned per call.
//   - Every returned job was atomically (read-modify-write) moved from
//     queued to assigned and persisted; a concurrent claim of the same job
//     is detected by re-reading before each assignment.
func (s *Service) PollJobs(ctx context.Context, agentID string, capabilities []string) ([]*domain.BackupJob, error) {
	out := []*domain.BackupJob{}
	agent, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		// Unknown agent: nothing due. Callers needing to distinguish
		// auth failures handle them before reaching this service.
		if isNotFound(err) {
			return out, nil
		}
		return nil, err
	}
	if agent.Disabled {
		return out, nil
	}
	if s.limits.MaxPerAgent > 0 {
		n, err := s.countActive(ctx, agentID)
		if err != nil {
			return nil, err
		}
		if n >= s.limits.MaxPerAgent {
			return out, nil
		}
	}

	const listCap = 100
	res, err := s.jobs.ListJobs(ctx,
		domain.ListOptions{Limit: listCap},
		domain.JobFilter{AgentID: agentID, State: domain.JobQueued})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(res.Items, func(i, k int) bool {
		if res.Items[i].CreatedAt.Equal(res.Items[k].CreatedAt) {
			return res.Items[i].ID < res.Items[k].ID
		}
		return res.Items[i].CreatedAt.Before(res.Items[k].CreatedAt)
	})

	for i := range res.Items {
		if len(out) >= s.pollBatchSize {
			break
		}
		candidate := res.Items[i]
		ok, err := s.pollEligible(ctx, &candidate, capabilities)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		// Re-read: a concurrent poll may have claimed the job first.
		fresh, err := s.jobs.GetJob(ctx, candidate.ID)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		if fresh.State != domain.JobQueued {
			continue
		}
		if err := s.applyTransition(fresh, domain.JobAssigned); err != nil {
			continue
		}
		if err := s.jobs.UpdateJob(ctx, fresh); err != nil {
			return nil, err
		}
		out = append(out, fresh)
	}
	return out, nil
}

// pollEligible reports whether a queued job may be handed to an agent
// advertising the given capabilities.
func (s *Service) pollEligible(ctx context.Context, j *domain.BackupJob, capabilities []string) (bool, error) {
	policy, err := s.policies.GetPolicy(ctx, j.PolicyID)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if !policy.Enabled {
		return false, nil
	}
	version, err := s.policies.GetVersion(ctx, j.PolicyID, j.PolicyVersion)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, need := range requiredCapabilities(version.Targets) {
		if !hasCapability(capabilities, need) {
			return false, nil
		}
	}
	return true, nil
}

// requiredCapabilities maps pinned policy targets onto agent capability
// names (see domain.Agent.Capabilities). Only targets needing genuinely
// optional agent features gate assignment; core targets (filesystem,
// database dumps, app stacks) are always eligible so capability
// advertisement gaps can never stall mainstream backups.
//
// INTEGRATION NOTE (agents track): advertise "docker" when Docker
// integration is enabled and "mssql" when SQL Server tooling is present.
// An agent reporting no capabilities at all is treated as "unknown" and
// receives every job — always advertise to get precise matching.
func requiredCapabilities(targets []domain.BackupPolicyTarget) []string {
	var out []string
	seen := map[string]bool{}
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, t := range targets {
		switch t.Type {
		case domain.TargetDockerConfig, domain.TargetDockerVolume:
			add("docker")
		case domain.TargetMSSQL:
			add("mssql")
		}
	}
	return out
}

func hasCapability(capabilities []string, need string) bool {
	if len(capabilities) == 0 {
		return true // unadvertised: do not filter
	}
	for _, c := range capabilities {
		if c == need {
			return true
		}
	}
	return false
}

// ReportResult validates an agent's execution report and applies it.
//
// Contract for the agents track:
//   - Unknown job ids are rejected with an ErrNotFound-wrapping error.
//   - Reports for a job owned by another agent are rejected with an
//     ErrForbidden-wrapping error.
//   - Reports against terminal jobs, and state values that are not a legal
//     transition from the current state, are rejected with an
//     ErrConflict-wrapping error; nothing is persisted.
//   - A zero outcome State applies a progress/bytes/encryption/snapshot/
//     error update without transitioning.
//   - outcome.SnapshotID only registers artifact ids; success still
//     requires the verifying → succeeded transition after integrity
//     verification, so agents can never fabricate a successful snapshot.
func (s *Service) ReportResult(ctx context.Context, agentID, jobID string, outcome Outcome) (*domain.BackupJob, error) {
	j, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.AgentID != agentID {
		return nil, fmt.Errorf("job: job %s belongs to agent %s: %w", jobID, j.AgentID, domain.ErrForbidden)
	}
	if j.State.Terminal() {
		return nil, fmt.Errorf("job: cannot report on terminal job %s (%s): %w", jobID, j.State, domain.ErrConflict)
	}
	if err := checkProgress(outcome.Progress); err != nil {
		return nil, err
	}
	if outcome.Encrypted != nil && *outcome.Encrypted && outcome.KeyRef == "" && j.KeyRef == "" {
		return nil, fmt.Errorf("job: key reference is required when encryption is enabled: %w", domain.ErrValidation)
	}
	if outcome.State != "" {
		if err := s.applyTransition(j, outcome.State); err != nil {
			return nil, err
		}
	}
	if outcome.Progress != nil {
		j.Progress = outcome.Progress
	}
	if outcome.BytesRead != nil {
		j.BytesRead = outcome.BytesRead
	}
	if outcome.BytesCompressed != nil {
		j.BytesCompressed = outcome.BytesCompressed
	}
	if outcome.BytesUploaded != nil {
		j.BytesUploaded = outcome.BytesUploaded
	}
	if outcome.Encrypted != nil {
		j.Encrypted = *outcome.Encrypted
		if outcome.KeyRef != "" {
			j.KeyRef = outcome.KeyRef
		} else if !*outcome.Encrypted {
			j.KeyRef = ""
		}
	}
	if outcome.SnapshotID != "" {
		j.SnapshotID = outcome.SnapshotID
	}
	if outcome.ErrorCode != "" {
		j.ErrorCode = outcome.ErrorCode
	}
	if outcome.ErrorMessage != "" {
		j.ErrorMessage = outcome.ErrorMessage
	}
	j.UpdatedAt = s.clock.Now()
	if err := s.jobs.UpdateJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound)
}
