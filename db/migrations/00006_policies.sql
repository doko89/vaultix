-- Vaultix MVP schema: backup policies, versions, targets (PRD §7.4).
-- Every executed job records (policy_id, policy_version); the version row holds
-- the exact config snapshot so later edits never rewrite backup history.

-- +goose Up
CREATE TABLE backup_policies (
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   TEXT        NOT NULL UNIQUE,
    description            TEXT        NOT NULL DEFAULT '',
    agent_id               UUID        REFERENCES agents (id) ON DELETE SET NULL,
    storage_destination_id UUID        REFERENCES storage_destinations (id) ON DELETE RESTRICT,
    secret_profile_id      UUID        REFERENCES secret_profiles (id) ON DELETE SET NULL,
    compression            TEXT        NOT NULL DEFAULT 'zstd',
    compression_level      INTEGER     NOT NULL DEFAULT 3,
    encryption_enabled     BOOLEAN     NOT NULL DEFAULT FALSE,
    encryption_key_reference TEXT      NOT NULL DEFAULT '',
    is_enabled             BOOLEAN     NOT NULL DEFAULT TRUE,
    current_version        INTEGER     NOT NULL DEFAULT 1,
    created_by             UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_backup_policies_agent_id ON backup_policies (agent_id);
CREATE INDEX idx_backup_policies_enabled ON backup_policies (is_enabled) WHERE is_enabled;

CREATE TABLE backup_policy_versions (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id  UUID        NOT NULL REFERENCES backup_policies (id) ON DELETE CASCADE,
    version    INTEGER     NOT NULL,
    config     JSONB       NOT NULL,
    created_by UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (policy_id, version)
);
CREATE INDEX idx_backup_policy_versions_policy_id ON backup_policy_versions (policy_id, version DESC);

CREATE TABLE backup_policy_targets (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id     UUID        NOT NULL REFERENCES backup_policies (id) ON DELETE CASCADE,
    target_type   TEXT        NOT NULL,
    target_ref    TEXT        NOT NULL DEFAULT '',
    target_config JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (policy_id, target_type, target_ref),
    CONSTRAINT chk_backup_policy_targets_type CHECK (
        target_type IN ('host_path', 'container_meta', 'volume', 'bind_mount', 'postgres', 'mysql', 'mssql', 'redis', 'stack')
    )
);
CREATE INDEX idx_backup_policy_targets_policy_id ON backup_policy_targets (policy_id);

-- +goose Down
DROP TABLE IF EXISTS backup_policy_targets;
DROP TABLE IF EXISTS backup_policy_versions;
DROP TABLE IF EXISTS backup_policies;
