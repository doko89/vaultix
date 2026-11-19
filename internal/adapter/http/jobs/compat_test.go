package jobs_test

import (
	"github.com/vaultix/vaultix/internal/adapter/http/jobs"
	"github.com/vaultix/vaultix/internal/usecase/job"
)

// The job Service must always satisfy the handler contract; wiring passes
// *job.Service straight into jobs.Mount.
var _ jobs.Service = (*job.Service)(nil)
