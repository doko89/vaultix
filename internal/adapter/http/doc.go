// Package http is the Manager HTTP adapter root: router assembly, server
// lifecycle, JSON responses, health, metrics, middleware, and SPA serving.
//
// Router compatibility: slices expose Mount(r <subset>, deps) where <subset>
// is a chi-compatible interface (Get/Post/Put/Patch/Delete) each slice
// defines — see the agents track's Router. This package's Mux embeds
// *chi.Mux and satisfies both that method set and chi.Router itself, so
// every slice (including jobs, which needs Route) Mounts directly. The one
// exception is policies, which mounts its stdlib mux under /api/policies
// and /api/schedules (see mountPolicies).
package http
