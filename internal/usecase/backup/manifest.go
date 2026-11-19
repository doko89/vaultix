package backup

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Object kinds referenced by a manifest.
const (
	ObjectKindChunk    = "chunk"
	ObjectKindDump     = "dump"
	ObjectKindMetadata = "metadata"
	ObjectKindManifest = "manifest"
)

// ReportedObject is one agent-reported stored artifact: data chunk, database
// dump, or metadata file.
type ReportedObject struct {
	// ObjectKey is the storage key within the destination bucket/prefix.
	ObjectKey string
	// Kind is chunk, dump, or metadata.
	Kind string
	// Size is the stored byte count.
	Size uint64
	// Checksum is "algorithm:hex" (e.g. "sha256:ab12...").
	Checksum string
	// Sequence orders chunks for reassembly.
	Sequence int
	// Source records the original path, volume, or database for catalog detail.
	Source string
}

// TargetResult is the agent's per-target completion report.
type TargetResult struct {
	TargetID string
	Type     domain.TargetType
	Selector string
	Method   domain.BackupMethod
	// BytesOriginal is the pre-compression byte count.
	BytesOriginal uint64
	// ServerVersion pins the database version for restore compatibility.
	ServerVersion string
	// ConsistencyNotes carries agent-observed caveats (changed files, ...).
	ConsistencyNotes []string
	Objects          []ReportedObject
}

// ManifestInput assembles one snapshot's catalog models from a job and its
// agent-reported target results.
type ManifestInput struct {
	// SnapshotID is optional; a random ID is assigned when empty.
	SnapshotID string
	// Job carries agent/policy/version/storage/encryption context.
	Job *domain.BackupJob
	// Compression echoes the policy-version compression for the record.
	Compression domain.Compression
	Targets     []TargetResult
	// ManifestObjectKey/Checksum identify the manifest artifact itself, which
	// the agent uploads alongside data objects and which verification re-checks.
	ManifestObjectKey string
	ManifestChecksum  string
}

// AssembleManifest builds the Snapshot, SnapshotManifest, and SnapshotObject
// models for a completed upload. It performs no I/O and persists nothing;
// CompleteJob persists the result and then runs the verification gate.
//
// The snapshot starts in SnapshotVerifying: only VerifySnapshot may promote
// it to SnapshotVerified (restorable).
func AssembleManifest(in ManifestInput, newID func() string) (*domain.Snapshot, *domain.SnapshotManifest, []domain.SnapshotObject, error) {
	if in.Job == nil {
		return nil, nil, nil, fmt.Errorf("%w: job is required", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Job.AgentID) == "" || strings.TrimSpace(in.Job.PolicyID) == "" {
		return nil, nil, nil, fmt.Errorf("%w: job must carry agent and policy identity", domain.ErrValidation)
	}
	if len(in.Targets) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: at least one target result is required", domain.ErrValidation)
	}
	if strings.TrimSpace(in.ManifestObjectKey) == "" {
		return nil, nil, nil, fmt.Errorf("%w: manifest object key is required", domain.ErrValidation)
	}
	if err := checkChecksum(in.ManifestChecksum); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: manifest checksum: %v", domain.ErrValidation, err)
	}
	if in.Job.Encrypted && strings.TrimSpace(in.Job.KeyRef) == "" {
		return nil, nil, nil, fmt.Errorf("%w: encrypted job must carry a key reference", domain.ErrValidation)
	}
	if newID == nil {
		return nil, nil, nil, fmt.Errorf("%w: id generator is required", domain.ErrValidation)
	}

	var (
		objects     []domain.SnapshotObject
		bytesOrig   uint64
		bytesStored uint64
	)
	seenSeq := map[string]map[int]bool{}
	for ti, tr := range in.Targets {
		if !tr.Type.Valid() {
			return nil, nil, nil, fmt.Errorf("%w: target %d: unknown target type %q", domain.ErrValidation, ti, tr.Type)
		}
		if len(tr.Objects) == 0 {
			return nil, nil, nil, fmt.Errorf("%w: target %d (%q): at least one object is required", domain.ErrValidation, ti, tr.Selector)
		}
		bytesOrig += tr.BytesOriginal
		if seenSeq[tr.TargetID] == nil {
			seenSeq[tr.TargetID] = map[int]bool{}
		}
		for oi, ro := range tr.Objects {
			if strings.TrimSpace(ro.ObjectKey) == "" {
				return nil, nil, nil, fmt.Errorf("%w: target %d object %d: object key is required", domain.ErrValidation, ti, oi)
			}
			switch ro.Kind {
			case ObjectKindChunk, ObjectKindDump, ObjectKindMetadata:
			default:
				return nil, nil, nil, fmt.Errorf("%w: target %d object %d: unknown kind %q", domain.ErrValidation, ti, oi, ro.Kind)
			}
			if err := checkChecksum(ro.Checksum); err != nil {
				return nil, nil, nil, fmt.Errorf("%w: target %d object %d: %v", domain.ErrValidation, ti, oi, err)
			}
			if ro.Sequence < 0 {
				return nil, nil, nil, fmt.Errorf("%w: target %d object %d: negative sequence", domain.ErrValidation, ti, oi)
			}
			if seenSeq[tr.TargetID][ro.Sequence] {
				return nil, nil, nil, fmt.Errorf("%w: target %d: duplicate sequence %d", domain.ErrValidation, ti, ro.Sequence)
			}
			seenSeq[tr.TargetID][ro.Sequence] = true
			bytesStored += ro.Size
			objects = append(objects, domain.SnapshotObject{
				ID:        newID(),
				ObjectKey: ro.ObjectKey,
				Kind:      ro.Kind,
				Size:      ro.Size,
				Checksum:  ro.Checksum,
				Sequence:  oi,
			})
		}
	}

	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		snapshotID = newID()
	}
	manifestID := newID()
	for i := range objects {
		objects[i].ManifestID = manifestID
		objects[i].SnapshotID = snapshotID
	}

	now := in.Job.UpdatedAt
	snap := &domain.Snapshot{
		ID:            snapshotID,
		AgentID:       in.Job.AgentID,
		PolicyID:      in.Job.PolicyID,
		PolicyVersion: in.Job.PolicyVersion,
		JobID:         in.Job.ID,
		TargetType:    snapshotTargetType(in.Targets),
		Selector:      snapshotSelector(in.Job, in.Targets),
		Status:        domain.SnapshotVerifying,
		ObjectCount:   len(objects),
		Encrypted:     in.Job.Encrypted,
		KeyRef:        in.Job.KeyRef,
		ManifestID:    manifestID,
		StorageID:     in.Job.StorageID,
		CreatedAt:     now,
	}
	bytesOrigCopy, bytesStoredCopy := bytesOrig, bytesStored
	snap.BytesOriginal = &bytesOrigCopy
	snap.BytesStored = &bytesStoredCopy

	manifest := &domain.SnapshotManifest{
		ID:            manifestID,
		SnapshotID:    snapshotID,
		FormatVersion: 1,
		ObjectKey:     in.ManifestObjectKey,
		Checksum:      in.ManifestChecksum,
		Encrypted:     in.Job.Encrypted,
		KeyRef:        in.Job.KeyRef,
		ObjectCount:   len(objects),
		BytesStored:   bytesStored,
		CreatedAt:     now,
	}
	return snap, manifest, objects, nil
}

// snapshotTargetType reduces the target set to one catalog type: the single
// type when homogeneous, app_stack when a job spans several types.
func snapshotTargetType(targets []TargetResult) domain.TargetType {
	first := targets[0].Type
	for _, t := range targets[1:] {
		if t.Type != first {
			return domain.TargetAppStack
		}
	}
	return first
}

// snapshotSelector echoes the single-target selector for catalog browsing;
// multi-target snapshots reference their policy version instead.
func snapshotSelector(job *domain.BackupJob, targets []TargetResult) string {
	if len(targets) == 1 {
		return targets[0].Selector
	}
	return fmt.Sprintf("policy:%s@%d", job.PolicyID, job.PolicyVersion)
}

// checkChecksum validates "algorithm:hex" integrity references.
func checkChecksum(v string) error {
	algo, payload, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok || strings.TrimSpace(algo) == "" || strings.TrimSpace(payload) == "" {
		return fmt.Errorf("checksum %q must look like \"algorithm:hex\"", v)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(payload))
	if err != nil || len(raw) < 4 {
		return fmt.Errorf("checksum %q has an invalid hex payload", v)
	}
	return nil
}
