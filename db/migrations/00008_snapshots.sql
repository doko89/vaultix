-- Vaultix MVP schema: snapshots, manifests, objects, restore jobs
-- (PRD §7.13 catalog + restore).
-- Hot lookups: snapshots by (policy_id, snapshot_time DESC),
-- by (agent_id, snapshot_time DESC); one manifest per snapshot.

-- +goose Up
CREATE TABLE snapshots (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id                 UUID        REFERENCES agents (id) ON DELETE SET NULL,
    policy_id                UUID        REFERENCES backup_policies (id) ON DELETE SET NULL,
    policy_version           INTEGER,
    backup_job_id            UUID        UNIQUE REFERENCES backup_jobs (id) ON DELETE SET NULL,
    name                     TEXT        NOT NULL DEFAULT '',
    snapshot_time            TIMESTAMPTZ NOT NULL DEFAULT now(),
    state                    TEXT        NOT NULL DEFAULT 'pending',
    bytes_stored             BIGINT      NOT NULL DEFAULT 0,
    file_count               BIGINT      NOT NULL DEFAULT 0,
    encryption_enabled       BOOLEAN     NOT NULL DEFAULT FALSE,
    encryption_key_reference TEXT        NOT NULL DEFAULT '',
    storage_destination_id   UUID        REFERENCES storage_destinations (id) ON DELETE SET NULL,
    storage_prefix           TEXT        NOT NULL DEFAULT '',
    verified_at              TIMESTAMPTZ,
    expires_at               TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_snapshots_state CHECK (
        state IN ('pending', 'verifying', 'valid', 'invalid', 'expired', 'deleted')
    )
);
CREATE INDEX idx_snapshots_policy_time ON snapshots (policy_id, snapshot_time DESC);
CREATE INDEX idx_snapshots_agent_time ON snapshots (agent_id, snapshot_time DESC);
CREATE INDEX idx_snapshots_state ON snapshots (state);

CREATE TABLE snapshot_manifests (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    snapshot_id       UUID        NOT NULL UNIQUE REFERENCES snapshots (id) ON DELETE CASCADE,
    manifest_hash     TEXT        NOT NULL,
    manifest_size_bytes BIGINT    NOT NULL DEFAULT 0,
    storage_key       TEXT        NOT NULL,
    content           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE snapshot_objects (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    snapshot_id  UUID        NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
    object_key   TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    checksum     TEXT        NOT NULL DEFAULT '',
    checksum_alg TEXT        NOT NULL DEFAULT 'sha256',
    part_index   INTEGER     NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (snapshot_id, object_key)
);
CREATE INDEX idx_snapshot_objects_snapshot_id ON snapshot_objects (snapshot_id);

CREATE TABLE restore_jobs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id        UUID        REFERENCES agents (id) ON DELETE SET NULL,
    snapshot_id     UUID        REFERENCES snapshots (id) ON DELETE SET NULL,
    restore_type    TEXT        NOT NULL,
    source_paths    JSONB       NOT NULL DEFAULT '[]'::jsonb,
    destination_path TEXT,
    state           TEXT        NOT NULL DEFAULT 'queued',
    progress_pct    INTEGER     NOT NULL DEFAULT 0,
    error_code      TEXT,
    error_message   TEXT,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    requested_by    UUID        REFERENCES users (id) ON DELETE SET NULL,
    confirmed_at    TIMESTAMPTZ,
    queued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_restore_jobs_type CHECK (
        restore_type IN ('file', 'directory', 'database', 'volume', 'stack')
    ),
    CONSTRAINT chk_restore_jobs_state CHECK (
        state IN ('queued', 'assigned', 'running', 'downloading', 'restoring', 'verifying', 'succeeded', 'failed', 'cancelled')
    )
);
CREATE INDEX idx_restore_jobs_agent_state ON restore_jobs (agent_id, state);
CREATE INDEX idx_restore_jobs_snapshot_id ON restore_jobs (snapshot_id);
CREATE INDEX idx_restore_jobs_state ON restore_jobs (state);

-- +goose Down
DROP TABLE IF EXISTS restore_jobs;
DROP TABLE IF EXISTS snapshot_objects;
DROP TABLE IF EXISTS snapshot_manifests;
DROP TABLE IF EXISTS snapshots;
