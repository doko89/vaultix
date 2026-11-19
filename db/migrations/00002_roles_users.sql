-- Vaultix MVP schema: roles, users, user_roles.
-- Seeds the three PRD §5 roles (administrator, operator, readonly).

-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    permissions JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT,
    display_name  TEXT        NOT NULL DEFAULT '',
    is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
    disabled_at   TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_email_lower ON users (lower(email));
CREATE INDEX idx_users_is_active ON users (is_active) WHERE is_active;

CREATE TABLE user_roles (
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id    UUID        NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by UUID        REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX idx_user_roles_role_id ON user_roles (role_id);

INSERT INTO roles (name, description, permissions) VALUES
    ('administrator', 'Full administrative access', '["*"]'::jsonb),
    ('operator', 'Operate backups, view configuration', '["agents:read","policies:read","jobs:read","jobs:execute","snapshots:read","restore:execute","notifications:read"]'::jsonb),
    ('readonly', 'Read-only dashboard access', '["agents:read","policies:read","jobs:read","snapshots:read"]'::jsonb);

-- +goose Down
DELETE FROM roles WHERE name IN ('administrator', 'operator', 'readonly');
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS roles;
