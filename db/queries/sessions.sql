-- Server-side sessions (PRD §7.1.1).
-- NOTE: these statements mirror the hand-written parameterized pgx in
-- internal/adapter/session/store.go (same NULLIF/COALESCE semantics and
-- ::uuid/::text casts) so the auth track's store can move onto sqlc once
-- the schema stabilizes. Until then the generated code is intentionally
-- unused. Do not select token hashes into logs.

-- name: CreateSession :one
INSERT INTO sessions
    (user_id, token_hash, oidc_provider_id, id_token, ip, user_agent, expires_at, last_seen_at)
VALUES ($1, $2, NULLIF($3::text,'')::uuid, NULLIF($4::text,''), $5, $6, $7, $8)
RETURNING id::text AS id, user_id::text AS user_id, token_hash,
    oidc_provider_id::text AS oidc_provider_id,
    COALESCE(id_token,'') AS id_token,
    COALESCE(ip,'') AS ip, COALESCE(user_agent,'') AS user_agent,
    created_at, expires_at, last_seen_at;

-- name: GetSessionByTokenHash :one
SELECT id::text AS id, user_id::text AS user_id, token_hash,
    oidc_provider_id::text AS oidc_provider_id,
    COALESCE(id_token,'') AS id_token,
    COALESCE(ip,'') AS ip, COALESCE(user_agent,'') AS user_agent,
    created_at, expires_at, last_seen_at
FROM sessions WHERE token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2 WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessionsExcept :exec
DELETE FROM sessions
WHERE user_id = $1 AND ($2 = '' OR id::text <> $2);

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= $1;
