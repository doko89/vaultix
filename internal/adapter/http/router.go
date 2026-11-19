package http

import (
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/adapter/http/middleware"
)

// Router is the chi-compatible routing subset slices Mount onto. *Mux
// implements it today; *chi.Mux satisfies the identical method set, so
// slices Mount unchanged after integrator-2 swaps router internals to chi.
type Router interface {
	Get(pattern string, h nethttp.HandlerFunc)
	Post(pattern string, h nethttp.HandlerFunc)
	Put(pattern string, h nethttp.HandlerFunc)
	Patch(pattern string, h nethttp.HandlerFunc)
	Delete(pattern string, h nethttp.HandlerFunc)
}

// RouterDeps wires NewRouter. Health.DB is required; Metrics may be nil (a
// default registry is built); SPA follows spa.go semantics.
type RouterDeps struct {
	Health         HealthDeps
	Metrics        *Metrics
	SPA            SPAConfig
	RequestTimeout time.Duration
	Logger         *slog.Logger
}

// Mux is the Manager root router: stdlib-mux-backed, chi-pattern-compatible,
// served behind the request-ID/logging/recovery/timeout middleware chain.
// It serves /healthz and /metrics, routes /api/* to slices (JSON 404 when
// unmatched), and falls back to the SPA for everything else.
type Mux struct {
	mux     *nethttp.ServeMux
	handler nethttp.Handler
	metrics *Metrics
	spa     *spaHandler
	log     *slog.Logger
}

// Compile-time proof that Mux is a slice-mountable router and an http handler.
var (
	_ Router          = (*Mux)(nil)
	_ nethttp.Handler = (*Mux)(nil)
)

// NewRouter assembles the root router with middleware, health, metrics, and
// the SPA fallback. Slice mounts live in mountSlices below.
func NewRouter(deps RouterDeps) *Mux {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	metrics := deps.Metrics
	if metrics == nil {
		metrics = NewMetrics(deps.Health.Version, log)
	}
	m := &Mux{
		mux:     nethttp.NewServeMux(),
		metrics: metrics,
		spa:     newSPAHandler(deps.SPA, log),
		log:     log,
	}
	m.Get("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		res, code := CheckHealth(r.Context(), deps.Health)
		WriteJSON(w, code, res)
	})
	m.Get("/metrics", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(metrics.Exposition()))
	})

	m.mountSlices()

	// Chain order mirrors chi's conventional stack: request ID, access log,
	// panic recovery, then the request timeout around dispatch. Metrics
	// observation wraps dispatch last so it sees the final status.
	var h nethttp.Handler = nethttp.HandlerFunc(m.dispatch)
	h = m.observe(h)
	h = middleware.Timeout(deps.RequestTimeout)(h)
	h = middleware.Recovery(log)(h)
	h = middleware.Logging(log)(h)
	h = middleware.RequestID(h)
	m.handler = h
	return m
}

// mountSlices registers the seven slice packages.
//
// TODO(integrator-2): uncomment each Mount once the slice packages land in
// the merged tree and their Deps are wired in cmd/manager serve.go:
//
//	auth.Mount(m, auth.Deps{...})             // local users, sessions, OIDC login
//	agents.Mount(m, agents.Deps{...})         // agent UI + /api/agent/* protocol
//	policies.Mount(m, policies.Deps{...})     // policies + schedules
//	jobs.Mount(chiRouter, jobs.Deps{...})     // NOTE: jobs.Mount takes chi.Router and uses Route() —
//	                                          // needs real chi; swap Mux internals first (see doc.go)
//	restore.Mount(m, restore.Deps{...})       // snapshot catalog + restore jobs
//	storage.Mount(m, storage.Deps{...})       // destinations + secret profiles
//	platform.Mount(m, platform.Deps{...})     // dashboard, notifications, audit, settings (this track)
//
// Only jobs.Mount needs chi; every other slice defines the same Router
// subset *Mux already satisfies, so those calls compile unchanged.
func (m *Mux) mountSlices() {
	// No slices in this worktree by design (parallel tracks own them).
}

// ServeHTTP serves requests through the middleware chain.
func (m *Mux) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	m.handler.ServeHTTP(w, r)
}

// dispatch routes: exact mux matches serve; path-known-but-wrong-method
// gets a JSON 405; unmatched /api/* gets a JSON 404; everything else falls
// back to the SPA.
func (m *Mux) dispatch(w nethttp.ResponseWriter, r *nethttp.Request) {
	_, pattern := m.mux.Handler(r)
	if pattern != "" {
		// Re-serve through the mux (rather than calling h directly) so
		// path values are populated on the request.
		m.mux.ServeHTTP(w, r)
		return
	}
	if m.methodMismatch(r) {
		WriteError(w, r, nethttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		WriteError(w, r, nethttp.StatusNotFound, "not found")
		return
	}
	m.spa.ServeHTTP(w, r)
}

// methodMismatch reports whether the request path is registered under a
// different method. The stdlib mux folds method mismatches into empty-pattern
// misses, so unmatched requests probe the other methods (misses are rare;
// the extra lookups are cheap).
func (m *Mux) methodMismatch(r *nethttp.Request) bool {
	for _, method := range []string{
		nethttp.MethodGet, nethttp.MethodPost, nethttp.MethodPut,
		nethttp.MethodPatch, nethttp.MethodDelete,
	} {
		if method == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := m.mux.Handler(probe); pattern != "" {
			return true
		}
	}
	return false
}

func (m *Mux) observe(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: nethttp.StatusOK}
		next.ServeHTTP(rec, r)
		m.metrics.ObserveRequest(r.Method, rec.status)
	})
}

type statusRecorder struct {
	nethttp.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// ---- Router implementation (chi-pattern-compatible) ----

func (m *Mux) Get(pattern string, h nethttp.HandlerFunc)    { m.handle("GET", pattern, h) }
func (m *Mux) Post(pattern string, h nethttp.HandlerFunc)   { m.handle("POST", pattern, h) }
func (m *Mux) Put(pattern string, h nethttp.HandlerFunc)    { m.handle("PUT", pattern, h) }
func (m *Mux) Patch(pattern string, h nethttp.HandlerFunc)  { m.handle("PATCH", pattern, h) }
func (m *Mux) Delete(pattern string, h nethttp.HandlerFunc) { m.handle("DELETE", pattern, h) }

func (m *Mux) handle(method, pattern string, h nethttp.HandlerFunc) {
	m.mux.HandleFunc(method+" "+translatePattern(pattern), h)
}

// translatePattern converts chi-style patterns to stdlib ServeMux patterns:
// ":id" segments become "{id}" (existing "{id}" segments pass through), and
// a trailing "/*" becomes the stdlib "{_rest...}" wildcard. Slices must parse
// path IDs from r.URL.Path directly (never via router helpers) so handlers
// work under both this Mux and chi — the agents track already follows this
// rule.
func translatePattern(pattern string) string {
	if pattern == "" {
		return pattern
	}
	trailing := ""
	if strings.HasSuffix(pattern, "/*") {
		pattern, trailing = strings.TrimSuffix(pattern, "/*"), "/{_rest...}"
	}
	segs := strings.Split(pattern, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") && len(s) > 1 {
			segs[i] = "{" + s[1:] + "}"
		}
	}
	out := strings.Join(segs, "/")
	if !strings.HasPrefix(out, "/") && out != "" {
		out = "/" + out
	}
	return out + trailing
}
