package retention

import (
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// DeleteEntry condemns one snapshot: its metadata plus every stored object
// (manifest artifact and data objects) named for deletion.
type DeleteEntry struct {
	SnapshotID  string
	StorageID   string
	ManifestID  string
	ObjectKeys  []string
	ObjectCount int
	BytesStored uint64
	// Reason cites the rule bound that condemned the snapshot.
	Reason string
	// Verified flags entries holding a valid recovery point.
	Verified    bool
	CompletedAt *time.Time
}

// ProtectedEntry explains why a condemned snapshot was spared.
type ProtectedEntry struct {
	SnapshotID string
	Reason     string
}

// DeletionPlan is the auditable retention decision: which snapshots go,
// which are protected, under which rule, by whom, and when. Evaluate builds
// it without side effects (dry-run); Apply executes it.
type DeletionPlan struct {
	ID        string
	PolicyID  string
	Rule      domain.RetentionRule
	CreatedAt time.Time
	Actor     string
	DryRun    bool
	// Deletes lists condemned snapshots in oldest-first order.
	Deletes []DeleteEntry
	// Protected lists condemned-but-spared snapshots (recovery-point floor).
	Protected []ProtectedEntry
	// Kept counts snapshots retained by the rule.
	Kept int
	// OverrideRequired is true when the rule condemns the last valid
	// recovery point(s): Apply refuses unless Override is confirmed.
	OverrideRequired bool
	// OverrideGranted records an explicitly confirmed override.
	OverrideGranted bool
	TotalBytes      uint64
	TotalObjects    int
}

// ApplyOptions authorizes plan execution.
type ApplyOptions struct {
	// Override confirms deletion of the last valid recovery point(s).
	// It must come from an explicit operator confirmation, never a default.
	Override bool
}

// EvaluateOptions tunes rule evaluation.
type EvaluateOptions struct {
	// Actor records who requested the evaluation for the audit trail.
	Actor string
	// DryRun marks the plan as a preview (Evaluate never mutates anyway).
	DryRun bool
	// OverrideLastRecoveryPoint evaluates as if the recovery-point
	// protection were overridden: condemned recovery points stay condemned
	// and the plan records OverrideGranted.
	OverrideLastRecoveryPoint bool
}

// ApplyResult reports executed deletions.
type ApplyResult struct {
	DeletedSnapshots []string
	DeletedObjects   int
	DeletedBytes     uint64
	// FailedSnapshots names snapshots whose deletion did not complete;
	// their metadata is left in place for retry.
	FailedSnapshots []string
}
