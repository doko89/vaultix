package platform

import (
	"encoding/json"
	"net/http"
)

func (h *handler) listProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.oidc.ListProviders(r.Context())
	if err != nil {
		h.log.Error("settings: list OIDC providers", "error", err)
		writeDomainError(w, err)
		return
	}
	out := make([]OIDCProviderDTO, 0, len(providers))
	for _, p := range providers {
		out = append(out, ToDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *handler) createProvider(w http.ResponseWriter, r *http.Request) {
	var in OIDCProviderInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.validate.Struct(in); err != nil {
		writeError(w, http.StatusBadRequest, "validation failed: "+err.Error())
		return
	}
	created, err := h.oidc.CreateProvider(r.Context(), in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ToDTO(created))
}

func (h *handler) getProvider(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing provider id")
		return
	}
	p, err := h.oidc.GetProvider(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ToDTO(p))
}

func (h *handler) updateProvider(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing provider id")
		return
	}
	var in OIDCProviderInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.validate.Struct(in); err != nil {
		writeError(w, http.StatusBadRequest, "validation failed: "+err.Error())
		return
	}
	updated, err := h.oidc.UpdateProvider(r.Context(), id, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ToDTO(updated))
}

func (h *handler) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing provider id")
		return
	}
	if err := h.oidc.DeleteProvider(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) testProvider(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/test")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing provider id")
		return
	}
	// Break-glass guard (PRD §7.1): testing never disables providers — the
	// auth track's TestProvider only runs discovery + an authenticated probe.
	if err := h.oidc.TestProvider(r.Context(), id); err != nil {
		h.log.Warn("settings: OIDC provider test failed", "provider_id", id, "error", err)
		// The operator is testing their own just-submitted config, so the
		// failure detail is actionable (and safe) to return.
		writeError(w, http.StatusBadRequest, "provider test failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
