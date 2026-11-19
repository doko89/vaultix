package backup

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// CompletionError reports a completion that failed the job: the manifest was
// invalid or verification failed. The job is already marked failed when this
// is returned — callers (the agent protocol) must surface Code/Message to the
// agent as the actionable outcome, not retry blindly. Result is non-nil when
// verification ran (code verification_failed) and nil for manifest-shape
// failures (code manifest_invalid / manifest_missing).
type CompletionError struct {
	Code    string
	Message string
	Result  *VerificationResult
}

// Error keeps the historic "backup completion failed [code]: msg" format.
func (e *CompletionError) Error() string {
	return fmt.Sprintf("backup completion failed [%s]: %s", e.Code, e.Message)
}

// AgentObject is one flat manifest entry from an agent completion report:
// the storage key, stored byte count, and lowercase hex sha256 of the bytes
// the agent uploaded.
type AgentObject struct {
	Key    string
	Size   uint64
	SHA256 string
}

// FlatManifest is the agent's end-of-upload report in flat form (agent
// protocol v1: manifest {objects, bytes_*, compression, encryption}).
// Objects lists data chunks/dumps the agent uploaded under its job-scoped
// keys, plus the job manifest artifact itself (see ManifestObjectName).
type FlatManifest struct {
	Objects []AgentObject
	// BytesRead/BytesCompressed/BytesUploaded echo agent-measured counters.
	// BytesRead seeds the snapshot's original-byte count; stored bytes always
	// come from the verified object sizes.
	BytesRead       *uint64
	BytesCompressed *uint64
	BytesUploaded   *uint64
	// Compression is informational (the pinned version owns the record).
	Compression string
	// Encrypted/KeyRef cross-check the pinned job config when set: an agent
	// must not silently flip encryption off or on.
	Encrypted *bool
	KeyRef    string
}

// ManifestObjectName is the relative object name the agent must upload its
// job manifest under (inside its job-scoped prefix) and include in
// FlatManifest.Objects. The manager identifies it by exact name or by a
// "/manifest.json" suffix on a full key.
const ManifestObjectName = "manifest.json"

// CompleteJobFlat completes a job from an agent flat manifest: it maps the
// flat objects onto the pinned version's target context, assembles the
// snapshot manifest, and runs the same verification gate as CompleteJob. The
// job succeeds if and only if the manifest and ALL objects verify; anything
// else fails the job with a *CompletionError carrying the actionable detail.
func (s *Service) CompleteJobFlat(ctx context.Context, jobID string, m FlatManifest) (*VerificationResult, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("%w: job_id is required", domain.ErrValidation)
	}
	job, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	switch job.State {
	case domain.JobUploading, domain.JobVerifying:
		// Expected completion states; Verifying allows a safe retry.
	default:
		return nil, fmt.Errorf("%w: cannot complete job in state %q", domain.ErrConflict, job.State)
	}
	pv, err := s.policies.GetVersion(ctx, job.PolicyID, job.PolicyVersion)
	if err != nil {
		return nil, err
	}
	if len(pv.Targets) == 0 {
		return nil, s.failJob(ctx, job, "manifest_invalid",
			fmt.Sprintf("pinned policy %s@%d has no targets; cannot attribute uploaded objects", job.PolicyID, job.PolicyVersion))
	}
	if err := checkManifestEncryption(job, m); err != nil {
		return nil, s.failJob(ctx, job, "manifest_invalid", err.Error())
	}
	manifestObj, dataObjs, err := splitManifestObject(jobID, m.Objects)
	if err != nil {
		return nil, s.failJob(ctx, job, "manifest_missing", err.Error())
	}
	report, err := flatReport(job, pv, manifestObj, dataObjs, m.BytesRead)
	if err != nil {
		return nil, s.failJob(ctx, job, "manifest_invalid", err.Error())
	}
	return s.runCompletion(ctx, job, pv.Compression, report)
}

// checkManifestEncryption cross-checks the agent-declared encryption against
// the pinned job config. A nil declaration skips the check (older agents).
func checkManifestEncryption(job *domain.BackupJob, m FlatManifest) error {
	if m.Encrypted == nil {
		return nil
	}
	switch {
	case job.Encrypted && !*m.Encrypted:
		return fmt.Errorf("job requires encryption (key_ref %q) but the agent reported an unencrypted payload; re-run with encryption enabled", job.KeyRef)
	case !job.Encrypted && *m.Encrypted:
		return fmt.Errorf("job is not encrypted but the agent reported an encrypted payload; re-run without encryption or enable it on the policy")
	case job.Encrypted && strings.TrimSpace(m.KeyRef) != "" && m.KeyRef != job.KeyRef:
		return fmt.Errorf("agent key_ref %q does not match the job key_ref %q", m.KeyRef, job.KeyRef)
	default:
		return nil
	}
}

// splitManifestObject separates the manifest artifact from data objects. The
// manifest is the entry named exactly ManifestObjectName or ending in
// "/manifest.json"; exactly one must be present.
func splitManifestObject(jobID string, objects []AgentObject) (AgentObject, []AgentObject, error) {
	var (
		manifest AgentObject
		data     []AgentObject
		found    bool
	)
	for _, o := range objects {
		if o.Key == ManifestObjectName || strings.HasSuffix(o.Key, "/"+ManifestObjectName) {
			if found {
				return AgentObject{}, nil, fmt.Errorf("multiple %q objects in manifest; upload exactly one job manifest", ManifestObjectName)
			}
			manifest, found = o, true
			continue
		}
		data = append(data, o)
	}
	if !found {
		return AgentObject{}, nil, fmt.Errorf("completion manifest has no %q object; upload the job manifest to the job scope (e.g. jobs/%s/%s) and list it in objects[]", ManifestObjectName, jobID, ManifestObjectName)
	}
	return manifest, data, nil
}

// flatReport maps the flat agent objects onto one CompletionReport. All data
// objects are attributed to a single target result derived from the pinned
// version's first target (type/selector/method); per-target attribution needs
// the structured CompletionReport path.
func flatReport(job *domain.BackupJob, pv *domain.BackupPolicyVersion, manifest AgentObject, data []AgentObject, bytesRead *uint64) (CompletionReport, error) {
	manifestSum, err := flatChecksum(manifest.SHA256)
	if err != nil {
		return CompletionReport{}, fmt.Errorf("manifest object %q: %v", manifest.Key, err)
	}
	first := pv.Targets[0]
	kind := ObjectKindChunk
	switch first.Type {
	case domain.TargetPostgres, domain.TargetMySQL, domain.TargetMSSQL:
		kind = ObjectKindDump
	}
	tr := TargetResult{
		TargetID: first.ID,
		Type:     first.Type,
		Selector: first.Selector,
		Method:   first.Method,
	}
	if tr.TargetID == "" {
		tr.TargetID = fmt.Sprintf("%s@%d", pv.PolicyID, pv.Version)
	}
	var original uint64
	for i, o := range data {
		if strings.TrimSpace(o.Key) == "" {
			return CompletionReport{}, fmt.Errorf("object %d: object key is required", i)
		}
		sum, err := flatChecksum(o.SHA256)
		if err != nil {
			return CompletionReport{}, fmt.Errorf("object %d (%q): %v", i, o.Key, err)
		}
		original += o.Size
		tr.Objects = append(tr.Objects, ReportedObject{
			ObjectKey: o.Key,
			Kind:      kind,
			Size:      o.Size,
			Checksum:  sum,
			Sequence:  i,
			Source:    first.Selector,
		})
	}
	if bytesRead != nil {
		tr.BytesOriginal = *bytesRead
	} else {
		tr.BytesOriginal = original
	}
	return CompletionReport{
		Targets:           []TargetResult{tr},
		ManifestObjectKey: manifest.Key,
		ManifestChecksum:  manifestSum,
	}, nil
}

// flatChecksum validates a bare lowercase/uppercase hex sha256 digest and
// returns it in "algorithm:hex" form.
func flatChecksum(sha string) (string, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(sha))
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("sha256 %q must be 64 hex characters", sha)
	}
	return "sha256:" + strings.ToLower(strings.TrimSpace(sha)), nil
}
