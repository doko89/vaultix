-- Vaultix MVP schema: application stacks + stack resources (PRD §7.8).
-- A stack groups related resources on one agent into a logical backup unit;
-- members carry an explicit restore order (lower first). ResourceID is an
-- opaque reference (no FK): the selector pins the exact backup target when
-- the discovered resource no longer exists.

-- +goose Up
CREATE TABLE application_stacks (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID        NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    compose_path TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, name)
);
CREATE INDEX idx_application_stacks_agent_id ON application_stacks (agent_id);

CREATE TABLE application_stack_resources (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    stack_id      UUID        NOT NULL REFERENCES application_stacks (id) ON DELETE CASCADE,
    resource_id   TEXT        NOT NULL DEFAULT '',
    selector      TEXT        NOT NULL DEFAULT '',
    resource_type TEXT        NOT NULL DEFAULT '',
    restore_order INTEGER     NOT NULL DEFAULT 0,
    required      BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_application_stack_resources_type CHECK (
        resource_type IN ('host_filesystem', 'docker_config', 'docker_volume', 'postgres', 'mysql', 'mssql', 'redis', 'app_stack', '')
    )
);
CREATE INDEX idx_application_stack_resources_stack_id ON application_stack_resources (stack_id, restore_order);

-- +goose Down
DROP TABLE IF EXISTS application_stack_resources;
DROP TABLE IF EXISTS application_stacks;
