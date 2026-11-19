package apikeys

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/vaultix/vaultix/internal/domain"
	apikeysvc "github.com/vaultix/vaultix/internal/usecase/apikey"
)

// Router is the minimal routing surface Mount needs. It deliberately
// mirrors the root Router method set so the platform track passes its
// router directly, with no adapter import here:
//
//	r := chi.NewRouter()
//	apikeys.Mount(r, deps)
type Router interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}

// Service is the API key behavior this package needs. It is satisfied by
// *usecase/apikey.Service (asserted below).
type Service interface {
	Issue(ctx context.Context, userID, name string, scopes []string, ttl time.Duration) (*apikeysvc.IssuedKey, error)
	List(ctx context.Context, userID string) ([]domain.ApiKey, error)
	Revoke(ctx context.Context, actorID, keyID string) error
}

var _ Service = (*apikeysvc.Service)(nil)

// Deps wires Mount. Service is required; Session enforces session-cookie
// auth on every route (the integrator passes gate.RequireSession —
// session-only, keys cannot manage keys); Actor resolves the session user
// ID (a closure over authhttp.SessionFromContext). A nil Session disables
// the check (unit tests, like the jobs slice WriteGate); a nil Actor
// behaves as unauthenticated (401).
type Deps struct {
	Service Service
	Session func(http.HandlerFunc) http.HandlerFunc
	Actor   func(*http.Request) (string, bool)
}

type handler struct {
	svc      Service
	actor    func(*http.Request) (string, bool)
	validate *validator.Validate
}

// Mount registers the API key routes on r:
//
//	GET    /api/api-keys      list my keys (metadata only)
//	POST   /api/api-keys      issue a key (full secret returned ONCE)
//	DELETE /api/api-keys/{id} revoke a key (owner or admin)
//
// Every route is wrapped with Deps.Session, so callers must hold a valid
// session cookie; API-key bearers are rejected here by construction
// (RequireSession never consults the key verifier).
func Mount(r Router, deps Deps) {
	session := deps.Session
	if session == nil {
		session = func(next http.HandlerFunc) http.HandlerFunc { return next }
	}
	h := &handler{
		svc:      deps.Service,
		actor:    deps.Actor,
		validate: validator.New(validator.WithRequiredStructEnabled()),
	}
	r.Get("/api/api-keys", session(h.list))
	r.Post("/api/api-keys", session(h.issue))
	r.Delete("/api/api-keys/{id}", session(h.revoke))
}

// actorID resolves the session user ID, or reports unauthenticated.
func (h *handler) actorID(r *http.Request) (string, bool) {
	if h.actor == nil {
		return "", false
	}
	id, ok := h.actor(r)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}
