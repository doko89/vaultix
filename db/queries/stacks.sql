-- Application stacks + stack resources (PRD §7.8).

-- name: CreateStack :one
INSERT INTO application_stacks (agent_id, name, description, compose_path)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetStackByID :one
SELECT * FROM application_stacks WHERE id = $1;

-- name: ListStacks :many
SELECT * FROM application_stacks
ORDER BY created_at DESC;

-- name: ListStacksByAgent :many
SELECT * FROM application_stacks
WHERE agent_id = $1
ORDER BY created_at DESC;

-- name: UpdateStack :one
UPDATE application_stacks
SET agent_id = $2, name = $3, description = $4, compose_path = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteStack :execrows
DELETE FROM application_stacks WHERE id = $1;

-- name: AddStackResource :one
INSERT INTO application_stack_resources (stack_id, resource_id, selector, resource_type, restore_order, required)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListStackResources :many
SELECT * FROM application_stack_resources
WHERE stack_id = $1
ORDER BY restore_order ASC, created_at ASC;

-- name: DeleteStackResource :exec
DELETE FROM application_stack_resources WHERE id = $1;

-- name: DeleteStackResourcesByStack :exec
DELETE FROM application_stack_resources WHERE stack_id = $1;
