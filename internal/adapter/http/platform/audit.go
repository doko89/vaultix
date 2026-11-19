package platform

import (
	"net/http"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
)

// listAuditLogs serves GET /api/audit-logs with filters: actor, action,
// target_type, target_id, outcome, since/until (RFC3339), limit/offset.
func (h *handler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := audituc.Filter{
		ActorID:    strings.TrimSpace(q.Get("actor")),
		Action:     domain.AuditAction(strings.TrimSpace(q.Get("action"))),
		TargetType: strings.TrimSpace(q.Get("target_type")),
		TargetID:   strings.TrimSpace(q.Get("target_id")),
		Outcome:    strings.TrimSpace(q.Get("outcome")),
	}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since (want RFC3339)")
			return
		}
		f.Since = &t
	}
	if v := strings.TrimSpace(q.Get("until")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until (want RFC3339)")
			return
		}
		f.Until = &t
	}
	res, err := h.audit.Query(r.Context(), paging(r), f)
	if err != nil {
		h.log.Error("audit: query", "error", err)
		writeDomainError(w, err)
		return
	}
	items := make([]AuditLogDTO, 0, len(res.Items))
	for _, e := range res.Items {
		items = append(items, AuditLogDTO{
			ID: e.ID, ActorID: e.ActorID, ActorType: e.ActorType,
			Action: string(e.Action), TargetType: e.TargetType, TargetID: e.TargetID,
			Outcome: e.Outcome, Detail: e.Detail, IPAddress: e.IPAddress,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, AuditListDTO{Items: items, Total: res.Total})
}
