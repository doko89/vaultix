package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeBackupExecutor struct {
	mu    sync.Mutex
	calls []BackupExecutionArgs
	fail  int // fail the first N calls
	n     atomic.Int32
}

func (f *fakeBackupExecutor) EnqueueScheduled(_ context.Context, agentID, policyID, scheduleID string) (*domain.BackupJob, error) {
	n := int(f.n.Add(1))
	f.mu.Lock()
	f.calls = append(f.calls, BackupExecutionArgs{AgentID: agentID, PolicyID: policyID, ScheduleID: scheduleID})
	f.mu.Unlock()
	if n <= f.fail {
		return nil, fmt.Errorf("boom")
	}
	return &domain.BackupJob{ID: "job-1", AgentID: agentID, PolicyID: policyID}, nil
}

type fakeRetentionEnforcer struct {
	mu       sync.Mutex
	policies []string
	err      error
}

func (f *fakeRetentionEnforcer) EnforceRetention(_ context.Context, policyID string) error {
	f.mu.Lock()
	f.policies = append(f.policies, policyID)
	f.mu.Unlock()
	return f.err
}

func fastRetry() map[string]RetryPolicy {
	fast := RetryPolicy{MaxAttempts: 3, InitialInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond, Multiplier: 2}
	return map[string]RetryPolicy{
		BackupExecutionArgs{}.Kind(): fast,
		RetentionArgs{}.Kind():       fast,
	}
}

func startTestScheduler(t *testing.T, backup BackupExecutor, retention RetentionEnforcer) (*Scheduler, context.Context) {
	t.Helper()
	sched, err := New(Config{
		Retry:           fastRetry(),
		ShutdownTimeout: 5 * time.Second,
		Logger:          discardLogger(),
	}, backup, retention)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, sched.Start(ctx))
	t.Cleanup(func() { _ = sched.Stop(context.Background()) })
	return sched, ctx
}

func TestNewRequiresExecutors(t *testing.T) {
	_, err := New(Config{}, nil, &fakeRetentionEnforcer{})
	require.Error(t, err)
	_, err = New(Config{}, &fakeBackupExecutor{}, nil)
	require.Error(t, err)
}

func TestBackupDispatch(t *testing.T) {
	exec := &fakeBackupExecutor{}
	sched, ctx := startTestScheduler(t, exec, &fakeRetentionEnforcer{})

	require.NoError(t, sched.EnqueueBackup(ctx, BackupExecutionArgs{AgentID: "a-1", PolicyID: "p-1", ScheduleID: "s-1"}))
	require.Eventually(t, func() bool {
		exec.mu.Lock()
		defer exec.mu.Unlock()
		return len(exec.calls) == 1
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, BackupExecutionArgs{AgentID: "a-1", PolicyID: "p-1", ScheduleID: "s-1"}, exec.calls[0])
}

func TestRetentionDispatch(t *testing.T) {
	enforcer := &fakeRetentionEnforcer{}
	sched, ctx := startTestScheduler(t, &fakeBackupExecutor{}, enforcer)

	require.NoError(t, sched.EnqueueRetention(ctx, RetentionArgs{PolicyID: "p-9", Trigger: "manual"}))
	require.Eventually(t, func() bool {
		enforcer.mu.Lock()
		defer enforcer.mu.Unlock()
		return len(enforcer.policies) == 1
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{"p-9"}, enforcer.policies)
}

func TestRetryThenSuccess(t *testing.T) {
	exec := &fakeBackupExecutor{fail: 2}
	sched, ctx := startTestScheduler(t, exec, &fakeRetentionEnforcer{})

	require.NoError(t, sched.EnqueueBackup(ctx, BackupExecutionArgs{AgentID: "a-1", PolicyID: "p-1"}))
	require.Eventually(t, func() bool { return exec.n.Load() == 3 },
		5*time.Second, 5*time.Millisecond, "two failures then success")
}

func TestRetryBudgetExhausted(t *testing.T) {
	exec := &fakeBackupExecutor{fail: 100}
	sched, ctx := startTestScheduler(t, exec, &fakeRetentionEnforcer{})

	require.NoError(t, sched.EnqueueBackup(ctx, BackupExecutionArgs{AgentID: "a-1", PolicyID: "p-1"}))
	require.Eventually(t, func() bool { return exec.n.Load() == 3 },
		5*time.Second, 5*time.Millisecond)
	// Give the loop a chance to (incorrectly) retry past budget.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(3), exec.n.Load(), "must stop after MaxAttempts")
}

func TestEnqueueBeforeStartFails(t *testing.T) {
	sched, err := New(Config{Logger: discardLogger()}, &fakeBackupExecutor{}, &fakeRetentionEnforcer{})
	require.NoError(t, err)
	err = sched.EnqueueBackup(context.Background(), BackupExecutionArgs{})
	require.ErrorContains(t, err, "not running")
	require.Error(t, sched.Health(context.Background()))
}

func TestDoubleStartFails(t *testing.T) {
	sched, err := New(Config{Logger: discardLogger()}, &fakeBackupExecutor{}, &fakeRetentionEnforcer{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sched.Start(ctx))
	require.ErrorContains(t, sched.Start(ctx), "already started")
	require.NoError(t, sched.Health(ctx))
	require.NoError(t, sched.Stop(context.Background()))
	require.Error(t, sched.Health(context.Background()))
	require.ErrorContains(t, sched.Start(ctx), "already stopped")
}

func TestGracefulStopDrainsInflight(t *testing.T) {
	release := make(chan struct{})
	exec := &fakeBackupExecutor{}
	_ = exec
	blocking := &blockingExecutor{release: release}
	sched, err := New(Config{ShutdownTimeout: 5 * time.Second, Logger: discardLogger()},
		blocking, &fakeRetentionEnforcer{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sched.Start(ctx))
	require.NoError(t, sched.EnqueueBackup(ctx, BackupExecutionArgs{AgentID: "a-1"}))
	// Wait until the worker picked the job up.
	require.Eventually(t, blocking.started.Load, 5*time.Second, time.Millisecond)
	stopped := make(chan error, 1)
	go func() { stopped <- sched.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before in-flight job finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after in-flight job finished")
	}
}

type blockingExecutor struct {
	release chan struct{}
	started atomic.Bool
}

func (b *blockingExecutor) EnqueueScheduled(ctx context.Context, agentID, policyID, scheduleID string) (*domain.BackupJob, error) {
	b.started.Store(true)
	select {
	case <-b.release:
		return &domain.BackupJob{ID: "job-1"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestBackoffCurve(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second, MaxInterval: 5 * time.Second, Multiplier: 2}
	assert.Equal(t, time.Second, BackoffForAttempt(p, 1))
	assert.Equal(t, 2*time.Second, BackoffForAttempt(p, 2))
	assert.Equal(t, 4*time.Second, BackoffForAttempt(p, 3))
	assert.Equal(t, 5*time.Second, BackoffForAttempt(p, 4), "capped at MaxInterval")
	assert.Equal(t, 5*time.Second, BackoffForAttempt(p, 10))
}

func TestRetryPolicyMapping(t *testing.T) {
	backup := RetryPolicyFor(BackupExecutionArgs{}.Kind())
	assert.Equal(t, 5, backup.MaxAttempts)
	retention := RetryPolicyFor(RetentionArgs{}.Kind())
	assert.Equal(t, 3, retention.MaxAttempts)
	def := RetryPolicyFor("unknown-kind")
	assert.Equal(t, 3, def.MaxAttempts)
	for _, p := range []RetryPolicy{backup, retention, def} {
		assert.Positive(t, p.InitialInterval)
		assert.GreaterOrEqual(t, p.MaxInterval, p.InitialInterval)
	}
}

func weekday(d time.Weekday) *time.Weekday { return &d }

func TestNextRun(t *testing.T) {
	utc := time.UTC
	after := time.Date(2026, 11, 19, 12, 0, 0, 0, utc) // a Thursday

	daily := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "14:30", Timezone: "UTC"}
	next, ok := NextRun(daily, after)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 11, 19, 14, 30, 0, 0, utc), next)

	later := time.Date(2026, 11, 19, 15, 0, 0, 0, utc)
	next, ok = NextRun(daily, later)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 11, 20, 14, 30, 0, 0, utc), next, "rolls to next day")

	weekly := domain.Schedule{Kind: domain.ScheduleWeekly, TimeOfDay: "09:00", Weekday: weekday(time.Monday), Timezone: "UTC"}
	next, ok = NextRun(weekly, after)
	require.True(t, ok)
	assert.Equal(t, time.Monday, next.Weekday())
	assert.Equal(t, time.Date(2026, 11, 23, 9, 0, 0, 0, utc), next)

	hourly := domain.Schedule{Kind: domain.ScheduleHourly, IntervalHours: 6, Timezone: "UTC"}
	next, ok = NextRun(hourly, after)
	require.True(t, ok)
	assert.True(t, next.After(after))
	assert.True(t, next.Sub(after) <= 6*time.Hour)
	assert.Zero(t, next.Minute())

	runAt := after.Add(2 * time.Hour)
	oneTime := domain.Schedule{Kind: domain.ScheduleOneTime, RunAt: &runAt, Timezone: "UTC"}
	next, ok = NextRun(oneTime, after)
	require.True(t, ok)
	assert.Equal(t, runAt, next)

	past := after.Add(-time.Hour)
	oneTimePast := domain.Schedule{Kind: domain.ScheduleOneTime, RunAt: &past, Timezone: "UTC"}
	_, ok = NextRun(oneTimePast, after)
	assert.False(t, ok, "one-time in the past has no next run")

	paused := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC", Paused: true}
	_, ok = NextRun(paused, after)
	assert.False(t, ok, "paused schedules never fire")

	cron := domain.Schedule{Kind: domain.ScheduleCron, CronExpr: "0 9 * * *", Timezone: "UTC"}
	_, ok = NextRun(cron, after)
	assert.False(t, ok, "cron evaluation rides on River periodics (see PeriodicSpecs)")

	// Timezone-aware: 09:00 in Jakarta is 02:00 UTC.
	jakarta := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "09:00", Timezone: "Asia/Jakarta"}
	next, ok = NextRun(jakarta, time.Date(2026, 11, 19, 0, 0, 0, 0, utc))
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 11, 19, 2, 0, 0, 0, utc), next.UTC())
}

func TestAdvance(t *testing.T) {
	after := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	s := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "14:30", Timezone: "UTC"}
	require.True(t, Advance(&s, after))
	require.NotNil(t, s.NextRunAt)
	assert.Equal(t, time.Date(2026, 11, 19, 14, 30, 0, 0, time.UTC), *s.NextRunAt)

	past := after.Add(-time.Hour)
	one := domain.Schedule{Kind: domain.ScheduleOneTime, RunAt: &past, Timezone: "UTC"}
	assert.False(t, Advance(&one, after))
	assert.Nil(t, one.NextRunAt)
}

func TestPeriodicSpecs(t *testing.T) {
	specs, err := PeriodicSpecs([]domain.Schedule{
		{ID: "s-hourly", PolicyID: "p-1", Kind: domain.ScheduleHourly, IntervalHours: 6, Timezone: "UTC"},
		{ID: "s-daily", PolicyID: "p-1", Kind: domain.ScheduleDaily, TimeOfDay: "02:30", Timezone: "Asia/Jakarta"},
		{ID: "s-weekly", PolicyID: "p-2", Kind: domain.ScheduleWeekly, TimeOfDay: "03:00", Weekday: weekday(time.Sunday), Timezone: "UTC"},
		{ID: "s-paused", PolicyID: "p-2", Kind: domain.ScheduleDaily, TimeOfDay: "03:00", Timezone: "UTC", Paused: true},
	})
	require.NoError(t, err)
	require.Len(t, specs, 3, "paused schedules are skipped")
	assert.Equal(t, 6*time.Hour, specs[0].Interval)
	assert.Equal(t, "30 2 * * *", specs[1].Cron)
	assert.Equal(t, "Asia/Jakarta", specs[1].Timezone)
	assert.Equal(t, "0 3 * * 0", specs[2].Cron)

	_, err = PeriodicSpecs([]domain.Schedule{
		{ID: "bad", PolicyID: "p-1", Kind: domain.ScheduleDaily, TimeOfDay: "nope", Timezone: "UTC"},
	})
	require.Error(t, err)
}

func TestWorkerRequiresExecutor(t *testing.T) {
	var w BackupExecutionWorker
	require.ErrorContains(t, w.Work(context.Background(), BackupExecutionArgs{}), "not wired")
	var r RetentionWorker
	require.ErrorContains(t, r.Work(context.Background(), RetentionArgs{}), "not wired")
}

func TestDefaultQueues(t *testing.T) {
	q := DefaultQueues()
	for _, name := range []string{QueueDefault, QueueBackup, QueueRetention, QueueMaintenance} {
		cfg, ok := q[name]
		require.True(t, ok, "queue %s defined", name)
		assert.Positive(t, cfg.MaxWorkers)
		assert.Positive(t, cfg.MaxQueued)
	}
}
