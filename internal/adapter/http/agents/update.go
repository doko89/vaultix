package agents

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

// artifactIOTimeout bounds artifact byte transfers (DB bytea reads/writes
// of tens of MB exceed the repo's 5s default).
const artifactIOTimeout = 2 * time.Minute

// checkTimeout bounds a full release sync (metadata + binary downloads).
const checkTimeout = 10 * time.Minute

func toArtifactSummary(a domain.AgentArtifact) artifactSummary {
	return artifactSummary{
		ID: a.ID, Version: a.Version, GOOS: a.GOOS, GOARCH: a.GOARCH,
		SHA256: hex.EncodeToString(a.SHA256), SizeBytes: a.SizeBytes,
		UploadedBy: a.UploadedBy, CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toUpdateSlot(u domain.AgentUpdate) updateSlotDTO {
	return updateSlotDTO{
		TargetVersion: u.TargetVersion, GOOS: u.GOOS, GOARCH: u.GOARCH,
		Status: u.Status, Error: u.Error, RequestedBy: u.RequestedBy,
		RequestedAt: u.RequestedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   u.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func requireUpdates(h *handler) bool { return h.updates != nil }

// handleListArtifacts lists manager-hosted agent binaries (metadata only).
// GET /api/agents/updates/artifacts
func (h *handler) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	arts, err := h.updates.ListArtifacts(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := make([]artifactSummary, 0, len(arts))
	for _, a := range arts {
		out = append(out, toArtifactSummary(a))
	}
	writeJSON(w, http.StatusOK, listArtifactsResponse{Items: out})
}

// handleCheckUpdates syncs the latest release's agent binaries into the
// manager cache and reports what is new.
// POST /api/agents/updates/check
func (h *handler) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	status, err := h.updates.SyncReleases(ctx)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	cached := make([]artifactSummary, 0, len(status.Cached))
	for _, a := range status.Cached {
		cached = append(cached, toArtifactSummary(a))
	}
	writeJSON(w, http.StatusOK, checkUpdatesResponse{
		CheckedAt: status.At.UTC().Format(time.RFC3339),
		Cached:    cached,
		UpToDate:  len(cached) == 0,
	})
}

// handleDeleteArtifact removes one hosted binary.
// DELETE /api/agents/updates/artifacts/:id
func (h *handler) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	id := segmentAfter(r, "/api/agents/updates/artifacts/")
	if strings.TrimSpace(id) == "" {
		h.writeError(w, r, fmt.Errorf("%w: artifact id is required", domain.ErrValidation))
		return
	}
	if err := h.updates.DeleteArtifact(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRequestUpdate targets one agent at one explicit artifact.
// POST /api/agents/:id/update {"artifact_id": ...}
func (h *handler) handleRequestUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req requestUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	slot, err := h.updates.RequestUpdate(r.Context(), id, req.ArtifactID, "")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toUpdateSlot(*slot))
}

// handleDescribeUpdate reports update availability for one agent (UI state).
// GET /api/agents/:id/update
func (h *handler) handleDescribeUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	id, err := requireAgentID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	info, err := h.updates.Describe(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := describeUpdateResponse{Available: info.Available}
	if info.Latest != nil {
		s := toArtifactSummary(*info.Latest)
		out.Latest = &s
	}
	if info.Pending != nil {
		s := toUpdateSlot(*info.Pending)
		out.Pending = &s
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDownloadUpdate streams the agent's pending target bytes.
// GET /api/agent/update/download (agent bearer auth)
func (h *handler) handleDownloadUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), artifactIOTimeout)
	defer cancel()
	art, err := h.updates.DownloadArtifact(ctx, cred.AgentID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Vaultix-Version", art.Version)
	w.Header().Set("X-Vaultix-SHA256", hex.EncodeToString(art.SHA256))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(art.Bytes)
}

// handleReportUpdate records the agent's applied/failed report.
// POST /api/agent/update/report (agent bearer auth)
func (h *handler) handleReportUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireUpdates(h) {
		h.writeError(w, r, errNotConfigured)
		return
	}
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	var req updateReportRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.updates.ReportUpdate(r.Context(), cred.AgentID, req.Status, req.Version, req.Error); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
