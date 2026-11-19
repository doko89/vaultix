package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// ResolvedTarget is one executable backup target pinned to an immutable
// policy version, with its enforced method and recorded caveats.
type ResolvedTarget struct {
	// Target is the frozen policy-version target (or a stack member expanded
	// from one; see StackMember below).
	Target domain.BackupPolicyTarget
	// EffectiveMethod is the enforced backup procedure after defaults and
	// per-type consistency rules are applied.
	EffectiveMethod domain.BackupMethod
	// StackMember is set when the target was expanded from an app_stack
	// policy target: it carries the member's restore order and requirement.
	StackMember *domain.ApplicationStackResource
	// Caveats records consistency limitations the operator must see
	// (live-file behavior, symlink handling, volume scope, ...).
	Caveats []string
}

// ResolveTargets loads the pinned policy version and resolves every target
// into an executable form. Jobs always execute against the frozen version,
// never the live policy (PRD §7.4: edits must not rewrite history).
//
// App-stack targets (selector = stack ID) expand into one ResolvedTarget per
// stack member so each member backs up with its own type-appropriate method.
func (s *Service) ResolveTargets(ctx context.Context, policyID string, version int) ([]ResolvedTarget, error) {
	if strings.TrimSpace(policyID) == "" {
		return nil, fmt.Errorf("%w: policy_id is required", domain.ErrValidation)
	}
	if version <= 0 {
		return nil, fmt.Errorf("%w: pinned policy version must be positive", domain.ErrValidation)
	}
	pv, err := s.policies.GetVersion(ctx, policyID, version)
	if err != nil {
		return nil, err
	}
	if len(pv.Targets) == 0 {
		return nil, fmt.Errorf("%w: policy version %s@%d has no targets", domain.ErrValidation, policyID, version)
	}
	var out []ResolvedTarget
	for _, t := range pv.Targets {
		if t.Type == domain.TargetAppStack {
			members, err := s.expandStack(ctx, t)
			if err != nil {
				return nil, err
			}
			out = append(out, members...)
			continue
		}
		rt, err := ResolveTarget(t)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, nil
}

// expandStack resolves an app_stack policy target into per-member targets.
// The selector must be the stack ID.
func (s *Service) expandStack(ctx context.Context, t domain.BackupPolicyTarget) ([]ResolvedTarget, error) {
	stackID := strings.TrimSpace(t.Selector)
	if stackID == "" {
		return nil, fmt.Errorf("%w: app_stack target requires a stack ID selector", domain.ErrValidation)
	}
	members, err := s.stacks.ListStackResources(ctx, stackID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("%w: stack %q has no members", domain.ErrValidation, stackID)
	}
	out := make([]ResolvedTarget, 0, len(members))
	for _, m := range members {
		mt := domain.BackupPolicyTarget{
			ID:            t.ID + ":" + m.Selector,
			PolicyID:      t.PolicyID,
			Type:          m.Type,
			Method:        t.Method, // explicit member override when set; else type default
			Selector:      m.Selector,
			Options:       t.Options,
			CredentialRef: t.CredentialRef,
		}
		// A database member needs its own credential ref; the stack-level
		// target may carry it when all members share one profile.
		rt, err := ResolveTarget(mt)
		if err != nil {
			return nil, fmt.Errorf("stack %q member %q: %w", stackID, m.Selector, err)
		}
		member := m
		rt.StackMember = &member
		rt.Caveats = append(rt.Caveats,
			"stack member: application-consistent recovery is claimed only when every required member backup verifies")
		out = append(out, rt)
	}
	return out, nil
}

// ResolveTarget enforces per-target consistency rules and records caveats.
// It is pure (no I/O) so agents and planners can share the same rules.
//
//   - Database targets accept database-aware methods ONLY. Copying a live
//     database data directory as ordinary files is rejected (PRD §7.7):
//     postgres/mysql require logical_dump, mssql requires native_full,
//     redis requires rdb_snapshot.
//   - Filesystem targets record live-file caveats: files mutating during
//     backup have best-effort consistency, and symlink handling is the
//     configured mode (default "preserve").
func ResolveTarget(t domain.BackupPolicyTarget) (ResolvedTarget, error) {
	if err := t.Validate(); err != nil {
		return ResolvedTarget{}, err
	}
	method, err := effectiveMethod(t)
	if err != nil {
		return ResolvedTarget{}, err
	}
	rt := ResolvedTarget{Target: t, EffectiveMethod: method}
	switch t.Type {
	case domain.TargetHostFilesystem:
		mode := "preserve"
		if v, ok := t.Options["symlink"]; ok && strings.TrimSpace(v) != "" {
			mode = strings.TrimSpace(v)
		}
		rt.Caveats = append(rt.Caveats,
			"live files may change during backup; consistency for mutating files is best-effort",
			"symbolic link handling: "+mode,
		)
		if len(t.Exclude) > 0 {
			rt.Caveats = append(rt.Caveats,
				fmt.Sprintf("%d exclude pattern(s) apply; excluded data is not recoverable", len(t.Exclude)))
		}
	case domain.TargetDockerConfig:
		rt.Caveats = append(rt.Caveats,
			"container image blobs are not exported; image reference and digest are recorded")
	case domain.TargetDockerVolume:
		rt.Caveats = append(rt.Caveats,
			"only mounted volume/bind-mount data is captured; a running container's writable layer is not a substitute for persistent data",
			"stop or quiesce writers for a consistent volume capture")
	case domain.TargetPostgres, domain.TargetMySQL:
		rt.Caveats = append(rt.Caveats,
			"logical dump is consistent as of dump start; transactions committed after that point are not captured")
	case domain.TargetMSSQL:
		rt.Caveats = append(rt.Caveats,
			"native full backup; differential/log backups apply only when the recovery model permits them")
	case domain.TargetRedis:
		rt.Caveats = append(rt.Caveats,
			"RDB snapshot reflects the last persistence point; AOF tail written after the snapshot is not captured unless AOF is included")
	}
	return rt, nil
}

// effectiveMethod enforces database-aware methods and fills the default.
// Unset methods default to the type-appropriate procedure (logical dumps for
// postgres/mysql, native full for mssql, RDB snapshots for redis); an
// explicitly wrong method for a database target — notably a plain file copy
// of a live data directory — is rejected.
func effectiveMethod(t domain.BackupPolicyTarget) (domain.BackupMethod, error) {
	if t.Method == "" {
		return defaultMethod(t.Type), nil
	}
	switch t.Type {
	case domain.TargetPostgres, domain.TargetMySQL:
		if t.Method != domain.BackupMethodLogicalDump {
			return "", fmt.Errorf("%w: %s target %q requires database-aware method %q (got %q); copying a live data directory as files is not acceptable",
				domain.ErrValidation, t.Type, t.Selector, domain.BackupMethodLogicalDump, t.Method)
		}
	case domain.TargetMSSQL:
		if t.Method != domain.BackupMethodNativeFull {
			return "", fmt.Errorf("%w: mssql target %q requires database-aware method %q (got %q)",
				domain.ErrValidation, t.Selector, domain.BackupMethodNativeFull, t.Method)
		}
	case domain.TargetRedis:
		if t.Method != domain.BackupMethodRDPSnapshot {
			return "", fmt.Errorf("%w: redis target %q requires method %q (got %q)",
				domain.ErrValidation, t.Selector, domain.BackupMethodRDPSnapshot, t.Method)
		}
	}
	return t.Method, nil
}

// defaultMethod returns the type-appropriate backup procedure.
func defaultMethod(t domain.TargetType) domain.BackupMethod {
	switch t {
	case domain.TargetPostgres, domain.TargetMySQL:
		return domain.BackupMethodLogicalDump
	case domain.TargetMSSQL:
		return domain.BackupMethodNativeFull
	case domain.TargetRedis:
		return domain.BackupMethodRDPSnapshot
	default:
		return domain.BackupMethodFull
	}
}
