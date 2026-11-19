-- Vaultix: unclaimed enrollment tokens carry no agent (PRD §7.2, domain
-- AgentCredential: "AgentID is empty for unclaimed enrollment tokens").
-- agent_credentials.agent_id becomes nullable so the agent track's
-- IssueEnrollmentToken can persist through the repos port; bound
-- credentials always carry an agent_id. Phase 2 integration fix for the
-- agents/repos track mismatch (enrollment was unusable against Postgres).

-- +goose Up
ALTER TABLE agent_credentials ALTER COLUMN agent_id DROP NOT NULL;

-- +goose Down
-- Downgrade drops unclaimed single-use tokens (they are re-issuable), then
-- restores the constraint.
DELETE FROM agent_credentials WHERE agent_id IS NULL;
ALTER TABLE agent_credentials ALTER COLUMN agent_id SET NOT NULL;
