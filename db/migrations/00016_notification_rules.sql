-- Notification rules become durable (previously in-memory only) and gain
-- per-rule webhook headers plus a body template. The outbox carries the
-- resolved headers per notification so delivery needs no rule lookup.

-- +goose Up
CREATE TABLE notification_rules (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT        NOT NULL,
    events        TEXT[]      NOT NULL DEFAULT '{}',
    channel       TEXT        NOT NULL,
    recipient     TEXT        NOT NULL,
    headers       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    body_template TEXT        NOT NULL DEFAULT '',
    enabled       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE notifications ADD COLUMN headers JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE notifications ADD COLUMN body_raw BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE notifications DROP COLUMN IF EXISTS headers;
ALTER TABLE notifications DROP COLUMN IF EXISTS body_raw;
DROP TABLE IF EXISTS notification_rules;
