-- Snapshots, manifests, objects (PRD §7.13 catalog).
-- Hot lookups: by (policy_id, snapshot_time DESC), by (agent_id, snapshot_time DESC).
-- A snapshot is restorable only after manifest + objects pass integrity checks.

-- name: GetSnapshot :one
SELECT * FROM snapshots WHERE id = $1;

-- name: GetSnapshotByJob :one
SELECT * FROM snapshots WHERE backup_job_id = $1;

-- name: ListSnapshots :many
SELECT * FROM snapshots
ORDER BY snapshot_time DESC
LIMIT $1 OFFSET $2;

-- name: ListSnapshotsByPolicy :many
SELECT * FROM snapshots
WHERE policy_id = $1
ORDER BY snapshot_time DESC
LIMIT $2 OFFSET $3;

-- name: ListSnapshotsByAgent :many
SELECT * FROM snapshots
WHERE agent_id = $1
ORDER BY snapshot_time DESC
LIMIT $2 OFFSET $3;

-- name: ListValidSnapshotsByPolicy :many
SELECT * FROM snapshots
WHERE policy_id = $1 AND state = 'valid'
ORDER BY snapshot_time DESC
LIMIT $2 OFFSET $3;

-- name: CreateSnapshot :one
INSERT INTO snapshots (
    agent_id, policy_id, policy_version, backup_job_id, name,
    encryption_enabled, encryption_key_reference,
    storage_destination_id, storage_prefix
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: SetSnapshotState :one
UPDATE snapshots
SET state = $2,
    verified_at = CASE WHEN $2 = 'valid' THEN now() ELSE verified_at END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RecordSnapshotStats :exec
UPDATE snapshots
SET bytes_stored = $2, file_count = $3, updated_at = now()
WHERE id = $1;

-- name: SetSnapshotExpiry :exec
UPDATE snapshots SET expires_at = $2, updated_at = now() WHERE id = $1;

-- name: ListExpiredSnapshots :many
SELECT * FROM snapshots
WHERE state = 'valid' AND expires_at IS NOT NULL AND expires_at <= now()
ORDER BY expires_at ASC
LIMIT $1;

-- name: DeleteSnapshot :exec
DELETE FROM snapshots WHERE id = $1;

-- name: GetManifestBySnapshot :one
SELECT * FROM snapshot_manifests WHERE snapshot_id = $1;

-- name: CreateManifest :one
INSERT INTO snapshot_manifests (snapshot_id, manifest_hash, manifest_size_bytes, storage_key, content)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListObjectsBySnapshot :many
SELECT * FROM snapshot_objects
WHERE snapshot_id = $1
ORDER BY part_index ASC;

-- name: AddSnapshotObject :one
INSERT INTO snapshot_objects (snapshot_id, object_key, size_bytes, checksum, checksum_alg, part_index)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CountSnapshotObjects :one
SELECT count(*) AS object_count, coalesce(sum(size_bytes), 0)::bigint AS total_bytes
FROM snapshot_objects WHERE snapshot_id = $1;
