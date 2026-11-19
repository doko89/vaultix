package job

import (
	"context"
	"sort"
	"sync"
)

// CancelTracker is a CancelNotifier that also remembers which jobs were
// cancelled per agent so the agent protocol can surface them (poll
// cancelled_job_ids). Notifiers that only push (streams, webhooks) do not
// implement it; the service then reports no pending cancellations.
type CancelTracker interface {
	CancelNotifier
	// TakeCancelled returns the IDs of jobs cancelled for agentID since the
	// last take, oldest first, and clears the backlog.
	TakeCancelled(agentID string) []string
}

// Compile-time proof that the production notifier tracks cancellations.
var _ CancelTracker = (*MemoryCancelNotifier)(nil)

// maxTrackedCancelsPerAgent bounds the per-agent backlog; beyond it the
// oldest entries are dropped (the persisted cancelled state stays
// authoritative — the agent revalidates on ReportResult regardless).
const maxTrackedCancelsPerAgent = 100

// MemoryCancelNotifier is the production CancelNotifier: it records every
// cancelled job ID per agent so the next agent poll surfaces it via
// cancelled_job_ids, and duplicate notifications collapse (one entry per
// job). It is safe for concurrent use.
//
// Single-instance scope: entries live in process memory. Until a shared
// store lands, run one manager or accept that a poll hitting another
// instance misses the push hint — correctness never depends on it, because
// ReportResult still rejects reports on cancelled jobs with a conflict and
// PollJobs never assigns cancelled jobs.
type MemoryCancelNotifier struct {
	mu   sync.Mutex
	seen map[string][]string // agentID -> job IDs, oldest first, deduped
}

// NewMemoryCancelNotifier builds the production CancelNotifier.
func NewMemoryCancelNotifier() *MemoryCancelNotifier {
	return &MemoryCancelNotifier{seen: map[string][]string{}}
}

// NotifyCancel records jobID as cancelled for agentID. It never fails:
// notification is best-effort by contract (the persisted cancelled state is
// the source of truth).
func (n *MemoryCancelNotifier) NotifyCancel(_ context.Context, agentID, jobID string) error {
	if agentID == "" || jobID == "" {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen == nil {
		n.seen = map[string][]string{}
	}
	for _, id := range n.seen[agentID] {
		if id == jobID {
			return nil // already tracked
		}
	}
	n.seen[agentID] = append(n.seen[agentID], jobID)
	if len(n.seen[agentID]) > maxTrackedCancelsPerAgent {
		n.seen[agentID] = append([]string(nil), n.seen[agentID][len(n.seen[agentID])-maxTrackedCancelsPerAgent:]...)
	}
	return nil
}

// TakeCancelled returns the pending cancelled job IDs for agentID, oldest
// first, and clears the backlog. It returns a non-nil empty slice when
// nothing is pending.
func (n *MemoryCancelNotifier) TakeCancelled(agentID string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.seen[agentID]
	delete(n.seen, agentID)
	if out == nil {
		return []string{}
	}
	out = append([]string(nil), out...)
	sort.Strings(out)
	return out
}

// TakeCancelled returns the IDs of jobs cancelled for agentID since the last
// take (see CancelTracker). When the wired CancelNotifier does not track
// (e.g. the noop default), it returns an empty non-nil slice and no error.
func (s *Service) TakeCancelled(_ context.Context, agentID string) ([]string, error) {
	if t, ok := s.cancels.(CancelTracker); ok {
		return t.TakeCancelled(agentID), nil
	}
	return []string{}, nil
}
