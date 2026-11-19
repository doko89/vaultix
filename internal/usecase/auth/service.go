package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/vaultix/vaultix/internal/domain"
)

// Service implements local + OIDC authentication, session issuance, and
// provider/identity administration.
type Service struct {
	users     domain.UserRepository
	providers domain.OIDCProviderRepository
	sessions  domain.SessionStore
	oidc      domain.OIDCClient
	tester    ProviderTester
	secrets   ProviderSecretStore
	box       SecretBox
	hash      Hasher
	audit     Auditor
	clock     domain.Clock
	cfg       Config

	mu      sync.Mutex
	pending map[string]pendingLogin
}

// NewService wires a Service. Users, Providers, Sessions, OIDC, Secrets,
// Box, Hash, and Clock are required; Audit may be nil (tests only); Tester
// may be nil (TestProvider then reports unavailability).
func NewService(d Deps) (*Service, error) {
	if d.Users == nil || d.Providers == nil || d.Sessions == nil || d.OIDC == nil ||
		d.Secrets == nil || d.Box == nil || d.Hash == nil {
		return nil, errors.New("auth: missing required dependencies")
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock{}
	}
	return &Service{
		users: d.Users, providers: d.Providers, sessions: d.Sessions,
		oidc: d.OIDC, tester: d.Tester, secrets: d.Secrets, box: d.Box,
		hash: d.Hash, audit: d.Audit, clock: d.Clock,
		cfg: d.Config.withDefaults(), pending: map[string]pendingLogin{},
	}, nil
}

// LocalLoginEnabled reports whether password login is currently allowed.
func (s *Service) LocalLoginEnabled() bool { return *s.cfg.LocalLoginEnabled }

// SetLocalLoginEnabled flips the global local-login switch. Disabling
// requires explicit confirmation and refuses when no enabled OIDC provider
// exists (that would brick every login path, PRD §14).
func (s *Service) SetLocalLoginEnabled(ctx context.Context, enabled, confirmed bool, actorID string) error {
	if !enabled {
		if !confirmed {
			return fmt.Errorf("%w: confirmation required", ErrNoBreakGlass)
		}
		provs, err := s.providers.ListProviders(ctx)
		if err != nil {
			return fmt.Errorf("auth: list providers: %w", err)
		}
		anyEnabled := false
		for _, p := range provs {
			if p.Enabled {
				anyEnabled = true
				break
			}
		}
		if !anyEnabled {
			return fmt.Errorf("%w: no enabled OIDC provider", ErrNoBreakGlass)
		}
	}
	*s.cfg.LocalLoginEnabled = enabled
	return s.record(ctx, AuditEvent{
		Action: "auth.local_login", Actor: actorID, Result: "success",
		Detail: map[bool]string{true: "enabled", false: "disabled"}[enabled],
	})
}

// Login authenticates local credentials and issues a session. Lookup order
// (verify-before-disabled-check, dummy hash for unknown users) avoids
// account enumeration via error messages or timing.
func (s *Service) Login(ctx context.Context, identifier, password, ip, userAgent string) (*LoginResult, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	if !s.LocalLoginEnabled() {
		_ = s.record(ctx, AuditEvent{Action: "auth.login", Actor: identifier, IP: ip, Result: "denied", Detail: "local_login_disabled"})
		return nil, ErrLocalLoginDisabled
	}
	user, err := s.findLocalUser(ctx, identifier)
	switch {
	case err == nil:
		if verr := s.hash.Verify(user.PasswordHash, password); verr != nil {
			_ = s.record(ctx, AuditEvent{Action: "auth.login", Actor: identifier, Target: user.ID, IP: ip, Result: "failed", Detail: "bad_password"})
			return nil, ErrInvalidCredentials
		}
	case errors.Is(err, domain.ErrNotFound):
		dummyVerify()
		_ = s.record(ctx, AuditEvent{Action: "auth.login", Actor: identifier, IP: ip, Result: "failed", Detail: "bad_password"})
		return nil, ErrInvalidCredentials
	default:
		return nil, fmt.Errorf("auth: user lookup: %w", err)
	}
	if user.Disabled || user.LocalLoginDisabled {
		_ = s.record(ctx, AuditEvent{Action: "auth.login", Actor: user.Username, Target: user.ID, IP: ip, Result: "denied", Detail: "account_disabled"})
		return nil, ErrAccountDisabled
	}
	roles, err := s.users.ListUserRoles(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: list roles: %w", err)
	}
	if len(roles) == 0 {
		_ = s.record(ctx, AuditEvent{Action: "auth.login", Actor: user.Username, Target: user.ID, IP: ip, Result: "denied", Detail: "no_roles"})
		return nil, ErrNoRolesAssigned
	}
	sess, token, err := s.issueSession(ctx, user.ID, "", ip, userAgent)
	if err != nil {
		return nil, err
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.login", Actor: user.Username, Target: user.ID, IP: ip, Result: "success"})
	return &LoginResult{Token: token, Session: sess, User: user, Roles: roles}, nil
}

// findLocalUser resolves a login identifier: username first, then an
// email fallback via listing (the users table is keyed by email; the repo
// track maps User.Username — see INTEGRATION NOTES).
func (s *Service) findLocalUser(ctx context.Context, identifier string) (*domain.User, error) {
	if u, err := s.users.GetUserByUsername(ctx, identifier); err == nil {
		return u, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if !strings.Contains(identifier, "@") {
		return nil, domain.ErrNotFound
	}
	res, err := s.users.ListUsers(ctx, domain.ListOptions{Limit: 200, Query: identifier})
	if err != nil {
		return nil, err
	}
	for _, u := range res.Items {
		if strings.EqualFold(u.Email, identifier) {
			u := u
			return &u, nil
		}
	}
	return nil, domain.ErrNotFound
}

// ValidateSession resolves a presented token to its session, user, and
// roles, enforcing absolute + idle expiry. Valid sessions are touched
// lazily (TouchThreshold) to bound store writes.
func (s *Service) ValidateSession(ctx context.Context, token string) (*SessionInfo, error) {
	if !validTokenShape(token) {
		return nil, ErrInvalidCredentials
	}
	now := s.clock.Now()
	sess, err := s.sessions.GetByTokenHash(ctx, HashSessionToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth: session lookup: %w", err)
	}
	if sess.Expired(now) || sess.IdleExpired(now, s.cfg.SessionIdleTimeout) {
		_ = s.sessions.Delete(ctx, sess.ID) // best-effort prune
		return nil, ErrInvalidCredentials
	}
	if now.Sub(sess.LastSeenAt) >= s.cfg.TouchThreshold {
		if err := s.sessions.Touch(ctx, sess.ID, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("auth: session touch: %w", err)
		}
		sess.LastSeenAt = now
	}
	user, err := s.users.GetUser(ctx, sess.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth: session user: %w", err)
	}
	if user.Disabled {
		_ = s.sessions.Delete(ctx, sess.ID)
		return nil, ErrAccountDisabled
	}
	roles, err := s.users.ListUserRoles(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: session roles: %w", err)
	}
	return &SessionInfo{Session: sess, User: user, Roles: roles}, nil
}

// Logout ends a Manager session; when the session came from OIDC and the
// provider publishes an end_session_endpoint, it returns the RP-initiated
// logout URL the UI should redirect to afterwards.
func (s *Service) Logout(ctx context.Context, token, postLogoutRedirect string) (string, error) {
	if !validTokenShape(token) {
		return "", nil // unknown token: nothing to do, stay silent
	}
	sess, err := s.sessions.GetByTokenHash(ctx, HashSessionToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("auth: session lookup: %w", err)
	}
	if err := s.sessions.Delete(ctx, sess.ID); err != nil {
		return "", fmt.Errorf("auth: session delete: %w", err)
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.logout", Actor: sess.UserID, Target: sess.ID, IP: sess.IP, Result: "success"})
	if sess.OIDCProviderID == "" {
		return "", nil
	}
	prov, err := s.providers.GetProvider(ctx, sess.OIDCProviderID)
	if err != nil {
		return "", nil // provider gone: local logout already done
	}
	return s.oidc.LogoutURL(prov, postLogoutRedirect), nil
}

// RevokeUserSessions ends sessions for a user (password change, disable).
func (s *Service) RevokeUserSessions(ctx context.Context, userID, exceptSessionID string) error {
	return s.sessions.DeleteUserSessions(ctx, userID, exceptSessionID)
}

// issueSession mints a token, persists its hash, and returns both.
func (s *Service) issueSession(ctx context.Context, userID, oidcProviderID, ip, userAgent string) (*domain.Session, string, error) {
	token, err := GenerateSessionToken()
	if err != nil {
		return nil, "", err
	}
	now := s.clock.Now()
	sess := &domain.Session{
		UserID: userID, TokenHash: HashSessionToken(token),
		OIDCProviderID: oidcProviderID, IP: ip, UserAgent: userAgent,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.SessionAbsoluteTTL), LastSeenAt: now,
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, "", fmt.Errorf("auth: session create: %w", err)
	}
	return sess, token, nil
}

func (s *Service) record(ctx context.Context, e AuditEvent) error {
	if s.audit == nil {
		return nil
	}
	e.At = s.clock.Now()
	if err := s.audit.RecordAuthEvent(ctx, e); err != nil {
		return fmt.Errorf("auth: audit: %w", err)
	}
	return nil
}
