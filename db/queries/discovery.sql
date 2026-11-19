-- Discovered resources: agent-reported host/Docker inventory (PRD §7.3).
-- Discovery is informational; nothing here is backed up without a policy.

-- name: GetDiscoveredResource :one
SELECT * FROM discovered_resources WHERE id = $1;

-- name: ListResourcesByAgent :many
SELECT * FROM discovered_resources
WHERE agent_id = $1
ORDER BY resource_type ASC, display_name ASC
LIMIT $2 OFFSET $3;

-- name: ListResourcesByAgentAndType :many
SELECT * FROM discovered_resources
WHERE agent_id = $1 AND resource_type = $2
ORDER BY last_seen_at DESC
LIMIT $3 OFFSET $4;

-- name: UpsertDiscoveredResource :one
INSERT INTO discovered_resources (agent_id, resource_type, resource_key, display_name, details, last_seen_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (agent_id, resource_type, resource_key)
DO UPDATE SET display_name = EXCLUDED.display_name,
              details = EXCLUDED.details,
              last_seen_at = now(),
              updated_at = now()
RETURNING *;

-- name: DeleteStaleResources :execrows
DELETE FROM discovered_resources WHERE agent_id = $1 AND last_seen_at < $2;

-- name: DeleteAgentResources :exec
DELETE FROM discovered_resources WHERE agent_id = $1;
