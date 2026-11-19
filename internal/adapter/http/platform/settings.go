package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

// MemorySettingsStore is a concurrency-safe in-memory SettingsStore holding
// one GlobalSettings value. Serve wires it until the repos track provides a
// DB-backed store (see the SettingsStore INTEGRATION NOTE).
type MemorySettingsStore struct {
	mu       sync.RWMutex
	settings GlobalSettings
}

// NewMemorySettingsStore returns a store holding current.
func NewMemorySettingsStore(current GlobalSettings) *MemorySettingsStore {
	return &MemorySettingsStore{settings: current}
}

// Get returns the current settings.
func (s *MemorySettingsStore) Get(context.Context) (GlobalSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, nil
}

// Update validates and replaces the settings.
func (s *MemorySettingsStore) Update(_ context.Context, next GlobalSettings) (GlobalSettings, error) {
	if err := next.Validate(); err != nil {
		return GlobalSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = next
	return next, nil
}

func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings.Get(r.Context())
	if err != nil {
		h.log.Error("settings: get", "error", err)
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var next GlobalSettings
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "validation failed: "+err.Error())
		return
	}
	updated, err := h.settings.Update(r.Context(), next)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
