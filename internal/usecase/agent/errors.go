package agent

import "errors"

// Structured boundary errors. Adapters map these to transport statuses:
// token/credential failures -> 401, revoked agents -> 403. Plaintext tokens
// never appear in error values.
var (
	// ErrInvalidToken indicates an unknown or wrong-kind token.
	ErrInvalidToken = errors.New("agent: invalid enrollment token")
	// ErrExpiredToken indicates the token passed its expiry.
	ErrExpiredToken = errors.New("agent: enrollment token expired")
	// ErrTokenReused indicates a single-use token was already redeemed.
	ErrTokenReused = errors.New("agent: enrollment token already redeemed")
	// ErrUnauthorized indicates missing or invalid agent credentials.
	ErrUnauthorized = errors.New("agent: unauthorized")
	// ErrAgentRevoked indicates the agent was revoked/disabled by an admin.
	ErrAgentRevoked = errors.New("agent: agent is revoked")
)
