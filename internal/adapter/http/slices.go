package http

import (
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	authhttp "github.com/vaultix/vaultix/internal/adapter/http/auth"
	jobshttp "github.com/vaultix/vaultix/internal/adapter/http/jobs"
	platformhttp "github.com/vaultix/vaultix/internal/adapter/http/platform"
	policieshttp "github.com/vaultix/vaultix/internal/adapter/http/policies"
	restorehttp "github.com/vaultix/vaultix/internal/adapter/http/restore"
	storagehttp "github.com/vaultix/vaultix/internal/adapter/http/storage"
)

// SliceDeps carries one Deps per slice package. Sections whose required
// service is nil are skipped, so the zero value mounts no slices
// (health/metrics/SPA only).
type SliceDeps struct {
	Auth     authhttp.Deps
	Agents   agentshttp.Deps
	Policies policieshttp.Deps
	Jobs     jobshttp.Deps
	Restore  restorehttp.Deps
	Storage  storagehttp.Deps
	Platform platformhttp.Deps
}

// mountSlices registers the seven slice packages. Auth mounts first and
// returns the session Gate; the UI slices (jobs, restore, storage,
// platform, policies) mount behind RequireAuth, while auth itself and the
// agent slice stay open — agents authenticate with bearer credentials
// inside their own handlers.
//
// TODO(authz): /api/agents/* UI routes share the agents Mount with the
// agent protocol, so they cannot take the cookie gate until the tracks
// split UI and protocol mounts. Per-route RequireRole (e.g. admin-only
// settings/OIDC) likewise needs slice-level support.
func (m *Mux) mountSlices(deps SliceDeps) {
	var gate *authhttp.Gate
	if deps.Auth.Auth != nil {
		gate = authhttp.Mount(m, deps.Auth)
	}
	ui := chi.Router(m)
	if gate != nil {
		ui = &authedRouter{Router: m, gate: gate}
	}

	if deps.Agents.Agents != nil {
		// Ungated by design (see TODO above): bearer-auth protocol.
		agentshttp.Mount(m, deps.Agents)
	}
	if deps.Policies.Policies != nil {
		m.mountPolicies(deps.Policies, gate)
	}
	if deps.Jobs.Service != nil {
		jobshttp.Mount(ui, deps.Jobs)
	}
	if deps.Restore.Restores != nil {
		restorehttp.Mount(ui, deps.Restore)
	}
	if deps.Storage.Service != nil {
		storagehttp.Mount(ui, deps.Storage)
	}
	// Platform skips nil sections internally; mounting unconditionally
	// keeps settings available even when other sections are unwired.
	platformhttp.Mount(ui, deps.Platform)
}

// mountPolicies serves the policies stdlib mux under its two API prefixes.
// Prefix mounts (rather than one "/" catch-all) preserve chi's native 405
// and NotFound behavior for every other route.
func (m *Mux) mountPolicies(deps policieshttp.Deps, gate *authhttp.Gate) {
	pmux := nethttp.NewServeMux()
	policieshttp.Mount(pmux, deps)
	var h nethttp.Handler = relayNormalized(pmux)
	if gate != nil {
		inner := h
		h = nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			gate.RequireAuth(inner.ServeHTTP)(w, r)
		})
	}
	m.Mount("/api/policies", h)
	m.Mount("/api/schedules", h)
}

// authedRouter wraps route registration with the auth gate so every
// handler mounted through it requires a valid session. It embeds
// chi.Router, so chi-specific mounts (jobs) keep working.
type authedRouter struct {
	chi.Router
	gate *authhttp.Gate
}

var _ chi.Router = (*authedRouter)(nil)

func (a *authedRouter) Get(pattern string, h nethttp.HandlerFunc) {
	a.Router.Get(pattern, a.gate.RequireAuth(h))
}

func (a *authedRouter) Post(pattern string, h nethttp.HandlerFunc) {
	a.Router.Post(pattern, a.gate.RequireAuth(h))
}

func (a *authedRouter) Put(pattern string, h nethttp.HandlerFunc) {
	a.Router.Put(pattern, a.gate.RequireAuth(h))
}

func (a *authedRouter) Patch(pattern string, h nethttp.HandlerFunc) {
	a.Router.Patch(pattern, a.gate.RequireAuth(h))
}

func (a *authedRouter) Delete(pattern string, h nethttp.HandlerFunc) {
	a.Router.Delete(pattern, a.gate.RequireAuth(h))
}

// Route registers a gated sub-router: chi builds a fresh router for the
// group, so the gate applies as sub-router middleware rather than per
// route. Parent NotFound/MethodNotAllowed propagation is unchanged.
func (a *authedRouter) Route(pattern string, fn func(r chi.Router)) chi.Router {
	gate := a.gate
	return a.Router.Route(pattern, func(r chi.Router) {
		r.Use(func(next nethttp.Handler) nethttp.Handler {
			return gate.RequireAuth(next.ServeHTTP)
		})
		fn(r)
	})
}
