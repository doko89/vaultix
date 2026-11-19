package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// SecretMeta is the read model for a secret profile: metadata references
// only. Plaintext is write-only (accepted on create/update, sealed
// immediately) and is never returned by any read path; ciphertext stays
// inside the repository.
type SecretMeta struct {
	ID        string
	Name      string
	Kind      domain.SecretKind
	KeyRef    string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateSecretInput carries a write-only plaintext payload.
type CreateSecretInput struct {
	Name      string
	Kind      domain.SecretKind
	Plaintext []byte
	CreatedBy string
}

// UpdateSecretInput rotates a profile: full replacement with fresh
// write-only plaintext. Renames ride along with rotation so every update
// re-seals under the current key.
type UpdateSecretInput struct {
	ID        string
	Name      string
	Kind      domain.SecretKind
	Plaintext []byte
}

// secretUpdater is optionally implemented by StorageRepository adapters to
// update a secret in place. Repositories without it get delete+create
// emulation from UpdateSecret, preserving ID and creation metadata.
//
// INTEGRATION NOTE: the repo track may add
// UpdateSecret(ctx, *domain.SecretProfile) error to its implementation;
// storage works either way.
type secretUpdater interface {
	UpdateSecret(ctx context.Context, s *domain.SecretProfile) error
}

// CreateSecret seals the plaintext and persists the profile, returning
// metadata only.
func (s *Service) CreateSecret(ctx context.Context, in CreateSecretInput) (*SecretMeta, error) {
	if s.box == nil {
		return nil, fmt.Errorf("storage: create secret: secret encryption not configured")
	}
	name := strings.TrimSpace(in.Name)
	if err := validSecretKind(in.Kind); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("%w: secret name is required", domain.ErrValidation)
	}
	if len(in.Plaintext) == 0 {
		return nil, fmt.Errorf("%w: secret value is required", domain.ErrValidation)
	}
	if len(in.Plaintext) > MaxSecretBytes {
		return nil, fmt.Errorf("%w: secret value exceeds %d bytes", domain.ErrValidation, MaxSecretBytes)
	}
	if err := s.checkSecretNameUnique(ctx, name, ""); err != nil {
		return nil, err
	}
	ciphertext, keyRef, err := s.box.Seal(ctx, in.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("storage: create secret: seal: %w", err)
	}
	now := s.clock.Now().UTC()
	profile := &domain.SecretProfile{
		ID:         s.newID(),
		Name:       name,
		Kind:       in.Kind,
		Ciphertext: ciphertext,
		KeyRef:     keyRef,
		CreatedBy:  strings.TrimSpace(in.CreatedBy),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.CreateSecret(ctx, profile); err != nil {
		return nil, fmt.Errorf("storage: create secret: %w", err)
	}
	meta := toSecretMeta(profile)
	return &meta, nil
}

// GetSecret returns one profile's metadata (never plaintext).
func (s *Service) GetSecret(ctx context.Context, id string) (*SecretMeta, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: secret id is required", domain.ErrValidation)
	}
	profile, err := s.repo.GetSecret(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("storage: get secret: %w", err)
	}
	meta := toSecretMeta(profile)
	return &meta, nil
}

// UpdateSecret rotates a profile's sealed value (and optionally its name or
// kind), returning metadata only.
func (s *Service) UpdateSecret(ctx context.Context, in UpdateSecretInput) (*SecretMeta, error) {
	if s.box == nil {
		return nil, fmt.Errorf("storage: update secret: secret encryption not configured")
	}
	if strings.TrimSpace(in.ID) == "" {
		return nil, fmt.Errorf("%w: secret id is required", domain.ErrValidation)
	}
	name := strings.TrimSpace(in.Name)
	if err := validSecretKind(in.Kind); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("%w: secret name is required", domain.ErrValidation)
	}
	if len(in.Plaintext) == 0 {
		return nil, fmt.Errorf("%w: secret value is required", domain.ErrValidation)
	}
	if len(in.Plaintext) > MaxSecretBytes {
		return nil, fmt.Errorf("%w: secret value exceeds %d bytes", domain.ErrValidation, MaxSecretBytes)
	}
	existing, err := s.repo.GetSecret(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("storage: update secret: %w", err)
	}
	if err := s.checkSecretNameUnique(ctx, name, existing.ID); err != nil {
		return nil, err
	}
	ciphertext, keyRef, err := s.box.Seal(ctx, in.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("storage: update secret: seal: %w", err)
	}
	updated := &domain.SecretProfile{
		ID:         existing.ID,
		Name:       name,
		Kind:       in.Kind,
		Ciphertext: ciphertext,
		KeyRef:     keyRef,
		CreatedBy:  existing.CreatedBy,
		CreatedAt:  existing.CreatedAt,
		UpdatedAt:  s.clock.Now().UTC(),
	}
	if err := updated.Validate(); err != nil {
		return nil, err
	}
	if up, ok := s.repo.(secretUpdater); ok {
		if err := up.UpdateSecret(ctx, updated); err != nil {
			return nil, fmt.Errorf("storage: update secret: %w", err)
		}
	} else {
		// Emulate update on the frozen port: delete + recreate under the
		// same ID, preserving creation metadata.
		if err := s.repo.DeleteSecret(ctx, existing.ID); err != nil {
			return nil, fmt.Errorf("storage: update secret: %w", err)
		}
		if err := s.repo.CreateSecret(ctx, updated); err != nil {
			return nil, fmt.Errorf("storage: update secret: %w", err)
		}
	}
	meta := toSecretMeta(updated)
	return &meta, nil
}

// DeleteSecret removes a profile, refusing while a destination references it.
func (s *Service) DeleteSecret(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: secret id is required", domain.ErrValidation)
	}
	destinations, err := s.repo.ListDestinations(ctx)
	if err != nil {
		return fmt.Errorf("storage: delete secret: %w", err)
	}
	for _, d := range destinations {
		if d.CredentialRef == id {
			return fmt.Errorf("%w: secret is referenced by destination %q", domain.ErrConflict, d.Name)
		}
	}
	if err := s.repo.DeleteSecret(ctx, id); err != nil {
		return fmt.Errorf("storage: delete secret: %w", err)
	}
	return nil
}

// ListSecrets returns all profiles' metadata (never plaintext).
func (s *Service) ListSecrets(ctx context.Context) ([]SecretMeta, error) {
	profiles, err := s.repo.ListSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: list secrets: %w", err)
	}
	out := make([]SecretMeta, 0, len(profiles))
	for i := range profiles {
		out = append(out, toSecretMeta(&profiles[i]))
	}
	return out, nil
}

// toSecretMeta projects a profile to its metadata read model. Ciphertext
// and plaintext never cross this boundary.
func toSecretMeta(p *domain.SecretProfile) SecretMeta {
	return SecretMeta{
		ID:        p.ID,
		Name:      p.Name,
		Kind:      p.Kind,
		KeyRef:    p.KeyRef,
		CreatedBy: p.CreatedBy,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

func validSecretKind(k domain.SecretKind) error {
	switch k {
	case domain.SecretKindDatabase, domain.SecretKindStorage,
		domain.SecretKindEncryption, domain.SecretKindGeneric:
		return nil
	default:
		return fmt.Errorf("%w: unknown secret kind %q", domain.ErrValidation, string(k))
	}
}

func (s *Service) checkSecretNameUnique(ctx context.Context, name, selfID string) error {
	all, err := s.repo.ListSecrets(ctx)
	if err != nil {
		return fmt.Errorf("storage: validate secret: %w", err)
	}
	for _, o := range all {
		if o.ID != selfID && strings.EqualFold(o.Name, name) {
			return fmt.Errorf("%w: secret name %q already exists", domain.ErrConflict, name)
		}
	}
	return nil
}
