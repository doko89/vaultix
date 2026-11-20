package repo

import (
	"context"
	"encoding/base64"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// StorageRepo implements domain.StorageRepository on
// storage_destinations/secret_profiles.
//
// Mapping notes: CredentialRef, UsePathStyle, and UploadMode live in the
// auth JSON object (references and behavior flags only, never secret
// values). Exactly one destination should be default; creating or updating
// a default clears the flag on all others in the same transaction. Secret
// ciphertext is base64-encoded into secret_encrypted; SecretKind round-trips
// via the description column because the owner_type check has no
// encryption/generic values. List queries never select secret_encrypted
// (ListSecrets returns Ciphertext nil). Legacy auth objects without
// upload_mode read back as presigned (see mapDestination).
type StorageRepo struct {
	baseRepo
}

// storageAuth is the storage_destinations.auth JSON codec. It carries
// references and flags only; credential values live in secret_profiles.
type storageAuth struct {
	CredentialRef string `json:"credential_ref,omitempty"`
	UsePathStyle  bool   `json:"use_path_style,omitempty"`
	// UploadMode rides the auth object (not a column) so the direct-upload
	// opt-in needs no schema migration; empty means presigned.
	UploadMode string `json:"upload_mode,omitempty"`
}

// CreateDestination inserts a storage destination. The ID is
// database-generated.
func (r *StorageRepo) CreateDestination(ctx context.Context, d *domain.StorageDestination) error {
	if d == nil {
		return validationError("destination is required")
	}
	if err := d.Validate(); err != nil {
		return err
	}
	auth, err := encodeJSON(storageAuth{CredentialRef: d.CredentialRef, UsePathStyle: d.UsePathStyle, UploadMode: string(d.UploadMode)})
	if err != nil {
		return err
	}
	params := gen.CreateStorageDestinationParams{
		Name: d.Name, Endpoint: d.Endpoint, Region: d.Region,
		Bucket: d.Bucket, Prefix: d.Prefix, Auth: auth,
		IsDefault: d.Default, IsEnabled: true,
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if d.Default {
		var id string
		err = r.inTx(ctx, func(q *gen.Queries) error {
			if err := q.ClearDefaultStorageDestinations(ctx); err != nil {
				return err
			}
			row, err := q.CreateStorageDestination(ctx, params)
			if err != nil {
				return err
			}
			id = uuidString(row.ID)
			d.CreatedAt = timeOrZero(row.CreatedAt)
			d.UpdatedAt = timeOrZero(row.UpdatedAt)
			return nil
		})
		if err != nil {
			return mapErr(err)
		}
		d.ID = id
		return nil
	}
	row, err := r.q.CreateStorageDestination(ctx, params)
	if err != nil {
		return mapErr(err)
	}
	d.ID = uuidString(row.ID)
	d.CreatedAt = timeOrZero(row.CreatedAt)
	d.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetDestination returns one destination by ID.
func (r *StorageRepo) GetDestination(ctx context.Context, id string) (*domain.StorageDestination, error) {
	did, err := mustParseUUID(id, "destination id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetStorageDestination(ctx, gen.GetStorageDestinationParams{ID: did})
	if err != nil {
		return nil, mapErr(err)
	}
	d, err := mapDestination(row)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDefaultDestination returns the default destination, or ErrNotFound
// when none is marked default.
func (r *StorageRepo) GetDefaultDestination(ctx context.Context) (*domain.StorageDestination, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetDefaultStorageDestination(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	d, err := mapDestination(row)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateDestination rewrites a destination, preserving its enabled flag
// (domain has none). Promoting to default clears the flag elsewhere.
func (r *StorageRepo) UpdateDestination(ctx context.Context, d *domain.StorageDestination) error {
	if d == nil {
		return validationError("destination is required")
	}
	if err := d.Validate(); err != nil {
		return err
	}
	did, err := mustParseUUID(d.ID, "destination id")
	if err != nil {
		return err
	}
	auth, err := encodeJSON(storageAuth{CredentialRef: d.CredentialRef, UsePathStyle: d.UsePathStyle, UploadMode: string(d.UploadMode)})
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	current, err := r.q.GetStorageDestination(ctx, gen.GetStorageDestinationParams{ID: did})
	if err != nil {
		return mapErr(err)
	}
	params := gen.UpdateStorageDestinationParams{
		ID: did, Name: d.Name, Endpoint: d.Endpoint, Region: d.Region,
		Bucket: d.Bucket, Prefix: d.Prefix, Auth: auth,
		IsDefault: d.Default, IsEnabled: current.IsEnabled,
	}
	if d.Default && !current.IsDefault {
		return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
			if err := q.ClearDefaultStorageDestinations(ctx); err != nil {
				return err
			}
			_, err := q.UpdateStorageDestination(ctx, params)
			return err
		}))
	}
	_, err = r.q.UpdateStorageDestination(ctx, params)
	return mapErr(err)
}

// DeleteDestination removes a destination after verifying it exists.
func (r *StorageRepo) DeleteDestination(ctx context.Context, id string) error {
	did, err := mustParseUUID(id, "destination id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetStorageDestination(ctx, gen.GetStorageDestinationParams{ID: did}); err != nil {
		return mapErr(err)
	}
	if err := r.q.DeleteStorageDestination(ctx, gen.DeleteStorageDestinationParams{ID: did}); err != nil {
		return mapErr(err)
	}
	return nil
}

// ListDestinations returns all destinations ordered by name.
func (r *StorageRepo) ListDestinations(ctx context.Context) ([]domain.StorageDestination, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListStorageDestinations(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.StorageDestination, 0, len(rows))
	for _, row := range rows {
		d, err := mapDestination(row)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// CreateSecret inserts an encrypted credential bundle. Only ciphertext is
// stored; the ID and timestamps are populated from the redacted row.
func (r *StorageRepo) CreateSecret(ctx context.Context, s *domain.SecretProfile) error {
	if s == nil {
		return validationError("secret is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	createdBy, err := mustOptUUID(s.CreatedBy, "created_by id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateSecretProfile(ctx, gen.CreateSecretProfileParams{
		Name:            s.Name,
		OwnerType:       secretKindToOwner(s.Kind),
		SecretEncrypted: base64.StdEncoding.EncodeToString(s.Ciphertext),
		KeyReference:    s.KeyRef,
		Description:     string(s.Kind),
		CreatedBy:       createdBy,
	})
	if err != nil {
		return mapErr(err)
	}
	s.ID = uuidString(row.ID)
	s.CreatedAt = timeOrZero(row.CreatedAt)
	s.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetSecret returns one secret including its ciphertext (server-side use
// only; never serialize to APIs or logs).
func (r *StorageRepo) GetSecret(ctx context.Context, id string) (*domain.SecretProfile, error) {
	sid, err := mustParseUUID(id, "secret id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetSecretProfile(ctx, gen.GetSecretProfileParams{ID: sid})
	if err != nil {
		return nil, mapErr(err)
	}
	s, err := mapSecret(row)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSecret removes a secret after verifying it exists.
func (r *StorageRepo) DeleteSecret(ctx context.Context, id string) error {
	sid, err := mustParseUUID(id, "secret id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetSecretProfile(ctx, gen.GetSecretProfileParams{ID: sid}); err != nil {
		return mapErr(err)
	}
	if err := r.q.DeleteSecretProfile(ctx, gen.DeleteSecretProfileParams{ID: sid}); err != nil {
		return mapErr(err)
	}
	return nil
}

// ListSecrets returns all secret profiles with ciphertext redacted
// (Ciphertext is always nil here; use GetSecret server-side).
func (r *StorageRepo) ListSecrets(ctx context.Context) ([]domain.SecretProfile, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListSecretProfiles(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.SecretProfile, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.SecretProfile{
			ID:        uuidString(row.ID),
			Name:      row.Name,
			Kind:      secretKindFromRow(row.OwnerType, row.Description),
			KeyRef:    row.KeyReference,
			CreatedBy: uuidString(row.CreatedBy),
			CreatedAt: timeOrZero(row.CreatedAt),
			UpdatedAt: timeOrZero(row.UpdatedAt),
		})
	}
	return out, nil
}

func mapDestination(row gen.StorageDestination) (domain.StorageDestination, error) {
	var auth storageAuth
	if err := decodeJSON(row.Auth, &auth); err != nil {
		return domain.StorageDestination{}, err
	}
	// Legacy rows predate upload_mode: their auth object omits the key and
	// they behave as presigned. Unknown stored values pass through untouched;
	// issuance only fires on the exact "direct" value, so garbage can never
	// widen into credential issuance (fail closed).
	mode := domain.UploadMode(auth.UploadMode)
	if mode == "" {
		mode = domain.UploadModePresigned
	}
	return domain.StorageDestination{
		ID:            uuidString(row.ID),
		Name:          row.Name,
		Endpoint:      row.Endpoint,
		Region:        row.Region,
		Bucket:        row.Bucket,
		Prefix:        row.Prefix,
		CredentialRef: auth.CredentialRef,
		UsePathStyle:  auth.UsePathStyle,
		UploadMode:    mode,
		Default:       row.IsDefault,
		CreatedAt:     timeOrZero(row.CreatedAt),
		UpdatedAt:     timeOrZero(row.UpdatedAt),
	}, nil
}

// secretKindToOwner maps domain kinds onto the owner_type check values.
// encryption/generic share "env"; the exact kind round-trips via the
// description column.
func secretKindToOwner(kind domain.SecretKind) string {
	switch kind {
	case domain.SecretKindDatabase:
		return "database"
	case domain.SecretKindStorage:
		return "storage"
	default:
		return "env"
	}
}

// secretKindFromRow restores the domain kind, preferring the description
// marker written by CreateSecret.
func secretKindFromRow(owner, description string) domain.SecretKind {
	switch domain.SecretKind(description) {
	case domain.SecretKindDatabase,
		domain.SecretKindStorage,
		domain.SecretKindEncryption,
		domain.SecretKindGeneric:
		return domain.SecretKind(description)
	}
	switch owner {
	case "database":
		return domain.SecretKindDatabase
	case "storage":
		return domain.SecretKindStorage
	default:
		return domain.SecretKindGeneric
	}
}

func mapSecret(row gen.SecretProfile) (domain.SecretProfile, error) {
	raw, err := base64.StdEncoding.DecodeString(row.SecretEncrypted)
	if err != nil {
		return domain.SecretProfile{}, validationError("secret payload is corrupt")
	}
	return domain.SecretProfile{
		ID:         uuidString(row.ID),
		Name:       row.Name,
		Kind:       secretKindFromRow(row.OwnerType, row.Description),
		Ciphertext: raw,
		KeyRef:     row.KeyReference,
		CreatedBy:  uuidString(row.CreatedBy),
		CreatedAt:  timeOrZero(row.CreatedAt),
		UpdatedAt:  timeOrZero(row.UpdatedAt),
	}, nil
}
