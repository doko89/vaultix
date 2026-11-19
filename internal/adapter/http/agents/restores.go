package agents

import (
	"fmt"
	"net/http"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

// Restore polling, download grants, and execution reports ride the same
// bearer-authenticated agent protocol as backup jobs. Every call is scoped to
// the credential's agent.

// handleRestorePoll returns the agent's next due restore, or null when none
// is due. The body agent_id must match the credential: a mismatch is
// forbidden, never silently reinterpreted.
// POST /api/agent/restores/poll
func (h *handler) handleRestorePoll(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.restores == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	var req restorePollRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if req.AgentID != cred.AgentID {
		h.writeError(w, r, fmt.Errorf("%w: restore poll is scoped to the authenticated agent", domain.ErrForbidden))
		return
	}
	ctx := ContextWithTransportSecure(r.Context(), transportSecure(r))
	assignment, err := h.restores.PollRestore(ctx, cred.AgentID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, restorePollResponse{Restore: assignment})
}

// handleRestoreDownloadGrants mints short-lived GET grants confined to the
// restore's snapshot objects.
// POST /api/agent/restores/:id/download-grants
func (h *handler) handleRestoreDownloadGrants(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.restores == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	restoreID, err := requireRestoreID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req downloadGrantsRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	grants, err := h.restores.IssueDownloadGrants(r.Context(), cred.AgentID, restoreID, req.Keys)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if grants == nil {
		grants = []ObjectGrant{}
	}
	writeJSON(w, http.StatusOK, downloadGrantsResponse{Grants: grants})
}

// handleRestoreReport tracks restore-job execution from the agent.
// POST /api/agent/restores/:id/report
func (h *handler) handleRestoreReport(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.restores == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	restoreID, err := requireRestoreID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req restoreReportRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if !validJobState(req.State) {
		h.writeError(w, r, fmt.Errorf("%w: unknown restore state %q", domain.ErrValidation, req.State))
		return
	}
	if err := h.restores.ReportRestore(r.Context(), cred.AgentID, restoreID, RestoreReport{
		State: domain.JobState(req.State), Error: req.Error,
	}); err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, restoreReportResponse{OK: true})
}
