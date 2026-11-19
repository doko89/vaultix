package retention

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// evalPageSize bounds one snapshot list page during evaluation.
const evalPageSize = 200

// Service evaluates retention rules and applies deletion plans.
type Service struct {
	snapshots domain.SnapshotRepository
	objects   ObjectAccess
	clock     domain.Clock
	newID     func() string
}

// Deps wires a Service. Clock and IDGen are optional test seams.
type Deps struct {
	Snapshots domain.SnapshotRepository
	Objects   ObjectAccess

	Clock domain.Clock
	IDGen func() string
}

// New builds a Service, rejecting nil required dependencies.
func New(d Deps) (*Service, error) {
	if d.Snapshots == nil || d.Objects == nil {
		return nil, fmt.Errorf("%w: retention service requires snapshots and object access", domain.ErrValidation)
	}
	clock := d.Clock
	if clock == nil {
		clock = domain.SystemClock{}
	}
	newID := d.IDGen
	if newID == nil {
		newID = randomID
	}
	return &Service{snapshots: d.Snapshots, objects: d.Objects, clock: clock, newID: newID}, nil
}

// Evaluate builds the deletion plan for a policy's snapshots without side
// effects, so callers can dry-run by evaluating and discarding the plan.
// Snapshots rank newest-first; a snapshot is condemned when the age or count
// bound applies. Already-expired snapshots are skipped (nothing to do).
//
// The valid-recovery-point floor always holds at least one verified
// snapshot (or KeepMinimum when higher): condemned recovery points beyond
// the floor move to Protected and set OverrideRequired, unless
// OverrideLastRecoveryPoint is set — in which case they stay condemned and
// the plan records OverrideGranted for the audit trail.
func (s *Service) Evaluate(ctx context.Context, policyID string, rule domain.RetentionRule, opts EvaluateOptions) (*DeletionPlan, error) {
	if strings.TrimSpace(policyID) == "" {
		return nil, fmt.Errorf("%w: policy_id is required", domain.ErrValidation)
	}
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	all, err := s.listPolicySnapshots(ctx, policyID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	// Newest first: rank 0 is the latest recovery point.
	slices.SortFunc(all, func(a, b domain.Snapshot) int {
		return snapshotTime(b).Compare(snapshotTime(a))
	})

	plan := &DeletionPlan{
		ID:        s.newID(),
		PolicyID:  policyID,
		Rule:      rule,
		CreatedAt: now,
		Actor:     strings.TrimSpace(opts.Actor),
		DryRun:    opts.DryRun,
	}
	var keptVerified int
	for i, snap := range all {
		if snap.Status == domain.SnapshotExpired {
			continue
		}
		age := now.Sub(snapshotTime(snap))
		if !rule.Applies(i, age) {
			plan.Kept++
			if snap.Status == domain.SnapshotVerified {
				keptVerified++
			}
			continue
		}
		entry, err := s.buildEntry(ctx, snap, i, age, rule)
		if err != nil {
			return nil, err
		}
		plan.Deletes = append(plan.Deletes, entry)
	}
	// Oldest-first execution order is the safer, more predictable sequence.
	slices.Reverse(plan.Deletes)

	s.applyRecoveryPointFloor(plan, keptVerified, opts.OverrideLastRecoveryPoint)
	return plan, nil
}

// buildEntry resolves one condemned snapshot to its full object key set.
func (s *Service) buildEntry(ctx context.Context, snap domain.Snapshot, rank int, age time.Duration, rule domain.RetentionRule) (DeleteEntry, error) {
	entry := DeleteEntry{
		SnapshotID:  snap.ID,
		StorageID:   snap.StorageID,
		ManifestID:  snap.ManifestID,
		Reason:      condemnReason(rank, age, rule),
		Verified:    snap.Status == domain.SnapshotVerified,
		CompletedAt: snap.CompletedAt,
	}
	manifest, err := s.snapshots.GetManifest(ctx, snap.ID)
	if err != nil || manifest == nil || strings.TrimSpace(manifest.ObjectKey) == "" {
		entry.Reason += "; manifest record missing, metadata-only delete"
		return entry, nil
	}
	entry.ObjectKeys = append(entry.ObjectKeys, manifest.ObjectKey)
	objects, err := s.snapshots.ListObjects(ctx, manifest.ID)
	if err != nil {
		return DeleteEntry{}, err
	}
	for _, o := range objects {
		entry.ObjectKeys = append(entry.ObjectKeys, o.ObjectKey)
		entry.BytesStored += o.Size
	}
	entry.ObjectCount = len(objects)
	return entry, nil
}

// condemnReason cites the bound(s) that condemned the snapshot.
func condemnReason(rank int, age time.Duration, rule domain.RetentionRule) string {
	var parts []string
	if rule.KeepLastN > 0 && rank >= rule.KeepLastN {
		parts = append(parts, fmt.Sprintf("rank %d exceeds keep_last_n=%d", rank, rule.KeepLastN))
	}
	if rule.KeepDays > 0 && age >= time.Duration(rule.KeepDays)*24*time.Hour {
		parts = append(parts, fmt.Sprintf("age %s exceeds keep_days=%d", age.Round(time.Hour), rule.KeepDays))
	}
	return strings.Join(parts, " and ")
}

// applyRecoveryPointFloor spares condemned recovery points when the floor
// would be breached. The floor is max(1, KeepMinimum) verified snapshots;
// sparing prefers the newest recovery points.
func (s *Service) applyRecoveryPointFloor(plan *DeletionPlan, keptVerified int, override bool) {
	floor := 1
	if plan.Rule.KeepMinimum > floor {
		floor = plan.Rule.KeepMinimum
	}
	var condemnedVerified int
	for _, d := range plan.Deletes {
		if d.Verified {
			condemnedVerified++
		}
	}
	if override {
		if keptVerified < floor && condemnedVerified > 0 {
			plan.OverrideGranted = true
		}
		plan.TotalBytes, plan.TotalObjects = planTotals(plan.Deletes)
		return
	}
	// Protect newest condemned recovery points until the floor holds.
	// Deletes are oldest-first, so walk from the end (newest).
	need := floor - keptVerified
	if need <= 0 {
		plan.TotalBytes, plan.TotalObjects = planTotals(plan.Deletes)
		return
	}
	protect := map[int]bool{}
	for i := len(plan.Deletes) - 1; i >= 0 && need > 0; i-- {
		if plan.Deletes[i].Verified {
			protect[i] = true
			need--
		}
	}
	if len(protect) == 0 {
		plan.TotalBytes, plan.TotalObjects = planTotals(plan.Deletes)
		return
	}
	kept := plan.Deletes[:0]
	for i, d := range plan.Deletes {
		if protect[i] {
			plan.Protected = append(plan.Protected, ProtectedEntry{
				SnapshotID: d.SnapshotID,
				Reason:     fmt.Sprintf("last valid recovery point protection (floor %d); condemned by: %s", floor, d.Reason),
			})
			continue
		}
		kept = append(kept, d)
	}
	plan.Deletes = kept
	plan.OverrideRequired = true
	plan.TotalBytes, plan.TotalObjects = planTotals(plan.Deletes)
}

// planTotals sums the condemned object set.
func planTotals(deletes []DeleteEntry) (uint64, int) {
	var b uint64
	var n int
	for _, d := range deletes {
		b += d.BytesStored
		n += len(d.ObjectKeys)
	}
	return b, n
}

// Apply executes a deletion plan: every condemned object is deleted via
// ObjectAccess, then the snapshot metadata is removed. Plans that require
// an override are refused unless ApplyOptions.Override confirms it.
//
// Object failures fail only their snapshot (metadata left for retry); all
// other snapshots still proceed. The returned error joins per-snapshot
// failures for the caller to log.
func (s *Service) Apply(ctx context.Context, plan *DeletionPlan, opts ApplyOptions) (*ApplyResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("%w: deletion plan is required", domain.ErrValidation)
	}
	if plan.OverrideRequired && !opts.Override {
		return nil, fmt.Errorf("%w: plan %q deletes protected recovery points", ErrLastRecoveryPoint, plan.ID)
	}
	res := &ApplyResult{}
	var failures []string
	for _, d := range plan.Deletes {
		if err := s.deleteSnapshotObjects(ctx, d); err != nil {
			res.FailedSnapshots = append(res.FailedSnapshots, d.SnapshotID)
			failures = append(failures, fmt.Sprintf("%s: %v", d.SnapshotID, err))
			continue
		}
		if err := s.snapshots.DeleteSnapshot(ctx, d.SnapshotID); err != nil {
			res.FailedSnapshots = append(res.FailedSnapshots, d.SnapshotID)
			failures = append(failures, fmt.Sprintf("%s: %v", d.SnapshotID, err))
			continue
		}
		res.DeletedSnapshots = append(res.DeletedSnapshots, d.SnapshotID)
		res.DeletedObjects += len(d.ObjectKeys)
		res.DeletedBytes += d.BytesStored
	}
	if len(failures) > 0 {
		return res, fmt.Errorf("retention apply partial: %s", strings.Join(failures, "; "))
	}
	return res, nil
}

// deleteSnapshotObjects removes every condemned object for one snapshot.
func (s *Service) deleteSnapshotObjects(ctx context.Context, d DeleteEntry) error {
	for _, key := range d.ObjectKeys {
		if err := s.objects.Delete(ctx, d.StorageID, key); err != nil {
			return fmt.Errorf("delete object %q: %w", key, err)
		}
	}
	return nil
}

// listPolicySnapshots pages through every snapshot of a policy.
func (s *Service) listPolicySnapshots(ctx context.Context, policyID string) ([]domain.Snapshot, error) {
	var all []domain.Snapshot
	offset := 0
	for {
		page, err := s.snapshots.ListSnapshots(ctx,
			domain.ListOptions{Limit: evalPageSize, Offset: offset},
			domain.SnapshotFilter{PolicyID: policyID})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if len(all) >= page.Total || len(page.Items) == 0 {
			return all, nil
		}
		offset += len(page.Items)
	}
}

// snapshotTime prefers completion time, falling back to creation time.
func snapshotTime(s domain.Snapshot) time.Time {
	if s.CompletedAt != nil {
		return *s.CompletedAt
	}
	return s.CreatedAt
}

// randomID returns a 128-bit hex identifier.
func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
