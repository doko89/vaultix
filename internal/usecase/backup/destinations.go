package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// DefaultDestinationStore resolves the default storage destination. It is
// the narrow seam backup needs over domain.StorageRepository: completion
// must mirror the grants-side fallback ("empty means default") without
// importing the storage usecase or adapter packages.
type DefaultDestinationStore interface {
	GetDefaultDestination(ctx context.Context) (*domain.StorageDestination, error)
}

// resolveStorageID is the single completion-path resolution point (shared by
// CompleteJob and CompleteJobFlat via runCompletion), mirroring the
// grants-side destinationForJob fallback. Precedence: explicit job
// StorageID wins untouched; an empty StorageID resolves the default
// destination once; with no default configured the job fails with one
// actionable error naming the cause and the remediation.
//
// The resolved ID (never empty on success) must be stamped onto the job
// before manifest assembly so the snapshot record carries it and every
// verify HEAD targets the same destination the agent uploaded to.
func (s *Service) resolveStorageID(ctx context.Context, job *domain.BackupJob) (string, error) {
	if strings.TrimSpace(job.StorageID) != "" {
		return job.StorageID, nil
	}
	if s.destinations == nil {
		return "", noDefaultError(job.ID)
	}
	d, err := s.destinations.GetDefaultDestination(ctx)
	if err != nil || d == nil {
		return "", noDefaultError(job.ID)
	}
	s.log.Debug("backup completion: resolved default destination",
		"job_id", job.ID, "storage_id", d.ID)
	return d.ID, nil
}

// noDefaultError reports a completion that names no destination while no
// default is configured. The message is the whole operator contract: cause
// plus remediation, secret-free, with no raw store error text.
func noDefaultError(jobID string) error {
	return fmt.Errorf("job %s names no storage destination and no default is configured: "+
		"set storage_id on the policy or configure a default destination", jobID)
}
