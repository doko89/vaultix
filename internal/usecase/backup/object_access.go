package backup

import (
	"context"

	"github.com/vaultix/vaultix/internal/domain"
)

// ObjectAccess is the narrow storage seam backup needs: integrity metadata
// reads for the completion verification gate.
//
// INTEGRATION NOTE: the storage track provides the real implementation by
// wrapping its S3 adapter (domain.ObjectStorage.Head). Backup code must not
// import adapter packages directly.
type ObjectAccess interface {
	// Head returns stored size/checksum for one object.
	Head(ctx context.Context, destinationID, objectKey string) (domain.ObjectMeta, error)
}
