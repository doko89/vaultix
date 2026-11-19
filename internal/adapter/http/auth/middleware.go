package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
	apikeysvc "github.com/vaultix/vaultix/internal/usecase/apikey"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

// middleware.go: session-cookie authentication, API-key bearer
// authentication, and role enforcement.

// ctxKey is the private context key for the validated session.
type ctxKey struct{}

// SessionFromContext returns the validated session for requests that passed
// RequireAuth/RequireRole (synthesized from key scopes for API-key
// callers — see the scope→role mapping on Gate).
func SessionFromContext(ctx context.Context) (*authsvc.SessionInfo, bool) {
	info, ok := ctx.Value(ctxKey{}).(*authsvc.SessionInfo)
	return info, ok && info != nil
}

// keyCtxKey is the private context key for the verified API key.
type keyCtxKey struct{}

// KeyActorFromContext returns the verified API key for requests that
// passed RequireAuth/RequireRole via a `vxk_...` bearer. It reports false
// for session callers, so handlers can tell the two apart when it matters
// (audit actor kind, key self-management refusal).
func KeyActorFromContext(ctx context.Context) (*apikeysvc.VerifiedKey, bool) {
	vk, ok := ctx.Value(keyCtxKey{}).(*apikeysvc.VerifiedKey)
	return vk, ok && vk != nil
}

// KeyVerifier validates user API keys (`vxk_...` bearers). It is
// satisfied by *usecase/apikey.Service (asserted below) with zero glue;
// tests substitute fakes.
type KeyVerifier interface {
	Verify(ctx context.Context, secret string) (*apikeysvc.VerifiedKey, error)
}

var _ KeyVerifier = (*apikeysvc.Service)(nil)

// Gate enforces session authentication and roles. Mount returns one so the
// platform track can protect non-auth routes with identical semantics.
//
// API-KEY BEARERS (CLI/programmatic access): when a KeyVerifier is wired
// (Deps.Keys or SetKeyVerifier), RequireAuth also accepts
// `Authorization: Bearer vxk_...` on every gated route, with scopes
// enforced as follows:
//
//	SCOPE→ROLE MAPPING (onto the existing RequireRole chain):
//	  read  → readonly                        (read calls)
//	  write → operator + readonly             (all mutations)
//	  admin → administrator + operator + readonly (admin-only routes)
//
// The hierarchy is implied downward (write includes read, admin includes
// everything), mirroring how the administrator role implies every
// permission. Enforcement has two layers:
//  1. Method gate (in RequireAuth): safe methods (GET/HEAD/OPTIONS/TRACE)
//     pass with any valid key; mutating methods need the write or admin
//     scope, else 403 — so a read-scoped key is confined to GETs even on
//     routes that only carry RequireAuth.
//  2. Role gate (in RequireRole, unchanged): key callers carry the
//     synthesized roles above, so admin-only routes keep refusing
//     non-admin keys with 403.
//
// Sessions are untouched: cookie validation, expiry, and role checks
// behave exactly as before, and unwired gates (keys == nil) never consult
// the verifier. When both a session cookie and a vxk_ Bearer [REDACTED] present,
// the Bearer [REDACTED] wins; non-vxk_ bearers fall through to the cookie path.
type Gate struct {
	auth       AuthService
	cookieName string
	clear      func(http.ResponseWriter)
	keys       KeyVerifier
}

// NewGate builds a Gate directly (tests and platform routers that mount
// auth routes piecemeal instead of via Mount).
func NewGate(auth AuthService, cookieName string) *Gate {
	if cookieName == "" {
		cookieName = "vaultix_session"
	}
	return &Gate{auth: auth, cookieName: cookieName, clear: func(w http.ResponseWriter) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, MaxAge: -1, Path: "/"})
	}}
}

// SetKeyVerifier wires API-key Bearer [REDACTED] onto the gate (nil disables it).
// Mount wires the same seam via Deps.Keys; prefer that in cmd wiring.
func (g *Gate) SetKeyVerifier(v KeyVerifier) { g.keys = v }

// RequireSession validates the session cookie and stashes the SessionInfo
// in the request context. Missing/invalid/expired sessions get 401. It
// never consults the key verifier: session-only routes (login-adjacent
// flows, API-key CRUD) use this so keys cannot act through them.
func (g *Gate) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(g.cookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		info, err := g.auth.ValidateSession(r.Context(), cookie.Value)
		if err != nil {
			if g.clear != nil {
				g.clear(w)
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "session is invalid or expired")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, info)))
	}
}

// RequireAuth validates the caller and stashes the SessionInfo in the
// request context. When a KeyVerifier is wired and the request carries
// `Authorization: Bearer vxk_...`, the key path runs (verify → scope
// method-gate → attach); otherwise the session-cookie path runs unchanged.
// Missing/invalid credentials get 401; valid keys with an insufficient
// scope for a mutating method get 403.
func (g *Gate) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	session := g.RequireSession(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if g.keys != nil {
			if token, ok := apiKeyBearer(r); ok {
				g.requireKey(w, r, token, next)
				return
			}
		}
		session(w, r)
	}
}

// requireKey verifies a `vxk_...` Bearer [REDACTED] enforces the scope method gate,
// and attaches both the VerifiedKey and the synthesized SessionInfo (roles
// per the scope→role mapping, so RequireRole works unchanged).
func (g *Gate) requireKey(w http.ResponseWriter, r *http.Request, token string, next http.HandlerFunc) {
	vk, err := g.keys.Verify(r.Context(), token)
	if err != nil || vk == nil || vk.Key == nil || vk.User == nil {
		// Generic 401: revoked/expired/unknown keys are
		// indistinguishable to callers (no key enumeration).
		writeError(w, http.StatusUnauthorized, "unauthorized", "API key is invalid, revoked, or expired")
		return
	}
	if !safeMethod(r.Method) && !vk.Key.MayWrite() {
		writeError(w, http.StatusForbidden, "forbidden", "API key scope is insufficient for this operation")
		return
	}
	ctx := context.WithValue(r.Context(), keyCtxKey{}, vk)
	info := &authsvc.SessionInfo{
		// Placeholder session: key callers have no server-side
		// session, but downstream helpers expect User/Roles on the
		// SessionInfo. The ID is namespaced so it can never collide
		// with a real session lookup.
		Session: &domain.Session{ID: "apikey:" + vk.Key.ID, UserID: vk.User.ID},
		User:    vk.User,
		Roles:   rolesForScopes(vk.Key.Scopes),
	}
	next(w, r.WithContext(context.WithValue(ctx, ctxKey{}, info)))
}

// apiKeyBearer extracts a user-API-key Bearer [REDACTED] `vxk_...`). Any other
// (or missing) Authorization header reports false so the caller falls
// through to the session-cookie path.
func apiKeyBearer(r *http.Request) (string, bool) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		return "", false
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, apikeysvc.KeyScheme) {
		return "", false
	}
	return token, true
}

// safeMethod reports whether the method is read-only for scope gating.
func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// rolesForScopes synthesizes roles from key scopes with downward
// implication (write includes read, admin includes everything).
func rolesForScopes(scopes []string) []domain.Role {
	have := map[string]bool{}
	for _, s := range scopes {
		have[s] = true
	}
	var roles []domain.Role
	if have[domain.ScopeAdmin] {
		roles = append(roles, domain.Role{Name: domain.RoleAdministrator, Description: "synthesized from API key admin scope"})
	}
	if have[domain.ScopeAdmin] || have[domain.ScopeWrite] {
		roles = append(roles, domain.Role{Name: domain.RoleOperator, Description: "synthesized from API key write scope"})
	}
	roles = append(roles, domain.Role{Name: domain.RoleReadOnly, Description: "synthesized from API key read scope"})
	return roles
}

// RequireRole enforces role membership on top of RequireAuth: the request
// passes when the user holds any of the named roles. The administrator
// role implies every permission and always passes.
func (g *Gate) RequireRole(names ...string) func(http.HandlerFunc) http.HandlerFunc {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return g.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
			info, _ := SessionFromContext(r.Context())
			allow := false
			for _, role := range info.Roles {
				if role.Name == domain.RoleAdministrator || want[role.Name] {
					allow = true
					break
				}
			}
			if !allow {
				writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
				return
			}
			next(w, r)
		})
	}
}

// requireAuth binds the gate for route registration.
func (h *handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return h.gate.RequireAuth(next)
}

// requireAdmin binds RequireRole(administrator) for route registration.
func (h *handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.gate.RequireRole(domain.RoleAdministrator)(next)
}

// actorID returns the authenticated user ID, or "" for public routes.
func actorID(r *http.Request) string {
	if info, ok := SessionFromContext(r.Context()); ok {
		return info.User.ID
	}
	return ""
}

func clearCookie(cookie CookieConfig) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		http.SetCookie(w, &http.Cookie{
			Name: cookie.Name, Value: "", Path: cookie.Path,
			Domain: cookie.Domain, MaxAge: -1, HttpOnly: true,
			Secure: cookie.Secure, SameSite: cookie.SameSite,
		})
	}
}
