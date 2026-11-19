package main

// Postgres integration test for the schedule/jobs/agents adapters: manual
// enqueue through scheduleJobEnqueuer (version pinning), load through
// jobConcurrencyGate, and agent dispatch through agentJobQueue (capability
// lookup, pinned-version target join, kind projection, report mapping).
//
// Skipped unless TEST_DATABASE_URL is set; shares the isolated
// "<db>_manager" database from oidc_secret_integration_test.go (unique
// names per test; rows are cleaned up).

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	"github.com/vaultix/vaultix/internal/adapter/repo"
	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	jobuc "github.com/vaultix/vaultix/internal/usecase/job"
	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIntegrationScheduleJobsAgentsFlow(t *testing.T) {
	pool := openManagerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	log := testLogger()
	repos := repo.New(pool)

	agentSvc, err := agentuc.NewService(repos.Agents, repos.Policies, domain.SystemClock{})
	require.NoError(t, err)
	policySvc := policyuc.NewService(repos.Policies,
		policyuc.WithStorage(repos.Storage), policyuc.WithStacks(repos.Stacks))
	jobSvc := jobuc.NewService(jobuc.Deps{
		Jobs: repos.BackupJobs, Agents: repos.Agents, Policies: repos.Policies,
	})

	// One agent advertising docker, enrolled through the real flow.
	token, _, err := agentSvc.IssueEnrollmentToken(ctx, time.Hour)
	require.NoError(t, err)
	agent, _, err := agentSvc.RedeemEnrollmentToken(ctx, token, agentuc.Registration{
		Hostname: "integ-agent", Version: "0.0.0", Capabilities: []string{"docker"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agents WHERE id = $1::uuid`, agent.ID)
	})

	// One policy with a filesystem + docker target; Create publishes v1.
	policyName := "integ-policy"
	p, v, err := policySvc.Create(ctx, policyuc.CreateInput{
		Name:     policyName,
		AgentIDs: []string{agent.ID},
		Targets: []policyuc.TargetInput{
			{Type: domain.TargetHostFilesystem, Selector: "/data"},
			{Type: domain.TargetDockerVolume, Selector: "vol1"},
		},
		Retention: domain.RetentionRule{KeepLastN: 3},
	})
	require.NoError(t, err)
	// NOTE: the real repo numbers the first published version from the
	// schema default (v2), while usecase fakes start at v1. Either way the
	// policy, version, and pinned jobs stay mutually consistent.
	require.Equal(t, p.CurrentVersion, v.Version)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM backup_policies WHERE id = $1::uuid`, p.ID)
	})

	// A real schedule to record as the firing definition.
	scheduleSvc := scheduleuc.NewService(repos.Schedules, repos.Policies)
	sched, err := scheduleSvc.Create(ctx, scheduleuc.CreateInput{
		PolicyID: p.ID, Kind: domain.ScheduleHourly, IntervalHours: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM schedules WHERE id = $1::uuid`, sched.ID)
	})

	// Manual enqueue pins the requested version and records the schedule.
	enqueuer := &scheduleJobEnqueuer{jobs: jobSvc, policies: repos.Policies, repo: repos.BackupJobs}
	job, err := enqueuer.EnqueueJob(ctx, scheduleuc.EnqueueRequest{
		PolicyID: p.ID, PolicyVersion: v.Version, ScheduleID: sched.ID, AgentID: agent.ID,
	})
	require.NoError(t, err)
	require.Equal(t, v.Version, job.PolicyVersion)
	require.Equal(t, sched.ID, job.ScheduleID)
	require.Equal(t, domain.JobTriggerSchedule, job.Trigger)

	// Stale version requests fail loudly instead of pinning wrong.
	_, err = enqueuer.EnqueueJob(ctx, scheduleuc.EnqueueRequest{
		PolicyID: p.ID, PolicyVersion: v.Version + 99, ScheduleID: sched.ID, AgentID: agent.ID,
	})
	require.ErrorIs(t, err, domain.ErrConflict)

	// The gate observes the queued job.
	gate := &jobConcurrencyGate{repo: repos.BackupJobs}
	counts, err := gate.ActiveCounts(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, counts.Global, 1)
	require.GreaterOrEqual(t, counts.PerAgent[agent.ID], 1)
	require.GreaterOrEqual(t, counts.PerPolicy[p.ID], 1)

	// The agent polls: capability lookup + pinned-version target join.
	queue := &agentJobQueue{jobs: jobSvc, agents: repos.Agents, policies: repos.Policies, log: log}
	queued, err := queue.Poll(ctx, agent.ID, 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	require.Equal(t, job.ID, queued[0].ID)
	require.Equal(t, p.ID, queued[0].PolicyID)
	require.Equal(t, v.Version, queued[0].PolicyVersion)
	require.Equal(t, "filesystem", queued[0].Kind)
	require.Equal(t, []string{"host_filesystem:/data", "docker_volume:vol1"}, queued[0].Targets)
	require.Equal(t, string(domain.JobAssigned), queued[0].State)

	// Progress report maps onto the outcome without transitioning.
	require.NoError(t, queue.Report(ctx, agent.ID, agentshttp.JobReport{
		JobID: job.ID, BytesRead: u64(100), BytesUploaded: u64(50),
	}))
	got, err := jobSvc.Get(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobAssigned, got.State)
	require.NotNil(t, got.BytesRead)
	require.Equal(t, uint64(100), *got.BytesRead)
}

func u64(v uint64) *uint64 { return &v }
