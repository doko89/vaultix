package apikey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

// Secrets (full `vxk_...` keys) must never appear in errors, audit
// details, or logs produced here; only key IDs and prefixes are recorded.
//
// Sentinel errors. Verify failures stay generic toward callers (no key
// enumeration via messages); details go to audit events, never to callers.
var (
	// ErrInvalidKey is returned for malformed or unknown secrets.
	ErrInvalidKey = errors.New("apikey: invalid API key")
	// ErrKeyRevoked is returned when the key was revoked.
	ErrKeyRevoked = errors.New("apikey: API key revoked")
	// ErrKeyExpired is returned when the key passed its expiry.
	ErrKeyExpired = errors.New("apikey: API key expired")
)

// TTL bounds for issuance: TTL 0 means "never expires"; a positive TTL
// must lie within [MinTTL, MaxTTL].
const (
	MinTTL = time.Minute
	MaxTTL = 365 * 24 * time.Hour
)

// MaxNameLength caps key names at the service boundary.
const MaxNameLength = 128

// Auditor records API key security events. The method set matches
// usecase/auth.Auditor so one implementation serves both (same pattern as
// the HTTP auth package).
type Auditor interface {
	RecordAuthEvent(ctx context.Context, e authsvc.AuditEvent) error
}

// Deps wires the Service. Keys and Users are required; Audit may be nil
// (tests only — production must audit); Clock may be nil (system clock).
type Deps struct {
	Keys  domain.ApiKeyRepository
	Users domain.UserRepository
	Audit Auditor
	Clock domain.Clock
}

// Service issues, verifies, lists, and revokes user API keys.
type Service struct {
	keys  domain.ApiKeyRepository
	users domain.UserRepository
	audit Auditor
	clock domain.Clock
}

// NewService wires a Service.
func NewService(d Deps) (*Service, error) {
	if d.Keys == nil || d.Users == nil {
		return nil, errors.New("apikey: missing required dependencies")
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock{}
	}
	return &Service{keys: d.Keys, users: d.Users, audit: d.Audit, clock: d.Clock}, nil
}

// IssuedKey is the one-time issuance view: Secret is the full `vxk_...`
// key, returned exactly once. Key carries metadata with the hash stripped.
type IssuedKey struct {
	Key    *domain.ApiKey
	Secret string
}

// Issue creates a key for userID. ttl==0 means never expires. The admin
// scope requires the user to hold the administrator role.
func (s *Service) Issue(ctx context.Context, userID, name string, scopes []string, ttl time.Duration) (*IssuedKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: API key name is required", domain.ErrValidation)
	}
	if len(name) > MaxNameLength {
		return nil, fmt.Errorf("%w: API key name is too long", domain.ErrValidation)
	}
	normalized, err := domain.NormalizeScopes(scopes)
	if err != nil {
		return nil, err
	}
	if ttl < 0 || (ttl > 0 && (ttl < MinTTL || ttl > MaxTTL)) {
		return nil, fmt.Errorf("%w: ttl must be 0 (no expiry) or within 1m..8760h", domain.ErrValidation)
	}
	user, err := s.users.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("apikey: user lookup: %w", err)
	}
	if !user.Active() {
		return nil, fmt.Errorf("apikey: issue for disabled account: %w", domain.ErrForbidden)
	}
	for _, sc := range normalized {
		if sc == domain.ScopeAdmin && !s.isAdmin(ctx, userID) {
			_ = s.record(ctx, authsvc.AuditEvent{
				Action: "apikey.issue", Actor: userID, Result: "denied",
				Detail: "admin_scope_requires_admin",
			})
			return nil, fmt.Errorf("apikey: admin scope requires administrator role: %w", domain.ErrForbidden)
		}
	}
	secret, err := GenerateSecret()
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	key := &domain.ApiKey{
		UserID: userID, Name: name,
		KeyHash: HashSecret(secret), KeyPrefix: DisplayPrefix(secret),
		Scopes: normalized, CreatedAt: now,
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		key.ExpiresAt = &exp
	}
	if err := s.keys.Create(ctx, key); err != nil {
		return nil, fmt.Errorf("apikey: store: %w", err)
	}
	if err := s.record(ctx, authsvc.AuditEvent{
		Action: "apikey.issue", Actor: userID, Target: key.ID,
		Result: "success", Detail: "prefix=" + key.KeyPrefix,
	}); err != nil {
		return nil, err
	}
	out := *key
	out.StripSecret()
	return &IssuedKey{Key: &out, Secret: secret}, nil
}

// VerifiedKey is the verified identity of an API-key bearer: metadata
// (hash stripped) plus the owning user.
type VerifiedKey struct {
	Key  *domain.ApiKey
	User *domain.User
}

// Verify resolves a presented secret to its key and owner. Unknown,
// revoked, and expired secrets yield typed errors; every failure is
// audited (best-effort — authentication must not fail on audit outage).
// Successful verification touches last_used_at.
func (s *Service) Verify(ctx context.Context, secret string) (*VerifiedKey, error) {
	if !ValidSecretShape(secret) {
		_ = s.record(ctx, authsvc.AuditEvent{
			Action: "apikey.verify", Actor: "anonymous",
			Result: "failed", Detail: "malformed",
		})
		return nil, ErrInvalidKey
	}
	key, err := s.keys.GetByHash(ctx, HashSecret(secret))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = s.record(ctx, authsvc.AuditEvent{
				Action: "apikey.verify", Actor: "anonymous",
				Result: "failed", Detail: "unknown_key",
			})
			return nil, ErrInvalidKey
		}
		return nil, fmt.Errorf("apikey: key lookup: %w", err)
	}
	now := s.clock.Now()
	switch {
	case key.Revoked():
		_ = s.record(ctx, authsvc.AuditEvent{
			Action: "apikey.verify", Actor: key.UserID, Target: key.ID,
			Result: "denied", Detail: "revoked",
		})
		return nil, ErrKeyRevoked
	case key.Expired(now):
		_ = s.record(ctx, authsvc.AuditEvent{
			Action: "apikey.verify", Actor: key.UserID, Target: key.ID,
			Result: "denied", Detail: "expired",
		})
		return nil, ErrKeyExpired
	}
	user, err := s.users.GetUser(ctx, key.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = s.record(ctx, authsvc.AuditEvent{
				Action: "apikey.verify", Actor: key.UserID, Target: key.ID,
				Result: "denied", Detail: "owner_missing",
			})
			return nil, ErrInvalidKey
		}
		return nil, fmt.Errorf("apikey: owner lookup: %w", err)
	}
	if !user.Active() {
		_ = s.record(ctx, authsvc.AuditEvent{
			Action: "apikey.verify", Actor: key.UserID, Target: key.ID,
			Result: "denied", Detail: "owner_disabled",
		})
		return nil, ErrInvalidKey
	}
	if err := s.keys.TouchLastUsed(ctx, key.ID, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("apikey: touch: %w", err)
	}
	key.LastUsedAt = &now
	out := *key
	out.StripSecret()
	sanitized := *user
	sanitized.PasswordHash = ""
	return &VerifiedKey{Key: &out, User: &sanitized}, nil
}

// List returns a user's keys, newest first, metadata only (hashes always
// stripped — the secret is unrecoverable after issuance).
func (s *Service) List(ctx context.Context, userID string) ([]domain.ApiKey, error) {
	keys, err := s.keys.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("apikey: list: %w", err)
	}
	for i := range keys {
		keys[i].StripSecret()
	}
	return keys, nil
}

// Revoke ends a key. The owner or any administrator may revoke; anyone
// else gets domain.ErrForbidden. Unknown keys yield domain.ErrNotFound.
// Revoking an already-revoked key succeeds (idempotent).
func (s *Service) Revoke(ctx context.Context, actorID, keyID string) error {
	key, err := s.keys.GetByID(ctx, keyID)
	if err != nil {
		return err
	}
	if key.UserID != actorID && !s.isAdmin(ctx, actorID) {
		_ = s.record(ctx, authsvc.AuditEvent{
			Action: "apikey.revoke", Actor: actorID, Target: keyID,
			Result: "denied", Detail: "not_owner",
		})
		return fmt.Errorf("apikey: revoke requires ownership or administrator role: %w", domain.ErrForbidden)
	}
	if err := s.keys.Revoke(ctx, keyID, s.clock.Now()); err != nil {
		return fmt.Errorf("apikey: revoke: %w", err)
	}
	if err := s.record(ctx, authsvc.AuditEvent{
		Action: "apikey.revoke", Actor: actorID, Target: keyID,
		Result: "success",
	}); err != nil {
		return err
	}
	return nil
}

// isAdmin reports whether userID holds the administrator role. Lookup
// failures fail closed (false) — callers deny on false.
func (s *Service) isAdmin(ctx context.Context, userID string) bool {
	roles, err := s.users.ListUserRoles(ctx, userID)
	if err != nil {
		return false
	}
	for _, r := range roles {
		if r.Name == domain.RoleAdministrator {
			return true
		}
	}
	return false
}

func (s *Service) record(ctx context.Context, e authsvc.AuditEvent) error {
	if s.audit == nil {
		return nil
	}
	e.At = s.clock.Now()
	if err := s.audit.RecordAuthEvent(ctx, e); err != nil {
		return fmt.Errorf("apikey: audit: %w", err)
	}
	return nil
}
