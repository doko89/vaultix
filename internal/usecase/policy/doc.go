// Package policy holds backup-policy use-case rules (PRD §7.4).
//
// The Service owns policy CRUD, per-target-type validation, and immutable
// version publishing. Every mutation of a policy's effective configuration
// (targets, compression, encryption, retention, destination) publishes a new
// BackupPolicyVersion; jobs pin (policy ID, version) so completed-job configs
// never mutate. Only enable/disable and metadata edits (name, description,
// agent assignment) touch the policy row without publishing — metadata edits
// are bundled into Update, which always publishes a new version.
package policy
