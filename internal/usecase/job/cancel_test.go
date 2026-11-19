package job

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

func cancelTestService(jobs *fakeJobs) *Service {
	return NewService(Deps{
		Jobs:     jobs,
		Agents:   newFakeAgents(&domain.Agent{ID: "ag-1"}),
		Policies: newFakePolicies(nil, nil),
		Cancels:  NewMemoryCancelNotifier(),
	})
}

func TestCancelSurfacedViaTakeCancelled(t *testing.T) {
	ctx := context.Background()
	jobs := newFakeJobs(
		&domain.BackupJob{ID: "j1", AgentID: "ag-1", PolicyID: "p1", State: domain.JobRunning},
		&domain.BackupJob{ID: "j2", AgentID: "ag-1", PolicyID: "p1", State: domain.JobQueued},
	)
	s := cancelTestService(jobs)

	// Nothing pending before any cancellation (non-nil for clean JSON).
	ids, err := s.TakeCancelled(ctx, "ag-1")
	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Empty(t, ids)

	_, err = s.Cancel(ctx, "j1")
	require.NoError(t, err)
	_, err = s.Cancel(ctx, "j2")
	require.NoError(t, err)

	ids, err = s.TakeCancelled(ctx, "ag-1")
	require.NoError(t, err)
	// Both surface: cancelled_job_ids carries every job of this agent that
	// was cancelled since the last take (the agent ignores IDs it never
	// saw; the poll filter is per-agent, not per-dispatch).
	require.ElementsMatch(t, []string{"j1", "j2"}, ids)

	// Take clears: a second take is empty (no repeat delivery).
	ids, err = s.TakeCancelled(ctx, "ag-1")
	require.NoError(t, err)
	require.Empty(t, ids)

	// Other agents never see these cancellations.
	ids, err = s.TakeCancelled(ctx, "ag-2")
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestCancelExcludedFromPoll(t *testing.T) {
	ctx := context.Background()
	jobs := newFakeJobs(
		&domain.BackupJob{ID: "j1", AgentID: "ag-1", PolicyID: "p1", State: domain.JobQueued},
	)
	s := cancelTestService(jobs)
	_, err := s.Cancel(ctx, "j1")
	require.NoError(t, err)

	got, err := s.PollJobs(ctx, "ag-1", nil)
	require.NoError(t, err)
	require.Empty(t, got, "cancelled jobs must never be assigned by poll")
}

func TestReportOnCancelledJobConflicts(t *testing.T) {
	ctx := context.Background()
	jobs := newFakeJobs(
		&domain.BackupJob{ID: "j1", AgentID: "ag-1", PolicyID: "p1", State: domain.JobRunning},
	)
	s := cancelTestService(jobs)
	_, err := s.Cancel(ctx, "j1")
	require.NoError(t, err)

	// In-flight report on the cancelled job is a loud conflict, never a
	// silent no-op: the agent must stop work, not assume acceptance.
	_, err = s.ReportResult(ctx, "ag-1", "j1", Outcome{State: domain.JobUploading})
	require.ErrorIs(t, err, domain.ErrConflict)

	// Progress-only reports conflict too: nothing is persisted for a
	// cancelled job.
	_, err = s.ReportResult(ctx, "ag-1", "j1", Outcome{BytesRead: u64ptr(10)})
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestTakeCancelledNoopNotifier(t *testing.T) {
	s := NewService(Deps{
		Jobs:     newFakeJobs(),
		Agents:   newFakeAgents(),
		Policies: newFakePolicies(nil, nil),
		// No Cancels: the noop default does not track.
	})
	ids, err := s.TakeCancelled(context.Background(), "ag-1")
	require.NoError(t, err)
	require.NotNil(t, ids)
	require.Empty(t, ids)
}

func TestMemoryCancelNotifierDedupes(t *testing.T) {
	ctx := context.Background()
	n := NewMemoryCancelNotifier()
	require.NoError(t, n.NotifyCancel(ctx, "ag-1", "j1"))
	require.NoError(t, n.NotifyCancel(ctx, "ag-1", "j1"))
	require.NoError(t, n.NotifyCancel(ctx, "", "j1")) // ignored, never fails
	require.Equal(t, []string{"j1"}, n.TakeCancelled("ag-1"))
}

func u64ptr(v uint64) *uint64 { return &v }
