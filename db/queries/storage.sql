-- Storage destinations + secret profiles (PRD §7.12, §7.7).
-- Read APIs must redact secret_profiles.secret_encrypted and the
-- credential fields inside storage_destinations.auth.

-- name: GetStorageDestination :one
SELECT * FROM storage_destinations WHERE id = $1;

-- name: GetStorageDestinationByName :one
SELECT * FROM storage_destinations WHERE name = $1;

-- name: ListStorageDestinations :many
SELECT * FROM storage_destinations ORDER BY name ASC;

-- name: ListEnabledStorageDestinations :many
SELECT * FROM storage_destinations WHERE is_enabled ORDER BY name ASC;

-- name: CreateStorageDestination :one
INSERT INTO storage_destinations (name, endpoint, region, bucket, prefix, auth, is_default, is_enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: SetStorageEnabled :exec
UPDATE storage_destinations SET is_enabled = $2, updated_at = now() WHERE id = $1;

-- name: RecordStorageTest :exec
UPDATE storage_destinations
SET last_tested_at = now(), last_test_ok = $2, updated_at = now()
WHERE id = $1;

-- name: DeleteStorageDestination :exec
DELETE FROM storage_destinations WHERE id = $1;

-- name: GetSecretProfile :one
SELECT * FROM secret_profiles WHERE id = $1;

-- name: GetSecretProfileByName :one
SELECT * FROM secret_profiles WHERE name = $1;

-- name: ListSecretProfiles :many
SELECT id, name, owner_type, key_reference, description, created_by, created_at, updated_at
FROM secret_profiles ORDER BY name ASC;

-- name: CreateSecretProfile :one
INSERT INTO secret_profiles (name, owner_type, secret_encrypted, key_reference, description, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, owner_type, key_reference, description, created_by, created_at, updated_at;

-- name: DeleteSecretProfile :exec
DELETE FROM secret_profiles WHERE id = $1;
