package restore

import "errors"

// Sentinel errors for restore planning and confirmation.
var (
	// ErrNotRestorable refuses restores from snapshots that never verified.
	ErrNotRestorable = errors.New("restore: snapshot is not restorable (manifest and objects must verify first)")
	// ErrTokenInvalid rejects unknown confirmation tokens.
	ErrTokenInvalid = errors.New("restore: invalid confirmation token")
	// ErrTokenExpired rejects consumed-after-expiry confirmation tokens.
	ErrTokenExpired = errors.New("restore: confirmation token expired")
	// ErrTokenReused rejects second use of a single-use confirmation token.
	ErrTokenReused = errors.New("restore: confirmation token already used")
	// ErrOrderRequired rejects stack restores without an explicit order.
	ErrOrderRequired = errors.New("restore: stack restore requires an explicit restore order")
	// ErrPlanExpired rejects confirmation of an expired plan.
	ErrPlanExpired = errors.New("restore: restore plan expired")
	// ErrPlanNotFound rejects confirmation of an unknown plan.
	ErrPlanNotFound = errors.New("restore: restore plan not found")
)
