// Package policies serves the policy and schedule REST API.
//
// It is self-contained: only stdlib, domain, the policy/schedule use-cases,
// and validator are imported — never sibling http packages. Mount registers
// /api/policies* and /api/schedules* on a stdlib ServeMux (chi-free: chi is
// the approved router per TECH_SPEC but is not vendored in go.mod, and this
// package must not add dependencies; the parent wires the mux into chi with
// r.Mount — see INTEGRATION NOTE on Mount).
package policies
