-- OIDC providers + external identities (PRD §7.1.1).
-- NOTE: never select client_secret_encrypted in read APIs; the
-- GetProviderSecret query exists only for the server-side token exchange.
-- Column ownership: client_secret_encrypted holds the auth track's Box
-- envelope, written only through auth.ProviderSecretStore. Provider
-- create/update statements must NEVER write it (the secret reference
-- lives in endpoints JSON); UpdateProvider excludes it so config edits
-- cannot clobber a stored secret.

-- name: GetProviderByID :one
SELECT id, name, issuer, discovery_url, endpoints, client_id,
       scopes, claims_mapping, default_role_id, is_enabled,
       allow_jit_provisioning, created_at, updated_at
FROM oidc_providers WHERE id = $1;

-- name: GetProviderByName :one
SELECT id, name, issuer, discovery_url, endpoints, client_id,
       scopes, claims_mapping, default_role_id, is_enabled,
       allow_jit_provisioning, created_at, updated_at
FROM oidc_providers WHERE name = $1;

-- name: ListProviders :many
SELECT id, name, issuer, discovery_url, endpoints, client_id,
       scopes, claims_mapping, default_role_id, is_enabled,
       allow_jit_provisioning, created_at, updated_at
FROM oidc_providers ORDER BY name ASC;

-- name: ListEnabledProviders :many
SELECT id, name, issuer, discovery_url, endpoints, client_id,
       scopes, claims_mapping, default_role_id, is_enabled,
       allow_jit_provisioning, created_at, updated_at
FROM oidc_providers WHERE is_enabled ORDER BY name ASC;

-- name: CreateProvider :one
INSERT INTO oidc_providers (
    name, issuer, discovery_url, endpoints, client_id, client_secret_encrypted,
    scopes, claims_mapping, default_role_id, is_enabled, allow_jit_provisioning
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, name, issuer, discovery_url, endpoints, client_id,
    scopes, claims_mapping, default_role_id, is_enabled,
    allow_jit_provisioning, created_at, updated_at;

-- name: SetProviderEnabled :exec
UPDATE oidc_providers SET is_enabled = $2, updated_at = now() WHERE id = $1;

-- name: DeleteProvider :exec
DELETE FROM oidc_providers WHERE id = $1;

-- name: GetProviderSecret :one
SELECT client_secret_encrypted FROM oidc_providers WHERE id = $1;

-- name: GetIdentityByIssuerSubject :one
SELECT * FROM user_external_identities WHERE issuer = $1 AND subject = $2;

-- name: ListIdentitiesByUser :many
SELECT * FROM user_external_identities WHERE user_id = $1 ORDER BY created_at ASC;

-- name: CreateIdentity :one
INSERT INTO user_external_identities (user_id, provider_id, issuer, subject, email, claims)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateIdentityLogin :exec
UPDATE user_external_identities
SET last_login_at = now(), claims = $2, updated_at = now()
WHERE id = $1;

-- name: SetIdentityDisabled :exec
UPDATE user_external_identities SET is_disabled = $2, updated_at = now() WHERE id = $1;

-- name: DeleteIdentity :exec
DELETE FROM user_external_identities WHERE id = $1;

-- name: GetProviderByIssuer :one
SELECT id, name, issuer, discovery_url, endpoints, client_id,
       scopes, claims_mapping, default_role_id, is_enabled,
       allow_jit_provisioning, created_at, updated_at
FROM oidc_providers WHERE issuer = $1;

-- name: UpdateProvider :one
UPDATE oidc_providers
SET name = $2, issuer = $3, discovery_url = $4, endpoints = $5,
    client_id = $6, scopes = $7,
    claims_mapping = $8, default_role_id = $9, is_enabled = $10,
    allow_jit_provisioning = $11, updated_at = now()
WHERE id = $1
RETURNING id, name, issuer, discovery_url, endpoints, client_id,
    scopes, claims_mapping, default_role_id, is_enabled,
    allow_jit_provisioning, created_at, updated_at;

-- name: GetIdentityByID :one
SELECT * FROM user_external_identities WHERE id = $1;

-- name: UpdateIdentity :one
UPDATE user_external_identities
SET email = $2, claims = $3, is_disabled = $4, last_login_at = $5, updated_at = now()
WHERE id = $1
RETURNING *;
