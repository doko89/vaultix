-- Vaultix MVP schema: retention rules, notifications, audit logs
-- (PRD §7.14 retention, §7.16 notifications + audit).

-- +goose Up
CREATE TABLE retention_rules (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id        UUID        NOT NULL UNIQUE REFERENCES backup_policies (id) ON DELETE CASCADE,
    keep_last_n      INTEGER,
    keep_days        INTEGER,
    protect_last_valid BOOLEAN  NOT NULL DEFAULT TRUE,
    last_applied_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_retention_rules_nonempty CHECK (
        keep_last_n IS NOT NULL OR keep_days IS NOT NULL
    )
);

CREATE TABLE notifications (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel       TEXT        NOT NULL,
    recipient     TEXT        NOT NULL,
    event_type    TEXT        NOT NULL,
    subject       TEXT        NOT NULL DEFAULT '',
    body          TEXT        NOT NULL DEFAULT '',
    state         TEXT        NOT NULL DEFAULT 'pending',
    attempts      INTEGER     NOT NULL DEFAULT 0,
    last_error    TEXT,
    related_type  TEXT,
    related_id    UUID,
    scheduled_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_notifications_channel CHECK (channel IN ('email', 'webhook')),
    CONSTRAINT chk_notifications_state CHECK (state IN ('pending', 'sent', 'failed'))
);
CREATE INDEX idx_notifications_state_scheduled ON notifications (state, scheduled_at ASC);
CREATE INDEX idx_notifications_event_type ON notifications (event_type);

CREATE TABLE audit_logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id    UUID        REFERENCES users (id) ON DELETE SET NULL,
    actor_type  TEXT        NOT NULL DEFAULT 'user',
    actor_label TEXT        NOT NULL DEFAULT '',
    action      TEXT        NOT NULL,
    target_type TEXT,
    target_id   TEXT,
    outcome     TEXT        NOT NULL DEFAULT 'success',
    ip_address  TEXT        NOT NULL DEFAULT '',
    details     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_audit_logs_actor CHECK (actor_type IN ('user', 'agent', 'system')),
    CONSTRAINT chk_audit_logs_outcome CHECK (outcome IN ('success', 'denied', 'error'))
);
CREATE INDEX idx_audit_logs_actor_created ON audit_logs (actor_id, created_at DESC);
CREATE INDEX idx_audit_logs_action_created ON audit_logs (action, created_at DESC);
CREATE INDEX idx_audit_logs_created ON audit_logs (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS retention_rules;
