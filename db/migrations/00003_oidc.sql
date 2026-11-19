-- Vaultix MVP schema: OIDC providers + external identities (PRD §7.1.1).
-- Client secrets are stored encrypted; read APIs must never return them.

-- +goose Up
CREATE TABLE oidc_providers (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    TEXT        NOT NULL UNIQUE,
    issuer                  TEXT        NOT NULL,
    discovery_url           TEXT,
    endpoints               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    client_id               TEXT        NOT NULL,
    client_secret_encrypted TEXT,
    scopes                  TEXT        NOT NULL DEFAULT 'openid profile email',
    claims_mapping          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    default_role_id         UUID        REFERENCES roles (id) ON DELETE SET NULL,
    is_enabled              BOOLEAN     NOT NULL DEFAULT TRUE,
    allow_jit_provisioning  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_oidc_providers_enabled ON oidc_providers (is_enabled) WHERE is_enabled;

CREATE TABLE user_external_identities (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider_id   UUID        REFERENCES oidc_providers (id) ON DELETE SET NULL,
    issuer        TEXT        NOT NULL,
    subject       TEXT        NOT NULL,
    email         TEXT        NOT NULL DEFAULT '',
    claims        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    is_disabled   BOOLEAN     NOT NULL DEFAULT FALSE,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);
CREATE INDEX idx_user_external_identities_user_id ON user_external_identities (user_id);
CREATE INDEX idx_user_external_identities_provider ON user_external_identities (provider_id);

-- +goose Down
DROP TABLE IF EXISTS user_external_identities;
DROP TABLE IF EXISTS oidc_providers;
