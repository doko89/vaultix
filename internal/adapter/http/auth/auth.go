// Package auth is the self-contained HTTP surface for authentication and
// user/role administration (PRD §7.1, §7.1.1, §8 users & settings pages).
//
// It imports no sibling http packages: routing arrives through the minimal
// Router interface (satisfied by *chi.Mux — see Mount), services through
// the Deps interfaces (satisfied by usecase/auth.Service and
// usecase/user.Service), and auditing through the narrow local Auditor.
//
// INTEGRATION NOTES (platform track):
//   - Mount: call Mount(r, deps) from the manager router with a *chi.Mux
//     (chi.Router works too: both expose the Router method set). All routes
//     live under /api/auth and parse path segments directly from
//     r.URL.Path, so they behave identically on any router.
//   - Auditor: implement RecordAuthEvent by appending to the audit log
//     (domain.AuditRepository via the audit usecase). The same implementation
//     also satisfies usecase/auth.Auditor and usecase/user.Auditor's sibling
//     method — wire one instance into all three.
//   - Sessions: the session middleware validates the session cookie through
//     AuthService.ValidateSession; no direct SessionStore wiring is needed
//     here. Run a janitor calling SessionStore.DeleteExpired periodically.
//   - Crypto key: adapter/crypto configured from VAULTIX_CRYPTO_KEY
//     (32 bytes, base64 or hex); the same Box must back ProviderSecretStore
//     encryption and the cmd-wired oidc.SecretResolver closure
//     (auth.Service.ProviderSecret).
//   - Pending OIDC logins live in the auth service's process memory:
//     single instance or sticky callback routing until a shared store lands.
//   - OIDC endpoints need Deps.OIDCRedirectURL (absolute callback URL
//     registered at the IdP) and Deps.UIRedirectAfterLogin (default "/").
//   - Cookies are HttpOnly + SameSite=Lax; set Cookie.Secure in production
//     (TLS is mandatory per PRD §10.2) and add CSRF middleware at the
//     platform layer for cookie-authenticated mutations.
//   - Client IP is taken from RemoteAddr; when running behind a trusted
//     proxy, translate X-Forwarded-For in platform middleware instead.
package auth

import (
	"context"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
	usersvc "github.com/vaultix/vaultix/internal/usecase/user"
)

// Router is the minimal routing surface Mount needs. It deliberately
// mirrors *chi.Mux method signatures so the platform track passes its chi
// router directly, with no adapter import here:
//
//	r := chi.NewRouter()
//	authhttp.Mount(r, deps)
type Router interface {
	Use(middlewares ...func(http.Handler) http.Handler)
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}

// AuthService is the auth behavior this package needs. It is satisfied by
// *usecase/auth.Service (asserted in wiring_test.go).
type AuthService interface {
	Login(ctx context.Context, identifier, password, ip, userAgent string) (*authsvc.LoginResult, error)
	Logout(ctx context.Context, token, postLogoutRedirect string) (string, error)
	ValidateSession(ctx context.Context, token string) (*authsvc.SessionInfo, error)
	StartOIDCLogin(ctx context.Context, providerID, redirectURI string) (*authsvc.OIDCStart, error)
	CompleteOIDCLogin(ctx context.Context, providerID, state, code, redirectURI, ip, userAgent string) (*authsvc.LoginResult, error)
	ListProviders(ctx context.Context) ([]domain.OIDCProvider, error)
	GetProvider(ctx context.Context, id string) (*domain.OIDCProvider, error)
	CreateProvider(ctx context.Context, in authsvc.ProviderInput, actorID string) (*domain.OIDCProvider, error)
	UpdateProvider(ctx context.Context, id string, in authsvc.ProviderInput, actorID string) (*domain.OIDCProvider, error)
	SetProviderEnabled(ctx context.Context, id string, enabled bool, actorID string) error
	RotateProviderSecret(ctx context.Context, id, newSecret, actorID string) error
	TestProvider(ctx context.Context, id, actorID string) (*authsvc.ProviderTestReport, error)
	ListUserIdentities(ctx context.Context, userID string) ([]domain.UserExternalIdentity, error)
	LinkIdentity(ctx context.Context, userID, providerID, issuer, subject, email, actorID string) (*domain.UserExternalIdentity, error)
	UnlinkIdentity(ctx context.Context, userID, identityID, actorID string) error
	SetIdentityDisabled(ctx context.Context, userID, identityID string, disabled bool, actorID string) error
	SetLocalLoginEnabled(ctx context.Context, enabled, confirmed bool, actorID string) error
	LocalLoginEnabled() bool
}

// UserService is the user behavior this package needs. It is satisfied by
// *usecase/user.Service (asserted in wiring_test.go).
type UserService interface {
	CreateUser(ctx context.Context, in usersvc.CreateInput, actorID string) (*domain.User, error)
	GetUser(ctx context.Context, id string) (*domain.User, error)
	ListUsers(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.User], error)
	UpdateUser(ctx context.Context, id string, in usersvc.UpdateInput, actorID string) (*domain.User, error)
	SetDisabled(ctx context.Context, id string, disabled bool, actorID string) error
	SetPassword(ctx context.Context, id, newPassword, actorID string) error
	ChangePassword(ctx context.Context, id, oldPassword, newPassword string) error
	ListRoles(ctx context.Context) ([]domain.Role, error)
	ListUserRoles(ctx context.Context, userID string) ([]domain.Role, error)
	GrantRole(ctx context.Context, userID, roleName, actorID string) error
	RevokeRole(ctx context.Context, userID, roleName, actorID string) error
}

// Auditor records transport-level auth security events (malformed OIDC
// callbacks that never reach the service, etc.). The method set matches
// usecase/auth.Auditor so one implementation serves both.
type Auditor interface {
	RecordAuthEvent(ctx context.Context, e authsvc.AuditEvent) error
}

// CookieConfig tunes the session cookie.
type CookieConfig struct {
	// Name defaults to "vaultix_session".
	Name string
	// Secure must be true in production (TLS mandatory, PRD §10.2).
	Secure bool
	// SameSite defaults to http.SameSiteLaxMode.
	SameSite http.SameSite
	// Path defaults to "/".
	Path string
	// Domain is empty by default (host-only cookie).
	Domain string
}

// Deps wires Mount. Auth, Users, and Validate are required; Audit may be
// nil (transport audit disabled); OIDCRedirectURL is required for the OIDC
// routes; UIRedirectAfterLogin defaults to "/".
type Deps struct {
	Auth   AuthService
	Users  UserService
	Audit  Auditor
	Cookie CookieConfig
	Clock  domain.Clock
	// OIDCRedirectURL is the absolute callback URL registered at the IdP.
	OIDCRedirectURL string
	// UIRedirectAfterLogin is the post-callback landing page (default "/").
	UIRedirectAfterLogin string
	// PostLogoutRedirect is advertised to the IdP on RP-initiated logout.
	PostLogoutRedirect string
}

type handler struct {
	gate     *Gate
	auth     AuthService
	users    UserService
	audit    Auditor
	cookie   CookieConfig
	clock    domain.Clock
	validate *validator.Validate
	oidcCB   string
	uiAfter  string
	postOut  string
}

// Mount registers all auth routes on r and returns the Gate so platform code
// can protect non-auth routes with the same session/role enforcement:
//
//	gate := authhttp.Mount(r, deps)
//	r.Get("/api/jobs", gate.RequireRole("operator", "readonly")(jobs.List))
//
// See the package doc for wiring.
func Mount(r Router, deps Deps) *Gate {
	cookie := deps.Cookie
	if cookie.Name == "" {
		cookie.Name = "vaultix_session"
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if cookie.SameSite == 0 {
		cookie.SameSite = http.SameSiteLaxMode
	}
	clock := deps.Clock
	if clock == nil {
		clock = domain.SystemClock{}
	}
	uiAfter := deps.UIRedirectAfterLogin
	if uiAfter == "" {
		uiAfter = "/"
	}
	h := &handler{
		gate: &Gate{auth: deps.Auth, cookieName: cookie.Name, clear: clearCookie(cookie)},
		auth: deps.Auth, users: deps.Users, audit: deps.Audit,
		cookie: cookie, clock: clock,
		validate: validator.New(validator.WithRequiredStructEnabled()),
		oidcCB:   deps.OIDCRedirectURL, uiAfter: uiAfter,
		postOut: deps.PostLogoutRedirect,
	}

	// Public: login/logout/OIDC + login-page provider list.
	r.Post("/api/auth/login", h.login)
	r.Post("/api/auth/logout", h.logout)
	r.Get("/api/auth/oidc/start", h.oidcStart)
	r.Get("/api/auth/oidc/callback", h.oidcCallback)
	r.Get("/api/auth/providers/public", h.listPublicProviders)

	// Authenticated.
	r.Get("/api/auth/me", h.requireAuth(h.me))
	r.Post("/api/auth/password/change", h.requireAuth(h.changePassword))

	// Admin: users & roles.
	r.Get("/api/auth/users", h.requireAdmin(h.listUsers))
	r.Post("/api/auth/users", h.requireAdmin(h.createUser))
	r.Get("/api/auth/users/{id}", h.requireAdmin(h.getUser))
	r.Patch("/api/auth/users/{id}", h.requireAdmin(h.updateUser))
	r.Post("/api/auth/users/{id}/disable", h.requireAdmin(h.disableUser(true)))
	r.Post("/api/auth/users/{id}/enable", h.requireAdmin(h.disableUser(false)))
	r.Post("/api/auth/users/{id}/password", h.requireAdmin(h.setPassword))
	r.Get("/api/auth/users/{id}/roles", h.requireAdmin(h.listUserRoles))
	r.Post("/api/auth/users/{id}/roles", h.requireAdmin(h.grantRole))
	r.Delete("/api/auth/users/{id}/roles/{role}", h.requireAdmin(h.revokeRole))
	r.Get("/api/auth/roles", h.requireAdmin(h.listRoles))

	// Admin: identity links.
	r.Get("/api/auth/users/{id}/identities", h.requireAdmin(h.listIdentities))
	r.Post("/api/auth/users/{id}/identities", h.requireAdmin(h.linkIdentity))
	r.Delete("/api/auth/users/{id}/identities/{iid}", h.requireAdmin(h.unlinkIdentity))
	r.Post("/api/auth/users/{id}/identities/{iid}/disable", h.requireAdmin(h.disableIdentity(true)))
	r.Post("/api/auth/users/{id}/identities/{iid}/enable", h.requireAdmin(h.disableIdentity(false)))

	// Admin: OIDC providers.
	r.Get("/api/auth/providers", h.requireAdmin(h.listProviders))
	r.Post("/api/auth/providers", h.requireAdmin(h.createProvider))
	r.Get("/api/auth/providers/{id}", h.requireAdmin(h.getProvider))
	r.Put("/api/auth/providers/{id}", h.requireAdmin(h.updateProvider))
	r.Post("/api/auth/providers/{id}/enable", h.requireAdmin(h.enableProvider(true)))
	r.Post("/api/auth/providers/{id}/disable", h.requireAdmin(h.enableProvider(false)))
	r.Post("/api/auth/providers/{id}/test", h.requireAdmin(h.testProvider))
	r.Post("/api/auth/providers/{id}/secret/rotate", h.requireAdmin(h.rotateSecret))

	// Admin: login settings (break-glass switch).
	r.Post("/api/auth/settings/local-login", h.requireAdmin(h.setLocalLogin))
	r.Get("/api/auth/settings/local-login", h.requireAdmin(h.getLocalLogin))

	return h.gate
}

// SessionCookieName reports the configured cookie name (tests/docs).
func (d Deps) SessionCookieName() string {
	if d.Cookie.Name != "" {
		return d.Cookie.Name
	}
	return "vaultix_session"
}
