-- Vaultix MVP schema: discovered resources, storage destinations, secret profiles
-- (PRD §7.3 discovery, §7.12 storage, §7.7 secrets).

-- +goose Up
CREATE TABLE discovered_resources (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id      UUID        NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    resource_type TEXT        NOT NULL,
    resource_key  TEXT        NOT NULL,
    display_name  TEXT        NOT NULL DEFAULT '',
    details       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, resource_type, resource_key),
    CONSTRAINT chk_discovered_resources_type CHECK (
        resource_type IN ('host_path', 'container', 'volume', 'bind_mount', 'database', 'compose_project')
    )
);
CREATE INDEX idx_discovered_resources_agent_type ON discovered_resources (agent_id, resource_type);

CREATE TABLE storage_destinations (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT        NOT NULL UNIQUE,
    endpoint      TEXT        NOT NULL,
    region        TEXT        NOT NULL DEFAULT '',
    bucket        TEXT        NOT NULL,
    prefix        TEXT        NOT NULL DEFAULT '',
    auth          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    is_default    BOOLEAN     NOT NULL DEFAULT FALSE,
    is_enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    last_tested_at TIMESTAMPTZ,
    last_test_ok  BOOLEAN,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_storage_destinations_enabled ON storage_destinations (is_enabled) WHERE is_enabled;

CREATE TABLE secret_profiles (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL UNIQUE,
    owner_type       TEXT        NOT NULL,
    secret_encrypted TEXT        NOT NULL,
    key_reference    TEXT        NOT NULL DEFAULT '',
    description      TEXT        NOT NULL DEFAULT '',
    created_by       UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_secret_profiles_owner CHECK (
        owner_type IN ('database', 'storage', 'env')
    )
);

-- +goose Down
DROP TABLE IF EXISTS secret_profiles;
DROP TABLE IF EXISTS storage_destinations;
DROP TABLE IF EXISTS discovered_resources;
