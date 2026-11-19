package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Service owns backup-policy rules. It programs against domain ports only.
//
// Reference-existence checks (destinations, credential/encryption profiles,
// stacks) run when the corresponding repository is provided via options;
// a nil repository skips that check. Production wiring SHOULD provide all of
// them; tests may omit them for pure-validation coverage.
type Service struct {
	policies domain.PolicyRepository
	storage  domain.StorageRepository
	stacks   domain.StackRepository
	clock    domain.Clock
}

// Option customizes a Service.
type Option func(*Service)

// WithStorage enables destination + secret-profile existence checks.
func WithStorage(r domain.StorageRepository) Option {
	return func(s *Service) { s.storage = r }
}

// WithStacks enables application-stack membership checks.
func WithStacks(r domain.StackRepository) Option {
	return func(s *Service) { s.stacks = r }
}

// WithClock overrides the clock (tests).
func WithClock(c domain.Clock) Option {
	return func(s *Service) { s.clock = c }
}

// NewService builds a policy Service. policies is required.
func NewService(policies domain.PolicyRepository, opts ...Option) *Service {
	s := &Service{policies: policies, clock: domain.SystemClock{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateInput carries everything needed to create a policy plus its v1.
type CreateInput struct {
	Name        string
	Description string
	AgentIDs    []string
	StorageID   string // "" = default destination
	Compression domain.Compression
	Encryption  domain.Encryption
	Retention   domain.RetentionRule
	Schedule    *domain.Schedule // optional frozen schedule snapshot
	Targets     []TargetInput
	CreatedBy   string
	Note        string
}

// UpdateInput replaces metadata and publishes a new version. Every edit
// creates a version; only SetEnabled mutates without versioning.
type UpdateInput struct {
	Name        string
	Description string
	AgentIDs    []string
	StorageID   string
	Compression domain.Compression
	Encryption  domain.Encryption
	Retention   domain.RetentionRule
	Schedule    *domain.Schedule
	Targets     []TargetInput
	UpdatedBy   string
	Note        string
}

// PublishInput replaces the frozen config and publishes version+1.
type PublishInput struct {
	Targets     []TargetInput
	Compression domain.Compression
	Encryption  domain.Encryption
	Retention   domain.RetentionRule
	StorageID   string
	Schedule    *domain.Schedule
	CreatedBy   string
	Note        string
}

// ChangeMeta attributes target-level edits.
type ChangeMeta struct {
	Actor string
	Note  string
}

// Create validates the input, persists the policy, and publishes v1.
func (s *Service) Create(ctx context.Context, in CreateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
	p := &domain.BackupPolicy{
		ID:          newID("pol"),
		Name:        strings.TrimSpace(in.Name),
		Description: in.Description,
		AgentIDs:    append([]string{}, in.AgentIDs...),
		StorageID:   strings.TrimSpace(in.StorageID),
		Compression: in.Compression,
		Encryption:  in.Encryption,
		Enabled:     true,
		CreatedBy:   in.CreatedBy,
		CreatedAt:   s.clock.Now(),
		UpdatedAt:   s.clock.Now(),
	}
	if err := p.Validate(); err != nil {
		return nil, nil, err
	}
	if err := s.policies.CreatePolicy(ctx, p); err != nil {
		return nil, nil, err
	}
	candidate := make([]domain.BackupPolicyTarget, len(in.Targets))
	for i, t := range in.Targets {
		candidate[i] = toDomainTarget(p.ID, t)
	}
	v, err := s.publishVersioned(ctx, p, candidate, in.Compression, in.Encryption, in.Retention, p.StorageID, in.Schedule, in.CreatedBy, firstNonEmpty(in.Note, "initial version"))
	if err != nil {
		return nil, nil, err
	}
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, nil, err
	}
	return p, v, nil
}

// Get returns one policy by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.BackupPolicy, error) {
	return s.policies.GetPolicy(ctx, id)
}

// List returns a page of policies.
func (s *Service) List(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	return s.policies.ListPolicies(ctx, opts)
}

// Update replaces metadata and publishes a new version with the new config.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
	p, err := s.policies.GetPolicy(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	p.Name = strings.TrimSpace(in.Name)
	p.Description = in.Description
	p.AgentIDs = append([]string{}, in.AgentIDs...)
	if err := p.Validate(); err != nil {
		return nil, nil, fmt.Errorf("policy metadata: %w", err)
	}
	v, err := s.publishNext(ctx, p, PublishInput{
		Targets:     in.Targets,
		Compression: in.Compression,
		Encryption:  in.Encryption,
		Retention:   in.Retention,
		StorageID:   strings.TrimSpace(in.StorageID),
		Schedule:    in.Schedule,
		CreatedBy:   in.UpdatedBy,
		Note:        in.Note,
	})
	if err != nil {
		return nil, nil, err
	}
	p.UpdatedAt = s.clock.Now()
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, nil, err
	}
	return p, v, nil
}

// Delete removes the policy row. Published versions are retained (the port
// has no version delete) so completed-job configs stay intact.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.policies.DeletePolicy(ctx, id)
}

// SetEnabled flips the operational state without publishing a version:
// enabled/disabled is runtime state, not frozen config.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, actor string) (*domain.BackupPolicy, error) {
	p, err := s.policies.GetPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = actor
	p.Enabled = enabled
	p.UpdatedAt = s.clock.Now()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// PublishVersion validates a new frozen config and publishes version+1.
// The policy row mirrors the current config (storage, compression,
// encryption) and its CurrentVersion pointer advances.
//
// NOTE: pointer bump + version insert are two port calls; repository
// adapters SHOULD apply them atomically where the store allows it.
func (s *Service) PublishVersion(ctx context.Context, policyID string, in PublishInput) (*domain.BackupPolicyVersion, error) {
	p, err := s.policies.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	v, err := s.publishNext(ctx, p, in)
	if err != nil {
		return nil, err
	}
	p.UpdatedAt = s.clock.Now()
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, err
	}
	return v, nil
}

// GetVersion returns one immutable version.
func (s *Service) GetVersion(ctx context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	if version <= 0 {
		return nil, fmt.Errorf("%w: version must be positive, got %d", domain.ErrValidation, version)
	}
	return s.policies.GetVersion(ctx, policyID, version)
}

// ListVersions returns all versions of a policy, oldest first.
func (s *Service) ListVersions(ctx context.Context, policyID string) ([]domain.BackupPolicyVersion, error) {
	if _, err := s.policies.GetPolicy(ctx, policyID); err != nil {
		return nil, err
	}
	return s.policies.ListVersions(ctx, policyID)
}

// CurrentVersion returns the latest published version.
func (s *Service) CurrentVersion(ctx context.Context, policyID string) (*domain.BackupPolicyVersion, error) {
	p, err := s.policies.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	if p.CurrentVersion <= 0 {
		return nil, fmt.Errorf("%w: policy %q has no published version", domain.ErrNotFound, policyID)
	}
	return s.policies.GetVersion(ctx, policyID, p.CurrentVersion)
}

// AddTarget appends one target and publishes a new version.
func (s *Service) AddTarget(ctx context.Context, policyID string, in TargetInput, meta ChangeMeta) (*domain.BackupPolicyVersion, error) {
	p, err := s.policies.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	cur, err := s.currentVersionOf(ctx, p)
	if err != nil {
		return nil, err
	}
	effective := append(cloneTargets(cur.Targets), toDomainTarget(policyID, in))
	sched := cur.Schedule
	v, err := s.publishVersioned(ctx, p, effective, cur.Compression, cur.Encryption, cur.Retention, cur.StorageID, &sched, meta.Actor, firstNonEmpty(meta.Note, fmt.Sprintf("add %s target %q", in.Type, in.Selector)))
	if err != nil {
		return nil, err
	}
	p.UpdatedAt = s.clock.Now()
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, err
	}
	return v, nil
}

// UpdateTarget replaces one target by ID and publishes a new version.
func (s *Service) UpdateTarget(ctx context.Context, policyID, targetID string, in TargetInput, meta ChangeMeta) (*domain.BackupPolicyVersion, error) {
	p, err := s.policies.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	cur, err := s.currentVersionOf(ctx, p)
	if err != nil {
		return nil, err
	}
	found := false
	next := cloneTargets(cur.Targets)
	for i := range next {
		if next[i].ID == targetID {
			repl := toDomainTarget(policyID, in)
			repl.ID = targetID
			repl.PolicyID = policyID
			next[i] = repl
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: target %q not found in policy %q", domain.ErrNotFound, targetID, policyID)
	}
	sched := cur.Schedule
	v, err := s.publishVersioned(ctx, p, next, cur.Compression, cur.Encryption, cur.Retention, cur.StorageID, &sched, meta.Actor, firstNonEmpty(meta.Note, fmt.Sprintf("update target %q", targetID)))
	if err != nil {
		return nil, err
	}
	p.UpdatedAt = s.clock.Now()
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, err
	}
	return v, nil
}

// RemoveTarget drops one target by ID and publishes a new version. The last
// target cannot be removed; delete the policy instead.
func (s *Service) RemoveTarget(ctx context.Context, policyID, targetID string, meta ChangeMeta) (*domain.BackupPolicyVersion, error) {
	p, err := s.policies.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	cur, err := s.currentVersionOf(ctx, p)
	if err != nil {
		return nil, err
	}
	next := make([]domain.BackupPolicyTarget, 0, len(cur.Targets))
	for _, t := range cur.Targets {
		if t.ID != targetID {
			next = append(next, t)
		}
	}
	if len(next) == len(cur.Targets) {
		return nil, fmt.Errorf("%w: target %q not found in policy %q", domain.ErrNotFound, targetID, policyID)
	}
	if len(next) == 0 {
		return nil, fmt.Errorf("%w: policy must keep at least one target; delete the policy instead",
			domain.ErrValidation)
	}
	sched := cur.Schedule
	v, err := s.publishVersioned(ctx, p, next, cur.Compression, cur.Encryption, cur.Retention, cur.StorageID, &sched, meta.Actor, firstNonEmpty(meta.Note, fmt.Sprintf("remove target %q", targetID)))
	if err != nil {
		return nil, err
	}
	p.UpdatedAt = s.clock.Now()
	if err := s.policies.UpdatePolicy(ctx, p); err != nil {
		return nil, err
	}
	return v, nil
}

// currentVersionOf loads the policy's current version.
func (s *Service) currentVersionOf(ctx context.Context, p *domain.BackupPolicy) (*domain.BackupPolicyVersion, error) {
	if p.CurrentVersion <= 0 {
		return nil, fmt.Errorf("%w: policy %q has no published version", domain.ErrNotFound, p.ID)
	}
	return s.policies.GetVersion(ctx, p.ID, p.CurrentVersion)
}

// publishNext validates an input-based config and publishes version+1.
func (s *Service) publishNext(ctx context.Context, p *domain.BackupPolicy, in PublishInput) (*domain.BackupPolicyVersion, error) {
	candidate := make([]domain.BackupPolicyTarget, len(in.Targets))
	for i, t := range in.Targets {
		candidate[i] = toDomainTarget(p.ID, t)
	}
	return s.publishVersioned(ctx, p, candidate, in.Compression, in.Encryption, in.Retention, strings.TrimSpace(in.StorageID), in.Schedule, in.CreatedBy, in.Note)
}

// publishVersioned validates the candidate config, deep-copies it into
// version+1, persists it, and advances the policy pointer. Existing target
// IDs are preserved (only empty IDs are minted) so clients can track a
// target across versions. The policy row's mirrored config fields update
// too; the caller persists the row.
func (s *Service) publishVersioned(ctx context.Context, p *domain.BackupPolicy, candidate []domain.BackupPolicyTarget, comp domain.Compression, enc domain.Encryption, ret domain.RetentionRule, storageID string, sched *domain.Schedule, createdBy, note string) (*domain.BackupPolicyVersion, error) {
	if len(candidate) == 0 {
		return nil, fmt.Errorf("%w: at least one target is required", domain.ErrValidation)
	}
	inputs := fromDomainTargets(candidate)
	for i, t := range inputs {
		if err := validateTarget(t); err != nil {
			return nil, fmt.Errorf("target[%d]: %w", i, err)
		}
	}
	if err := validateCompression(comp); err != nil {
		return nil, err
	}
	if err := enc.Validate(); err != nil {
		return nil, err
	}
	if err := ret.Validate(); err != nil {
		return nil, fmt.Errorf("retention: %w", err)
	}
	var frozen *domain.Schedule
	if sched != nil && sched.Kind != "" {
		if err := sched.Validate(); err != nil {
			return nil, fmt.Errorf("schedule snapshot: %w", err)
		}
		cp := *sched
		frozen = &cp
	}
	if err := s.checkRefs(ctx, inputs, enc, storageID); err != nil {
		return nil, err
	}
	v := &domain.BackupPolicyVersion{
		Version:     p.CurrentVersion + 1,
		PolicyID:    p.ID,
		CreatedBy:   createdBy,
		CreatedAt:   s.clock.Now(),
		Targets:     cloneTargets(candidate),
		Retention:   ret,
		Compression: comp,
		Encryption:  enc,
		StorageID:   storageID,
		Note:        note,
	}
	if frozen != nil {
		v.Schedule = *frozen
	}
	for i := range v.Targets {
		v.Targets[i].PolicyID = p.ID
		if v.Targets[i].ID == "" {
			v.Targets[i].ID = newID("tgt")
		}
	}
	if err := s.policies.PublishVersion(ctx, v); err != nil {
		return nil, err
	}
	p.CurrentVersion = v.Version
	p.StorageID = storageID
	p.Compression = comp
	p.Encryption = enc
	return v, nil
}

// checkRefs verifies destination, credential-profile, encryption-key, and
// stack references exist. Each check is skipped when its repository is nil.
func (s *Service) checkRefs(ctx context.Context, tgts []TargetInput, enc domain.Encryption, storageID string) error {
	if s.storage != nil {
		if storageID != "" {
			if _, err := s.storage.GetDestination(ctx, storageID); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: unknown destination %q", domain.ErrValidation, storageID)
				}
				return err
			}
		}
		seen := map[string]bool{}
		refs := []string{}
		if enc.Enabled {
			refs = append(refs, enc.KeyRef)
		}
		for _, t := range tgts {
			if strings.TrimSpace(t.CredentialRef) != "" {
				refs = append(refs, t.CredentialRef)
			}
		}
		for _, ref := range refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if _, err := s.storage.GetSecret(ctx, ref); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: unknown credential/key profile %q", domain.ErrValidation, ref)
				}
				return err
			}
		}
	}
	if s.stacks != nil {
		for _, t := range tgts {
			if t.Type != domain.TargetAppStack {
				continue
			}
			if _, err := s.stacks.GetStack(ctx, t.Selector); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: unknown application stack %q", domain.ErrValidation, t.Selector)
				}
				return err
			}
			members, err := s.stacks.ListStackResources(ctx, t.Selector)
			if err != nil {
				return err
			}
			if len(members) == 0 {
				return fmt.Errorf("%w: application stack %q has no member resources",
					domain.ErrValidation, t.Selector)
			}
		}
	}
	return nil
}

// fromDomainTargets converts stored targets back to inputs for re-validation
// during target-level edits (IDs are preserved separately).
func fromDomainTargets(ts []domain.BackupPolicyTarget) []TargetInput {
	out := make([]TargetInput, len(ts))
	for i, t := range ts {
		out[i] = TargetInput{
			Type:          t.Type,
			Method:        t.Method,
			Selector:      t.Selector,
			Include:       append([]string{}, t.Include...),
			Exclude:       append([]string{}, t.Exclude...),
			Options:       cloneOptions(t.Options),
			CredentialRef: t.CredentialRef,
		}
	}
	return out
}

func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// newID mints a prefixed random ID (stdlib only; 128 bits of entropy).
func newID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
