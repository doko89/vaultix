-- Retention rules (PRD §7.14). One rule per policy for MVP.

-- name: GetRetentionRule :one
SELECT * FROM retention_rules WHERE id = $1;

-- name: GetRetentionRuleByPolicy :one
SELECT * FROM retention_rules WHERE policy_id = $1;

-- name: ListRetentionRules :many
SELECT * FROM retention_rules ORDER BY created_at DESC;

-- name: UpsertRetentionRule :one
INSERT INTO retention_rules (policy_id, keep_last_n, keep_days, protect_last_valid)
VALUES ($1, $2, $3, $4)
ON CONFLICT (policy_id)
DO UPDATE SET keep_last_n = EXCLUDED.keep_last_n,
              keep_days = EXCLUDED.keep_days,
              protect_last_valid = EXCLUDED.protect_last_valid,
              updated_at = now()
RETURNING *;

-- name: RecordRetentionRun :exec
UPDATE retention_rules SET last_applied_at = now(), updated_at = now() WHERE id = $1;

-- name: DeleteRetentionRule :exec
DELETE FROM retention_rules WHERE id = $1;
