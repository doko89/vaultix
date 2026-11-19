-- Users, roles, and role grants (MVP CRUD + lookups).

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);

-- name: ListUsers :many
SELECT * FROM users
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListActiveUsers :many
SELECT * FROM users
WHERE is_active
ORDER BY email ASC
LIMIT $1 OFFSET $2;

-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateUserLogin :one
UPDATE users SET last_login_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetUserActive :one
UPDATE users
SET is_active = $2,
    disabled_at = CASE WHEN $2 THEN NULL ELSE now() END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserDisplayName :one
UPDATE users SET display_name = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetRoleByID :one
SELECT * FROM roles WHERE id = $1;

-- name: GetRoleByName :one
SELECT * FROM roles WHERE name = $1;

-- name: ListRoles :many
SELECT * FROM roles ORDER BY name ASC;

-- name: ListUserRoles :many
SELECT r.* FROM roles r
JOIN user_roles ur ON ur.role_id = r.id
WHERE ur.user_id = $1
ORDER BY r.name ASC;

-- name: GrantUserRole :exec
INSERT INTO user_roles (user_id, role_id, granted_by)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, role_id) DO NOTHING;

-- name: RevokeUserRole :exec
DELETE FROM user_roles WHERE user_id = $1 AND role_id = $2;

-- name: RevokeAllUserRoles :exec
DELETE FROM user_roles WHERE user_id = $1;

-- name: UpdateUser :one
UPDATE users
SET email = $2,
    password_hash = $3,
    display_name = $4,
    is_active = $5,
    disabled_at = CASE WHEN $5 THEN NULL ELSE coalesce(disabled_at, now()) END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: SearchUsers :many
SELECT * FROM users
WHERE ($1 = '' OR email ILIKE '%' || $1 || '%' OR display_name ILIKE '%' || $1 || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountSearchUsers :one
SELECT count(*) FROM users
WHERE ($1 = '' OR email ILIKE '%' || $1 || '%' OR display_name ILIKE '%' || $1 || '%');
