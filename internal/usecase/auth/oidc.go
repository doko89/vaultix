package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// oidc.go: OIDC login orchestration (PRD §7.1.1) — start/callback, JIT
// provisioning, claim refresh, and pre-provisioned-only mode.

// StartOIDCLogin begins an Authorization Code + PKCE login: it discovers the
// provider, mints single-use state/nonce/verifier, and returns the IdP URL.
//
// NOTE: pending logins live in process memory. Multi-instance deployments
// need sticky routing on the callback or a shared pending store (see
// INTEGRATION NOTES).
func (s *Service) StartOIDCLogin(ctx context.Context, providerID, redirectURI string) (*OIDCStart, error) {
	if strings.TrimSpace(redirectURI) == "" {
		return nil, errors.New("auth: redirect_uri is required")
	}
	prov, err := s.providers.GetProvider(ctx, providerID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrProviderDisabled
		}
		return nil, fmt.Errorf("auth: provider lookup: %w", err)
	}
	if !prov.Enabled {
		return nil, ErrProviderDisabled
	}
	// Fail fast when discovery is broken so the UI shows an actionable
	// error instead of redirecting into a doomed flow. Exchange also
	// discovers internally, so the callback stays safe regardless.
	if disc, ok := s.oidc.(ProviderDiscoverer); ok {
		if err := disc.DiscoverProvider(ctx, prov); err != nil {
			_ = s.record(ctx, AuditEvent{Action: "auth.oidc.start", Target: prov.ID, Result: "error", Detail: "discovery_failed"})
			return nil, fmt.Errorf("auth: OIDC discovery: %w", err)
		}
	}
	state, err := GenerateState()
	if err != nil {
		return nil, err
	}
	nonce, err := GenerateNonce()
	if err != nil {
		return nil, err
	}
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	authURL, err := s.oidc.AuthURL(prov, state, nonce, challenge, redirectURI)
	if err != nil {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.start", Target: prov.ID, Result: "error", Detail: "auth_url_failed"})
		return nil, fmt.Errorf("auth: OIDC start: %w", err)
	}
	s.mu.Lock()
	s.purgePendingLocked()
	s.pending[state] = pendingLogin{
		providerID: prov.ID, state: state, nonce: nonce,
		verifier: verifier, redirectURI: redirectURI,
		expiresAt: s.clock.Now().Add(s.cfg.PendingLoginTTL),
	}
	s.mu.Unlock()
	return &OIDCStart{AuthURL: authURL, State: state}, nil
}

// CompleteOIDCLogin validates the callback and issues a Manager session.
// Claim refresh runs on every login: group mapping is recomputed and the
// user's roles are replaced, so IdP removals take effect at next login.
func (s *Service) CompleteOIDCLogin(ctx context.Context, providerID, state, code, redirectURI, ip, userAgent string) (*LoginResult, error) {
	pend, ok := s.popPending(state)
	if !ok {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.callback", Target: providerID, IP: ip, Result: "denied", Detail: "bad_state"})
		return nil, ErrInvalidState
	}
	if pend.providerID != providerID || !pend.validRedirect(redirectURI) {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.callback", Target: providerID, IP: ip, Result: "denied", Detail: "state_mismatch"})
		return nil, ErrInvalidState
	}
	prov, err := s.providers.GetProvider(ctx, pend.providerID)
	if err != nil || !prov.Enabled {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.callback", Target: pend.providerID, IP: ip, Result: "denied", Detail: "provider_disabled"})
		return nil, ErrProviderDisabled
	}
	claims, err := s.oidc.Exchange(ctx, prov, code, pend.verifier, redirectURI, pend.nonce)
	if err != nil {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.callback", Target: prov.ID, IP: ip, Result: "failed", Detail: "exchange_failed"})
		return nil, fmt.Errorf("auth: OIDC exchange: %w", err)
	}
	user, ident, err := s.resolveIdentity(ctx, prov, claims)
	if err != nil {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.login", Actor: claims.Subject, Target: prov.ID, IP: ip, Result: "denied", Detail: denyDetail(err)})
		return nil, err
	}
	roleName, ok := prov.RoleForGroups(claims.Groups)
	if !ok {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.login", Actor: user.Username, Target: user.ID, IP: ip, Result: "denied", Detail: "no_mapped_role"})
		return nil, ErrNoMappedRole
	}
	role, err := s.users.GetRoleByName(ctx, roleName)
	if err != nil {
		_ = s.record(ctx, AuditEvent{Action: "auth.oidc.login", Actor: user.Username, Target: user.ID, IP: ip, Result: "error", Detail: "role_unresolved"})
		return nil, fmt.Errorf("auth: mapped role %q is not provisioned", roleName)
	}
	if err := s.users.ReplaceUserRoles(ctx, user.ID, []string{role.ID}); err != nil {
		return nil, fmt.Errorf("auth: role refresh: %w", err)
	}
	now := s.clock.Now()
	ident.Email = claims.Email
	ident.LastLogin = &now
	if err := s.providers.UpdateIdentity(ctx, ident); err != nil {
		return nil, fmt.Errorf("auth: identity touch: %w", err)
	}
	sess, token, err := s.issueSession(ctx, user.ID, prov.ID, ip, userAgent)
	if err != nil {
		return nil, err
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.oidc.login", Actor: user.Username, Target: user.ID, IP: ip, Result: "success", Detail: "provider:" + prov.Name})
	return &LoginResult{Token: token, Session: sess, User: user, Roles: []domain.Role{*role}, OIDCProviderID: prov.ID}, nil
}

// resolveIdentity maps (issuer, sub) to a user, JIT-provisioning when the
// provider allows it.
func (s *Service) resolveIdentity(ctx context.Context, prov *domain.OIDCProvider, claims *domain.OIDCClaims) (*domain.User, *domain.UserExternalIdentity, error) {
	ident, err := s.providers.GetIdentity(ctx, claims.Issuer, claims.Subject)
	switch {
	case err == nil:
		if ident.Disabled {
			return nil, nil, ErrAccountDisabled
		}
		user, err := s.users.GetUser(ctx, ident.UserID)
		if err != nil {
			return nil, nil, fmt.Errorf("auth: linked user: %w", err)
		}
		if user.Disabled {
			return nil, nil, ErrAccountDisabled
		}
		return user, ident, nil
	case errors.Is(err, domain.ErrNotFound):
		if !prov.JITProvisioning {
			return nil, nil, ErrPreprovisionedOnly
		}
		return s.jitProvision(ctx, prov, claims)
	default:
		return nil, nil, fmt.Errorf("auth: identity lookup: %w", err)
	}
}

// jitProvision creates a local user + identity link on first OIDC login.
func (s *Service) jitProvision(ctx context.Context, prov *domain.OIDCProvider, claims *domain.OIDCClaims) (*domain.User, *domain.UserExternalIdentity, error) {
	if strings.TrimSpace(claims.Email) == "" {
		return nil, nil, fmt.Errorf("%w: OIDC claims carry no email", ErrPreprovisionedOnly)
	}
	username, err := s.uniqueUsername(ctx, claims)
	if err != nil {
		return nil, nil, err
	}
	display := claims.Name
	if display == "" {
		display = claims.Email
	}
	user := &domain.User{
		Username: username, Email: claims.Email, DisplayName: display,
		LocalLoginDisabled: true, // SSO-only unless an admin says otherwise
	}
	if err := user.Validate(); err != nil {
		return nil, nil, err
	}
	if err := s.users.CreateUser(ctx, user); err != nil {
		return nil, nil, fmt.Errorf("auth: JIT create user: %w", err)
	}
	ident := &domain.UserExternalIdentity{
		UserID: user.ID, ProviderID: prov.ID,
		Issuer: claims.Issuer, Subject: claims.Subject, Email: claims.Email,
	}
	if err := ident.Validate(); err != nil {
		return nil, nil, err
	}
	if err := s.providers.LinkIdentity(ctx, ident); err != nil {
		return nil, nil, fmt.Errorf("auth: JIT link identity: %w", err)
	}
	return user, ident, nil
}

// uniqueUsername derives a username from the email local-part (fallback:
// oidc-<sub>) and disambiguates against existing users.
func (s *Service) uniqueUsername(ctx context.Context, claims *domain.OIDCClaims) (string, error) {
	base := strings.ToLower(strings.TrimSpace(claims.Email))
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at]
	}
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return -1
		}
	}, base)
	if base == "" {
		base = "oidc-user"
	}
	candidate := base
	for i := 0; i < 100; i++ {
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		_, err := s.users.GetUserByUsername(ctx, candidate)
		if errors.Is(err, domain.ErrNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("auth: username check: %w", err)
		}
	}
	return "", errors.New("auth: cannot derive a unique username")
}

func (s *Service) popPending(state string) (pendingLogin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgePendingLocked()
	p, ok := s.pending[state]
	if !ok {
		return pendingLogin{}, false
	}
	delete(s.pending, state) // single-use
	return p, true
}

func (s *Service) purgePendingLocked() {
	now := s.clock.Now()
	for k, p := range s.pending {
		if p.expired(now) {
			delete(s.pending, k)
		}
	}
}

// PendingCount reports in-flight OIDC logins (tests/ops).
func (s *Service) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgePendingLocked()
	return len(s.pending)
}

func denyDetail(err error) string {
	switch {
	case errors.Is(err, ErrAccountDisabled):
		return "account_disabled"
	case errors.Is(err, ErrPreprovisionedOnly):
		return "preprovisioned_only"
	default:
		return "identity_failed"
	}
}
