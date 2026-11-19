package policies

import (
	"net/http"
	"strings"

	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

type scheduleListResponse struct {
	Items []scheduleDTO `json:"items"`
	Total int           `json:"total"`
}

type runResponse struct {
	Jobs []jobDTO `json:"jobs"`
}

// handleListSchedules serves GET /api/schedules?policy_id= (required).
func (h *handler) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	policyID := strings.TrimSpace(r.URL.Query().Get("policy_id"))
	if policyID == "" {
		writeError(w, badRequest("policy_id query parameter is required"))
		return
	}
	items, err := h.deps.Schedules.List(r.Context(), policyID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]scheduleDTO, len(items))
	for i := range items {
		out[i] = scheduleFromDomain(&items[i])
	}
	writeJSON(w, http.StatusOK, scheduleListResponse{Items: out, Total: len(out)})
}

// handleCreateSchedule serves POST /api/schedules (→ 201).
func (h *handler) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := req.checkCreate(); err != nil {
		writeError(w, err)
		return
	}
	s, err := h.deps.Schedules.Create(r.Context(), req.createInput())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, scheduleFromDomain(s))
}

// handleGetSchedule serves GET /api/schedules/{id}.
func (h *handler) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	s, err := h.deps.Schedules.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleFromDomain(s))
}

// handleUpdateSchedule serves PUT /api/schedules/{id}: full timing replace
// (pause state preserved, NextRunAt recomputed).
func (h *handler) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := decode(r, &req); err != nil {
		writeError(w, badRequest("invalid JSON: %v", err))
		return
	}
	if err := req.checkUpdate(); err != nil {
		writeError(w, err)
		return
	}
	s, err := h.deps.Schedules.Update(r.Context(), r.PathValue("id"), req.updateInput())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleFromDomain(s))
}

// handleDeleteSchedule serves DELETE /api/schedules/{id} (→ 204).
func (h *handler) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := h.deps.Schedules.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePauseSchedule serves POST /api/schedules/{id}/pause.
func (h *handler) handlePauseSchedule(w http.ResponseWriter, r *http.Request) {
	s, err := h.deps.Schedules.Pause(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleFromDomain(s))
}

// handleResumeSchedule serves POST /api/schedules/{id}/resume.
func (h *handler) handleResumeSchedule(w http.ResponseWriter, r *http.Request) {
	s, err := h.deps.Schedules.Resume(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleFromDomain(s))
}

// handleRunSchedule serves POST /api/schedules/{id}/run (→ 202). It needs
// the jobs track's JobEnqueuer; until wired it answers 501. Overlap and
// capacity denials surface as 409.
func (h *handler) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	if h.deps.Enqueuer == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: errorDetail{
			Code:    "not_implemented",
			Message: "manual run unavailable: JobEnqueuer not wired yet (jobs track owns the implementation)",
		}})
		return
	}
	var req runRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, badRequest("invalid JSON: %v", err))
			return
		}
	}
	jobs, err := h.deps.Schedules.TriggerManual(r.Context(), scheduleuc.ManualTrigger{
		ScheduleID: r.PathValue("id"),
		AgentID:    strings.TrimSpace(req.AgentID),
		CreatedBy:  actor(r),
		Enqueuer:   h.deps.Enqueuer,
		Gate:       h.deps.Gate,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]jobDTO, len(jobs))
	for i, j := range jobs {
		out[i] = jobFromDomain(j)
	}
	writeJSON(w, http.StatusAccepted, runResponse{Jobs: out})
}
