-- Notifications outbox (PRD §7.16). Worker claims pending rows, sends, marks sent/failed.

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = $1;

-- name: ListPendingNotifications :many
SELECT * FROM notifications
WHERE state = 'pending' AND scheduled_at <= now()
ORDER BY scheduled_at ASC
LIMIT $1;

-- name: EnqueueNotification :one
INSERT INTO notifications (channel, recipient, event_type, subject, body, related_type, related_id, scheduled_at, headers, body_raw)
VALUES ($1, $2, $3, $4, $5, $6, $7, coalesce($8, now()), $9, $10)
RETURNING *;

-- name: MarkNotificationSent :exec
UPDATE notifications SET state = 'sent', sent_at = now() WHERE id = $1;

-- name: MarkNotificationFailed :exec
UPDATE notifications SET state = 'failed', attempts = attempts + 1, last_error = $2 WHERE id = $1;

-- name: RequeueNotification :exec
UPDATE notifications
SET state = 'pending', attempts = attempts + 1, last_error = $2, scheduled_at = $3
WHERE id = $1;

-- name: UpdateNotification :exec
UPDATE notifications
SET recipient = $2, subject = $3, body = $4, state = $5,
    attempts = $6, last_error = $7, sent_at = $8
WHERE id = $1;

-- name: CreateNotificationRule :one
INSERT INTO notification_rules (name, events, channel, recipient, headers, body_template, enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetNotificationRule :one
SELECT * FROM notification_rules WHERE id = $1;

-- name: ListNotificationRules :many
SELECT * FROM notification_rules ORDER BY created_at ASC;

-- name: UpdateNotificationRule :one
UPDATE notification_rules
SET name = $2, events = $3, channel = $4, recipient = $5,
    headers = $6, body_template = $7, enabled = $8, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteNotificationRule :execrows
DELETE FROM notification_rules WHERE id = $1;
