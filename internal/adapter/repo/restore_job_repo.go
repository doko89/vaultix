package repo

import (
	"context"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// RestoreJobRepo implements domain.RestoreJobRepository on restore_jobs.
//
// Mapping notes: restore_type derives from the request shape ("file" when
// Paths selects a subset, else "directory") because domain carries no
// restore type. CreatedBy maps to requested_by; ApprovedBy maps to the
// confirmation timestamp (a set ApprovedBy confirms on write; reads report
// the requester as approver once confirmed, since the schema stores no
// separate approver).
type RestoreJobRepo struct {
	baseRepo
}

// CreateJob enqueues a restore job. AgentID and SnapshotID are required;
// a non-empty ApprovedBy confirms immediately, atomically.
func (r *RestoreJobRepo) CreateJob(ctx context.Context, j *domain.RestoreJob) error {
	if j == nil {
		return validationError("job is required")
	}
	aid, err := mustParseUUID(j.AgentID, "agent id")
	if err != nil {
		return err
	}
	sid, err := mustParseUUID(j.SnapshotID, "snapshot id")
	if err != nil {
		return err
	}
	paths, err := encodeJSON(nonNilStrings(j.Paths))
	if err != nil {
		return err
	}
	params := gen.CreateRestoreJobParams{
		AgentID:         aid,
		SnapshotID:      sid,
		RestoreType:     restoreTypeFor(j),
		SourcePaths:     paths,
		DestinationPath: textOrNull(j.TargetPath),
		RequestedBy:     textOrNull(j.CreatedBy),
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if j.ApprovedBy != "" {
		err = r.inTx(ctx, func(q *gen.Queries) error {
			row, err := q.CreateRestoreJob(ctx, params)
			if err != nil {
				return err
			}
			if _, err := q.ConfirmRestoreJob(ctx, gen.ConfirmRestoreJobParams{ID: row.ID}); err != nil {
				return err
			}
			j.ID = uuidString(row.ID)
			j.State = domain.JobState(row.State)
			j.CreatedAt = timeOrZero(row.CreatedAt)
			j.UpdatedAt = timeOrZero(row.UpdatedAt)
			return nil
		})
		return mapErr(err)
	}
	row, err := r.q.CreateRestoreJob(ctx, params)
	if err != nil {
		return mapErr(err)
	}
	j.ID = uuidString(row.ID)
	j.State = domain.JobState(row.State)
	j.CreatedAt = timeOrZero(row.CreatedAt)
	j.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetJob returns one restore job by ID.
func (r *RestoreJobRepo) GetJob(ctx context.Context, id string) (*domain.RestoreJob, error) {
	jid, err := mustParseUUID(id, "job id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetRestoreJob(ctx, gen.GetRestoreJobParams{ID: jid})
	if err != nil {
		return nil, mapErr(err)
	}
	j, err := mapRestoreJob(row)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// UpdateJob rewrites a restore job's mutable execution state, confirming
// when ApprovedBy transitions from empty to set.
func (r *RestoreJobRepo) UpdateJob(ctx context.Context, j *domain.RestoreJob) error {
	if j == nil {
		return validationError("job is required")
	}
	jid, err := mustParseUUID(j.ID, "job id")
	if err != nil {
		return err
	}
	paths, err := encodeJSON(nonNilStrings(j.Paths))
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	current, err := r.q.GetRestoreJob(ctx, gen.GetRestoreJobParams{ID: jid})
	if err != nil {
		return mapErr(err)
	}
	params := gen.UpdateRestoreJobParams{
		ID:              jid,
		State:           string(j.State),
		ProgressPct:     progressToPct(j.Progress),
		ErrorCode:       textOrNull(j.ErrorCode),
		ErrorMessage:    textOrNull(j.ErrorMessage),
		DestinationPath: textOrNull(j.TargetPath),
		SourcePaths:     paths,
		StartedAt:       timestamptzPtr(j.StartedAt),
		FinishedAt:      timestamptzPtr(j.FinishedAt),
	}
	if j.ApprovedBy != "" && !current.ConfirmedAt.Valid {
		return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
			if _, err := q.UpdateRestoreJob(ctx, params); err != nil {
				return err
			}
			_, err := q.ConfirmRestoreJob(ctx, gen.ConfirmRestoreJobParams{ID: jid})
			return err
		}))
	}
	_, err = r.q.UpdateRestoreJob(ctx, params)
	return mapErr(err)
}

// ListJobs returns one page of an agent's restore jobs (all agents when
// agentID is empty).
func (r *RestoreJobRepo) ListJobs(ctx context.Context, opts domain.ListOptions, agentID string) (domain.ListResult[domain.RestoreJob], error) {
	aid, err := mustOptUUID(agentID, "agent id")
	if err != nil {
		return domain.ListResult[domain.RestoreJob]{}, err
	}
	limit, offset := page(opts)
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListRestoreJobsFiltered(ctx, gen.ListRestoreJobsFilteredParams{
		Column1: aid,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return domain.ListResult[domain.RestoreJob]{}, mapErr(err)
	}
	total, err := r.q.CountRestoreJobsFiltered(ctx, gen.CountRestoreJobsFilteredParams{Column1: aid})
	if err != nil {
		return domain.ListResult[domain.RestoreJob]{}, mapErr(err)
	}
	out := make([]domain.RestoreJob, 0, len(rows))
	for _, row := range rows {
		j, err := mapRestoreJob(row)
		if err != nil {
			return domain.ListResult[domain.RestoreJob]{}, err
		}
		out = append(out, j)
	}
	return domain.ListResult[domain.RestoreJob]{Items: out, Total: int(total)}, nil
}

// restoreTypeFor derives the schema restore type from the request shape.
func restoreTypeFor(j *domain.RestoreJob) string {
	if len(j.Paths) > 0 {
		return "file"
	}
	return "directory"
}

func mapRestoreJob(row gen.RestoreJob) (domain.RestoreJob, error) {
	var paths []string
	if len(row.SourcePaths) > 0 {
		if err := decodeJSON(row.SourcePaths, &paths); err != nil {
			return domain.RestoreJob{}, err
		}
	}
	approvedBy := ""
	if row.ConfirmedAt.Valid {
		approvedBy = textOrEmpty(row.RequestedBy)
	}
	return domain.RestoreJob{
		ID:           uuidString(row.ID),
		AgentID:      uuidString(row.AgentID),
		SnapshotID:   uuidString(row.SnapshotID),
		TargetPath:   textOrEmpty(row.DestinationPath),
		Paths:        paths,
		State:        domain.JobState(row.State),
		Progress:     pctToProgress(row.ProgressPct),
		ErrorCode:    textOrEmpty(row.ErrorCode),
		ErrorMessage: textOrEmpty(row.ErrorMessage),
		ApprovedBy:   approvedBy,
		StartedAt:    timePtr(row.StartedAt),
		FinishedAt:   timePtr(row.FinishedAt),
		CreatedBy:    textOrEmpty(row.RequestedBy),
		CreatedAt:    timeOrZero(row.CreatedAt),
		UpdatedAt:    timeOrZero(row.UpdatedAt),
	}, nil
}
