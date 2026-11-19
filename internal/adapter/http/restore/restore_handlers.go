package restore

import (
	"net/http"
	"strings"

	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
)

// planRestore handles POST /api/restore/plan.
func (h *handler) planRestore(w http.ResponseWriter, r *http.Request) {
	var body PlanRequestDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	kind := restoreuc.Kind(strings.TrimSpace(body.Kind))
	if !kind.Valid() {
		writeError(w, http.StatusBadRequest, codeValidation, "kind must be one of file, database, volume, stack")
		return
	}
	if strings.TrimSpace(body.RequestedBy) == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "requested_by is required")
		return
	}
	if kind == restoreuc.KindStack {
		if len(body.SnapshotIDs) == 0 {
			writeError(w, http.StatusBadRequest, codeValidation, "stack restore needs snapshot_ids")
			return
		}
		if len(body.Order) == 0 {
			writeError(w, http.StatusBadRequest, codeValidation, "stack restore needs an explicit order")
			return
		}
	} else if strings.TrimSpace(body.SnapshotID) == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "snapshot_id is required")
		return
	}
	plan, err := h.restores.Plan(r.Context(), restoreuc.PlanRequest{
		Kind: kind, SnapshotID: strings.TrimSpace(body.SnapshotID),
		SnapshotIDs: body.SnapshotIDs, Engine: body.Engine, Paths: body.Paths,
		Destination: body.Destination, TargetDB: body.TargetDB,
		Order: body.Order, RequestedBy: body.RequestedBy,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPlanDTO(plan))
}

// confirmRestore handles POST /api/restore/confirm.
func (h *handler) confirmRestore(w http.ResponseWriter, r *http.Request) {
	var body ConfirmRequestDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.PlanID) == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "plan_id is required")
		return
	}
	if strings.TrimSpace(body.Actor) == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "actor is required")
		return
	}
	conf, err := h.restores.Confirm(r.Context(), body.PlanID, body.Actor)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ConfirmationDTO{Token: conf.Token, PlanID: conf.PlanID, ExpiresAt: conf.ExpiresAt})
}

// executeRestore handles POST /api/restore/execute.
func (h *handler) executeRestore(w http.ResponseWriter, r *http.Request) {
	var body ExecuteRequestDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "token is required")
		return
	}
	if strings.TrimSpace(body.Actor) == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "actor is required")
		return
	}
	job, err := h.restores.Execute(r.Context(), body.Token, body.Actor)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toRestoreJobDTO(job))
}

// restoreStatus handles GET /api/restore/status?id=....
func (h *handler) restoreStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := requireParam(w, r, "id")
	if !ok {
		return
	}
	job, err := h.restores.Status(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toRestoreJobDTO(job))
}
