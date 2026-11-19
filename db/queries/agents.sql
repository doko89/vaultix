-- Agents, credentials, heartbeats (MVP CRUD + hot lookups).

-- name: GetAgentByID :one
SELECT * FROM agents WHERE id = $1;

-- name: ListAgents :many
SELECT * FROM agents
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAgentsByStatus :many
SELECT * FROM agents
WHERE status = $1
ORDER BY last_heartbeat_at DESC NULLS LAST
LIMIT $2 OFFSET $3;

-- name: CreateAgent :one
INSERT INTO agents (name, hostname, ip_address, os, arch, enrollment_token_hash)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAgentByEnrollmentTokenHash :one
SELECT * FROM agents WHERE enrollment_token_hash = $1;

-- name: MarkAgentEnrolled :one
UPDATE agents
SET status = 'online', enrolled_at = now(), enrollment_token_hash = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAgentStatus :one
UPDATE agents SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateAgentHeartbeatPointer :exec
UPDATE agents
SET last_heartbeat_at = now(), ip_address = $2, agent_version = $3,
    status = CASE WHEN status = 'pending' THEN 'online' ELSE status END,
    updated_at = now()
WHERE id = $1;

-- name: UpdateAgentVersion :exec
UPDATE agents SET agent_version = $2, updated_at = now() WHERE id = $1;

-- name: CreateAgentCredential :one
INSERT INTO agent_credentials (agent_id, credential_hash, label, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListActiveAgentCredentials :many
SELECT * FROM agent_credentials
WHERE agent_id = $1 AND revoked_at IS NULL
ORDER BY issued_at DESC;

-- name: RevokeAgentCredential :exec
UPDATE agent_credentials SET revoked_at = now() WHERE id = $1;

-- name: RevokeAllAgentCredentials :exec
UPDATE agent_credentials SET revoked_at = now() WHERE agent_id = $1 AND revoked_at IS NULL;

-- name: TouchCredentialUsed :exec
UPDATE agent_credentials SET last_used_at = now() WHERE id = $1;

-- name: RecordHeartbeat :one
INSERT INTO agent_heartbeats (
    agent_id, status, agent_version, ip_address,
    disk_total_bytes, disk_free_bytes, running_jobs, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetLatestHeartbeat :one
SELECT * FROM agent_heartbeats
WHERE agent_id = $1
ORDER BY received_at DESC
LIMIT 1;

-- name: ListRecentHeartbeats :many
SELECT * FROM agent_heartbeats
WHERE agent_id = $1
ORDER BY received_at DESC
LIMIT $2;

-- name: DeleteOldHeartbeats :execrows
DELETE FROM agent_heartbeats WHERE received_at < $1;
