package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	"github.com/vaultix/vaultix/internal/domain"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
	backupuc "github.com/vaultix/vaultix/internal/usecase/backup"
	jobuc "github.com/vaultix/vaultix/internal/usecase/job"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// This file implements the manager side of agent protocol v1: manifest
// completion on the job queue, short-lived storage grants with strict
// job-key binding, restore dispatch/download/report, and TLS-only data-key
// issuance with audit. Errors are secret-free: they name jobs, keys, and
// key references, never key material or credentials.

var (
	_ agentshttp.JobQueue            = (*agentJobQueue)(nil)
	_ agentshttp.GrantService        = (*agentDataPlane)(nil)
	_ agentshttp.RestoreAgentService = (*agentDataPlane)(nil)
)

// keyIssueAuditAction records data-key issuance in the audit log. The action
// column is free text; the detail names agent/job/key_ref only.
const keyIssueAuditAction = domain.AuditAction("key_issue")

// ---- job queue: cancellations + manifest completion ----

// CancelledIDs returns jobs cancelled while assigned to the agent (served
// from the job service's CancelTracker).
func (q *agentJobQueue) CancelledIDs(ctx context.Context, agentID string) ([]string, error) {
	return q.jobs.TakeCancelled(ctx, agentID)
}

// CompleteWithManifest applies a manifest-bearing report: progress/bytes are
// recorded, the job is walked forward to uploading, and the flat manifest
// drives backup verify-then-complete. The job succeeds only when verified.
//
// Terminal agent states are honored without completion: failed/cancelled end
// the job via the legacy transition, and a "succeeded" claim is treated as
// "upload finished, please verify" — success is decided by the verification
// gate alone, so agents can never fabricate it.
func (q *agentJobQueue) CompleteWithManifest(ctx context.Context, agentID string, rep agentshttp.JobReport) (*agentshttp.CompletionOutcome, error) {
	if rep.Manifest == nil {
		return nil, fmt.Errorf("jobproto: completion requires a manifest: %w", domain.ErrValidation)
	}
	if q.backup == nil {
		return nil, fmt.Errorf("jobproto: backup service not wired")
	}
	job, err := q.jobs.Get(ctx, rep.JobID)
	if err != nil {
		return nil, err
	}
	if job.AgentID != agentID {
		return nil, fmt.Errorf("jobproto: job %s belongs to agent %s: %w", rep.JobID, job.AgentID, domain.ErrForbidden)
	}
	switch rep.State {
	case domain.JobFailed, domain.JobCancelled:
		// Honor the terminal report; completion is not attempted.
		if _, err := q.jobs.ReportResult(ctx, agentID, rep.JobID, q.outcome(rep, rep.State)); err != nil {
			return nil, err
		}
		msg := rep.Error
		if msg == "" {
			msg = "agent reported " + string(rep.State)
		}
		return &agentshttp.CompletionOutcome{Verified: false, Failures: []string{msg}}, nil
	}
	// Progress-only application first (zero state never transitions), then
	// walk forward to uploading: the manifest proves the agent executed and
	// uploaded, so missed intermediate heartbeats must not block completion.
	if _, err := q.jobs.ReportResult(ctx, agentID, rep.JobID, q.outcome(rep, "")); err != nil {
		return nil, err
	}
	for _, next := range []domain.JobState{domain.JobRunning, domain.JobUploading} {
		cur, err := q.jobs.Get(ctx, rep.JobID)
		if err != nil {
			return nil, err
		}
		if cur.State == domain.JobUploading || cur.State == domain.JobVerifying {
			break
		}
		if cur.State == next {
			continue // already at this step
		}
		if _, err := q.jobs.Transition(ctx, rep.JobID, next); err != nil {
			return nil, fmt.Errorf("jobproto: cannot advance job %s to completion (%s): %w",
				rep.JobID, cur.State, err)
		}
	}
	res, err := q.backup.CompleteJobFlat(ctx, rep.JobID, toFlatManifest(rep.Manifest))
	if err != nil {
		var cerr *backupuc.CompletionError
		if errors.As(err, &cerr) {
			return &agentshttp.CompletionOutcome{Verified: false, Failures: completionFailures(cerr)}, nil
		}
		return nil, err
	}
	done, err := q.jobs.Get(ctx, rep.JobID)
	if err != nil {
		return nil, err
	}
	return &agentshttp.CompletionOutcome{Verified: res.OK, SnapshotID: done.SnapshotID}, nil
}

// outcome maps the narrow agent report onto a job outcome with the given
// state override.
func (q *agentJobQueue) outcome(rep agentshttp.JobReport, state domain.JobState) jobuc.Outcome {
	outcome := jobuc.Outcome{
		State:         state,
		BytesRead:     rep.BytesRead,
		BytesUploaded: rep.BytesUploaded,
	}
	if rep.Manifest != nil {
		if rep.Manifest.BytesRead != nil {
			outcome.BytesRead = rep.Manifest.BytesRead
		}
		outcome.BytesCompressed = rep.Manifest.BytesCompressed
		if rep.Manifest.BytesUploaded != nil {
			outcome.BytesUploaded = rep.Manifest.BytesUploaded
		}
	}
	if rep.Error != "" {
		outcome.ErrorMessage = rep.Error
	}
	return outcome
}

// toFlatManifest maps the queue-seam manifest onto the backup usecase shape.
func toFlatManifest(m *agentshttp.JobManifest) backupuc.FlatManifest {
	fm := backupuc.FlatManifest{
		BytesRead: m.BytesRead, BytesCompressed: m.BytesCompressed,
		BytesUploaded: m.BytesUploaded, Compression: m.Compression,
		Encrypted: m.Encrypted, KeyRef: m.KeyRef,
	}
	for _, o := range m.Objects {
		fm.Objects = append(fm.Objects, backupuc.AgentObject{
			Key: o.Key, Size: o.SizeBytes, SHA256: o.SHA256,
		})
	}
	return fm
}

// completionFailures renders a CompletionError as actionable agent-facing
// lines: one per failed object when verification ran, else the code/message.
func completionFailures(cerr *backupuc.CompletionError) []string {
	if cerr.Result != nil && len(cerr.Result.Failures) > 0 {
		out := make([]string, 0, len(cerr.Result.Failures))
		for _, f := range cerr.Result.Failures {
			line := f.Reason
			if f.ObjectKey != "" {
				line = f.ObjectKey + ": " + line
			}
			if f.Detail != "" {
				line += " (" + f.Detail + ")"
			}
			out = append(out, line)
		}
		return out
	}
	return []string{"[" + cerr.Code + "] " + cerr.Message}
}

// ---- key issuance ----

// keyIssuer opens data keys from secret profiles and audits every issuance.
// Plaintext keys never reach errors or logs.
type keyIssuer struct {
	store domain.StorageRepository
	box   ustorage.SecretBox
	audit *audituc.Service
	log   *slog.Logger
}

// openDataKey returns the base64 data key sealed in the secret profile
// keyRef. It fails closed: missing/undecryptable profiles, unwired seams,
// and audit failures all yield ok=false with no key material exposed.
func (k *keyIssuer) openDataKey(ctx context.Context, agentID, keyRef, use string) (b64 string, ok bool) {
	if k == nil || k.store == nil || k.box == nil {
		return "", false
	}
	if strings.TrimSpace(keyRef) == "" {
		return "", false
	}
	log := k.log
	if log == nil {
		log = slog.Default()
	}
	sec, err := k.store.GetSecret(ctx, keyRef)
	if err != nil {
		log.Warn("key issuance: secret profile unavailable", "agent_id", agentID, "key_ref", keyRef, "use", use)
		return "", false
	}
	plain, err := k.box.Open(ctx, sec.Ciphertext, sec.KeyRef)
	if err != nil {
		log.Warn("key issuance: secret undecryptable", "agent_id", agentID, "key_ref", keyRef, "use", use)
		return "", false
	}
	// Audit before returning: issuance without a trail fails closed.
	if k.audit != nil {
		_, err = k.audit.Append(ctx, audituc.Entry{
			ActorType:  "agent",
			Action:     keyIssueAuditAction,
			TargetType: "secret",
			TargetID:   keyRef,
			Outcome:    audituc.OutcomeOK,
			Detail:     "agent:" + agentID + " use:" + use,
		})
		if err != nil {
			log.Warn("key issuance: audit append failed; withholding key", "agent_id", agentID, "key_ref", keyRef)
			return "", false
		}
	}
	return base64.StdEncoding.EncodeToString(plain), true
}

// jobEncryption builds the poll encryption envelope from the pinned policy
// version. key_b64 is minted only over TLS; over plaintext (or when the key
// cannot be opened) the agent still gets enabled/key_ref so it can fail with
// an actionable error instead of guessing.
func (q *agentJobQueue) jobEncryption(ctx context.Context, agentID string, v *domain.BackupPolicyVersion) agentshttp.JobEncryption {
	enc := agentshttp.JobEncryption{Enabled: v.Encryption.Enabled, KeyRef: v.Encryption.KeyRef}
	if !v.Encryption.Enabled {
		return enc
	}
	if !agentshttp.TransportSecure(ctx) {
		return enc
	}
	if b64, ok := q.keys.openDataKey(ctx, agentID, v.Encryption.KeyRef, "poll"); ok {
		enc.KeyB64 = b64
	}
	return enc
}

// ---- data plane: grants + restores ----

// agentDataPlane implements the grant and restore seams over the storage,
// snapshot, and restore-job ports.
type agentDataPlane struct {
	jobs      *jobuc.Service
	restores  domain.RestoreJobRepository
	snapshots domain.SnapshotRepository
	storage   *ustorage.Service
	store     domain.StorageRepository
	keys      *keyIssuer
	clock     domain.Clock
}

func (p *agentDataPlane) now() time.Time {
	if p.clock != nil {
		return p.clock.Now()
	}
	return time.Now()
}

// ownBackupJob loads a backup job and proves it belongs to the agent.
func (p *agentDataPlane) ownBackupJob(ctx context.Context, agentID, jobID string) (*domain.BackupJob, error) {
	job, err := p.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.AgentID != agentID {
		return nil, fmt.Errorf("jobproto: job %s belongs to agent %s: %w", jobID, job.AgentID, domain.ErrForbidden)
	}
	return job, nil
}

// destinationForJob resolves the job's storage destination, falling back to
// the default destination when the job names none.
func (p *agentDataPlane) destinationForJob(ctx context.Context, job *domain.BackupJob) (*domain.StorageDestination, error) {
	if strings.TrimSpace(job.StorageID) != "" {
		return p.store.GetDestination(ctx, job.StorageID)
	}
	d, err := p.store.GetDefaultDestination(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobproto: job %s names no storage destination and no default is configured: %w", job.ID, domain.ErrValidation)
	}
	return d, nil
}

// relativeObject resolves an agent-supplied key to a name relative to the
// job's scope. Full keys must already sit inside this job's scope (a job A
// key shaped for job B is rejected); anything else is treated as a relative
// name and re-validated. Traversal, absolute keys, and cross-job scopes all
// fail.
func relativeObject(prefix, jobID, key string) (string, error) {
	if err := ustorage.ValidateJobKey(prefix, jobID, key); err == nil {
		dir := "jobs/" + jobID
		if trimmed := strings.Trim(prefix, "/"); trimmed != "" {
			dir = trimmed + "/" + dir
		}
		return strings.TrimPrefix(key, dir+"/"), nil
	}
	if isScopedKey(prefix, key) {
		return "", fmt.Errorf("%w: object key %q is outside job %q scope", domain.ErrValidation, key, jobID)
	}
	if _, err := ustorage.ScopeObjectKey(prefix, jobID, key); err != nil {
		return "", err
	}
	return key, nil
}

// isScopedKey reports whether key wears a full jobs-tree shape (this job's
// or any other's), as opposed to a bare relative object name.
func isScopedKey(prefix, key string) bool {
	if !strings.Contains(key, "/") {
		return false
	}
	if strings.HasPrefix(key, "jobs/") {
		return true
	}
	if trimmed := strings.Trim(prefix, "/"); trimmed != "" && strings.HasPrefix(key, trimmed+"/jobs/") {
		return true
	}
	return false
}

// IssueUploadGrants mints one PUT grant per object, strictly bound to the
// job's scope.
func (p *agentDataPlane) IssueUploadGrants(ctx context.Context, agentID, jobID string, objects []agentshttp.UploadObjectRequest) ([]agentshttp.ObjectGrant, error) {
	job, err := p.ownBackupJob(ctx, agentID, jobID)
	if err != nil {
		return nil, err
	}
	if job.State.Terminal() {
		return nil, fmt.Errorf("jobproto: cannot mint grants for terminal job %s (%s): %w", jobID, job.State, domain.ErrConflict)
	}
	dest, err := p.destinationForJob(ctx, job)
	if err != nil {
		return nil, err
	}
	out := make([]agentshttp.ObjectGrant, 0, len(objects))
	for _, o := range objects {
		rel, err := relativeObject(dest.Prefix, jobID, o.Key)
		if err != nil {
			return nil, err
		}
		g, err := p.storage.IssueUploadGrant(ctx, dest.ID, jobID, rel, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, agentshttp.ObjectGrant{Key: g.ObjectKey, URL: g.URL, Headers: g.Headers, ExpiresAt: g.ExpiresAt})
	}
	return out, nil
}

// Multipart bounds for init: part sizes between 1 MiB and 5 GiB, at most
// 10000 parts per upload (the S3 ceiling).
const (
	minMultipartPartSize = 1 << 20
	maxMultipartPartSize = 5 << 30
	maxMultipartParts    = 10000
)

// InitMultipartUpload starts a multipart upload for one job-scoped object
// and returns the FULL part-URL list (part i uses partURLs[i-1]) sized from
// sizeBytes/partSize. Part count is ceil(size/partSize), min 1.
func (p *agentDataPlane) InitMultipartUpload(ctx context.Context, agentID, jobID, object string, sizeBytes uint64, partSize int64) (string, string, []string, error) {
	job, err := p.ownBackupJob(ctx, agentID, jobID)
	if err != nil {
		return "", "", nil, err
	}
	if job.State.Terminal() {
		return "", "", nil, fmt.Errorf("jobproto: cannot start uploads for terminal job %s (%s): %w", jobID, job.State, domain.ErrConflict)
	}
	if partSize < minMultipartPartSize || partSize > maxMultipartPartSize {
		return "", "", nil, fmt.Errorf("%w: part_size must be 1MiB..5GiB", domain.ErrValidation)
	}
	numParts := int((sizeBytes + uint64(partSize) - 1) / uint64(partSize))
	if numParts < 1 {
		numParts = 1
	}
	if numParts > maxMultipartParts {
		return "", "", nil, fmt.Errorf("%w: size needs %d parts, exceeding the 10000-part ceiling; raise part_size", domain.ErrValidation, numParts)
	}
	dest, err := p.destinationForJob(ctx, job)
	if err != nil {
		return "", "", nil, err
	}
	rel, err := relativeObject(dest.Prefix, jobID, object)
	if err != nil {
		return "", "", nil, err
	}
	key, uploadID, err := p.storage.InitiateJobUpload(ctx, dest.ID, jobID, rel)
	if err != nil {
		return "", "", nil, err
	}
	numbers := make([]int, 0, numParts)
	for i := 1; i <= numParts; i++ {
		numbers = append(numbers, i)
	}
	grants, err := p.storage.PresignJobParts(ctx, dest.ID, jobID, key, uploadID, numbers, 0)
	if err != nil {
		return "", "", nil, err
	}
	urls := make([]string, 0, len(grants))
	for _, g := range grants {
		urls = append(urls, g.URL)
	}
	return key, uploadID, urls, nil
}

// CompleteMultipartUpload finishes a multipart upload for one job's object.
func (p *agentDataPlane) CompleteMultipartUpload(ctx context.Context, agentID, jobID, key, uploadID string, parts []agentshttp.MultipartPart) error {
	job, err := p.ownBackupJob(ctx, agentID, jobID)
	if err != nil {
		return err
	}
	dest, err := p.destinationForJob(ctx, job)
	if err != nil {
		return err
	}
	in := make([]ustorage.MultipartPart, 0, len(parts))
	for _, pt := range parts {
		in = append(in, ustorage.MultipartPart{PartNumber: pt.PartNumber, ETag: pt.ETag, SHA256: pt.SHA256, SizeBytes: pt.SizeBytes})
	}
	return p.storage.CompleteJobUpload(ctx, dest.ID, jobID, key, uploadID, in)
}

// AbortMultipartUpload cancels an in-flight multipart upload.
func (p *agentDataPlane) AbortMultipartUpload(ctx context.Context, agentID, jobID, key, uploadID string) error {
	job, err := p.ownBackupJob(ctx, agentID, jobID)
	if err != nil {
		return err
	}
	dest, err := p.destinationForJob(ctx, job)
	if err != nil {
		return err
	}
	return p.storage.AbortJobUpload(ctx, dest.ID, jobID, key, uploadID)
}

// ---- restores ----

// restoreOverwritePolicy is the MVP overwrite behavior. PRD §7.13 requires a
// warning before overwriting: the plan step carries that warning and the
// job exists only after explicit confirm+execute, so the dispatched
// assignment allows the confirmed overwrite ("warn" would make the agent
// refuse every in-place restore over existing data).
const restoreOverwritePolicy = "allow"

// PollRestore returns the agent's oldest unfinished restore job (resuming an
// in-flight one before starting a queued one), or (nil, nil) when none is
// due. Queued restores move to assigned on dispatch.
func (p *agentDataPlane) PollRestore(ctx context.Context, agentID string) (*agentshttp.RestoreAssignment, error) {
	const pageSize = 100
	var cands []*domain.RestoreJob
	for offset := 0; ; offset += pageSize {
		res, err := p.restores.ListJobs(ctx, domain.ListOptions{Limit: pageSize, Offset: offset}, agentID)
		if err != nil {
			return nil, err
		}
		for i := range res.Items {
			if !res.Items[i].State.Terminal() {
				j := res.Items[i]
				cands = append(cands, &j)
			}
		}
		if len(res.Items) < pageSize {
			break
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	sort.SliceStable(cands, func(i, k int) bool {
		if cands[i].CreatedAt.Equal(cands[k].CreatedAt) {
			return cands[i].ID < cands[k].ID
		}
		return cands[i].CreatedAt.Before(cands[k].CreatedAt)
	})
	job := cands[0]
	if job.State == domain.JobQueued {
		if err := domain.TransitionTo(&job.State, domain.JobAssigned); err != nil {
			return nil, err
		}
		job.UpdatedAt = p.now()
		if err := p.restores.UpdateJob(ctx, job); err != nil {
			return nil, err
		}
	}
	return p.assignRestore(ctx, agentID, job)
}

// assignRestore maps one restore job onto the agent-facing assignment.
func (p *agentDataPlane) assignRestore(ctx context.Context, agentID string, job *domain.RestoreJob) (*agentshttp.RestoreAssignment, error) {
	snap, err := p.snapshots.GetSnapshot(ctx, job.SnapshotID)
	if err != nil {
		return nil, fmt.Errorf("jobproto: restore %s references an unavailable snapshot: %w", job.ID, err)
	}
	objects, err := p.restoreObjects(ctx, snap)
	if err != nil {
		return nil, err
	}
	out := &agentshttp.RestoreAssignment{
		ID:              job.ID,
		SnapshotID:      snap.ID,
		Kind:            restoreKind(snap.TargetType),
		Destination:     agentshttp.RestoreDestination{Mode: "original"},
		OverwritePolicy: restoreOverwritePolicy,
		Objects:         objects,
		Paths:           append([]string(nil), job.Paths...),
		Encryption:      agentshttp.RestoreEncryption{Enabled: snap.Encrypted, KeyRef: snap.KeyRef},
	}
	if !snap.Encrypted {
		out.Encryption.KeyRef = ""
	}
	if strings.TrimSpace(job.TargetPath) != "" {
		out.Destination = agentshttp.RestoreDestination{Mode: "alternate", Path: job.TargetPath}
	} else if strings.HasPrefix(snap.Selector, "/") {
		// In-place restores name the original absolute path so the agent
		// has a concrete destination. Non-path selectors (volume names, db
		// names) stay empty and the agent refuses loudly.
		out.Destination = agentshttp.RestoreDestination{Mode: "original", Path: snap.Selector}
	}
	if snap.Encrypted && agentshttp.TransportSecure(ctx) {
		if b64, ok := p.keys.openDataKey(ctx, agentID, snap.KeyRef, "restore"); ok {
			out.Encryption.KeyB64 = b64
		}
	}
	return out, nil
}

// restoreObjects lists the snapshot's downloadable objects (manifest
// artifact first, then data objects) with bare hex sha256 digests.
func (p *agentDataPlane) restoreObjects(ctx context.Context, snap *domain.Snapshot) ([]agentshttp.RestoreObject, error) {
	manifest, err := p.snapshots.GetManifest(ctx, snap.ID)
	if err != nil {
		return nil, fmt.Errorf("jobproto: snapshot %s has no manifest: %w", snap.ID, err)
	}
	out := []agentshttp.RestoreObject{}
	if manifest != nil && strings.TrimSpace(manifest.ObjectKey) != "" {
		out = append(out, agentshttp.RestoreObject{
			Key: manifest.ObjectKey, SHA256: bareSHA256(manifest.Checksum),
		})
		objects, err := p.snapshots.ListObjects(ctx, manifest.ID)
		if err != nil {
			return nil, err
		}
		for _, o := range objects {
			out = append(out, agentshttp.RestoreObject{
				Key: o.ObjectKey, SizeBytes: o.Size, SHA256: bareSHA256(o.Checksum),
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("jobproto: snapshot %s catalogues no objects: %w", snap.ID, domain.ErrConflict)
	}
	return out, nil
}

// bareSHA256 strips the "algorithm:" prefix from a catalogued checksum,
// yielding the bare hex digest agents compare against.
func bareSHA256(sum string) string {
	if _, hex, ok := strings.Cut(sum, ":"); ok {
		return hex
	}
	return sum
}

// restoreKind maps a snapshot target type onto the restore kind vocabulary.
// Types without a counterpart keep their raw name so the agent rejects them
// explicitly instead of running the wrong procedure.
func restoreKind(t domain.TargetType) string {
	switch t {
	case domain.TargetHostFilesystem, domain.TargetDockerConfig:
		return string(restoreuc.KindFile)
	case domain.TargetPostgres, domain.TargetMySQL, domain.TargetMSSQL, domain.TargetRedis:
		return string(restoreuc.KindDatabase)
	case domain.TargetDockerVolume:
		return string(restoreuc.KindVolume)
	case domain.TargetAppStack:
		return string(restoreuc.KindStack)
	default:
		return string(t)
	}
}

// ownRestoreJob loads a restore job and proves it belongs to the agent.
func (p *agentDataPlane) ownRestoreJob(ctx context.Context, agentID, restoreID string) (*domain.RestoreJob, error) {
	job, err := p.restores.GetJob(ctx, restoreID)
	if err != nil {
		return nil, err
	}
	if job.AgentID != agentID {
		return nil, fmt.Errorf("jobproto: restore %s belongs to agent %s: %w", restoreID, job.AgentID, domain.ErrForbidden)
	}
	return job, nil
}

// IssueDownloadGrants mints one GET grant per key. Every key must belong to
// the restore's snapshot (manifest artifact or catalogued object): reading
// outside the snapshot is rejected.
func (p *agentDataPlane) IssueDownloadGrants(ctx context.Context, agentID, restoreID string, keys []string) ([]agentshttp.ObjectGrant, error) {
	job, err := p.ownRestoreJob(ctx, agentID, restoreID)
	if err != nil {
		return nil, err
	}
	snap, err := p.snapshots.GetSnapshot(ctx, job.SnapshotID)
	if err != nil {
		return nil, fmt.Errorf("jobproto: restore %s references an unavailable snapshot: %w", restoreID, err)
	}
	allowed, err := p.snapshotKeys(ctx, snap)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if !allowed[k] {
			return nil, fmt.Errorf("%w: object key %q is not part of snapshot %q", domain.ErrValidation, k, snap.ID)
		}
	}
	destID := snap.StorageID
	if strings.TrimSpace(destID) == "" {
		d, err := p.store.GetDefaultDestination(ctx)
		if err != nil {
			return nil, fmt.Errorf("jobproto: snapshot %s names no storage destination and no default is configured: %w", snap.ID, domain.ErrValidation)
		}
		destID = d.ID
	}
	out := make([]agentshttp.ObjectGrant, 0, len(keys))
	for _, k := range keys {
		g, err := p.storage.IssueDownloadGrant(ctx, destID, restoreID, k, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, agentshttp.ObjectGrant{Key: g.ObjectKey, URL: g.URL, Headers: g.Headers, ExpiresAt: g.ExpiresAt})
	}
	return out, nil
}

// snapshotKeys returns the set of downloadable keys for a snapshot: the
// manifest artifact plus every catalogued object.
func (p *agentDataPlane) snapshotKeys(ctx context.Context, snap *domain.Snapshot) (map[string]bool, error) {
	allowed := map[string]bool{}
	manifest, err := p.snapshots.GetManifest(ctx, snap.ID)
	if err != nil {
		return nil, fmt.Errorf("jobproto: snapshot %s has no manifest: %w", snap.ID, err)
	}
	if manifest != nil && strings.TrimSpace(manifest.ObjectKey) != "" {
		allowed[manifest.ObjectKey] = true
		objects, err := p.snapshots.ListObjects(ctx, manifest.ID)
		if err != nil {
			return nil, err
		}
		for _, o := range objects {
			allowed[o.ObjectKey] = true
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("jobproto: snapshot %s catalogues no objects: %w", snap.ID, domain.ErrConflict)
	}
	return allowed, nil
}

// restoreChain is the non-terminal lifecycle order shared by backup and
// restore jobs.
var restoreChain = []domain.JobState{
	domain.JobQueued, domain.JobAssigned, domain.JobRunning,
	domain.JobUploading, domain.JobVerifying,
}

// advanceRestoreState moves state toward target through legal steps. Agents
// report running/terminal states directly (never the intermediate upload
// steps), so the walk implies them; each step is still a legal transition,
// same-state reports are idempotent, and anything unparsable conflicts.
func advanceRestoreState(state *domain.JobState, target domain.JobState) error {
	for i := 0; i <= len(restoreChain); i++ {
		if *state == target {
			return nil
		}
		if state.CanTransitionTo(target) {
			return domain.TransitionTo(state, target)
		}
		idx := -1
		for n, s := range restoreChain {
			if s == *state {
				idx = n
				break
			}
		}
		if idx < 0 || idx+1 >= len(restoreChain) {
			break
		}
		if err := domain.TransitionTo(state, restoreChain[idx+1]); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: illegal job transition %s -> %s", domain.ErrConflict, *state, target)
}

// ReportRestore applies an agent execution report to a restore job. Reports
// on terminal jobs and illegal transitions conflict loudly; nothing is
// persisted for them.
func (p *agentDataPlane) ReportRestore(ctx context.Context, agentID, restoreID string, rep agentshttp.RestoreReport) error {
	job, err := p.ownRestoreJob(ctx, agentID, restoreID)
	if err != nil {
		return err
	}
	if job.State.Terminal() {
		return fmt.Errorf("jobproto: cannot report on terminal restore %s (%s): %w", restoreID, job.State, domain.ErrConflict)
	}
	if err := advanceRestoreState(&job.State, rep.State); err != nil {
		return fmt.Errorf("jobproto: restore %s: %w", restoreID, err)
	}
	now := p.now()
	if rep.State == domain.JobRunning && job.StartedAt == nil {
		job.StartedAt = &now
	}
	if rep.State.Terminal() && job.FinishedAt == nil {
		job.FinishedAt = &now
	}
	if rep.Error != "" {
		job.ErrorMessage = rep.Error
	}
	job.UpdatedAt = now
	return p.restores.UpdateJob(ctx, job)
}
