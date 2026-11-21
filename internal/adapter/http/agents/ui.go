package agents

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

// handleIssueToken issues an enrollment token (shown once).
// POST /api/agents/enroll-tokens
func (h *handler) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, r, fmt.Errorf("%w: method not allowed", domain.ErrValidation))
		return
	}
	var req issueTokenRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			h.writeError(w, r, err)
			return
		}
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	ttl := time.Duration(req.TTLHours) * time.Hour // 0 -> usecase default
	plaintext, cred, err := h.agents.IssueEnrollmentToken(r.Context(), ttl)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var exp time.Time
	if cred.ExpiresAt != nil {
		exp = *cred.ExpiresAt
	}
	writeJSON(w, http.StatusCreated, issueTokenResponse{
		Token: plaintext, CredentialID: cred.ID, ExpiresAt: exp,
	})
}

// handleListAgents lists agents with status derivation.
// GET /api/agents?status=&q=&limit=&offset=
func (h *handler) handleListAgents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := parseLimitOffset(q, defaultPageLimit, maxPageLimit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.agents.ListAgents(r.Context(),
		domain.ListOptions{Limit: limit, Offset: offset, Query: strings.TrimSpace(q.Get("q"))},
		agentuc.AgentFilter{Status: domain.AgentStatus(strings.TrimSpace(q.Get("status")))},
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	items := make([]agentSummary, 0, len(res.Items))
	for _, v := range res.Items {
		items = append(items, toSummary(v))
	}
	writeJSON(w, http.StatusOK, listAgentsResponse{Items: items, Total: res.Total})
}

// handleGetAgent returns one agent with derived status.
// GET /api/agents/:id
func (h *handler) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	view, err := h.agents.GetAgent(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSummary(*view))
}

// handlePatchAgent flips reversible agent fields (disabled).
// PATCH /api/agents/:id
func (h *handler) handlePatchAgent(w http.ResponseWriter, r *http.Request) {
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req patchAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if req.Disabled == nil {
		h.writeError(w, r, fmt.Errorf("%w: nothing to update: disabled is required", domain.ErrValidation))
		return
	}
	if err := h.agents.SetDisabled(r.Context(), id, *req.Disabled); err != nil {
		h.writeError(w, r, err)
		return
	}
	view, err := h.agents.GetAgent(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSummary(*view))
}

// handleRevokeAgent irreversibly revokes an agent and its credentials.
// POST /api/agents/:id/revoke
func (h *handler) handleRevokeAgent(w http.ResponseWriter, r *http.Request) {
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.agents.RevokeAgent(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	view, err := h.agents.GetAgent(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSummary(*view))
}

// handleDeleteAgent permanently removes a revoked agent.
// DELETE /api/agents/:id
func (h *handler) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.agents.DeleteAgent(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAgentPolicies inspects policy assignments for an agent.
// GET /api/agents/:id/policies
func (h *handler) handleAgentPolicies(w http.ResponseWriter, r *http.Request) {
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	policies, err := h.agents.AgentPolicies(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := make([]policySummary, 0, len(policies))
	for _, p := range policies {
		out = append(out, policySummary{
			ID: p.ID, Name: p.Name, Enabled: p.Enabled, CurrentVersion: p.CurrentVersion,
		})
	}
	writeJSON(w, http.StatusOK, agentPoliciesResponse{AgentID: id, Policies: out})
}

// handleAgentResources serves the agent's discovered inventory.
// GET /api/agents/:id/resources?kind=
func (h *handler) handleAgentResources(w http.ResponseWriter, r *http.Request) {
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	kind := domain.ResourceKind(strings.TrimSpace(r.URL.Query().Get("kind")))
	items, err := h.discovery.List(r.Context(), id, kind)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := make([]resourceDTO, 0, len(items))
	for _, res := range items {
		out = append(out, resourceDTO{
			ID: res.ID, Kind: string(res.Kind), Name: res.Name,
			Detail: res.Detail, DiscoveredAt: res.DiscoveredAt, LastSeenAt: res.LastSeenAt,
		})
	}
	writeJSON(w, http.StatusOK, agentResourcesResponse{AgentID: id, Items: out})
}

func toSummary(v agentuc.AgentView) agentSummary {
	return agentSummary{
		ID: v.Agent.ID, Hostname: v.Agent.Hostname, IPAddress: v.Agent.IPAddress,
		OS: v.Agent.OS, Architecture: v.Agent.Architecture, Version: v.Agent.Version,
		Capabilities: v.Agent.Capabilities, Disabled: v.Agent.Disabled,
		Status: string(v.Status), Outdated: v.Outdated,
		LastHeartbeatAt: v.Agent.LastHeartbeatAt,
		DiskTotalBytes:  v.Agent.DiskTotalBytes, DiskFreeBytes: v.Agent.DiskFreeBytes,
		EnrolledAt: v.Agent.EnrolledAt,
	}
}
