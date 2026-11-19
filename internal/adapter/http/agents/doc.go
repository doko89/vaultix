// Package agents is the self-contained HTTP surface for the agents vertical:
// manager UI endpoints under /api/agents and the agent protocol under
// /api/agent. It imports only domain, the agent/discovery usecases, and
// stdlib + validator — never sibling http packages.
//
// Wiring: call Mount with any chi-compatible router (*chi.Mux satisfies
// Router) and Deps. UI routes expect an upstream session/RBAC middleware
// owned by the http root; agent-protocol routes authenticate via agent
// bearer credentials. Job dispatch goes through the narrow JobQueue
// interface, which the jobs track implements.
package agents
