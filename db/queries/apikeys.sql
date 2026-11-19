-- User API keys (CLI/programmatic access). Only hashes are stored; the
-- plaintext secret is returned once at issuance and never persisted.

-- name: CreateApiKey :one
INSERT INTO api_keys (user_id, name, key_hash, key_prefix, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetApiKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1;

-- name: GetApiKeyByID :one
SELECT * FROM api_keys WHERE id = $1;

-- name: ListApiKeysByUser :many
SELECT * FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: RevokeApiKey :exec
UPDATE api_keys SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL;

-- name: TouchApiKeyLastUsed :exec
UPDATE api_keys SET last_used_at = $2 WHERE id = $1;
