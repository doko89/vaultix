-- Agent update artifacts + per-agent pending-update state.

-- name: CreateAgentArtifact :one
INSERT INTO agent_artifacts (version, goos, goarch, sha256, size_bytes, bytes, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListAgentArtifacts :many
SELECT id, version, goos, goarch, sha256, size_bytes, uploaded_by, created_at
FROM agent_artifacts
ORDER BY created_at DESC;

-- name: GetAgentArtifact :one
SELECT * FROM agent_artifacts WHERE id = $1;

-- name: ListPlatformArtifacts :many
SELECT id, version, goos, goarch, sha256, size_bytes, uploaded_by, created_at
FROM agent_artifacts
WHERE goos = $1 AND goarch = $2
ORDER BY created_at DESC;

-- name: DeleteAgentArtifact :execrows
DELETE FROM agent_artifacts WHERE id = $1;

-- name: UpsertAgentUpdate :one
INSERT INTO agent_updates (agent_id, target_version, goos, goarch, status, error, requested_by, requested_at, updated_at)
VALUES ($1, $2, $3, $4, 'pending', '', $5, now(), now())
ON CONFLICT (agent_id) DO UPDATE SET
    target_version = EXCLUDED.target_version,
    goos = EXCLUDED.goos,
    goarch = EXCLUDED.goarch,
    status = 'pending',
    error = '',
    requested_by = EXCLUDED.requested_by,
    requested_at = now(),
    updated_at = now()
RETURNING *;

-- name: GetAgentUpdate :one
SELECT * FROM agent_updates WHERE agent_id = $1;

-- name: SetAgentUpdateStatus :exec
UPDATE agent_updates
SET status = $2, error = $3, updated_at = now()
WHERE agent_id = $1;
