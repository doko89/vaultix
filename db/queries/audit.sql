-- Audit logs (PRD §7.16). Append-only; no update/delete queries by design.

-- name: CreateAuditEvent :one
INSERT INTO audit_logs (actor_id, actor_type, actor_label, action, target_type, target_id, outcome, ip_address, details)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListAuditEvents :many
SELECT * FROM audit_logs
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListAuditEventsByActor :many
SELECT * FROM audit_logs
WHERE actor_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEventsByAction :many
SELECT * FROM audit_logs
WHERE action = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEventsByTarget :many
SELECT * FROM audit_logs
WHERE target_type = $1 AND target_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListAuditEventsFiltered :many
SELECT * FROM audit_logs
WHERE ($1::uuid IS NULL OR actor_id = $1)
  AND ($2 = '' OR action = $2)
  AND ($3::timestamptz IS NULL OR created_at >= $3)
  AND ($4::timestamptz IS NULL OR created_at <= $4)
ORDER BY created_at DESC
LIMIT $5 OFFSET $6;

-- name: CountAuditEventsFiltered :one
SELECT count(*) FROM audit_logs
WHERE ($1::uuid IS NULL OR actor_id = $1)
  AND ($2 = '' OR action = $2)
  AND ($3::timestamptz IS NULL OR created_at >= $3)
  AND ($4::timestamptz IS NULL OR created_at <= $4);
