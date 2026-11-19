package apikeys

import (
	"net/http"
	"strings"
	"time"
)

// handlers.go: list, issue, revoke. All routes are session-authed (see
// Mount); the actor is the session user.

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	keys, err := h.svc.List(r.Context(), actor)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := make([]keyJSON, 0, len(keys))
	for _, k := range keys {
		items = append(items, toKeyJSON(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) issue(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	var in issueRequest
	if !h.decode(w, r, &in) {
		return
	}
	issued, err := h.svc.Issue(r.Context(), actor, in.Name, in.Scopes, ttlHours(in.TTlHours))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resp := issueResponse{keyJSON: toKeyJSON(*issued.Key), Key: issued.Secret}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *handler) revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	id, ok := pathID(r, "/api/api-keys/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "API key id is required")
		return
	}
	if err := h.svc.Revoke(r.Context(), actor, id); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ttlHours converts fractional hours to a duration; non-positive means no
// expiry (service validates the upper bound again).
func ttlHours(hours float64) time.Duration {
	if hours <= 0 {
		return 0
	}
	return time.Duration(hours * float64(time.Hour))
}

// pathID strips a known prefix and returns the first segment (router-
// agnostic: prefers the chi/stdlib parsed value, else the raw path).
func pathID(r *http.Request, prefix string) (string, bool) {
	if v := r.PathValue("id"); v != "" {
		return v, true
	}
	trimmed := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), prefix)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || trimmed == strings.TrimSuffix(r.URL.Path, "/") {
		return "", false
	}
	id, _, _ := strings.Cut(trimmed, "/")
	if id == "" {
		return "", false
	}
	return id, true
}
