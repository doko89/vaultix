package platform

import (
	"encoding/json"
	"net/http"

	"github.com/vaultix/vaultix/internal/domain"
	notificationuc "github.com/vaultix/vaultix/internal/usecase/notification"
)

func toRule(r RuleDTO) notificationuc.Rule {
	return notificationuc.Rule{
		Name: r.Name, Events: r.Events,
		Channel:   domain.NotificationChannel(r.Channel),
		Recipient: r.Recipient, Enabled: r.Enabled,
	}
}

func fromRule(r notificationuc.Rule) RuleDTO {
	return RuleDTO{
		ID: r.ID, Name: r.Name, Events: r.Events,
		Channel: string(r.Channel), Recipient: r.Recipient, Enabled: r.Enabled,
	}
}

func (h *handler) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.notifications.ListRules(r.Context())
	if err != nil {
		h.log.Error("notifications: list rules", "error", err)
		writeDomainError(w, err)
		return
	}
	out := make([]RuleDTO, 0, len(rules))
	for _, rule := range rules {
		out = append(out, fromRule(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *handler) createRule(w http.ResponseWriter, r *http.Request) {
	var dto RuleDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.validate.Struct(dto); err != nil {
		writeError(w, http.StatusBadRequest, "validation failed: "+err.Error())
		return
	}
	created, err := h.notifications.CreateRule(r.Context(), toRule(dto))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, fromRule(created))
}

func (h *handler) getRule(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing rule id")
		return
	}
	rule, err := h.notifications.GetRule(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fromRule(rule))
}

func (h *handler) updateRule(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing rule id")
		return
	}
	var dto RuleDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.validate.Struct(dto); err != nil {
		writeError(w, http.StatusBadRequest, "validation failed: "+err.Error())
		return
	}
	updated, err := h.notifications.UpdateRule(r.Context(), id, toRule(dto))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fromRule(updated))
}

func (h *handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing rule id")
		return
	}
	if err := h.notifications.DeleteRule(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) testRule(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "/test")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing rule id")
		return
	}
	if err := h.notifications.TestRule(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
