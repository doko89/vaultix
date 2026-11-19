// Package domain holds the Vaultix core model: entities, value types, and
// port (repository/external-service) interfaces.
//
// Dependency rule (docs/TECH_SPEC.md §1.1): this package has ZERO external
// (non-stdlib) imports. Entity IDs are opaque strings (UUIDs rendered as
// text); adapters own ID generation and persistence mapping.
//
// Aggregates: identity (user, role, OIDC), agent (agent, credential,
// heartbeat, discovered resources), policy (storage, secrets, policies,
// schedules, retention), execution (backup/restore jobs), catalog
// (snapshots, manifests, objects, application stacks), and operations
// (notifications, audit logs).
package domain
