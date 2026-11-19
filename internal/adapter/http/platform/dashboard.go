package platform

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// dashboard serves GET /api/dashboard (PRD §7.15).
//
// Query: recent_limit (default 10, max 50), gap_days (default 7 — policies
// with no successful backup within this window are flagged), trend_days
// (default 14, max 90).
func (h *handler) getDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	recentLimit := 10
	if v := strings.TrimSpace(q.Get("recent_limit")); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid recent_limit")
			return
		} else if n > 0 {
			recentLimit = min(n, 50)
		}
	}
	gapDays := 7
	if v := strings.TrimSpace(q.Get("gap_days")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid gap_days")
			return
		}
		gapDays = n
	}
	trendDays := 14
	if v := strings.TrimSpace(q.Get("trend_days")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid trend_days")
			return
		}
		trendDays = min(n, 90)
	}

	counts, err := h.dashboard.AgentCounts(ctx)
	if err != nil {
		h.log.Error("dashboard: agent counts", "error", err)
		writeDomainError(w, err)
		return
	}
	byStatus, err := h.dashboard.JobsByStatus(ctx)
	if err != nil {
		h.log.Error("dashboard: jobs by status", "error", err)
		writeDomainError(w, err)
		return
	}
	recent, err := h.dashboard.RecentJobs(ctx, recentLimit)
	if err != nil {
		h.log.Error("dashboard: recent jobs", "error", err)
		writeDomainError(w, err)
		return
	}
	perAgent, err := h.dashboard.LastBackupPerAgent(ctx)
	if err != nil {
		h.log.Error("dashboard: last backup per agent", "error", err)
		writeDomainError(w, err)
		return
	}
	perPolicy, err := h.dashboard.LastBackupPerPolicy(ctx)
	if err != nil {
		h.log.Error("dashboard: last backup per policy", "error", err)
		writeDomainError(w, err)
		return
	}
	gaps, err := h.dashboard.PoliciesWithoutRecentSuccess(ctx, time.Duration(gapDays)*24*time.Hour)
	if err != nil {
		h.log.Error("dashboard: policy gaps", "error", err)
		writeDomainError(w, err)
		return
	}
	trend, err := h.dashboard.JobTrend(ctx, trendDays)
	if err != nil {
		h.log.Error("dashboard: job trend", "error", err)
		writeDomainError(w, err)
		return
	}
	storage, err := h.dashboard.StorageUsage(ctx)
	if err != nil {
		h.log.Error("dashboard: storage usage", "error", err)
		writeDomainError(w, err)
		return
	}

	statusMap := make(map[string]int64, len(byStatus))
	for state, n := range byStatus {
		statusMap[string(state)] = n
	}
	writeJSON(w, http.StatusOK, DashboardDTO{
		Agents:                 counts,
		JobsByStatus:           statusMap,
		RecentJobs:             orEmpty(recent),
		LastBackupPerAgent:     orEmpty(perAgent),
		LastBackupPerPolicy:    orEmpty(perPolicy),
		PoliciesWithoutSuccess: orEmpty(gaps),
		Trend:                  orEmpty(trend),
		Storage:                storage,
	})
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
