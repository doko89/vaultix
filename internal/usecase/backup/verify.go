package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Failure reasons reported by the verification gate.
const (
	ReasonMissing          = "missing"
	ReasonChecksumMismatch = "checksum_mismatch"
	ReasonSizeMismatch     = "size_mismatch"
	ReasonHeadError        = "head_error"
	ReasonManifestInvalid  = "manifest_invalid"
)

// ObjectFailure describes one object (or the manifest artifact) that failed
// verification.
type ObjectFailure struct {
	ObjectKey string
	// Expected is the manifest-recorded checksum; Got is the stored checksum
	// (empty when the store reports none or the object is missing).
	Expected string
	Got      string
	Reason   string
	Detail   string
}

// VerificationResult is the outcome of the completion gate. OK is true only
// when the manifest and ALL required objects verified.
type VerificationResult struct {
	SnapshotID     string
	ManifestID     string
	OK             bool
	CheckedObjects int
	Failures       []ObjectFailure
}

// VerifySnapshot runs the completion VERIFICATION gate: the manifest and
// every required object must verify before the snapshot may succeed. It
// persists the resulting snapshot status (verified or failed) and returns
// the detailed result.
//
// A backup is NEVER successful with incomplete objects or manifest: any
// failure — missing manifest, object count/byte mismatch, missing object,
// size or checksum mismatch — yields OK=false and marks the snapshot failed.
func (s *Service) VerifySnapshot(ctx context.Context, snapshotID string) (*VerificationResult, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return nil, fmt.Errorf("%w: snapshot_id is required", domain.ErrValidation)
	}
	snap, err := s.snapshots.GetSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	res := &VerificationResult{SnapshotID: snap.ID, ManifestID: snap.ManifestID}

	manifest, err := s.snapshots.GetManifest(ctx, snap.ID)
	if err != nil || manifest == nil {
		res.Failures = append(res.Failures, ObjectFailure{Reason: ReasonManifestInvalid, Detail: "manifest record is missing"})
		return res, s.finishVerification(ctx, snap, manifest, res)
	}
	res.ManifestID = manifest.ID

	objects, err := s.snapshots.ListObjects(ctx, manifest.ID)
	if err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		res.Failures = append(res.Failures, ObjectFailure{Reason: ReasonManifestInvalid, Detail: "manifest references no objects"})
	}
	if manifest.ObjectCount != len(objects) {
		res.Failures = append(res.Failures, ObjectFailure{
			Reason: ReasonManifestInvalid,
			Detail: fmt.Sprintf("manifest declares %d objects but %d are registered", manifest.ObjectCount, len(objects)),
		})
	}
	var total uint64
	for _, o := range objects {
		total += o.Size
	}
	if manifest.BytesStored != total {
		res.Failures = append(res.Failures, ObjectFailure{
			Reason: ReasonManifestInvalid,
			Detail: fmt.Sprintf("manifest declares %d bytes but objects sum to %d", manifest.BytesStored, total),
		})
	}

	// The manifest artifact itself must exist and match.
	if strings.TrimSpace(manifest.ObjectKey) == "" {
		res.Failures = append(res.Failures, ObjectFailure{Reason: ReasonManifestInvalid, Detail: "manifest has no object key"})
	} else {
		meta, err := s.objects.Head(ctx, snap.StorageID, manifest.ObjectKey)
		if f, ok := checkHead(manifest.ObjectKey, manifest.Checksum, 0, true, meta, err); !ok {
			res.Failures = append(res.Failures, f)
		}
	}

	// Every required object must exist with matching size and checksum.
	for _, o := range objects {
		meta, err := s.objects.Head(ctx, snap.StorageID, o.ObjectKey)
		res.CheckedObjects++
		if f, ok := checkHead(o.ObjectKey, o.Checksum, o.Size, false, meta, err); !ok {
			res.Failures = append(res.Failures, f)
		}
	}

	res.OK = len(res.Failures) == 0
	return res, s.finishVerification(ctx, snap, manifest, res)
}

// checkHead compares one stored object against its manifest record. Size is
// skipped for the manifest artifact (skipSize) since only its checksum is
// pinned. A stored checksum is compared when the store reports one; stores
// whose Head cannot report checksums still verify existence and size.
func checkHead(key, expected string, size uint64, skipSize bool, meta domain.ObjectMeta, err error) (ObjectFailure, bool) {
	if err != nil {
		return ObjectFailure{ObjectKey: key, Expected: expected, Reason: ReasonHeadError, Detail: err.Error()}, false
	}
	if !meta.Exists {
		return ObjectFailure{ObjectKey: key, Expected: expected, Reason: ReasonMissing, Detail: "object not found in storage"}, false
	}
	if !skipSize && meta.Size != size {
		return ObjectFailure{ObjectKey: key, Expected: expected, Got: meta.Checksum, Reason: ReasonSizeMismatch,
			Detail: fmt.Sprintf("expected %d bytes, stored %d", size, meta.Size)}, false
	}
	if meta.Checksum != "" && !checksumEqual(meta.Checksum, expected) {
		return ObjectFailure{ObjectKey: key, Expected: expected, Got: meta.Checksum, Reason: ReasonChecksumMismatch,
			Detail: "stored checksum differs from manifest"}, false
	}
	return ObjectFailure{}, true
}

// checksumEqual compares stored vs manifest checksums, tolerating stores
// that return a bare hex digest (e.g. an S3 ETag) instead of "algorithm:hex".
func checksumEqual(stored, expected string) bool {
	s, e := normalizeChecksum(stored), normalizeChecksum(expected)
	if s == e {
		return true
	}
	_, sHex, _ := strings.Cut(s, ":")
	_, eHex, _ := strings.Cut(e, ":")
	return sHex != "" && sHex == eHex
}

// normalizeChecksum trims quotes/whitespace and lowercases for comparison.
func normalizeChecksum(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "\"'")
	return strings.ToLower(v)
}

// finishVerification persists the gate outcome on the snapshot.
func (s *Service) finishVerification(ctx context.Context, snap *domain.Snapshot, manifest *domain.SnapshotManifest, res *VerificationResult) error {
	now := s.clock.Now()
	if res.OK {
		snap.Status = domain.SnapshotVerified
		completed := now
		snap.CompletedAt = &completed
		if manifest != nil {
			verified := now
			manifest.VerifiedAt = &verified
			if err := s.snapshots.PutManifest(ctx, manifest); err != nil {
				return err
			}
		}
	} else {
		snap.Status = domain.SnapshotFailed
	}
	return s.snapshots.UpdateSnapshot(ctx, snap)
}
