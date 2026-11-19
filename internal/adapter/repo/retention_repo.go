package repo

import (
	"context"
	"time"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// RetentionStore persists per-policy retention rules on retention_rules.
// There is no domain port for retention (the rule is a value object owned
// by policies); this concrete store serves the retention worker until a
// later domain change promotes it.
//
// Mapping notes: KeepLastN/KeepDays of 0 mean NULL (no bound); at least one
// bound is required by both domain validation and the table check.
// KeepMinimum collapses to protect_last_valid (any positive minimum reads
// back as 1).
type RetentionStore struct {
	baseRepo
}

// RetentionRecord pairs a rule with its last-applied timestamp.
type RetentionRecord struct {
	Rule          domain.RetentionRule
	LastAppliedAt *time.Time
}

// GetByPolicy returns the retention rule for a policy.
func (s *RetentionStore) GetByPolicy(ctx context.Context, policyID string) (RetentionRecord, error) {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return RetentionRecord{}, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := s.q.GetRetentionRuleByPolicy(ctx, gen.GetRetentionRuleByPolicyParams{PolicyID: pid})
	if err != nil {
		return RetentionRecord{}, mapErr(err)
	}
	return mapRetention(row), nil
}

// UpsertRule creates or replaces the retention rule for a policy.
func (s *RetentionStore) UpsertRule(ctx context.Context, policyID string, rule domain.RetentionRule) (RetentionRecord, error) {
	if err := rule.Validate(); err != nil {
		return RetentionRecord{}, err
	}
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return RetentionRecord{}, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := s.q.UpsertRetentionRule(ctx, gen.UpsertRetentionRuleParams{
		PolicyID:         pid,
		KeepLastN:        int4OrNull(rule.KeepLastN),
		KeepDays:         int4OrNull(rule.KeepDays),
		ProtectLastValid: rule.KeepMinimum > 0,
	})
	if err != nil {
		return RetentionRecord{}, mapErr(err)
	}
	return mapRetention(row), nil
}

// DeleteRule removes the retention rule for a policy.
func (s *RetentionStore) DeleteRule(ctx context.Context, policyID string) error {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := s.q.GetRetentionRuleByPolicy(ctx, gen.GetRetentionRuleByPolicyParams{PolicyID: pid})
	if err != nil {
		return mapErr(err)
	}
	if err := s.q.DeleteRetentionRule(ctx, gen.DeleteRetentionRuleParams{ID: row.ID}); err != nil {
		return mapErr(err)
	}
	return nil
}

// RecordRun stamps last_applied_at after a retention pass.
func (s *RetentionStore) RecordRun(ctx context.Context, policyID string) error {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := s.q.GetRetentionRuleByPolicy(ctx, gen.GetRetentionRuleByPolicyParams{PolicyID: pid})
	if err != nil {
		return mapErr(err)
	}
	if err := s.q.RecordRetentionRun(ctx, gen.RecordRetentionRunParams{ID: row.ID}); err != nil {
		return mapErr(err)
	}
	return nil
}

func mapRetention(row gen.RetentionRule) RetentionRecord {
	keepMinimum := 0
	if row.ProtectLastValid {
		keepMinimum = 1
	}
	return RetentionRecord{
		Rule: domain.RetentionRule{
			KeepLastN:   intOrZero(row.KeepLastN),
			KeepDays:    intOrZero(row.KeepDays),
			KeepMinimum: keepMinimum,
		},
		LastAppliedAt: timePtr(row.LastAppliedAt),
	}
}
