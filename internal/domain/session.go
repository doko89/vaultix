// Package domain session entity and store port (auth track).
//
// This file is stdlib-only per the project dependency rule.
package domain

import (
	"context"
	"time"
)

// Session lifetimes (PRD §7.1.1: equal for local and OIDC sessions).
// PRD open decision §16.12 leaves exact values to implementation;
// these defaults can be overridden by usecase/auth Config.
const (
	DefaultSessionAbsoluteTTL = 12 * time.Hour
	DefaultSessionIdleTimeout = 30 * time.Minute
)

// Session is a server-side login session issued after local or OIDC
// authentication. The opaque token presented by the client is never stored;
// only its SHA-256 hash (TokenHash) is persisted.
type Session struct {
	ID        string
	UserID    string
	TokenHash string // hex-encoded SHA-256 of the opaque token

	// OIDCProviderID is empty for local sessions; otherwise the provider
	// that issued the login (used for RP-initiated logout).
	OIDCProviderID string
	// IDToken is kept server-side only as the RP-initiated logout hint
	// (id_token_hint). It must never be logged or returned by APIs.
	IDToken string

	IP        string
	UserAgent string

	CreatedAt  time.Time
	ExpiresAt  time.Time // absolute expiry
	LastSeenAt time.Time // last activity (idle timeout basis)
}

// Expired reports whether the absolute lifetime has passed.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// IdleExpired reports whether the session idled past the timeout.
func (s Session) IdleExpired(now time.Time, idleTimeout time.Duration) bool {
	if idleTimeout <= 0 {
		return false
	}
	return now.Sub(s.LastSeenAt) >= idleTimeout
}

// Valid reports whether the session is usable at now.
func (s Session) Valid(now time.Time, idleTimeout time.Duration) bool {
	return !s.Expired(now) && !s.IdleExpired(now, idleTimeout)
}

// SessionStore persists sessions. Implementations must use parameterized
// queries and must never log token hashes or ID tokens.
type SessionStore interface {
	Create(ctx context.Context, s *Session) error
	// GetByTokenHash looks up a session by hex SHA-256 token hash.
	// Returns ErrNotFound when no session matches.
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	// Touch updates LastSeenAt for idle-timeout tracking.
	Touch(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
	// DeleteUserSessions revokes all of a user's sessions, optionally
	// keeping exceptID (e.g. "log out everywhere else").
	DeleteUserSessions(ctx context.Context, userID, exceptID string) error
	// DeleteExpired removes sessions past absolute expiry; returns the
	// number of rows removed (for the janitor job).
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
