package repo

import (
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

func TestApiKeyRepoValidation(t *testing.T) {
	r := &ApiKeyRepo{}
	ctx := context.Background()
	require.ErrorIs(t, r.Create(ctx, nil), domain.ErrValidation)
	require.ErrorIs(t, r.Create(ctx, &domain.ApiKey{}), domain.ErrValidation)
	_, err := r.GetByID(ctx, "not-a-uuid")
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = r.GetByHash(ctx, nil)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = r.ListByUser(ctx, "nope")
	require.ErrorIs(t, err, domain.ErrValidation)
	require.ErrorIs(t, r.Revoke(ctx, "nope", time.Now()), domain.ErrValidation)
	require.ErrorIs(t, r.TouchLastUsed(ctx, "", time.Now()), domain.ErrValidation)
}

func TestMapApiKeyCopiesSlices(t *testing.T) {
	row := gen.ApiKey{KeyHash: []byte{1, 2, 3}, Scopes: []string{"read"}}
	k := mapApiKey(row)
	row.KeyHash[0] = 9
	row.Scopes[0] = "admin"
	if k.KeyHash[0] != 1 || k.Scopes[0] != "read" {
		t.Fatal("mapApiKey must copy slices, not alias row memory")
	}
	if k.Revoked() || k.ExpiresAt != nil || k.LastUsedAt != nil || k.RevokedAt != nil {
		t.Fatal("null timestamps must map to nil")
	}
}

// openApiKeyRepoTestDB ensures the shared migrated test database exists
// (via the bundle harness, which skips when TEST_DATABASE_URL is unset)
// and returns an ApiKeyRepo on a fresh pool to the same database.
func openApiKeyRepoTestDB(t *testing.T) (*ApiKeyRepo, *pgxpool.Pool) {
	t.Helper()
	openIntegrationRepos(t) // migrates once, or skips
	ctx, cancel := testCtx()
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return NewApiKeyRepo(pool), pool
}

func seedApiKeyUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx, cancel := testCtx()
	defer cancel()
	var id string
	email := "apikey-" + uniqueSuffix(t) + "@example.com"
	err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestIntegrationApiKeys(t *testing.T) {
	repo, pool := openApiKeyRepoTestDB(t)
	ctx, cancel := testCtx()
	defer cancel()
	userID := seedApiKeyUser(t, pool)

	secret := "vxk_" + strings.Repeat("a1", 16) // 36-char presented key
	sum := sha256.Sum256([]byte(secret))
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)

	k := &domain.ApiKey{
		UserID: userID, Name: "cli",
		KeyHash: append([]byte(nil), sum[:]...), KeyPrefix: secret[:8],
		Scopes: []string{"read", "write"}, ExpiresAt: &exp,
	}
	require.NoError(t, repo.Create(ctx, k))
	require.NotEmpty(t, k.ID)
	require.False(t, k.CreatedAt.IsZero())

	// Hash-not-plaintext at rest: the stored hash is exactly the SHA-256
	// digest, and no column holds the full presented secret.
	var stored []byte
	var name, prefix string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT key_hash, name, key_prefix FROM api_keys WHERE id = $1::uuid`, k.ID,
	).Scan(&stored, &name, &prefix))
	require.Equal(t, sum[:], stored)
	require.NotContains(t, name+"|"+prefix, secret)
	var fullLeak int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys WHERE key_prefix = $1 OR name = $1`, secret,
	).Scan(&fullLeak))
	require.Zero(t, fullLeak)

	byHash, err := repo.GetByHash(ctx, sum[:])
	require.NoError(t, err)
	require.Equal(t, k.ID, byHash.ID)
	require.Equal(t, []string{"read", "write"}, byHash.Scopes)
	require.NotNil(t, byHash.ExpiresAt)

	byID, err := repo.GetByID(ctx, k.ID)
	require.NoError(t, err)
	require.Equal(t, userID, byID.UserID)

	listed, err := repo.ListByUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	used := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, repo.TouchLastUsed(ctx, k.ID, used))
	byID, err = repo.GetByID(ctx, k.ID)
	require.NoError(t, err)
	require.NotNil(t, byID.LastUsedAt)

	at := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, repo.Revoke(ctx, k.ID, at))
	byID, err = repo.GetByID(ctx, k.ID)
	require.NoError(t, err)
	require.True(t, byID.Revoked())
	// Idempotent re-revoke keeps the original stamp.
	require.NoError(t, repo.Revoke(ctx, k.ID, at.Add(time.Hour)))

	miss := sha256.Sum256([]byte("vxk_nope"))
	_, err = repo.GetByHash(ctx, miss[:])
	require.ErrorIs(t, err, domain.ErrNotFound)
}
