package policies

import (
	"net/http"
)

// Mount registers the policy and schedule routes on mux:
//
//	GET/POST              /api/policies
//	GET/PUT/DELETE        /api/policies/{id}
//	POST                  /api/policies/{id}/enable /disable
//	GET/POST              /api/policies/{id}/versions
//	GET                   /api/policies/{id}/versions/{v}
//	GET/POST              /api/policies/{id}/targets
//	PUT/DELETE            /api/policies/{id}/targets/{tid}
//	GET/POST              /api/schedules
//	GET/PUT/DELETE        /api/schedules/{id}
//	POST                  /api/schedules/{id}/pause /resume /run
//
// It panics when Deps.Policies or Deps.Schedules is nil (fail fast at
// wiring time). Deps.Enqueuer / Deps.Gate may be nil until the jobs track
// lands (manual-run answers 501 / skips admission respectively).
//
// INTEGRATION NOTE — chi wiring: this package mounts on a stdlib
// *http.ServeMux on purpose (chi is not in go.mod and this package must not
// add dependencies). cmd/manager wires it into the chi router with:
//
//	mux := http.NewServeMux()
//	policies.Mount(mux, policies.Deps{...})
//	r.Mount("/", mux) // or r.Mount("/api", strippedMux)
//
// INTEGRATION NOTE — jobs track: provide Deps.Enqueuer (JobEnqueuer) and
// Deps.Gate (ConcurrencyGate). Until then, POST /run answers 501.
//
// INTEGRATION NOTE — scheduler track: drive recurring execution by calling
// schedule.Service.DispatchDue on a tick with your RecurringRunner; no HTTP
// endpoint dispatches recurring fires.
func Mount(mux *http.ServeMux, deps Deps) {
	if deps.Policies == nil || deps.Schedules == nil {
		panic("policies: Deps.Policies and Deps.Schedules are required")
	}
	h := &handler{deps: deps}

	mux.HandleFunc("GET /api/policies", h.handleListPolicies)
	mux.HandleFunc("POST /api/policies", h.handleCreatePolicy)
	mux.HandleFunc("GET /api/policies/{id}", h.handleGetPolicy)
	mux.HandleFunc("PUT /api/policies/{id}", h.handleUpdatePolicy)
	mux.HandleFunc("DELETE /api/policies/{id}", h.handleDeletePolicy)
	mux.HandleFunc("POST /api/policies/{id}/enable", h.handleSetEnabled(true))
	mux.HandleFunc("POST /api/policies/{id}/disable", h.handleSetEnabled(false))
	mux.HandleFunc("GET /api/policies/{id}/versions", h.handleListVersions)
	mux.HandleFunc("POST /api/policies/{id}/versions", h.handlePublishVersion)
	mux.HandleFunc("GET /api/policies/{id}/versions/{v}", h.handleGetVersion)
	mux.HandleFunc("GET /api/policies/{id}/targets", h.handleListTargets)
	mux.HandleFunc("POST /api/policies/{id}/targets", h.handleAddTarget)
	mux.HandleFunc("PUT /api/policies/{id}/targets/{tid}", h.handleUpdateTarget)
	mux.HandleFunc("DELETE /api/policies/{id}/targets/{tid}", h.handleDeleteTarget)

	mux.HandleFunc("GET /api/schedules", h.handleListSchedules)
	mux.HandleFunc("POST /api/schedules", h.handleCreateSchedule)
	mux.HandleFunc("GET /api/schedules/{id}", h.handleGetSchedule)
	mux.HandleFunc("PUT /api/schedules/{id}", h.handleUpdateSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", h.handleDeleteSchedule)
	mux.HandleFunc("POST /api/schedules/{id}/pause", h.handlePauseSchedule)
	mux.HandleFunc("POST /api/schedules/{id}/resume", h.handleResumeSchedule)
	mux.HandleFunc("POST /api/schedules/{id}/run", h.handleRunSchedule)
}
