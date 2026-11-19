// Package platform serves the Manager platform APIs: dashboard overview,
// notification rules, audit logs, and global settings incl. OIDC provider
// administration (PRD §7.15, §7.16, §8).
//
// Like the other slices it defines its own chi-compatible Router subset and
// exposes Mount; the http root mounts it (see TODO(integrator-2) in the
// root router). Sections whose Deps are nil are skipped, so cmd/manager can
// wire the sections this track owns (notifications, audit, settings store)
// while dashboard data and OIDC administration wait for their tracks.
package platform
