-- Vaultix agent updates: manager-hosted agent binaries plus per-agent
-- pending-update state. Agents pull (never pushed): the poll response
-- advertises a pending target, the agent downloads bytes from
-- /api/agent/update/download, verifies sha256, swaps, restarts, and
-- reports. Bytes live in-DB (bytea) so the manager stays a single
-- deployable with no artifact volume to back up separately.

-- +goose Up
CREATE TABLE agent_artifacts (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    version     TEXT        NOT NULL,
    goos        TEXT        NOT NULL,
    goarch      TEXT        NOT NULL,
    sha256      BYTEA       NOT NULL,
    size_bytes  BIGINT      NOT NULL CONSTRAINT agent_artifacts_size_check CHECK (size_bytes > 0),
    bytes       BYTEA       NOT NULL,
    uploaded_by TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_artifacts_version_platform_unique UNIQUE (version, goos, goarch)
);
CREATE INDEX idx_agent_artifacts_platform ON agent_artifacts (goos, goarch);

-- One pending-update slot per agent: requesting again retargets.
-- terminal states (applied/failed) stay readable until superseded.
CREATE TABLE agent_updates (
    agent_id        UUID        PRIMARY KEY REFERENCES agents (id) ON DELETE CASCADE,
    target_version  TEXT        NOT NULL,
    goos            TEXT        NOT NULL,
    goarch          TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending'
                    CONSTRAINT agent_updates_status_check
                    CHECK (status IN ('pending', 'applied', 'failed')),
    error           TEXT        NOT NULL DEFAULT '',
    requested_by    TEXT        NOT NULL DEFAULT '',
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS agent_updates;
DROP TABLE IF EXISTS agent_artifacts;
