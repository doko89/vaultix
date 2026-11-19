// Package audit holds the audit use-case rules (PRD §7.16).
//
// The Service programs against the domain.AuditRepository port (append-only);
// the repos track implements the port over db/gen. All writes pass through
// the secret scrubber so secrets and backup contents never land in audit
// storage (PRD §7.16, §10.2).
package audit
