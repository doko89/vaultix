package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// providers.go: OIDC provider and identity-link administration. Client
// secrets are encrypted immediately via SecretBox and persisted through
// ProviderSecretStore; they are never stored on the entity, never logged,
// and never returned by reads.

// secretRefScheme marks ClientSecretRef values managed by this service.
const secretRefScheme = "enc:v1:"

// CreateProvider stores a provider config plus its encrypted secret.
func (s *Service) CreateProvider(ctx context.Context, in ProviderInput, actorID string) (*domain.OIDCProvider, error) {
	p := providerFromInput(in)
	p.Enabled = true
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	p.JITProvisioning = true
	if in.JITProvisioning != nil {
		p.JITProvisioning = *in.JITProvisioning
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := validateRoleMapping(ctx, s.users, &p); err != nil {
		return nil, err
	}
	if err := s.providers.CreateProvider(ctx, &p); err != nil {
		return nil, fmt.Errorf("auth: create provider: %w", err)
	}
	if strings.TrimSpace(in.ClientSecret) != "" {
		if err := s.storeSecret(ctx, p.ID, in.ClientSecret); err != nil {
			return nil, err
		}
		p.ClientSecretRef = secretRefScheme + p.ID
		if err := s.providers.UpdateProvider(ctx, &p); err != nil {
			return nil, fmt.Errorf("auth: provider secret ref: %w", err)
		}
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.provider.create", Actor: actorID, Target: p.ID, Result: "success", Detail: "provider:" + p.Name})
	return &p, nil
}

// UpdateProvider rewrites a provider config. An empty ClientSecret keeps the
// stored secret; a non-empty one rotates it.
func (s *Service) UpdateProvider(ctx context.Context, id string, in ProviderInput, actorID string) (*domain.OIDCProvider, error) {
	p, err := s.providers.GetProvider(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("auth: provider lookup: %w", err)
	}
	updated := providerFromInput(in)
	updated.ID = p.ID
	updated.CreatedAt = p.CreatedAt
	updated.ClientSecretRef = p.ClientSecretRef
	updated.Enabled = p.Enabled
	if in.Enabled != nil {
		updated.Enabled = *in.Enabled
	}
	updated.JITProvisioning = p.JITProvisioning
	if in.JITProvisioning != nil {
		updated.JITProvisioning = *in.JITProvisioning
	}
	if err := updated.Validate(); err != nil {
		return nil, err
	}
	if err := validateRoleMapping(ctx, s.users, &updated); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.ClientSecret) != "" {
		if err := s.storeSecret(ctx, updated.ID, in.ClientSecret); err != nil {
			return nil, err
		}
		updated.ClientSecretRef = secretRefScheme + updated.ID
	}
	if err := s.providers.UpdateProvider(ctx, &updated); err != nil {
		return nil, fmt.Errorf("auth: update provider: %w", err)
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.provider.update", Actor: actorID, Target: id, Result: "success"})
	return &updated, nil
}

// GetProvider returns a provider config without secret material.
func (s *Service) GetProvider(ctx context.Context, id string) (*domain.OIDCProvider, error) {
	p, err := s.providers.GetProvider(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("auth: provider lookup: %w", err)
	}
	return p, nil
}

// ListProviders returns all provider configs without secret material.
func (s *Service) ListProviders(ctx context.Context) ([]domain.OIDCProvider, error) {
	provs, err := s.providers.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: list providers: %w", err)
	}
	// Reads are secret-free by construction: the entity carries only
	// ClientSecretRef (a storage reference), never the secret itself.
	return provs, nil
}

// SetProviderEnabled enables/disables a provider. Disabling the last
// enabled provider while local login is off is refused (lockout guard).
func (s *Service) SetProviderEnabled(ctx context.Context, id string, enabled bool, actorID string) error {
	p, err := s.providers.GetProvider(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: provider lookup: %w", err)
	}
	if !enabled {
		provs, err := s.providers.ListProviders(ctx)
		if err != nil {
			return fmt.Errorf("auth: list providers: %w", err)
		}
		others := false
		for _, q := range provs {
			if q.ID != id && q.Enabled {
				others = true
				break
			}
		}
		if !others && !s.LocalLoginEnabled() {
			return fmt.Errorf("%w: last login path", ErrNoBreakGlass)
		}
	}
	p.Enabled = enabled
	if err := s.providers.UpdateProvider(ctx, p); err != nil {
		return fmt.Errorf("auth: provider update: %w", err)
	}
	detail := "enabled"
	if !enabled {
		detail = "disabled"
	}
	return s.record(ctx, AuditEvent{Action: "auth.provider.enabled", Actor: actorID, Target: id, Result: "success", Detail: detail})
}

// RotateProviderSecret replaces a provider's client secret.
func (s *Service) RotateProviderSecret(ctx context.Context, id, newSecret, actorID string) error {
	if strings.TrimSpace(newSecret) == "" {
		return errors.New("auth: new secret is required")
	}
	p, err := s.providers.GetProvider(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: provider lookup: %w", err)
	}
	if err := s.storeSecret(ctx, id, newSecret); err != nil {
		return err
	}
	p.ClientSecretRef = secretRefScheme + id
	if err := s.providers.UpdateProvider(ctx, p); err != nil {
		return fmt.Errorf("auth: provider secret ref: %w", err)
	}
	return s.record(ctx, AuditEvent{Action: "auth.provider.secret.rotate", Actor: actorID, Target: id, Result: "success"})
}

// TestProvider runs discovery + JWKS checks so admins can validate config
// before activation (never locks out: read-only, audited).
func (s *Service) TestProvider(ctx context.Context, id, actorID string) (*ProviderTestReport, error) {
	p, err := s.providers.GetProvider(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("auth: provider lookup: %w", err)
	}
	if s.tester == nil {
		return nil, errors.New("auth: provider testing is unavailable")
	}
	rep, err := s.tester.TestProvider(ctx, p)
	if err != nil {
		return nil, err
	}
	detail := "ok"
	if !rep.OK {
		detail = "failed"
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.provider.test", Actor: actorID, Target: id, Result: "success", Detail: detail})
	return rep, nil
}

// ProviderSecret decrypts a provider secret for the token exchange.
// Server-side only: never expose via HTTP. Used by the cmd-wired
// oidc.SecretResolver.
func (s *Service) ProviderSecret(ctx context.Context, providerID string) (string, error) {
	env, err := s.secrets.GetProviderSecret(ctx, providerID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", nil // public client: PKCE-only
		}
		return "", errors.New("auth: provider secret unavailable")
	}
	if env == "" {
		return "", nil
	}
	plain, err := s.box.Decrypt(env)
	if err != nil {
		return "", errors.New("auth: provider secret unavailable")
	}
	return string(plain), nil
}

func (s *Service) storeSecret(ctx context.Context, providerID, secret string) error {
	env, err := s.box.Encrypt([]byte(secret))
	if err != nil {
		return errors.New("auth: secret encryption failed")
	}
	if err := s.secrets.SetProviderSecret(ctx, providerID, env); err != nil {
		return fmt.Errorf("auth: store provider secret: %w", err)
	}
	return nil
}

func providerFromInput(in ProviderInput) domain.OIDCProvider {
	return domain.OIDCProvider{
		Name: in.Name, Issuer: strings.TrimSpace(in.Issuer),
		DiscoveryURL: in.DiscoveryURL,
		AuthURL:      in.AuthURL, TokenURL: in.TokenURL, JWKSURL: in.JWKSURL,
		UserInfoURL: in.UserInfoURL, EndSession: in.EndSession,
		ClientID:         in.ClientID,
		Scopes:           append([]string(nil), in.Scopes...),
		GroupClaimNames:  append([]string(nil), in.GroupClaimNames...),
		GroupRoleMapping: copyMapping(in.GroupRoleMapping),
		DefaultRole:      in.DefaultRole,
	}
}

func copyMapping(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// validateRoleMapping ensures every mapped role name exists.
func validateRoleMapping(ctx context.Context, users domain.UserRepository, p *domain.OIDCProvider) error {
	names := map[string]bool{}
	for _, r := range p.GroupRoleMapping {
		if r != "" {
			names[r] = true
		}
	}
	if p.DefaultRole != "" {
		names[p.DefaultRole] = true
	}
	for name := range names {
		if _, err := users.GetRoleByName(ctx, name); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("auth: unknown role in mapping: %q", name)
			}
			return fmt.Errorf("auth: role lookup: %w", err)
		}
	}
	return nil
}

// ListUserIdentities returns a user's OIDC identity links (admin view).
func (s *Service) ListUserIdentities(ctx context.Context, userID string) ([]domain.UserExternalIdentity, error) {
	if _, err := s.users.GetUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("auth: user lookup: %w", err)
	}
	return s.providers.ListUserIdentities(ctx, userID)
}

// LinkIdentity pre-provisions (or re-links) an OIDC identity to a local user.
func (s *Service) LinkIdentity(ctx context.Context, userID, providerID, issuer, subject, email, actorID string) (*domain.UserExternalIdentity, error) {
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(subject) == "" {
		return nil, errors.New("auth: issuer and subject are required")
	}
	if _, err := s.users.GetUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("auth: user lookup: %w", err)
	}
	if providerID != "" {
		if _, err := s.providers.GetProvider(ctx, providerID); err != nil {
			return nil, fmt.Errorf("auth: provider lookup: %w", err)
		}
	}
	if existing, err := s.providers.GetIdentity(ctx, issuer, subject); err == nil {
		if existing.UserID == userID {
			return existing, nil // idempotent re-link
		}
		return nil, fmt.Errorf("auth: identity already linked: %w", domain.ErrConflict)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("auth: identity lookup: %w", err)
	}
	ident := &domain.UserExternalIdentity{
		UserID: userID, ProviderID: providerID,
		Issuer: issuer, Subject: subject, Email: email,
	}
	if err := ident.Validate(); err != nil {
		return nil, err
	}
	if err := s.providers.LinkIdentity(ctx, ident); err != nil {
		return nil, fmt.Errorf("auth: link identity: %w", err)
	}
	_ = s.record(ctx, AuditEvent{Action: "auth.identity.link", Actor: actorID, Target: ident.ID, Result: "success", Detail: "user:" + userID})
	return ident, nil
}

// UnlinkIdentity removes an identity link after verifying it belongs to the user.
func (s *Service) UnlinkIdentity(ctx context.Context, userID, identityID, actorID string) error {
	if _, err := s.findUserIdentity(ctx, userID, identityID); err != nil {
		return err
	}
	if err := s.providers.UnlinkIdentity(ctx, identityID); err != nil {
		return fmt.Errorf("auth: unlink identity: %w", err)
	}
	return s.record(ctx, AuditEvent{Action: "auth.identity.unlink", Actor: actorID, Target: identityID, Result: "success", Detail: "user:" + userID})
}

// SetIdentityDisabled disables/enables an identity link (kill-switch per link).
func (s *Service) SetIdentityDisabled(ctx context.Context, userID, identityID string, disabled bool, actorID string) error {
	ident, err := s.findUserIdentity(ctx, userID, identityID)
	if err != nil {
		return err
	}
	ident.Disabled = disabled
	if err := s.providers.UpdateIdentity(ctx, ident); err != nil {
		return fmt.Errorf("auth: identity update: %w", err)
	}
	detail := "enabled"
	if disabled {
		detail = "disabled"
	}
	return s.record(ctx, AuditEvent{Action: "auth.identity.disabled", Actor: actorID, Target: identityID, Result: "success", Detail: detail})
}

func (s *Service) findUserIdentity(ctx context.Context, userID, identityID string) (*domain.UserExternalIdentity, error) {
	idents, err := s.providers.ListUserIdentities(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list identities: %w", err)
	}
	for _, ident := range idents {
		if ident.ID == identityID {
			ident := ident
			return &ident, nil
		}
	}
	return nil, domain.ErrNotFound
}
