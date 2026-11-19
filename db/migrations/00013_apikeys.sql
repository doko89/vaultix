-- Vaultix MVP schema: user API keys (CLI/programmatic access).
-- Clients present `vxk_<32 random>`; only the SHA-256 hash is stored here
-- (key_hash). key_prefix holds the first 8 chars of the presented key for
-- display/lookup; scopes is a subset of {read,write,admin} enforced by
-- CHECK and re-validated in usecase/apikey.

-- +goose Up
CREATE TABLE api_keys (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    key_hash    BYTEA       NOT NULL UNIQUE,
    key_prefix  TEXT        NOT NULL,
    scopes      TEXT[]      NOT NULL DEFAULT '{}',
    expires_at  TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT api_keys_scopes_check CHECK (scopes <@ ARRAY['read', 'write', 'admin'])
);
CREATE INDEX idx_api_keys_user_id ON api_keys (user_id);
CREATE INDEX idx_api_keys_key_hash ON api_keys (key_hash);

-- +goose Down
DROP TABLE IF EXISTS api_keys;
