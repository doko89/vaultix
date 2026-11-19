package domain

import "errors"

// Sentinel errors returned by domain helpers and port implementations.
// Adapters map persistence/transport failures onto these where applicable.
var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = errors.New("domain: not found")
	// ErrConflict indicates a uniqueness or state conflict (e.g. duplicate
	// name, illegal state transition).
	ErrConflict = errors.New("domain: conflict")
	// ErrValidation indicates entity validation failed; details are wrapped.
	ErrValidation = errors.New("domain: validation failed")
	// ErrForbidden indicates the actor lacks permission for the operation.
	ErrForbidden = errors.New("domain: forbidden")
)
