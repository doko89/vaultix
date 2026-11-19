package main

// Postgres integration test for the OIDC secret lifecycle across the auth
// service, the repos OIDC port, and the wiring-layer ProviderSecretStore.
//
// It locks the Phase 2 integration fix for the client_secret_encrypted
// column-ownership bug: provider create/update used to write the secret
// *reference* into the column holding the auth track's *envelope*,
// destroying stored secrets. The column now holds envelopes only.
//
// Skipped unless TEST_DATABASE_URL is set. The test creates, resets, and
// migrates a sibling "<db>_manager" database, so it never interferes with
// the repos suite even when both run under one `go test ./...`.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/adapter/crypto"
	"github.com/vaultix/vaultix/internal/adapter/oidc"
	"github.com/vaultix/vaultix/internal/adapter/repo"
	"github.com/vaultix/vaultix/internal/adapter/session"
	authuc "github.com/vaultix/vaultix/internal/usecase/auth"
)

func openManagerTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL unset; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	dbName := cfg.ConnConfig.Database + "_manager"
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer admin.Close()
	_, err = admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`)
	if err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P04" {
			require.NoError(t, err)
		}
	}
	cfg.ConnConfig.Database = dbName
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	require.NoError(t, err)
	_, err = Runner{}.Up(ctx, pgxDB{pool: pool}, "../../db/migrations")
	require.NoError(t, err)
	return pool
}

func TestIntegrationOIDCSecretLifecycle(t *testing.T) {
	pool := openManagerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	box, err := crypto.NewBox([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	repos := repo.New(pool)
	store := providerSecretStore{pool: pool}
	authSvc, err := authuc.NewService(authuc.Deps{
		Users:     repos.Users,
		Providers: repos.OIDC,
		Sessions:  session.New(pool),
		OIDC:      oidc.New(),
		Secrets:   store,
		Box:       box,
		Hash:      authuc.BcryptHasher{},
	})
	require.NoError(t, err)

	// Create with a secret: the envelope lands in the column.
	p, err := authSvc.CreateProvider(ctx, authuc.ProviderInput{
		Name: "integ", Issuer: "https://integ.example",
		ClientID: "cid", ClientSecret: "s3cret", Scopes: []string{"openid"},
	}, "tester")
	require.NoError(t, err)
	require.NotEmpty(t, p.ID)
	require.Equal(t, "enc:v1:"+p.ID, p.ClientSecretRef)

	env, err := store.GetProviderSecret(ctx, p.ID)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(env, "v1:"), "column must hold the Box envelope, got %q", env)
	plain, err := box.Decrypt(env)
	require.NoError(t, err)
	require.Equal(t, "s3cret", string(plain))

	// Config-only update preserves the envelope.
	_, err = authSvc.UpdateProvider(ctx, p.ID, authuc.ProviderInput{
		Name: "integ-renamed", Issuer: "https://integ.example",
		ClientID: "cid", Scopes: []string{"openid"},
	}, "tester")
	require.NoError(t, err)
	env2, err := store.GetProviderSecret(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, env, env2, "config-only update must not clobber the stored secret")

	// Rotation replaces the envelope.
	require.NoError(t, authSvc.RotateProviderSecret(ctx, p.ID, "n3w", "tester"))
	env3, err := store.GetProviderSecret(ctx, p.ID)
	require.NoError(t, err)
	require.NotEqual(t, env, env3)
	plain, err = box.Decrypt(env3)
	require.NoError(t, err)
	require.Equal(t, "n3w", string(plain))

	// Delete clears it.
	require.NoError(t, store.DeleteProviderSecret(ctx, p.ID))
	env4, err := store.GetProviderSecret(ctx, p.ID)
	require.NoError(t, err)
	require.Empty(t, env4)
}
