-- Backup jobs + restore jobs (PRD §7.10, §7.13).
-- Hot lookups: by (policy_id, state), by (agent_id, state), by state.

-- name: GetBackupJob :one
SELECT * FROM backup_jobs WHERE id = $1;

-- name: ListBackupJobs :many
SELECT * FROM backup_jobs
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListBackupJobsByPolicy :many
SELECT * FROM backup_jobs
WHERE policy_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListBackupJobsByPolicyAndState :many
SELECT * FROM backup_jobs
WHERE policy_id = $1 AND state = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListBackupJobsByAgentAndState :many
SELECT * FROM backup_jobs
WHERE agent_id = $1 AND state = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListBackupJobsByState :many
SELECT * FROM backup_jobs
WHERE state = $1
ORDER BY queued_at ASC
LIMIT $2 OFFSET $3;

-- name: ListActiveBackupJobsByAgent :many
SELECT * FROM backup_jobs
WHERE agent_id = $1 AND state IN ('queued', 'assigned', 'running', 'uploading', 'verifying')
ORDER BY queued_at ASC;

-- name: CreateBackupJob :one
INSERT INTO backup_jobs (
    agent_id, policy_id, policy_version, policy_version_id, schedule_id,
    target_resources, encryption_enabled, encryption_key_reference
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: SetBackupJobState :one
UPDATE backup_jobs
SET state = $2,
    started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'cancelled') THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateBackupJobProgress :exec
UPDATE backup_jobs
SET progress_pct = $2, bytes_read = $3, bytes_compressed = $4,
    bytes_uploaded = $5, updated_at = now()
WHERE id = $1;

-- name: FailBackupJob :one
UPDATE backup_jobs
SET state = 'failed', error_code = $2, error_message = $3,
    attempts = attempts + 1, finished_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetRestoreJob :one
SELECT * FROM restore_jobs WHERE id = $1;

-- name: ListRestoreJobs :many
SELECT * FROM restore_jobs
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListRestoreJobsBySnapshot :many
SELECT * FROM restore_jobs
WHERE snapshot_id = $1
ORDER BY created_at DESC;

-- name: ListRestoreJobsByAgentAndState :many
SELECT * FROM restore_jobs
WHERE agent_id = $1 AND state = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CreateRestoreJob :one
INSERT INTO restore_jobs (
    agent_id, snapshot_id, restore_type, source_paths, destination_path, requested_by
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ConfirmRestoreJob :one
UPDATE restore_jobs SET confirmed_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetRestoreJobState :one
UPDATE restore_jobs
SET state = $2,
    started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'cancelled') THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: FailRestoreJob :one
UPDATE restore_jobs
SET state = 'failed', error_code = $2, error_message = $3,
    attempts = attempts + 1, finished_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;
