package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Service orchestrates target resolution, manifest assembly, and the
// completion verification gate.
type Service struct {
	policies  domain.PolicyRepository
	jobs      domain.BackupJobRepository
	snapshots domain.SnapshotRepository
	stacks    domain.StackRepository
	objects   ObjectAccess
	clock     domain.Clock
	newID     func() string
}

// Deps wires a Service. Clock and IDGen are optional test seams.
type Deps struct {
	Policies  domain.PolicyRepository
	Jobs      domain.BackupJobRepository
	Snapshots domain.SnapshotRepository
	Stacks    domain.StackRepository
	Objects   ObjectAccess

	Clock domain.Clock
	IDGen func() string
}

// New builds a Service, rejecting nil required dependencies.
func New(d Deps) (*Service, error) {
	if d.Policies == nil || d.Jobs == nil || d.Snapshots == nil || d.Stacks == nil || d.Objects == nil {
		return nil, fmt.Errorf("%w: backup service requires policies, jobs, snapshots, stacks, and object access", domain.ErrValidation)
	}
	clock := d.Clock
	if clock == nil {
		clock = domain.SystemClock{}
	}
	newID := d.IDGen
	if newID == nil {
		newID = randomID
	}
	return &Service{
		policies: d.Policies, jobs: d.Jobs, snapshots: d.Snapshots,
		stacks: d.Stacks, objects: d.Objects, clock: clock, newID: newID,
	}, nil
}

// CompletionReport is the agent's end-of-upload report for one job.
type CompletionReport struct {
	Targets           []TargetResult
	ManifestObjectKey string
	ManifestChecksum  string
}

// CompleteJob assembles the snapshot manifest for an uploaded job and runs
// the verification gate. The job succeeds if and only if the manifest and
// ALL required objects verify; anything else fails the job with details.
// Retrying completion is safe: it re-runs verification instead of
// fabricating success.
func (s *Service) CompleteJob(ctx context.Context, jobID string, report CompletionReport) (*VerificationResult, error) {
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

	// Resolve targets from the pinned version first: completion must reflect
	// the frozen configuration the job executed against.
	pv, err := s.policies.GetVersion(ctx, job.PolicyID, job.PolicyVersion)
	if err != nil {
		return nil, err
	}
	if len(report.Targets) == 0 {
		return nil, fmt.Errorf("%w: completion report has no target results", domain.ErrValidation)
	}

	now := s.clock.Now()
	job.UpdatedAt = now
	snap, manifest, objects, err := AssembleManifest(ManifestInput{
		Job:               job,
		Compression:       pv.Compression,
		Targets:           report.Targets,
		ManifestObjectKey: report.ManifestObjectKey,
		ManifestChecksum:  report.ManifestChecksum,
	}, s.newID)
	if err != nil {
		return nil, s.failJob(ctx, job, "manifest_invalid", err.Error())
	}
	snap.CreatedAt = now
	manifest.CreatedAt = now

	if err := s.snapshots.CreateSnapshot(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.snapshots.PutManifest(ctx, manifest); err != nil {
		return nil, err
	}
	if err := s.snapshots.PutObjects(ctx, objects); err != nil {
		return nil, err
	}

	job.SnapshotID = snap.ID
	if job.State == domain.JobUploading {
		if err := domain.TransitionTo(&job.State, domain.JobVerifying); err != nil {
			return nil, err
		}
		if err := s.jobs.UpdateJob(ctx, job); err != nil {
			return nil, err
		}
	}

	res, err := s.VerifySnapshot(ctx, snap.ID)
	if err != nil {
		return nil, err
	}
	job.UpdatedAt = s.clock.Now()
	if !res.OK {
		return res, s.failJob(ctx, job, "verification_failed", verificationSummary(res))
	}
	if err := domain.TransitionTo(&job.State, domain.JobSucceeded); err != nil {
		return nil, err
	}
	finished := s.clock.Now()
	job.FinishedAt = &finished
	job.UpdatedAt = finished
	if snap.BytesOriginal != nil {
		job.BytesRead = snap.BytesOriginal
	}
	if snap.BytesStored != nil {
		job.BytesUploaded = snap.BytesStored
		job.BytesCompressed = snap.BytesStored
	}
	return res, s.jobs.UpdateJob(ctx, job)
}

// failJob records a failed completion without ever reporting success.
func (s *Service) failJob(ctx context.Context, job *domain.BackupJob, code, msg string) error {
	// Best effort: move toward failed from any non-terminal state.
	if !job.State.Terminal() {
		if job.State == domain.JobUploading {
			_ = domain.TransitionTo(&job.State, domain.JobVerifying)
		}
		_ = domain.TransitionTo(&job.State, domain.JobFailed)
	}
	job.ErrorCode = code
	job.ErrorMessage = msg
	finished := s.clock.Now()
	job.FinishedAt = &finished
	job.UpdatedAt = finished
	if uerr := s.jobs.UpdateJob(ctx, job); uerr != nil {
		return uerr
	}
	return fmt.Errorf("backup completion failed [%s]: %s", code, msg)
}

// verificationSummary condenses gate failures for the job error detail.
func verificationSummary(res *VerificationResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d objects verified, %d failure(s): ",
		res.CheckedObjects-len(res.Failures), res.CheckedObjects, len(res.Failures))
	for i, f := range res.Failures {
		if i > 0 {
			b.WriteString("; ")
		}
		if f.ObjectKey != "" {
			fmt.Fprintf(&b, "%s: ", f.ObjectKey)
		}
		b.WriteString(f.Reason)
		if f.Detail != "" {
			fmt.Fprintf(&b, " (%s)", f.Detail)
		}
		if i >= 4 && len(res.Failures) > 5 {
			fmt.Fprintf(&b, "; +%d more", len(res.Failures)-i-1)
			break
		}
	}
	return b.String()
}

// randomID returns a 128-bit hex identifier.
func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
