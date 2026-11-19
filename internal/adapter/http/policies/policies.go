package policies

import (
	"net/http"

	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
)

// handler holds deps for method-style handlers.
type handler struct {
	deps Deps
}

type policyListResponse struct {
	Items []policyDTO `json:"items"`
	Total int         `json:"total"`
}

type policyVersionResponse struct {
	Policy  policyDTO  `json:"policy"`
	Version versionDTO `json:"version"`
}

type targetListResponse struct {
	Items []targetDTO `json:"items"`
	Total int         `json:"total"`
}

// handleListPolicies serves GET /api/policies.
func (h *handler) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	opts, err := pagination(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := h.deps.Policies.List(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]policyDTO, len(res.Items))
	for i := range res.Items {
		items[i] = policyFromDomain(&res.Items[i])
	}
	writeJSON(w, http.StatusOK, policyListResponse{Items: items, Total: res.Total})
}

// handleCreatePolicy serves POST /api/policies (→ 201 with v1).
func (h *handler) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := req.check(); err != nil {
		writeError(w, err)
		return
	}
	p, v, err := h.deps.Policies.Create(r.Context(), req.createInput(actor(r)))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, policyVersionResponse{Policy: policyFromDomain(p), Version: versionFromDomain(v)})
}

// handleGetPolicy serves GET /api/policies/{id}.
func (h *handler) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.deps.Policies.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policyFromDomain(p))
}

// handleUpdatePolicy serves PUT /api/policies/{id}: full metadata replace
// plus a new published version.
func (h *handler) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := req.check(); err != nil {
		writeError(w, err)
		return
	}
	p, v, err := h.deps.Policies.Update(r.Context(), r.PathValue("id"), req.updateInput(actor(r)))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policyVersionResponse{Policy: policyFromDomain(p), Version: versionFromDomain(v)})
}

// handleDeletePolicy serves DELETE /api/policies/{id} (→ 204).
func (h *handler) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.deps.Policies.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetEnabled serves POST /api/policies/{id}/enable|disable.
func (h *handler) handleSetEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.deps.Policies.SetEnabled(r.Context(), r.PathValue("id"), enabled, actor(r))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, policyFromDomain(p))
	}
}

type versionListResponse struct {
	Items []versionDTO `json:"items"`
	Total int          `json:"total"`
}

// handleListVersions serves GET /api/policies/{id}/versions.
func (h *handler) handleListVersions(w http.ResponseWriter, r *http.Request) {
	vs, err := h.deps.Policies.ListVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]versionDTO, len(vs))
	for i := range vs {
		items[i] = versionFromDomain(&vs[i])
	}
	writeJSON(w, http.StatusOK, versionListResponse{Items: items, Total: len(items)})
}

// handleGetVersion serves GET /api/policies/{id}/versions/{v}.
func (h *handler) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	v, err := versionParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	ver, err := h.deps.Policies.GetVersion(r.Context(), r.PathValue("id"), v)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versionFromDomain(ver))
}

// handlePublishVersion serves POST /api/policies/{id}/versions (→ 201).
func (h *handler) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := req.check(); err != nil {
		writeError(w, err)
		return
	}
	v, err := h.deps.Policies.PublishVersion(r.Context(), r.PathValue("id"), req.toInput(actor(r)))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, versionFromDomain(v))
}

// handleListTargets serves GET /api/policies/{id}/targets (current version).
func (h *handler) handleListTargets(w http.ResponseWriter, r *http.Request) {
	v, err := h.deps.Policies.CurrentVersion(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]targetDTO, len(v.Targets))
	for i := range v.Targets {
		items[i] = targetFromDomain(v.Targets[i])
	}
	writeJSON(w, http.StatusOK, targetListResponse{Items: items, Total: len(items)})
}

// handleAddTarget serves POST /api/policies/{id}/targets (→ 201 version).
func (h *handler) handleAddTarget(w http.ResponseWriter, r *http.Request) {
	var req targetRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := validate.Struct(&req); err != nil {
		writeError(w, badRequest("invalid target: %s", compactValidationError(err)))
		return
	}
	v, err := h.deps.Policies.AddTarget(r.Context(), r.PathValue("id"), req.toInput(), policyuc.ChangeMeta{Actor: actor(r)})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, versionFromDomain(v))
}

// handleUpdateTarget serves PUT /api/policies/{id}/targets/{tid}.
func (h *handler) handleUpdateTarget(w http.ResponseWriter, r *http.Request) {
	var req targetRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := validate.Struct(&req); err != nil {
		writeError(w, badRequest("invalid target: %s", compactValidationError(err)))
		return
	}
	v, err := h.deps.Policies.UpdateTarget(r.Context(), r.PathValue("id"), r.PathValue("tid"), req.toInput(), policyuc.ChangeMeta{Actor: actor(r)})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versionFromDomain(v))
}

// handleDeleteTarget serves DELETE /api/policies/{id}/targets/{tid}.
func (h *handler) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	v, err := h.deps.Policies.RemoveTarget(r.Context(), r.PathValue("id"), r.PathValue("tid"), policyuc.ChangeMeta{Actor: actor(r)})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versionFromDomain(v))
}
