package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/vaultix/vaultix/internal/domain"
)

// Router is the subset of chi.Router used by Mount (method + pattern
// registration only). It deliberately excludes Route so that *chi.Mux
// satisfies it once github.com/go-chi/chi/v5 is added to go.mod; until then
// NewMux serves the same routes on the stdlib multiplexer.
//
// INTEGRATION NOTE: chi is not yet in go.mod (adding dependencies is out of
// scope for the storage track). When the HTTP track adds chi, wire with
// Mount(mux, deps) where mux is a *chi.Mux; route patterns below ({id}
// segments) are valid for both routers.
type Router interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}

// Mount registers the storage routes on r:
//
//	GET/POST              /api/storage
//	GET/PUT/DELETE        /api/storage/{id}
//	POST                  /api/storage/{id}/test
//	GET/POST              /api/credentials
//	GET/PUT/DELETE        /api/credentials/{id}
func Mount(r Router, deps Deps) {
	h := NewHandler(deps)

	r.Get("/api/storage", h.listDestinations)
	r.Post("/api/storage", h.createDestination)
	r.Get("/api/storage/{id}", h.getDestination)
	r.Put("/api/storage/{id}", h.updateDestination)
	r.Delete("/api/storage/{id}", h.deleteDestination)
	r.Post("/api/storage/{id}/test", h.testDestination)

	r.Get("/api/credentials", h.listSecrets)
	r.Post("/api/credentials", h.createSecret)
	r.Get("/api/credentials/{id}", h.getSecret)
	r.Put("/api/credentials/{id}", h.updateSecret)
	r.Delete("/api/credentials/{id}", h.deleteSecret)
}

// muxAdapter adapts the stdlib multiplexer to Router.
type muxAdapter struct {
	mux *http.ServeMux
}

func (a muxAdapter) Get(pattern string, h http.HandlerFunc) {
	a.mux.HandleFunc("GET "+pattern, h)
}

func (a muxAdapter) Post(pattern string, h http.HandlerFunc) {
	a.mux.HandleFunc("POST "+pattern, h)
}

func (a muxAdapter) Put(pattern string, h http.HandlerFunc) {
	a.mux.HandleFunc("PUT "+pattern, h)
}

func (a muxAdapter) Patch(pattern string, h http.HandlerFunc) {
	a.mux.HandleFunc("PATCH "+pattern, h)
}

func (a muxAdapter) Delete(pattern string, h http.HandlerFunc) {
	a.mux.HandleFunc("DELETE "+pattern, h)
}

// NewMux returns a stdlib multiplexer serving the storage routes.
func NewMux(deps Deps) *http.ServeMux {
	mux := http.NewServeMux()
	Mount(muxAdapter{mux: mux}, deps)
	return mux
}

// Handler serves the storage API.
type Handler struct {
	svc      Service
	actor    string
	validate *validator.Validate
	now      func() time.Time
}

// NewHandler builds a Handler; deps.Service is required.
func NewHandler(deps Deps) *Handler {
	return &Handler{
		svc:      deps.Service,
		actor:    deps.Actor,
		validate: validator.New(),
		now:      time.Now,
	}
}

// maxBodyBytes caps request bodies (secret payloads up to 1 MiB plus JSON).
const maxBodyBytes = 2 << 20

func (h *Handler) listDestinations(w http.ResponseWriter, r *http.Request) {
	ds, err := h.svc.ListDestinations(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DestinationResponse, 0, len(ds))
	for i := range ds {
		out = append(out, toDestinationResponse(&ds[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) createDestination(w http.ResponseWriter, r *http.Request) {
	var req DestinationRequest
	if !decodeAndValidate(w, r, h.validate, &req) {
		return
	}
	d, err := h.svc.CreateDestination(r.Context(), req.toCreateInput())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toDestinationResponse(d))
}

func (h *Handler) getDestination(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/storage/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: destination id is required", domain.ErrValidation))
		return
	}
	d, err := h.svc.GetDestination(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDestinationResponse(d))
}

func (h *Handler) updateDestination(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/storage/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: destination id is required", domain.ErrValidation))
		return
	}
	var req DestinationRequest
	if !decodeAndValidate(w, r, h.validate, &req) {
		return
	}
	d, err := h.svc.UpdateDestination(r.Context(), req.toUpdateInput(id))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDestinationResponse(d))
}

func (h *Handler) deleteDestination(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/storage/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: destination id is required", domain.ErrValidation))
		return
	}
	if err := h.svc.DeleteDestination(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) testDestination(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/storage/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: destination id is required", domain.ErrValidation))
		return
	}
	resp := TestConnectionResponse{DestinationID: id, CheckedAt: h.now().UTC()}
	if err := h.svc.TestDestination(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrValidation) {
			writeError(w, err)
			return
		}
		// The probe ran and the storage did not answer: report, don't 500.
		resp.OK = false
		resp.Error = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.OK = true
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listSecrets(w http.ResponseWriter, r *http.Request) {
	ms, err := h.svc.ListSecrets(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]SecretMetaResponse, 0, len(ms))
	for i := range ms {
		out = append(out, toSecretMetaResponse(&ms[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) createSecret(w http.ResponseWriter, r *http.Request) {
	var req SecretRequest
	if !decodeAndValidate(w, r, h.validate, &req) {
		return
	}
	m, err := h.svc.CreateSecret(r.Context(), req.toCreateInput(h.actor))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSecretMetaResponse(m))
}

func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/credentials/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: secret id is required", domain.ErrValidation))
		return
	}
	m, err := h.svc.GetSecret(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSecretMetaResponse(m))
}

func (h *Handler) updateSecret(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/credentials/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: secret id is required", domain.ErrValidation))
		return
	}
	var req SecretRequest
	if !decodeAndValidate(w, r, h.validate, &req) {
		return
	}
	m, err := h.svc.UpdateSecret(r.Context(), req.toUpdateInput(id))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSecretMetaResponse(m))
}

func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/api/credentials/")
	if id == "" {
		writeError(w, fmt.Errorf("%w: secret id is required", domain.ErrValidation))
		return
	}
	if err := h.svc.DeleteSecret(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pathID extracts the {id} segment for routes registered under prefix. It
// prefers the stdlib path value and falls back to segment parsing so the
// same handlers work under chi without importing it.
func pathID(r *http.Request, prefix string) string {
	if v := r.PathValue("id"); v != "" {
		return v
	}
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok || rest == "" {
		return ""
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// decodeAndValidate reads a bounded JSON body and validates it, writing the
// 400 response on failure. Validation details name fields and rules only,
// never submitted values.
func decodeAndValidate(w http.ResponseWriter, r *http.Request, v *validator.Validate, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return false
	}
	if err := v.Struct(dst); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			details := make([]string, 0, len(verrs))
			for _, fe := range verrs {
				details = append(details, strings.ToLower(fe.Field())+": "+fe.Tag())
			}
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "validation failed", Details: details})
			return false
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "validation failed"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps domain failures to statuses. Internal failures render a
// generic message so storage errors (which may name endpoints) and secret
// handling details never leak across the API boundary.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "not found"})
	case errors.Is(err, domain.ErrValidation):
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: safeValidationMessage(err)})
	case errors.Is(err, domain.ErrConflict):
		writeJSON(w, http.StatusConflict, ErrorResponse{Error: safeValidationMessage(err)})
	case errors.Is(err, domain.ErrForbidden):
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "forbidden"})
	default:
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
}

// safeValidationMessage renders validation/conflict failures. Use-case
// validation messages name fields and constraints, never secret values.
func safeValidationMessage(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "domain: validation failed"); i >= 0 {
		if rest := strings.TrimSpace(msg[i+len("domain: validation failed"):]); rest != "" {
			return strings.TrimPrefix(rest, ": ")
		}
		return "validation failed"
	}
	if i := strings.Index(msg, "domain: conflict"); i >= 0 {
		if rest := strings.TrimSpace(msg[i+len("domain: conflict"):]); rest != "" {
			return strings.TrimPrefix(rest, ": ")
		}
		return "conflict"
	}
	return "invalid request"
}
