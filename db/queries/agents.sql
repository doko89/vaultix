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

-- name: UpdateAgent :one
UPDATE agents
SET name = $2, hostname = $3, ip_address = $4, os = $5, arch = $6,
    agent_version = $7, status = $8, capabilities = $9, metadata = $10,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountAgents :one
SELECT count(*) FROM agents;

-- name: SearchAgents :many
SELECT * FROM agents
WHERE ($1 = '' OR hostname ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountSearchAgents :one
SELECT count(*) FROM agents
WHERE ($1 = '' OR hostname ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%');

-- name: GetCredentialByHash :one
SELECT * FROM agent_credentials WHERE credential_hash = $1;

-- name: ListAgentCredentials :many
SELECT * FROM agent_credentials
WHERE agent_id = $1
ORDER BY issued_at DESC;

-- name: UpdateAgentCredential :one
UPDATE agent_credentials
SET label = $2,
    expires_at = $3,
    revoked_at = CASE WHEN $4::bool THEN coalesce(revoked_at, now()) ELSE NULL END,
    last_used_at = COALESCE($5, last_used_at)
WHERE id = $1
RETURNING *;

-- name: UpdateAgentHeartbeatStats :exec
UPDATE agents
SET metadata = coalesce(metadata, '{}'::jsonb) || $2::jsonb, updated_at = now()
WHERE id = $1;
