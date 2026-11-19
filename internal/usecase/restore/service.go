package restore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Default TTLs for plans and confirmation tokens.
const (
	DefaultPlanTTL  = 30 * time.Minute
	DefaultTokenTTL = 15 * time.Minute
)

// Service plans, confirms, and executes restores.
type Service struct {
	snapshots domain.SnapshotRepository
	plans     PlanStore
	tokens    TokenStore
	reporter  JobReporter
	clock     domain.Clock
	newID     func() string
	newToken  func() string
	planTTL   time.Duration
	tokenTTL  time.Duration
}

// Deps wires a Service. Plans/Tokens default to in-process memory stores,
// Clock to the system clock, and TTLs to their defaults.
type Deps struct {
	Snapshots domain.SnapshotRepository
	Plans     PlanStore
	Tokens    TokenStore
	Reporter  JobReporter

	Clock    domain.Clock
	IDGen    func() string
	TokenGen func() string
	PlanTTL  time.Duration
	TokenTTL time.Duration
}

// New builds a Service, rejecting nil required dependencies.
func New(d Deps) (*Service, error) {
	if d.Snapshots == nil || d.Reporter == nil {
		return nil, fmt.Errorf("%w: restore service requires snapshots and a job reporter", domain.ErrValidation)
	}
	plans := d.Plans
	if plans == nil {
		plans = NewMemoryPlanStore()
	}
	tokens := d.Tokens
	if tokens == nil {
		tokens = NewMemoryTokenStore()
	}
	clock := d.Clock
	if clock == nil {
		clock = domain.SystemClock{}
	}
	newID := d.IDGen
	if newID == nil {
		newID = randomID
	}
	newToken := d.TokenGen
	if newToken == nil {
		newToken = randomToken
	}
	planTTL := d.PlanTTL
	if planTTL <= 0 {
		planTTL = DefaultPlanTTL
	}
	tokenTTL := d.TokenTTL
	if tokenTTL <= 0 {
		tokenTTL = DefaultTokenTTL
	}
	return &Service{
		snapshots: d.Snapshots, plans: plans, tokens: tokens,
		reporter: d.Reporter, clock: clock,
		newID: newID, newToken: newToken,
		planTTL: planTTL, tokenTTL: tokenTTL,
	}, nil
}

// Plan validates a restore request and records the resulting plan.
// Only verified snapshots are restorable: anything else is refused.
func (s *Service) Plan(ctx context.Context, req PlanRequest) (*Plan, error) {
	if strings.TrimSpace(req.RequestedBy) == "" {
		return nil, fmt.Errorf("%w: requested_by is required", domain.ErrValidation)
	}
	ids := req.SnapshotIDs
	if req.Kind != KindStack {
		if strings.TrimSpace(req.SnapshotID) == "" {
			return nil, fmt.Errorf("%w: snapshot_id is required", domain.ErrValidation)
		}
		ids = []string{req.SnapshotID}
	} else if len(ids) == 0 {
		return nil, fmt.Errorf("%w: stack restore needs at least one snapshot_id", domain.ErrValidation)
	}
	snaps := make([]*domain.Snapshot, 0, len(ids))
	for _, id := range ids {
		snap, err := s.snapshots.GetSnapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		if !snap.IsRestorable() {
			return nil, fmt.Errorf("%w: snapshot %q has status %q", ErrNotRestorable, snap.ID, snap.Status)
		}
		snaps = append(snaps, snap)
	}
	now := s.clock.Now()
	plan, err := buildPlan(now.Unix(), now.Add(s.planTTL).Unix(), s.newID, req, snaps)
	if err != nil {
		return nil, err
	}
	if err := s.plans.SavePlan(ctx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// Confirmation is the single-use credential that authorizes one execution.
type Confirmation struct {
	Token     string
	PlanID    string
	ExpiresAt time.Time
}

// Confirm binds a plan to a single-use confirmation token for an explicit
// authorization. The actor is recorded on the token for the audit trail.
func (s *Service) Confirm(ctx context.Context, planID, actor string) (*Confirmation, error) {
	if strings.TrimSpace(planID) == "" {
		return nil, fmt.Errorf("%w: plan_id is required", domain.ErrValidation)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: confirming actor is required", domain.ErrValidation)
	}
	plan, err := s.plans.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if now.Unix() >= plan.ExpiresAt {
		return nil, ErrPlanExpired
	}
	tok := &ConfirmationToken{
		Token:     s.newToken(),
		PlanID:    plan.ID,
		IssuedTo:  strings.TrimSpace(actor),
		IssuedAt:  now,
		ExpiresAt: now.Add(s.tokenTTL),
	}
	if err := s.tokens.SaveToken(ctx, tok); err != nil {
		return nil, err
	}
	return &Confirmation{Token: tok.Token, PlanID: plan.ID, ExpiresAt: tok.ExpiresAt}, nil
}

// Execute consumes a confirmation token and starts the restore job. Tokens
// are single-use: reuse, expiry, or unknown values are refused and no job
// is created.
func (s *Service) Execute(ctx context.Context, token, actor string) (*domain.RestoreJob, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrTokenInvalid
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: executing actor is required", domain.ErrValidation)
	}
	tok, err := s.tokens.GetToken(ctx, token)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if tok.Used() {
		return nil, ErrTokenReused
	}
	if tok.Expired(now) {
		return nil, ErrTokenExpired
	}
	plan, err := s.plans.GetPlan(ctx, tok.PlanID)
	if err != nil {
		return nil, err
	}
	job := &domain.RestoreJob{
		ID:         s.newID(),
		AgentID:    plan.AgentID,
		SnapshotID: plan.SnapshotIDs[0],
		State:      domain.JobQueued,
		ApprovedBy: strings.TrimSpace(actor),
		CreatedBy:  tok.IssuedTo,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	applyPlanToJob(plan, job)
	if err := s.reporter.CreateRestoreJob(ctx, job); err != nil {
		return nil, err
	}
	if err := s.tokens.MarkTokenUsed(ctx, token, now); err != nil {
		return nil, err
	}
	return job, nil
}

// applyPlanToJob carries plan destinations and subsets onto the job record.
func applyPlanToJob(plan *Plan, job *domain.RestoreJob) {
	if len(plan.Steps) == 0 {
		return
	}
	first := plan.Steps[0]
	if plan.AlternateDestination {
		job.TargetPath = first.Destination
	}
	switch plan.Kind {
	case KindFile:
		job.Paths = append([]string(nil), plan.Paths...)
	case KindStack:
		// Stack execution order is the plan step sequence; the agent fans
		// out per member. Record the full ordered member list.
		for _, st := range plan.Steps {
			job.Paths = append(job.Paths, st.SnapshotID)
		}
	}
}

// Status returns restore-job tracking state via the job reporter.
func (s *Service) Status(ctx context.Context, jobID string) (*domain.RestoreJob, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("%w: job_id is required", domain.ErrValidation)
	}
	return s.reporter.GetRestoreJob(ctx, jobID)
}

// randomID returns a 128-bit hex identifier.
func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
