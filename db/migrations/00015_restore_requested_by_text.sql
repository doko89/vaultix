-- Restore actors are recorded as names (plan requested_by, token issued_to,
-- execute actor), but requested_by was a UUID FK to users: every execute
-- with a name failed validation. Store the actor name as TEXT; the FK is
-- dropped (audit text, not an identity link).

-- +goose Up
ALTER TABLE restore_jobs DROP CONSTRAINT IF EXISTS restore_jobs_requested_by_fkey;
ALTER TABLE restore_jobs ALTER COLUMN requested_by TYPE TEXT USING requested_by::text;

-- +goose Down
UPDATE restore_jobs SET requested_by = NULL
WHERE requested_by IS NOT NULL AND requested_by !~ '^[0-9a-fA-F-]{36}$';
ALTER TABLE restore_jobs ALTER COLUMN requested_by TYPE UUID USING requested_by::uuid;
ALTER TABLE restore_jobs ADD CONSTRAINT restore_jobs_requested_by_fkey
    FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE SET NULL;
