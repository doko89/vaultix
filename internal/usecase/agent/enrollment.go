package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Registration carries the agent-reported facts captured at enrollment.
type Registration struct {
	Hostname     string
	IPAddress    string
	OS           string
	Architecture string
	Version      string
	Capabilities []string
}

// IssueEnrollmentToken creates a single-use, expiring enrollment token. The
// plaintext is returned once and only its hash is stored.
func (s *Service) IssueEnrollmentToken(ctx context.Context, ttl time.Duration) (string, *domain.AgentCredential, error) {
	if ttl <= 0 {
		ttl = DefaultEnrollmentTTL
	}
	if ttl > MaxEnrollmentTTL {
		return "", nil, fmt.Errorf("%w: ttl exceeds maximum %s", domain.ErrValidation, MaxEnrollmentTTL)
	}
	plaintext, err := s.newToken()
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(plaintext) == "" {
		return "", nil, fmt.Errorf("%w: generated token is blank", domain.ErrValidation)
	}
	now := s.clock.Now().UTC()
	exp := now.Add(ttl)
	id, err := s.newID()
	if err != nil {
		return "", nil, err
	}
	cred := &domain.AgentCredential{
		ID:         id,
		TokenHash:  HashToken(plaintext),
		Enrollment: true,
		ExpiresAt:  &exp,
		CreatedAt:  now,
	}
	if err := s.agents.StoreCredential(ctx, cred); err != nil {
		return "", nil, fmt.Errorf("agent: store enrollment token: %w", err)
	}
	return plaintext, cred, nil
}

// RedeemEnrollmentToken exchanges a single-use enrollment token for a
// registered agent plus its long-lived agent-bound credential. The enrollment
// token is revoked as part of redemption, so concurrent double-redeem relies
// on the repository serializing credential updates.
func (s *Service) RedeemEnrollmentToken(ctx context.Context, token string, reg Registration) (*domain.Agent, string, error) {
	if strings.TrimSpace(token) == "" {
		return nil, "", fmt.Errorf("%w: enrollment token is required", domain.ErrValidation)
	}
	if err := (domain.Agent{Hostname: reg.Hostname}).Validate(); err != nil {
		return nil, "", err
	}
	cred, err := s.agents.GetCredentialByHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, "", ErrInvalidToken
		}
		return nil, "", fmt.Errorf("agent: lookup enrollment token: %w", err)
	}
	if !cred.Enrollment {
		return nil, "", ErrInvalidToken
	}
	now := s.clock.Now().UTC()
	if cred.Revoked {
		return nil, "", ErrTokenReused
	}
	if cred.ExpiresAt != nil && !now.Before(*cred.ExpiresAt) {
		return nil, "", ErrExpiredToken
	}

	agentID, err := s.newID()
	if err != nil {
		return nil, "", err
	}
	ag := &domain.Agent{
		ID:           agentID,
		Hostname:     strings.TrimSpace(reg.Hostname),
		IPAddress:    strings.TrimSpace(reg.IPAddress),
		OS:           strings.TrimSpace(reg.OS),
		Architecture: strings.TrimSpace(reg.Architecture),
		Version:      strings.TrimSpace(reg.Version),
		Capabilities: append([]string(nil), reg.Capabilities...),
		EnrolledAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := ag.Validate(); err != nil {
		return nil, "", err
	}
	if err := s.agents.CreateAgent(ctx, ag); err != nil {
		return nil, "", fmt.Errorf("agent: register agent: %w", err)
	}

	// Single-use: consume the enrollment token before issuing the credential.
	cred.Revoked = true
	cred.LastUsedAt = &now
	if err := s.agents.UpdateCredential(ctx, cred); err != nil {
		return nil, "", fmt.Errorf("agent: consume enrollment token: %w", err)
	}

	plaintext, err := s.newToken()
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(plaintext) == "" {
		return nil, "", fmt.Errorf("%w: generated credential is blank", domain.ErrValidation)
	}
	credID, err := s.newID()
	if err != nil {
		return nil, "", err
	}
	bound := &domain.AgentCredential{
		ID:        credID,
		AgentID:   ag.ID,
		TokenHash: HashToken(plaintext),
		CreatedAt: now,
	}
	if err := s.agents.StoreCredential(ctx, bound); err != nil {
		return nil, "", fmt.Errorf("agent: store agent credential: %w", err)
	}
	return ag, plaintext, nil
}

// AuthenticateAgent validates an agent bearer credential: it must be a
// non-enrollment, unrevoked, unexpired credential bound to an enabled agent.
// LastUsedAt is refreshed on success.
func (s *Service) AuthenticateAgent(ctx context.Context, bearer string) (*domain.AgentCredential, error) {
	if strings.TrimSpace(bearer) == "" {
		return nil, ErrUnauthorized
	}
	cred, err := s.agents.GetCredentialByHash(ctx, HashToken(bearer))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("agent: lookup credential: %w", err)
	}
	now := s.clock.Now().UTC()
	if !cred.Valid(now) {
		return nil, ErrUnauthorized
	}
	if cred.Enrollment || strings.TrimSpace(cred.AgentID) == "" {
		return nil, ErrUnauthorized
	}
	ag, err := s.agents.GetAgent(ctx, cred.AgentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("agent: lookup agent for credential: %w", err)
	}
	if ag.Disabled {
		return nil, ErrAgentRevoked
	}
	cred.LastUsedAt = &now
	if err := s.agents.UpdateCredential(ctx, cred); err != nil {
		return nil, fmt.Errorf("agent: refresh credential use: %w", err)
	}
	return cred, nil
}
