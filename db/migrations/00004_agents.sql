-- Vaultix MVP schema: agents, credentials, heartbeats (PRD §7.2).
-- Hot lookup: latest heartbeat per agent via (agent_id, received_at DESC).

-- +goose Up
CREATE TABLE agents (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT        NOT NULL,
    hostname            TEXT        NOT NULL,
    ip_address          TEXT        NOT NULL DEFAULT '',
    os                  TEXT        NOT NULL DEFAULT '',
    arch                TEXT        NOT NULL DEFAULT '',
    agent_version       TEXT        NOT NULL DEFAULT '',
    status              TEXT        NOT NULL DEFAULT 'pending',
    enrollment_token_hash TEXT,
    enrolled_at         TIMESTAMPTZ,
    last_heartbeat_at   TIMESTAMPTZ,
    capabilities        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    metadata            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_agents_status CHECK (status IN ('pending', 'online', 'offline', 'disabled', 'revoked'))
);
CREATE INDEX idx_agents_status ON agents (status);
CREATE INDEX idx_agents_last_heartbeat ON agents (last_heartbeat_at DESC NULLS LAST);
CREATE INDEX idx_agents_hostname ON agents (hostname);

CREATE TABLE agent_credentials (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id        UUID        NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    credential_hash TEXT        NOT NULL,
    label           TEXT        NOT NULL DEFAULT '',
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_credentials_agent_id ON agent_credentials (agent_id);
CREATE INDEX idx_agent_credentials_active ON agent_credentials (agent_id) WHERE revoked_at IS NULL;

CREATE TABLE agent_heartbeats (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id         UUID        NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT        NOT NULL DEFAULT 'online',
    agent_version    TEXT        NOT NULL DEFAULT '',
    ip_address       TEXT        NOT NULL DEFAULT '',
    disk_total_bytes BIGINT      NOT NULL DEFAULT 0,
    disk_free_bytes  BIGINT      NOT NULL DEFAULT 0,
    running_jobs     INTEGER     NOT NULL DEFAULT 0,
    payload          JSONB       NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_agent_heartbeats_agent_received ON agent_heartbeats (agent_id, received_at DESC);

-- +goose Down
DROP TABLE IF EXISTS agent_heartbeats;
DROP TABLE IF EXISTS agent_credentials;
DROP TABLE IF EXISTS agents;
