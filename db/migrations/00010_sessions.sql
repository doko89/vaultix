-- Vaultix MVP schema: server-side sessions (PRD §7.1.1).
-- Opaque random tokens are presented by clients; only the SHA-256 hash is
-- stored here. Absolute expiry + idle timeout are enforced in usecase/auth.
-- NOTE (auth track): queried via hand-written parameterized pgx in
-- internal/adapter/session — documented deviation to avoid sqlc-gen
-- conflicts; sqlc-ify once the schema stabilizes (see INTEGRATION NOTES).

-- +goose Up
CREATE TABLE sessions (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash       TEXT        NOT NULL UNIQUE,
    oidc_provider_id UUID        REFERENCES oidc_providers (id) ON DELETE SET NULL,
    id_token         TEXT,
    ip               TEXT        NOT NULL DEFAULT '',
    user_agent       TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);
CREATE INDEX idx_sessions_last_seen_at ON sessions (last_seen_at);

-- +goose Down
DROP TABLE IF EXISTS sessions;
