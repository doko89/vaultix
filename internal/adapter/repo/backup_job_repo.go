package repo

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// BackupJobRepo implements domain.BackupJobRepository on backup_jobs.
//
// Mapping notes: trigger/storage/retry/creator fields have no columns and
// ride in target_resources as a JSON object (legacy "[]" decodes to empty
// extras). Progress/byte counters use 0 for unknown. PolicyVersionID
// resolves best-effort from the published version on create. SnapshotID is
// derived via reverse lookup (snapshots point at jobs, not vice versa);
// list calls pay one lookup per row.
type BackupJobRepo struct {
	baseRepo
}

// backupJobExtras is the target_resources JSON codec for fields without
// columns. It must stay an object; a bare array means "no extras".
type backupJobExtras struct {
	Trigger   string `json:"trigger,omitempty"`
	StorageID string `json:"storage_id,omitempty"`
	RetryOf   string `json:"retry_of,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

// CreateJob enqueues a backup job. The ID is database-generated and the
// state starts queued.
func (r *BackupJobRepo) CreateJob(ctx context.Context, j *domain.BackupJob) error {
	if j == nil {
		return validationError("job is required")
	}
	agentID, err := mustOptUUID(j.AgentID, "agent id")
	if err != nil {
		return err
	}
	policyID, err := mustOptUUID(j.PolicyID, "policy id")
	if err != nil {
		return err
	}
	scheduleID, err := mustOptUUID(j.ScheduleID, "schedule id")
	if err != nil {
		return err
	}
	extras, err := encodeBackupJobExtras(j)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	versionID, _ := r.lookupPolicyVersionID(ctx, policyID, j.PolicyVersion)
	row, err := r.q.CreateBackupJob(ctx, gen.CreateBackupJobParams{
		AgentID:                agentID,
		PolicyID:               policyID,
		PolicyVersion:          int4OrNull(j.PolicyVersion),
		PolicyVersionID:        versionID,
		ScheduleID:             scheduleID,
		TargetResources:        extras,
		EncryptionEnabled:      j.Encrypted,
		EncryptionKeyReference: j.KeyRef,
	})
	if err != nil {
		return mapErr(err)
	}
	j.ID = uuidString(row.ID)
	j.State = domain.JobState(row.State)
	j.Attempt = int(row.Attempts)
	j.CreatedAt = timeOrZero(row.CreatedAt)
	j.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetJob returns one job by ID, resolving its snapshot when registered.
func (r *BackupJobRepo) GetJob(ctx context.Context, id string) (*domain.BackupJob, error) {
	jid, err := mustParseUUID(id, "job id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetBackupJob(ctx, gen.GetBackupJobParams{ID: jid})
	if err != nil {
		return nil, mapErr(err)
	}
	j, err := mapBackupJob(row)
	if err != nil {
		return nil, err
	}
	j.SnapshotID = r.snapshotIDForJob(ctx, jid)
	return &j, nil
}

// UpdateJob rewrites a job's mutable execution state.
func (r *BackupJobRepo) UpdateJob(ctx context.Context, j *domain.BackupJob) error {
	if j == nil {
		return validationError("job is required")
	}
	jid, err := mustParseUUID(j.ID, "job id")
	if err != nil {
		return err
	}
	extras, err := encodeBackupJobExtras(j)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdateBackupJob(ctx, gen.UpdateBackupJobParams{
		ID:                     jid,
		State:                  string(j.State),
		ProgressPct:            progressToPct(j.Progress),
		BytesRead:              uint64OrZero(j.BytesRead),
		BytesCompressed:        uint64OrZero(j.BytesCompressed),
		BytesUploaded:          uint64OrZero(j.BytesUploaded),
		EncryptionEnabled:      j.Encrypted,
		EncryptionKeyReference: j.KeyRef,
		TargetResources:        extras,
		ErrorCode:              textOrNull(j.ErrorCode),
		ErrorMessage:           textOrNull(j.ErrorMessage),
		Attempts:               int32OrZero(j.Attempt),
		StartedAt:              timestamptzPtr(j.StartedAt),
		FinishedAt:             timestamptzPtr(j.FinishedAt),
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// ListJobs returns one page of jobs narrowed by the filter.
func (r *BackupJobRepo) ListJobs(ctx context.Context, opts domain.ListOptions, filter domain.JobFilter) (domain.ListResult[domain.BackupJob], error) {
	agentID, err := mustOptUUID(filter.AgentID, "agent id")
	if err != nil {
		return domain.ListResult[domain.BackupJob]{}, err
	}
	policyID, err := mustOptUUID(filter.PolicyID, "policy id")
	if err != nil {
		return domain.ListResult[domain.BackupJob]{}, err
	}
	limit, offset := page(opts)
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListBackupJobsFiltered(ctx, gen.ListBackupJobsFilteredParams{
		Column1: agentID,
		Column2: policyID,
		Column3: string(filter.State),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return domain.ListResult[domain.BackupJob]{}, mapErr(err)
	}
	total, err := r.q.CountBackupJobsFiltered(ctx, gen.CountBackupJobsFilteredParams{
		Column1: agentID,
		Column2: policyID,
		Column3: string(filter.State),
	})
	if err != nil {
		return domain.ListResult[domain.BackupJob]{}, mapErr(err)
	}
	out := make([]domain.BackupJob, 0, len(rows))
	for _, row := range rows {
		j, err := mapBackupJob(row)
		if err != nil {
			return domain.ListResult[domain.BackupJob]{}, err
		}
		j.SnapshotID = r.snapshotIDForJob(ctx, row.ID)
		out = append(out, j)
	}
	return domain.ListResult[domain.BackupJob]{Items: out, Total: int(total)}, nil
}

// ClaimNextJob atomically assigns the oldest queued job of an agent,
// returning (nil, nil) when the queue is empty.
func (r *BackupJobRepo) ClaimNextJob(ctx context.Context, agentID string) (*domain.BackupJob, error) {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.ClaimNextBackupJob(ctx, gen.ClaimNextBackupJobParams{AgentID: aid})
	if err != nil {
		mapped := mapErr(err)
		if errors.Is(mapped, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, mapped
	}
	j, err := mapBackupJob(row)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// lookupPolicyVersionID resolves the version row best-effort; a missing
// version stores NULL rather than failing job creation.
func (r *BackupJobRepo) lookupPolicyVersionID(ctx context.Context, policyID pgtype.UUID, version int) (pgtype.UUID, error) {
	if !policyID.Valid || version <= 0 {
		return pgtype.UUID{}, nil
	}
	row, err := r.q.GetPolicyVersion(ctx, gen.GetPolicyVersionParams{
		PolicyID: policyID,
		Version:  int32OrZero(version),
	})
	if err != nil {
		return pgtype.UUID{}, nil
	}
	return row.ID, nil
}

// snapshotIDForJob reverse-resolves the snapshot registered for a job;
// missing snapshots resolve to "".
func (r *BackupJobRepo) snapshotIDForJob(ctx context.Context, jobID pgtype.UUID) string {
	row, err := r.q.GetSnapshotByJob(ctx, gen.GetSnapshotByJobParams{BackupJobID: jobID})
	if err != nil {
		return ""
	}
	return uuidString(row.ID)
}

func mapBackupJob(row gen.BackupJob) (domain.BackupJob, error) {
	extras, err := decodeBackupJobExtras(row.TargetResources)
	if err != nil {
		return domain.BackupJob{}, err
	}
	return domain.BackupJob{
		ID:              uuidString(row.ID),
		AgentID:         uuidString(row.AgentID),
		PolicyID:        uuidString(row.PolicyID),
		PolicyVersion:   intOrZero(row.PolicyVersion),
		ScheduleID:      uuidString(row.ScheduleID),
		Trigger:         domain.JobTrigger(extras.Trigger),
		State:           domain.JobState(row.State),
		Progress:        pctToProgress(row.ProgressPct),
		BytesRead:       uint64Ptr(row.BytesRead),
		BytesCompressed: uint64Ptr(row.BytesCompressed),
		BytesUploaded:   uint64Ptr(row.BytesUploaded),
		Encrypted:       row.EncryptionEnabled,
		KeyRef:          row.EncryptionKeyReference,
		StorageID:       extras.StorageID,
		ErrorCode:       textOrEmpty(row.ErrorCode),
		ErrorMessage:    textOrEmpty(row.ErrorMessage),
		RetryOf:         extras.RetryOf,
		Attempt:         int(row.Attempts),
		StartedAt:       timePtr(row.StartedAt),
		FinishedAt:      timePtr(row.FinishedAt),
		CreatedBy:       extras.CreatedBy,
		CreatedAt:       timeOrZero(row.CreatedAt),
		UpdatedAt:       timeOrZero(row.UpdatedAt),
	}, nil
}

func encodeBackupJobExtras(j *domain.BackupJob) ([]byte, error) {
	extras := backupJobExtras{
		Trigger:   string(j.Trigger),
		StorageID: j.StorageID,
		RetryOf:   j.RetryOf,
		CreatedBy: j.CreatedBy,
	}
	if extras == (backupJobExtras{}) {
		return []byte("[]"), nil
	}
	return encodeJSON(extras)
}

func decodeBackupJobExtras(data []byte) (backupJobExtras, error) {
	var extras backupJobExtras
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null")) {
		return extras, nil
	}
	if err := decodeJSON(data, &extras); err != nil {
		return backupJobExtras{}, err
	}
	return extras, nil
}
