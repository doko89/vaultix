package restore

import (
	"context"

	"github.com/vaultix/vaultix/internal/domain"
)

// JobReporter is the narrow seam restore uses to track restore jobs.
//
// INTEGRATION NOTE: the jobs track provides the real implementation backed
// by domain.RestoreJobRepository (Create/Get/Update). Restore code must not
// import adapter or jobs-track packages directly.
type JobReporter interface {
	CreateRestoreJob(ctx context.Context, job *domain.RestoreJob) error
	UpdateRestoreJob(ctx context.Context, job *domain.RestoreJob) error
	GetRestoreJob(ctx context.Context, id string) (*domain.RestoreJob, error)
}
