package http

import (
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vaultix/vaultix/internal/adapter/http/middleware"
)

// Router is the chi-compatible routing subset slices Mount onto. *Mux
// implements it, and *Mux also satisfies chi.Router itself, so slices
// Mount unchanged whether they need the subset or full chi.
type Router interface {
	Get(pattern string, h nethttp.HandlerFunc)
	Post(pattern string, h nethttp.HandlerFunc)
	Put(pattern string, h nethttp.HandlerFunc)
	Patch(pattern string, h nethttp.HandlerFunc)
	Delete(pattern string, h nethttp.HandlerFunc)
}

// RouterDeps wires NewRouter. Health.DB is required; Metrics may be nil (a
// default registry is built); SPA follows spa.go semantics; Slices mounts
// the seven slice packages (zero value mounts none).
type RouterDeps struct {
	Health         HealthDeps
	Metrics        *Metrics
	SPA            SPAConfig
	RequestTimeout time.Duration
	Logger         *slog.Logger
	// CSRFCookie names the session cookie the CSRF middleware guards;
	// empty selects the default session cookie name.
	CSRFCookie string
	Slices     SliceDeps
}

// Mux is the Manager root router: chi-backed, served behind the
// request-ID/logging/CSRF/recovery/timeout middleware chain. It serves
// /healthz and /metrics, routes /api/* to slices (JSON 404 when
// unmatched), and falls back to the SPA for everything else.
type Mux struct {
	*chi.Mux
	handler nethttp.Handler
	metrics *Metrics
	spa     *spaHandler
	log     *slog.Logger
}

// Compile-time proof that Mux is a slice-mountable router, a full chi
// router (the jobs slice needs Route), and an http handler.
var (
	_ Router          = (*Mux)(nil)
	_ chi.Router      = (*Mux)(nil)
	_ nethttp.Handler = (*Mux)(nil)
)

// NewRouter assembles the root router with middleware, health, metrics,
// slice mounts, and the SPA fallback.
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
		Mux:     chi.NewRouter(),
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
	m.Mux.NotFound(m.notFound)
	m.Mux.MethodNotAllowed(m.methodNotAllowed)

	m.mountSlices(deps.Slices)

	// Chain order mirrors chi's conventional stack: request ID, access log,
	// CSRF (cookie-aware; token-authed calls pass through), panic
	// recovery, then the request timeout around dispatch. Metrics
	// observation wraps dispatch last so it sees the final status.
	var h nethttp.Handler = m.Mux
	h = m.observe(h)
	h = middleware.Timeout(deps.RequestTimeout)(h)
	h = middleware.Recovery(log)(h)
	h = middleware.CSRF(deps.CSRFCookie)(h)
	h = middleware.Logging(log)(h)
	h = middleware.RequestID(h)
	m.handler = h
	return m
}

// ServeHTTP serves requests through the middleware chain.
func (m *Mux) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	m.handler.ServeHTTP(w, r)
}

// notFound answers unmatched routes: JSON 404 for /api/*, SPA fallback
// otherwise.
func (m *Mux) notFound(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		WriteError(w, r, nethttp.StatusNotFound, "not found")
		return
	}
	m.spa.ServeHTTP(w, r)
}

// methodNotAllowed answers path-known-but-wrong-method with a JSON 405.
func (m *Mux) methodNotAllowed(w nethttp.ResponseWriter, r *nethttp.Request) {
	WriteError(w, r, nethttp.StatusMethodNotAllowed, "method not allowed")
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

// ---- Router implementation (chi-native patterns) ----

// Get registers a GET route, translating ":id" segments to chi "{id}"
// (chi has no colon syntax) and bridging chi path params into the
// request's stdlib path values so handlers work identically under chi
// helpers and r.PathValue. Post/Put/Patch/Delete behave the same.
func (m *Mux) Get(pattern string, h nethttp.HandlerFunc) {
	m.Mux.Get(chiPattern(pattern), m.bridge(h))
}

func (m *Mux) Post(pattern string, h nethttp.HandlerFunc) {
	m.Mux.Post(chiPattern(pattern), m.bridge(h))
}

func (m *Mux) Put(pattern string, h nethttp.HandlerFunc) {
	m.Mux.Put(chiPattern(pattern), m.bridge(h))
}

func (m *Mux) Patch(pattern string, h nethttp.HandlerFunc) {
	m.Mux.Patch(chiPattern(pattern), m.bridge(h))
}

func (m *Mux) Delete(pattern string, h nethttp.HandlerFunc) {
	m.Mux.Delete(chiPattern(pattern), m.bridge(h))
}

// chiPattern rewrites ":name" segments to "{name}"; "{name}", "*", and
// static segments pass through untouched.
func chiPattern(pattern string) string {
	if pattern == "" || !strings.Contains(pattern, ":") {
		return pattern
	}
	segs := strings.Split(pattern, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") && len(s) > 1 {
			segs[i] = "{" + s[1:] + "}"
		}
	}
	return strings.Join(segs, "/")
}

// bridge copies chi URL params into stdlib path values before invoking h.
func (m *Mux) bridge(h nethttp.HandlerFunc) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			for i, k := range rctx.URLParams.Keys {
				if k == "*" || i >= len(rctx.URLParams.Values) {
					continue
				}
				r.SetPathValue(k, rctx.URLParams.Values[i])
			}
		}
		h(w, r)
	}
}

// translatePattern converts chi-style ":id" patterns to stdlib ServeMux
// "{id}" patterns (existing "{id}" segments pass through; trailing "/*"
// becomes "{_rest...}"). It documents the pre-chi translation and stays
// covered by TestTranslatePattern; chi serves ":id" natively now.
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

// relayNormalized serves the policies stdlib mux while keeping error
// shapes consistent with the chi root: the mux's plain-text 404/405
// defaults become the JSON envelope; JSON bodies pass through untouched.
func relayNormalized(pmux *nethttp.ServeMux) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		rec := httptest.NewRecorder()
		pmux.ServeHTTP(rec, r)
		if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			switch rec.Code {
			case nethttp.StatusNotFound:
				WriteError(w, r, nethttp.StatusNotFound, "not found")
				return
			case nethttp.StatusMethodNotAllowed:
				WriteError(w, r, nethttp.StatusMethodNotAllowed, "method not allowed")
				return
			}
		}
		for k, vs := range rec.Header() {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}
}
