-- Vaultix MVP schema: schedules + backup jobs (PRD §7.9, §7.10).
-- Hot lookups: jobs by (policy_id, state), by (agent_id, state), by state.

-- +goose Up
CREATE TABLE schedules (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id         UUID        NOT NULL REFERENCES backup_policies (id) ON DELETE CASCADE,
    name              TEXT        NOT NULL DEFAULT '',
    schedule_type     TEXT        NOT NULL,
    cron_expression   TEXT,
    interval_minutes  INTEGER,
    timezone          TEXT        NOT NULL DEFAULT 'UTC',
    next_run_at       TIMESTAMPTZ,
    last_run_at       TIMESTAMPTZ,
    is_paused         BOOLEAN     NOT NULL DEFAULT FALSE,
    concurrency_limit INTEGER     NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_schedules_type CHECK (
        schedule_type IN ('once', 'hourly', 'daily', 'weekly')
    )
);
CREATE INDEX idx_schedules_policy_id ON schedules (policy_id);
CREATE INDEX idx_schedules_next_run ON schedules (next_run_at ASC NULLS LAST) WHERE NOT is_paused;

CREATE TABLE backup_jobs (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id                 UUID        REFERENCES agents (id) ON DELETE SET NULL,
    policy_id                UUID        REFERENCES backup_policies (id) ON DELETE SET NULL,
    policy_version           INTEGER,
    policy_version_id        UUID        REFERENCES backup_policy_versions (id) ON DELETE SET NULL,
    schedule_id              UUID        REFERENCES schedules (id) ON DELETE SET NULL,
    state                    TEXT        NOT NULL DEFAULT 'queued',
    progress_pct             INTEGER     NOT NULL DEFAULT 0,
    bytes_read               BIGINT      NOT NULL DEFAULT 0,
    bytes_compressed         BIGINT      NOT NULL DEFAULT 0,
    bytes_uploaded           BIGINT      NOT NULL DEFAULT 0,
    encryption_enabled       BOOLEAN     NOT NULL DEFAULT FALSE,
    encryption_key_reference TEXT        NOT NULL DEFAULT '',
    target_resources         JSONB       NOT NULL DEFAULT '[]'::jsonb,
    error_code               TEXT,
    error_message            TEXT,
    attempts                 INTEGER     NOT NULL DEFAULT 0,
    queued_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at               TIMESTAMPTZ,
    finished_at              TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_backup_jobs_state CHECK (
        state IN ('queued', 'assigned', 'running', 'uploading', 'verifying', 'succeeded', 'failed', 'cancelled')
    )
);
CREATE INDEX idx_backup_jobs_policy_state ON backup_jobs (policy_id, state);
CREATE INDEX idx_backup_jobs_agent_state ON backup_jobs (agent_id, state);
CREATE INDEX idx_backup_jobs_state ON backup_jobs (state);
CREATE INDEX idx_backup_jobs_created_at ON backup_jobs (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS backup_jobs;
DROP TABLE IF EXISTS schedules;
