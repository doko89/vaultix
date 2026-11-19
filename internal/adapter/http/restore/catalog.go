package restore

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// listCatalog handles GET /api/catalog: browse snapshots by agent, policy,
// target, and date with encryption labels and restorable flags.
func (h *handler) listCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := domain.SnapshotFilter{
		AgentID:    strings.TrimSpace(q.Get("agent_id")),
		PolicyID:   strings.TrimSpace(q.Get("policy_id")),
		TargetType: domain.TargetType(strings.TrimSpace(q.Get("target"))),
	}
	if filter.TargetType != "" && !filter.TargetType.Valid() {
		writeError(w, http.StatusBadRequest, codeValidation, "unknown target type")
		return
	}
	var err error
	if filter.Since, err = parseTimeParam(q.Get("since")); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "since must be RFC3339")
		return
	}
	if filter.Until, err = parseTimeParam(q.Get("until")); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "until must be RFC3339")
		return
	}
	if raw := strings.TrimSpace(q.Get("restorable_only")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeValidation, "restorable_only must be a boolean")
			return
		}
		filter.Restorable = v
	}
	opts, ok := parseListOptions(w, r)
	if !ok {
		return
	}
	page, err := h.snapshots.ListSnapshots(r.Context(), opts, filter)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	dto := CatalogPageDTO{Items: []SnapshotDTO{}, Total: page.Total, Limit: opts.Limit, Offset: opts.Offset}
	for _, s := range page.Items {
		s := s
		dto.Items = append(dto.Items, toSnapshotDTO(&s))
	}
	writeJSON(w, http.StatusOK, dto)
}

// getSnapshot handles GET /api/catalog/snapshot?id=...: snapshot detail
// with its manifest when one is registered.
func (h *handler) getSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := requireParam(w, r, "id")
	if !ok {
		return
	}
	snap, err := h.snapshots.GetSnapshot(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	detail := SnapshotDetailDTO{Snapshot: toSnapshotDTO(snap)}
	if snap.ManifestID != "" {
		manifest, err := h.snapshots.GetManifest(r.Context(), snap.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			writeServiceError(w, err)
			return
		}
		if manifest != nil {
			objects, err := h.snapshots.ListObjects(r.Context(), manifest.ID)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			m := toManifestDTO(manifest, objects)
			detail.Manifest = &m
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

// getManifest handles GET /api/catalog/manifest?id=... (snapshot ID):
// manifest metadata plus its object set.
func (h *handler) getManifest(w http.ResponseWriter, r *http.Request) {
	id, ok := requireParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := h.snapshots.GetSnapshot(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	manifest, err := h.snapshots.GetManifest(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	objects, err := h.snapshots.ListObjects(r.Context(), manifest.ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toManifestDTO(manifest, objects))
}

// parseListOptions validates limit/offset pagination.
func parseListOptions(w http.ResponseWriter, r *http.Request) (domain.ListOptions, bool) {
	q := r.URL.Query()
	opts := domain.ListOptions{Limit: 25}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 100 {
			writeError(w, http.StatusBadRequest, codeValidation, "limit must be 1..100")
			return domain.ListOptions{}, false
		}
		opts.Limit = v
	}
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, codeValidation, "offset must be >= 0")
			return domain.ListOptions{}, false
		}
		opts.Offset = v
	}
	return opts, true
}

// parseTimeParam parses an optional RFC3339 query parameter.
func parseTimeParam(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
