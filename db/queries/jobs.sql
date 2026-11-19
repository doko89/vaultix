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

-- name: UpdateBackupJob :one
UPDATE backup_jobs
SET state = $2, progress_pct = $3, bytes_read = $4, bytes_compressed = $5,
    bytes_uploaded = $6, encryption_enabled = $7, encryption_key_reference = $8,
    target_resources = $9, error_code = $10, error_message = $11,
    attempts = $12, started_at = $13, finished_at = $14, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListBackupJobsFiltered :many
SELECT * FROM backup_jobs
WHERE ($1::uuid IS NULL OR agent_id = $1)
  AND ($2::uuid IS NULL OR policy_id = $2)
  AND ($3 = '' OR state = $3)
ORDER BY created_at DESC
LIMIT $4 OFFSET $5;

-- name: CountBackupJobsFiltered :one
SELECT count(*) FROM backup_jobs
WHERE ($1::uuid IS NULL OR agent_id = $1)
  AND ($2::uuid IS NULL OR policy_id = $2)
  AND ($3 = '' OR state = $3);

-- name: ClaimNextBackupJob :one
UPDATE backup_jobs AS bj
SET state = 'assigned', updated_at = now()
WHERE bj.id = (
    SELECT b.id FROM backup_jobs AS b
    WHERE b.state = 'queued' AND b.agent_id = $1
    ORDER BY b.queued_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: UpdateRestoreJob :one
UPDATE restore_jobs
SET state = $2, progress_pct = $3, error_code = $4, error_message = $5,
    destination_path = $6, source_paths = $7, started_at = $8, finished_at = $9,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListRestoreJobsFiltered :many
SELECT * FROM restore_jobs
WHERE ($1::uuid IS NULL OR agent_id = $1)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountRestoreJobsFiltered :one
SELECT count(*) FROM restore_jobs
WHERE ($1::uuid IS NULL OR agent_id = $1);
