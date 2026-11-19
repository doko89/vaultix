// Package domain API key entity and repository port (apikey track).
//
// This file is stdlib-only per the project dependency rule.
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// API key scopes (PRD §7.1: programmatic access for users/CLI).
//   - ScopeRead: read-only calls (GETs).
//   - ScopeWrite: read + mutating calls (POST/PUT/PATCH/DELETE).
//   - ScopeAdmin: everything, including admin-only routes.
//
// The HTTP gate maps scopes onto roles (see auth.Gate): read→readonly,
// write→operator, admin→administrator.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

// ValidScope reports whether s is a known API key scope.
func ValidScope(s string) bool {
	switch s {
	case ScopeRead, ScopeWrite, ScopeAdmin:
		return true
	default:
		return false
	}
}

// NormalizeScopes lowercases, trims, dedupes, and validates scopes.
// Unknown scopes yield ErrValidation; the empty set is rejected.
func NormalizeScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !ValidScope(s) {
			return nil, fmt.Errorf("%w: unknown API key scope %q", ErrValidation, s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: at least one API key scope is required", ErrValidation)
	}
	return out, nil
}

// ApiKey is a user-owned programmatic credential. Clients present the
// full secret (`vxk_...`) once at issuance; only the SHA-256 KeyHash is
// persisted. KeyHash must never be serialized to APIs or logs; list and
// verify paths return metadata with the hash stripped.
type ApiKey struct {
	ID        string
	UserID    string
	Name      string
	KeyHash   []byte // raw SHA-256 of the presented secret; never serialized
	KeyPrefix string // first 8 chars of the presented secret, for display
	Scopes    []string

	ExpiresAt  *time.Time // nil = never expires
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// Validate checks API key invariants at the domain boundary.
func (k ApiKey) Validate() error {
	if strings.TrimSpace(k.UserID) == "" {
		return fmt.Errorf("%w: user_id is required", ErrValidation)
	}
	if strings.TrimSpace(k.Name) == "" {
		return fmt.Errorf("%w: API key name is required", ErrValidation)
	}
	if len(k.KeyHash) == 0 {
		return fmt.Errorf("%w: API key hash is required", ErrValidation)
	}
	if _, err := NormalizeScopes(k.Scopes); err != nil {
		return err
	}
	return nil
}

// Revoked reports whether the key was revoked.
func (k ApiKey) Revoked() bool { return k.RevokedAt != nil }

// Expired reports whether the key passed its expiry at now.
// Keys without expiry never expire.
func (k ApiKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)
}

// Active reports whether the key is usable at now.
func (k ApiKey) Active(now time.Time) bool {
	return !k.Revoked() && !k.Expired(now)
}

// HasScope reports whether the key carries scope s.
func (k ApiKey) HasScope(s string) bool {
	for _, have := range k.Scopes {
		if have == s {
			return true
		}
	}
	return false
}

// MayRead reports whether the key may perform read (GET) calls.
func (k ApiKey) MayRead() bool {
	return k.HasScope(ScopeRead) || k.HasScope(ScopeWrite) || k.HasScope(ScopeAdmin)
}

// MayWrite reports whether the key may perform mutating calls.
func (k ApiKey) MayWrite() bool {
	return k.HasScope(ScopeWrite) || k.HasScope(ScopeAdmin)
}

// MayAdmin reports whether the key may call admin-only routes.
func (k ApiKey) MayAdmin() bool { return k.HasScope(ScopeAdmin) }

// StripSecret zeroes the stored hash so the value is safe to return from
// list/verify paths and to hand to the transport layer.
func (k *ApiKey) StripSecret() {
	if k == nil {
		return
	}
	for i := range k.KeyHash {
		k.KeyHash[i] = 0
	}
	k.KeyHash = nil
}

// ApiKeyRepository persists user API keys. Implementations must use
// parameterized queries and must never log hashes or prefixes.
type ApiKeyRepository interface {
	Create(ctx context.Context, k *ApiKey) error
	// GetByID resolves a key for owner/admin checks (revocation).
	// Returns ErrNotFound when no key matches.
	GetByID(ctx context.Context, id string) (*ApiKey, error)
	// GetByHash resolves a key by raw SHA-256 hash for verification.
	// Returns ErrNotFound when no key matches.
	GetByHash(ctx context.Context, hash []byte) (*ApiKey, error)
	// ListByUser returns a user's keys (including revoked), newest first.
	ListByUser(ctx context.Context, userID string) ([]ApiKey, error)
	// Revoke stamps revoked_at; revoking an already-revoked key is a no-op.
	Revoke(ctx context.Context, id string, at time.Time) error
	// TouchLastUsed stamps last_used_at after successful verification.
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}
