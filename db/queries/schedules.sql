-- Schedules (PRD §7.9). MVP: once + hourly/daily/weekly recurrence.

-- name: GetScheduleByID :one
SELECT * FROM schedules WHERE id = $1;

-- name: ListSchedulesByPolicy :many
SELECT * FROM schedules WHERE policy_id = $1 ORDER BY created_at ASC;

-- name: ListAllSchedules :many
SELECT * FROM schedules ORDER BY created_at ASC;

-- name: ListDueSchedules :many
SELECT * FROM schedules
WHERE NOT is_paused AND next_run_at IS NOT NULL AND next_run_at <= now()
ORDER BY next_run_at ASC
LIMIT $1;

-- name: CreateSchedule :one
INSERT INTO schedules (
    policy_id, name, schedule_type, cron_expression, interval_minutes,
    timezone, next_run_at, concurrency_limit
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: SetSchedulePaused :one
UPDATE schedules SET is_paused = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RecordScheduleRun :exec
UPDATE schedules SET last_run_at = now(), next_run_at = $2, updated_at = now()
WHERE id = $1;

-- name: DeleteSchedule :exec
DELETE FROM schedules WHERE id = $1;

-- name: UpdateSchedule :one
UPDATE schedules
SET schedule_type = $2, cron_expression = $3, interval_minutes = $4,
    timezone = $5, next_run_at = $6, is_paused = $7, concurrency_limit = $8,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListDueSchedulesBefore :many
SELECT * FROM schedules
WHERE NOT is_paused AND next_run_at IS NOT NULL AND next_run_at <= $1
ORDER BY next_run_at ASC
LIMIT $2;
