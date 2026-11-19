package auth

import (
	"context"
	"net/http"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

// middleware.go: session-cookie authentication and role enforcement.

// ctxKey is the private context key for the validated session.
type ctxKey struct{}

// SessionFromContext returns the validated session for requests that passed
// RequireAuth/RequireRole.
func SessionFromContext(ctx context.Context) (*authsvc.SessionInfo, bool) {
	info, ok := ctx.Value(ctxKey{}).(*authsvc.SessionInfo)
	return info, ok && info != nil
}

// Gate enforces session authentication and roles. Mount returns one so the
// platform track can protect non-auth routes with identical semantics.
type Gate struct {
	auth       AuthService
	cookieName string
	clear      func(http.ResponseWriter)
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

// RequireAuth validates the session cookie and stashes the SessionInfo in
// the request context. Missing/invalid/expired sessions get 401.
func (g *Gate) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
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
