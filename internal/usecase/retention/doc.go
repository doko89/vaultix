// Package retention evaluates age/count retention rules into auditable,
// dry-runnable deletion plans and applies them (PRD §7.14). The last known
// valid recovery point is never deleted without an explicitly confirmed
// override.
package retention
