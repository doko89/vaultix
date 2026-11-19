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
	"github.com/vaultix/vaultix/internal/domain"
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

// RBAC matrix (UI = session cookie + roles; protocol = agent bearer).
// Anonymous callers get 401 everywhere except the public auth endpoints;
// authenticated callers get 403 where their roles fall short. The
// administrator role implies every permission (see Gate.RequireRole), so
// "admin full" needs no explicit rules.
//
//	UI surface (session cookie):
//	  agents   GETs (list/get/policies/resources) ......... all authed
//	  agents   POST enroll-tokens ......................... admin|operator
//	  agents   PATCH :id, POST :id/revoke ................. admin only
//	  jobs     GETs (list/detail) ......................... all authed
//	  jobs     POST / (run), retry, cancel ................ admin|operator
//	  restore  GETs (catalog, status) ..................... all authed
//	  restore  POST plan/confirm/execute .................. admin|operator
//	  storage  GETs (destinations, credentials) ........... all authed
//	  storage  POST/PUT/DELETE, POST :id/test ............. admin|operator
//	  platform GETs (dashboard/audit/settings/...) ........ all authed
//	  platform settings/notification/OIDC writes .......... admin|operator
//	  policies GETs ....................................... all authed
//	  policies POST/PUT/DELETE ............................ admin|operator
//	  auth     login/logout/OIDC public; me/password ...... all authed
//	  auth     users/roles/providers/settings ............. admin only
//	Agent protocol (bearer, /api/agent/...): bearer-authenticated inside
//	the handlers; never behind the session gate.
//
// mountSlices registers the seven slice packages. Auth mounts first and
// returns the session Gate; the UI slices mount behind session auth with
// per-route roles (roleRouter / jobs WriteGate / policies method gate),
// while auth's public routes and the agent protocol stay off the session
// gate — agents authenticate with bearer credentials inside their handlers.
func (m *Mux) mountSlices(deps SliceDeps) {
	var gate *authhttp.Gate
	if deps.Auth.Auth != nil {
		gate = authhttp.Mount(m, deps.Auth)
	}
	ui := chi.Router(m)
	if gate != nil {
		ui = &authedRouter{Router: m, gate: gate}
	}
	roles := newRoleRouter(Router(m), gate, uiRoleRules())

	if deps.Agents.Agents != nil {
		agentshttp.MountUI(roles, deps.Agents)
		agentshttp.MountProtocol(m, deps.Agents)
	}
	if deps.Policies.Policies != nil {
		m.mountPolicies(deps.Policies, gate)
	}
	if deps.Jobs.Service != nil {
		d := deps.Jobs
		if gate != nil {
			d.WriteGate = gate.RequireRole(domain.RoleAdministrator, domain.RoleOperator)
		}
		jobshttp.Mount(ui, d)
	}
	if deps.Restore.Restores != nil {
		restorehttp.Mount(roles, deps.Restore)
	}
	if deps.Storage.Service != nil {
		storagehttp.Mount(roles, deps.Storage)
	}
	// Platform skips nil sections internally; mounting unconditionally
	// keeps settings available even when other sections are unwired.
	platformhttp.Mount(roles, deps.Platform)
}

// mountPolicies serves the policies stdlib mux under its two API prefixes.
// Prefix mounts (rather than one "/" catch-all) preserve chi's native 405
// and NotFound behavior for every other route. Reads take RequireAuth;
// writes take admin|operator (the policies routes use GET strictly for
// reads, so a method gate is exact).
func (m *Mux) mountPolicies(deps policieshttp.Deps, gate *authhttp.Gate) {
	pmux := nethttp.NewServeMux()
	policieshttp.Mount(pmux, deps)
	var h nethttp.Handler = relayNormalized(pmux)
	if gate != nil {
		inner := h
		write := gate.RequireRole(domain.RoleAdministrator, domain.RoleOperator)
		h = nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			switch r.Method {
			case nethttp.MethodPost, nethttp.MethodPut, nethttp.MethodPatch, nethttp.MethodDelete:
				write(inner.ServeHTTP)(w, r)
			default:
				gate.RequireAuth(inner.ServeHTTP)(w, r)
			}
		})
	}
	m.Mount("/api/policies", h)
	m.Mount("/api/schedules", h)
}

// roleRule gates one (method, pattern) pair — patterns exactly as the slice
// passes them to the router (":id" or "{id}" segments) — to the named roles.
type roleRule struct {
	method  string
	pattern string
	roles   []string
}

// uiRoleRules is the enforceable half of the RBAC matrix above: every UI
// write that must refuse readonly callers. Routes without a rule take plain
// RequireAuth (see roleRouter).
func uiRoleRules() []roleRule {
	op := []string{domain.RoleAdministrator, domain.RoleOperator}
	admin := []string{domain.RoleAdministrator}
	return []roleRule{
		// Agents UI.
		{"POST", "/api/agents/enroll-tokens", op},
		{"PATCH", "/api/agents/:id", admin},
		{"POST", "/api/agents/:id/revoke", admin},
		// Restore.
		{"POST", "/api/restore/plan", op},
		{"POST", "/api/restore/confirm", op},
		{"POST", "/api/restore/execute", op},
		// Storage + credentials.
		{"POST", "/api/storage", op},
		{"PUT", "/api/storage/{id}", op},
		{"DELETE", "/api/storage/{id}", op},
		{"POST", "/api/storage/{id}/test", op},
		{"POST", "/api/credentials", op},
		{"PUT", "/api/credentials/{id}", op},
		{"DELETE", "/api/credentials/{id}", op},
		// Platform: settings, notifications, OIDC.
		{"PUT", "/api/settings", op},
		{"POST", "/api/settings/oidc-providers", op},
		{"PUT", "/api/settings/oidc-providers/:id", op},
		{"DELETE", "/api/settings/oidc-providers/:id", op},
		{"POST", "/api/settings/oidc-providers/:id/test", op},
		{"POST", "/api/notifications/rules", op},
		{"PUT", "/api/notifications/rules/:id", op},
		{"DELETE", "/api/notifications/rules/:id", op},
		{"POST", "/api/notifications/rules/:id/test", op},
	}
}

// roleRouter wraps route registration with session auth plus per-route
// roles: matched (method, pattern) pairs take RequireRole, everything else
// takes RequireAuth. A nil gate returns r unwrapped (auth unwired).
type roleRouter struct {
	Router
	gate  *authhttp.Gate
	rules map[string][]string
}

func newRoleRouter(r Router, gate *authhttp.Gate, rules []roleRule) Router {
	if gate == nil {
		return r
	}
	m := make(map[string][]string, len(rules))
	for _, rule := range rules {
		m[rule.method+" "+rule.pattern] = rule.roles
	}
	return &roleRouter{Router: r, gate: gate, rules: m}
}

func (a *roleRouter) wrap(method, pattern string, h nethttp.HandlerFunc) nethttp.HandlerFunc {
	if roles, ok := a.rules[method+" "+pattern]; ok {
		return a.gate.RequireRole(roles...)(h)
	}
	return a.gate.RequireAuth(h)
}

func (a *roleRouter) Get(pattern string, h nethttp.HandlerFunc) {
	a.Router.Get(pattern, a.wrap(nethttp.MethodGet, pattern, h))
}

func (a *roleRouter) Post(pattern string, h nethttp.HandlerFunc) {
	a.Router.Post(pattern, a.wrap(nethttp.MethodPost, pattern, h))
}

func (a *roleRouter) Put(pattern string, h nethttp.HandlerFunc) {
	a.Router.Put(pattern, a.wrap(nethttp.MethodPut, pattern, h))
}

func (a *roleRouter) Patch(pattern string, h nethttp.HandlerFunc) {
	a.Router.Patch(pattern, a.wrap(nethttp.MethodPatch, pattern, h))
}

func (a *roleRouter) Delete(pattern string, h nethttp.HandlerFunc) {
	a.Router.Delete(pattern, a.wrap(nethttp.MethodDelete, pattern, h))
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
