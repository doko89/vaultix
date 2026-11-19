-- Backup policies, versions, targets (PRD §7.4).

-- name: GetPolicyByID :one
SELECT * FROM backup_policies WHERE id = $1;

-- name: GetPolicyByName :one
SELECT * FROM backup_policies WHERE name = $1;

-- name: ListPolicies :many
SELECT * FROM backup_policies
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListPoliciesByAgent :many
SELECT * FROM backup_policies
WHERE agent_id = $1
ORDER BY name ASC;

-- name: ListEnabledPolicies :many
SELECT * FROM backup_policies WHERE is_enabled ORDER BY name ASC;

-- name: CreatePolicy :one
INSERT INTO backup_policies (
    name, description, agent_id, storage_destination_id, secret_profile_id,
    compression, compression_level, encryption_enabled, encryption_key_reference,
    is_enabled, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: SetPolicyEnabled :one
UPDATE backup_policies SET is_enabled = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: BumpPolicyVersion :one
UPDATE backup_policies SET current_version = current_version + 1, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeletePolicy :exec
DELETE FROM backup_policies WHERE id = $1;

-- name: GetPolicyVersion :one
SELECT * FROM backup_policy_versions WHERE policy_id = $1 AND version = $2;

-- name: GetPolicyVersionByID :one
SELECT * FROM backup_policy_versions WHERE id = $1;

-- name: ListPolicyVersions :many
SELECT * FROM backup_policy_versions
WHERE policy_id = $1
ORDER BY version DESC
LIMIT $2 OFFSET $3;

-- name: CreatePolicyVersion :one
INSERT INTO backup_policy_versions (policy_id, version, config, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListPolicyTargets :many
SELECT * FROM backup_policy_targets
WHERE policy_id = $1
ORDER BY target_type ASC, target_ref ASC;

-- name: AddPolicyTarget :one
INSERT INTO backup_policy_targets (policy_id, target_type, target_ref, target_config)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeletePolicyTarget :exec
DELETE FROM backup_policy_targets WHERE id = $1;

-- name: DeletePolicyTargets :exec
DELETE FROM backup_policy_targets WHERE policy_id = $1;

-- name: UpdatePolicy :one
UPDATE backup_policies
SET name = $2, description = $3, agent_id = $4,
    storage_destination_id = $5, compression = $6, compression_level = $7,
    encryption_enabled = $8, encryption_key_reference = $9,
    is_enabled = $10, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountPolicies :one
SELECT count(*) FROM backup_policies;

-- name: SearchPolicies :many
SELECT * FROM backup_policies
WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountSearchPolicies :one
SELECT count(*) FROM backup_policies
WHERE ($1 = '' OR name ILIKE '%' || $1 || '%');
