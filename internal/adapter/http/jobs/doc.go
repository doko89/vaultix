// Package jobs exposes the UI-facing backup-job REST API (PRD §7.10, §8).
//
// It is self-contained: it imports only the standard library, chi,
// validator, domain, and the job use-case (for the Service contract and
// filter type) — never sibling http packages or the http root.
//
// Routes (all JSON, mounted by Mount):
//
//	GET  /api/jobs          list/filter jobs
//	POST /api/jobs          enqueue a manual job
//	GET  /api/jobs/{id}     job detail incl. progress shape and retry history
//	POST /api/jobs/{id}/retry   retry a failed job (creates a new attempt)
//	POST /api/jobs/{id}/cancel  cancel a queued or running job
//
// INTEGRATION NOTE (http root): call Mount on the authenticated chi
// subtree; auth, RBAC, audit logging, and request-ID middleware live in the
// http root and wrap these routes. Actor identity for CreatedBy is left
// empty here until the root defines its auth-context convention — audit
// middleware records the actor separately.
//
// INTEGRATION NOTE (wiring): *job.Service satisfies the Service interface
// (asserted in compat_test.go); pass it in Deps.
package jobs
