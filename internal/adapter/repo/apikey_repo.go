package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// ApiKeyRepo implements domain.ApiKeyRepository on api_keys.
//
// Only hashes are stored (never plaintext); mapping is direct because the
// schema mirrors the entity. Hash values must never appear in errors or
// logs — validation failures name the field, never the value.
//
// INTEGRATION NOTE: this repo is constructed with NewApiKeyRepo (it is not
// part of the Repos bundle, which the apikey track may not modify); pass
// it as domain.ApiKeyRepository into usecase/apikey.
type ApiKeyRepo struct {
	baseRepo
}

// NewApiKeyRepo builds an ApiKeyRepo on the shared pool. Queries run
// through sqlc-generated code only.
func NewApiKeyRepo(pool *pgxpool.Pool) *ApiKeyRepo {
	return &ApiKeyRepo{baseRepo: baseRepo{pool: pool, q: gen.New(pool)}}
}

var _ domain.ApiKeyRepository = (*ApiKeyRepo)(nil)

// Create persists a key. The ID and CreatedAt are database-assigned and
// written back onto k.
func (r *ApiKeyRepo) Create(ctx context.Context, k *domain.ApiKey) error {
	if k == nil {
		return validationError("API key is required")
	}
	if err := k.Validate(); err != nil {
		return err
	}
	uid, err := mustParseUUID(k.UserID, "user id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateApiKey(ctx, gen.CreateApiKeyParams{
		UserID: uid, Name: k.Name, KeyHash: k.KeyHash,
		KeyPrefix: k.KeyPrefix, Scopes: k.Scopes,
		ExpiresAt: timestamptzPtr(k.ExpiresAt),
	})
	if err != nil {
		return mapErr(err)
	}
	mapped := mapApiKey(row)
	*k = mapped
	return nil
}

// GetByID returns one key by ID.
func (r *ApiKeyRepo) GetByID(ctx context.Context, id string) (*domain.ApiKey, error) {
	aid, err := mustParseUUID(id, "API key id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetApiKeyByID(ctx, gen.GetApiKeyByIDParams{ID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	k := mapApiKey(row)
	return &k, nil
}

// GetByHash resolves a key by its raw SHA-256 hash for verification.
func (r *ApiKeyRepo) GetByHash(ctx context.Context, hash []byte) (*domain.ApiKey, error) {
	if len(hash) == 0 {
		return nil, notFound("API key")
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetApiKeyByHash(ctx, gen.GetApiKeyByHashParams{KeyHash: hash})
	if err != nil {
		return nil, mapErr(err)
	}
	k := mapApiKey(row)
	return &k, nil
}

// ListByUser returns a user's keys (including revoked), newest first.
func (r *ApiKeyRepo) ListByUser(ctx context.Context, userID string) ([]domain.ApiKey, error) {
	uid, err := mustParseUUID(userID, "user id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListApiKeysByUser(ctx, gen.ListApiKeysByUserParams{UserID: uid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.ApiKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapApiKey(row))
	}
	return out, nil
}

// Revoke stamps revoked_at; revoking an already-revoked key is a no-op.
func (r *ApiKeyRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	aid, err := mustParseUUID(id, "API key id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := r.q.RevokeApiKey(ctx, gen.RevokeApiKeyParams{
		ID: aid, RevokedAt: timestamptz(at),
	}); err != nil {
		return mapErr(err)
	}
	return nil
}

// TouchLastUsed stamps last_used_at after successful verification.
func (r *ApiKeyRepo) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	aid, err := mustParseUUID(id, "API key id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := r.q.TouchApiKeyLastUsed(ctx, gen.TouchApiKeyLastUsedParams{
		ID: aid, LastUsedAt: timestamptz(at),
	}); err != nil {
		return mapErr(err)
	}
	return nil
}

func mapApiKey(row gen.ApiKey) domain.ApiKey {
	return domain.ApiKey{
		ID:         uuidString(row.ID),
		UserID:     uuidString(row.UserID),
		Name:       row.Name,
		KeyHash:    append([]byte(nil), row.KeyHash...),
		KeyPrefix:  row.KeyPrefix,
		Scopes:     append([]string(nil), row.Scopes...),
		ExpiresAt:  timePtr(row.ExpiresAt),
		LastUsedAt: timePtr(row.LastUsedAt),
		RevokedAt:  timePtr(row.RevokedAt),
		CreatedAt:  timeOrZero(row.CreatedAt),
	}
}
