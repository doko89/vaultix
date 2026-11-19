package retention

import (
	"context"
	"errors"
)

// ObjectAccess is the narrow storage seam retention needs: object deletion
// for pruning condemned snapshots.
//
// INTEGRATION NOTE: the storage track provides the real implementation by
// wrapping its S3 adapter (domain.ObjectStorage.Delete). Retention code must
// not import adapter packages directly.
type ObjectAccess interface {
	// Delete removes one object from a destination.
	Delete(ctx context.Context, destinationID, objectKey string) error
}

// ErrLastRecoveryPoint refuses deletion of the only known valid recovery
// point without an explicitly confirmed override.
var ErrLastRecoveryPoint = errors.New("retention: refuses to delete the last known valid recovery point without an explicit override")
