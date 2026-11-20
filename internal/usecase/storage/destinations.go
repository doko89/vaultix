package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Destination inputs. Endpoint/region/prefix are optional (empty endpoint
// selects the AWS default); bucket follows S3 naming rules. UploadMode
// defaults to presigned when empty; "direct" is the explicit per-destination
// opt-in that hands per-job S3 credentials to the agent (see
// domain.UploadMode) and must only be used for S3 implementations with
// broken query auth.
type CreateDestinationInput struct {
	Name          string
	Endpoint      string
	Region        string
	Bucket        string
	Prefix        string
	CredentialRef string
	UsePathStyle  bool
	UploadMode    domain.UploadMode
	Default       bool
}

// UpdateDestinationInput is a full replacement; ID selects the row.
type UpdateDestinationInput struct {
	ID            string
	Name          string
	Endpoint      string
	Region        string
	Bucket        string
	Prefix        string
	CredentialRef string
	UsePathStyle  bool
	UploadMode    domain.UploadMode
	Default       bool
}

// normalizeUploadMode trims the requested mode and defaults empty to
// presigned. Anything else is rejected downstream by domain validation with
// an actionable error naming both legal values.
func normalizeUploadMode(m domain.UploadMode) domain.UploadMode {
	m = domain.UploadMode(strings.TrimSpace(string(m)))
	if m == "" {
		return domain.UploadModePresigned
	}
	return m
}

// CreateDestination validates and persists a new destination.
func (s *Service) CreateDestination(ctx context.Context, in CreateDestinationInput) (*domain.StorageDestination, error) {
	d := &domain.StorageDestination{
		ID:            s.newID(),
		Name:          strings.TrimSpace(in.Name),
		Endpoint:      strings.TrimSpace(in.Endpoint),
		Region:        strings.TrimSpace(in.Region),
		Bucket:        strings.TrimSpace(in.Bucket),
		Prefix:        strings.Trim(strings.TrimSpace(in.Prefix), "/"),
		CredentialRef: strings.TrimSpace(in.CredentialRef),
		UsePathStyle:  in.UsePathStyle,
		UploadMode:    normalizeUploadMode(in.UploadMode),
		Default:       in.Default,
		CreatedAt:     s.clock.Now().UTC(),
	}
	d.UpdatedAt = d.CreatedAt
	if err := s.validateDestination(ctx, d, ""); err != nil {
		return nil, err
	}
	if err := s.repo.CreateDestination(ctx, d); err != nil {
		return nil, fmt.Errorf("storage: create destination: %w", err)
	}
	if d.Default {
		if err := s.unsetOtherDefaults(ctx, d.ID); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// GetDestination returns one destination by ID.
func (s *Service) GetDestination(ctx context.Context, id string) (*domain.StorageDestination, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: destination id is required", domain.ErrValidation)
	}
	d, err := s.repo.GetDestination(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("storage: get destination: %w", err)
	}
	return d, nil
}

// UpdateDestination replaces a destination's configuration.
func (s *Service) UpdateDestination(ctx context.Context, in UpdateDestinationInput) (*domain.StorageDestination, error) {
	if strings.TrimSpace(in.ID) == "" {
		return nil, fmt.Errorf("%w: destination id is required", domain.ErrValidation)
	}
	existing, err := s.repo.GetDestination(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("storage: update destination: %w", err)
	}
	d := &domain.StorageDestination{
		ID:            existing.ID,
		Name:          strings.TrimSpace(in.Name),
		Endpoint:      strings.TrimSpace(in.Endpoint),
		Region:        strings.TrimSpace(in.Region),
		Bucket:        strings.TrimSpace(in.Bucket),
		Prefix:        strings.Trim(strings.TrimSpace(in.Prefix), "/"),
		CredentialRef: strings.TrimSpace(in.CredentialRef),
		UsePathStyle:  in.UsePathStyle,
		UploadMode:    normalizeUploadMode(in.UploadMode),
		Default:       in.Default,
		CreatedAt:     existing.CreatedAt,
		UpdatedAt:     s.clock.Now().UTC(),
	}
	if err := s.validateDestination(ctx, d, existing.ID); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateDestination(ctx, d); err != nil {
		return nil, fmt.Errorf("storage: update destination: %w", err)
	}
	if d.Default {
		if err := s.unsetOtherDefaults(ctx, d.ID); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// DeleteDestination removes a destination. Secrets it referenced are kept:
// profiles may be shared.
//
// INTEGRATION NOTE: policy track should refuse (or explicitly confirm)
// deleting a destination still selected by a policy; the policy repository
// is outside this aggregate so the check cannot live here.
func (s *Service) DeleteDestination(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: destination id is required", domain.ErrValidation)
	}
	if err := s.repo.DeleteDestination(ctx, id); err != nil {
		return fmt.Errorf("storage: delete destination: %w", err)
	}
	return nil
}

// ListDestinations returns all destinations.
func (s *Service) ListDestinations(ctx context.Context) ([]domain.StorageDestination, error) {
	ds, err := s.repo.ListDestinations(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: list destinations: %w", err)
	}
	return ds, nil
}

// TestDestination checks connectivity and credentials for one destination.
func (s *Service) TestDestination(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: destination id is required", domain.ErrValidation)
	}
	if _, err := s.repo.GetDestination(ctx, id); err != nil {
		return fmt.Errorf("storage: test destination: %w", err)
	}
	if s.store == nil {
		return fmt.Errorf("storage: test destination: object storage not configured")
	}
	if err := s.store.Ping(ctx, id); err != nil {
		return fmt.Errorf("storage: test destination: %w", err)
	}
	return nil
}

// ResolveForPolicy selects the destination for a policy: the policy's own
// StorageID when set, otherwise the default destination.
func (s *Service) ResolveForPolicy(ctx context.Context, storageID string) (*domain.StorageDestination, error) {
	if strings.TrimSpace(storageID) != "" {
		d, err := s.repo.GetDestination(ctx, storageID)
		if err != nil {
			return nil, fmt.Errorf("storage: resolve policy destination: %w", err)
		}
		return d, nil
	}
	d, err := s.repo.GetDefaultDestination(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve policy destination: no destination selected and no default configured: %w", err)
	}
	return d, nil
}

// validateDestination enforces destination invariants; selfID excludes the
// row being updated from the name-uniqueness check.
func (s *Service) validateDestination(ctx context.Context, d *domain.StorageDestination, selfID string) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if err := validBucket(d.Bucket); err != nil {
		return err
	}
	if d.Endpoint != "" {
		u, err := url.Parse(d.Endpoint)
		if err != nil {
			return fmt.Errorf("%w: invalid endpoint: %v", domain.ErrValidation, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%w: endpoint scheme must be http or https", domain.ErrValidation)
		}
		if u.Host == "" {
			return fmt.Errorf("%w: endpoint host is required", domain.ErrValidation)
		}
		if u.User != nil {
			return fmt.Errorf("%w: endpoint must not embed credentials", domain.ErrValidation)
		}
	}
	if d.Prefix != "" {
		for _, seg := range strings.Split(d.Prefix, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return fmt.Errorf("%w: invalid prefix %q", domain.ErrValidation, d.Prefix)
			}
		}
	}
	if d.CredentialRef != "" {
		if _, err := s.repo.GetSecret(ctx, d.CredentialRef); err != nil {
			return fmt.Errorf("%w: credential reference %q not found", domain.ErrValidation, d.CredentialRef)
		}
	}
	existing, err := s.repo.ListDestinations(ctx)
	if err != nil {
		return fmt.Errorf("storage: validate destination: %w", err)
	}
	for _, o := range existing {
		if o.ID != selfID && strings.EqualFold(o.Name, d.Name) {
			return fmt.Errorf("%w: destination name %q already exists", domain.ErrConflict, d.Name)
		}
	}
	return nil
}

// unsetOtherDefaults clears the default flag on every destination but id.
func (s *Service) unsetOtherDefaults(ctx context.Context, id string) error {
	all, err := s.repo.ListDestinations(ctx)
	if err != nil {
		return fmt.Errorf("storage: reassign default: %w", err)
	}
	for _, o := range all {
		if o.ID == id || !o.Default {
			continue
		}
		o.Default = false
		o.UpdatedAt = s.clock.Now().UTC()
		if err := s.repo.UpdateDestination(ctx, &o); err != nil {
			return fmt.Errorf("storage: reassign default: %w", err)
		}
	}
	return nil
}

// validBucket enforces S3-compatible bucket naming.
func validBucket(bucket string) error {
	if len(bucket) < 3 || len(bucket) > 63 {
		return fmt.Errorf("%w: bucket %q must be 3..63 characters", domain.ErrValidation, bucket)
	}
	for i := 0; i < len(bucket); i++ {
		ch := bucket[i]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '.' {
			continue
		}
		return fmt.Errorf("%w: bucket %q has invalid character %q", domain.ErrValidation, bucket, string(ch))
	}
	if bucket[0] == '.' || bucket[0] == '-' || bucket[len(bucket)-1] == '.' || bucket[len(bucket)-1] == '-' {
		return fmt.Errorf("%w: bucket %q must start and end with a letter or digit", domain.ErrValidation, bucket)
	}
	if strings.Contains(bucket, "..") {
		return fmt.Errorf("%w: bucket %q must not contain consecutive dots", domain.ErrValidation, bucket)
	}
	return nil
}
