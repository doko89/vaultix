package job

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---- Fakes ----

type fakeJobs struct {
	mu   sync.Mutex
	jobs map[string]*domain.BackupJob
}

func newFakeJobs(seed ...*domain.BackupJob) *fakeJobs {
	f := &fakeJobs{jobs: map[string]*domain.BackupJob{}}
	for _, j := range seed {
		cp := *j
		f.jobs[j.ID] = &cp
	}
	return f
}

func (f *fakeJobs) CreateJob(_ context.Context, j *domain.BackupJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.jobs[j.ID]; ok {
		return fmt.Errorf("fake: duplicate job: %w", domain.ErrConflict)
	}
	cp := *j
	f.jobs[j.ID] = &cp
	return nil
}

func (f *fakeJobs) GetJob(_ context.Context, id string) (*domain.BackupJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, fmt.Errorf("fake: job %s: %w", id, domain.ErrNotFound)
	}
	cp := *j
	return &cp, nil
}

func (f *fakeJobs) UpdateJob(_ context.Context, j *domain.BackupJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.jobs[j.ID]; !ok {
		return fmt.Errorf("fake: job %s: %w", j.ID, domain.ErrNotFound)
	}
	cp := *j
	f.jobs[j.ID] = &cp
	return nil
}

func (f *fakeJobs) ListJobs(_ context.Context, opts domain.ListOptions, filter domain.JobFilter) (domain.ListResult[domain.BackupJob], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []domain.BackupJob
	for _, j := range f.jobs {
		if filter.AgentID != "" && j.AgentID != filter.AgentID {
			continue
		}
		if filter.PolicyID != "" && j.PolicyID != filter.PolicyID {
			continue
		}
		if filter.State != "" && j.State != filter.State {
			continue
		}
		all = append(all, *j)
	}
	sort.Slice(all, func(i, k int) bool { return all[i].ID < all[k].ID })
	total := len(all)
	if opts.Offset < len(all) {
		all = all[opts.Offset:]
	} else {
		all = nil
	}
	if opts.Limit > 0 && len(all) > opts.Limit {
		all = all[:opts.Limit]
	}
	if all == nil {
		all = []domain.BackupJob{}
	}
	return domain.ListResult[domain.BackupJob]{Items: all, Total: total}, nil
}

func (f *fakeJobs) ClaimNextJob(_ context.Context, agentID string) (*domain.BackupJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *domain.BackupJob
	for _, j := range f.jobs {
		if j.AgentID != agentID || j.State != domain.JobQueued {
			continue
		}
		if best == nil || j.CreatedAt.Before(best.CreatedAt) {
			best = j
		}
	}
	if best == nil {
		return nil, fmt.Errorf("fake: no queued job: %w", domain.ErrNotFound)
	}
	best.State = domain.JobAssigned
	cp := *best
	return &cp, nil
}

type fakeAgents struct {
	mu     sync.Mutex
	agents map[string]*domain.Agent
}

func newFakeAgents(seed ...*domain.Agent) *fakeAgents {
	f := &fakeAgents{agents: map[string]*domain.Agent{}}
	for _, a := range seed {
		cp := *a
		f.agents[a.ID] = &cp
	}
	return f
}

func (f *fakeAgents) CreateAgent(_ context.Context, a *domain.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *a
	f.agents[a.ID] = &cp
	return nil
}

func (f *fakeAgents) GetAgent(_ context.Context, id string) (*domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[id]
	if !ok {
		return nil, fmt.Errorf("fake: agent %s: %w", id, domain.ErrNotFound)
	}
	cp := *a
	return &cp, nil
}

func (f *fakeAgents) UpdateAgent(_ context.Context, a *domain.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *a
	f.agents[a.ID] = &cp
	return nil
}

func (f *fakeAgents) ListAgents(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.Agent], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []domain.Agent
	for _, a := range f.agents {
		all = append(all, *a)
	}
	return domain.ListResult[domain.Agent]{Items: all, Total: len(all)}, nil
}

func (f *fakeAgents) StoreCredential(_ context.Context, _ *domain.AgentCredential) error {
	return nil
}

func (f *fakeAgents) GetCredentialByHash(_ context.Context, _ string) (*domain.AgentCredential, error) {
	return nil, fmt.Errorf("fake: no credential: %w", domain.ErrNotFound)
}

func (f *fakeAgents) ListAgentCredentials(_ context.Context, _ string) ([]domain.AgentCredential, error) {
	return nil, nil
}

func (f *fakeAgents) UpdateCredential(_ context.Context, _ *domain.AgentCredential) error {
	return nil
}

func (f *fakeAgents) RecordHeartbeat(_ context.Context, _ *domain.AgentHeartbeat) error {
	return nil
}

func (f *fakeAgents) LatestHeartbeat(_ context.Context, _ string) (*domain.AgentHeartbeat, error) {
	return nil, fmt.Errorf("fake: no heartbeat: %w", domain.ErrNotFound)
}

type fakePolicies struct {
	mu       sync.Mutex
	policies map[string]*domain.BackupPolicy
	versions map[string]*domain.BackupPolicyVersion // policyID:version
}

func newFakePolicies(policies []*domain.BackupPolicy, versions []*domain.BackupPolicyVersion) *fakePolicies {
	f := &fakePolicies{policies: map[string]*domain.BackupPolicy{}, versions: map[string]*domain.BackupPolicyVersion{}}
	for _, p := range policies {
		cp := *p
		f.policies[p.ID] = &cp
	}
	for _, v := range versions {
		cp := *v
		f.versions[v.PolicyID+":"+fmt.Sprint(v.Version)] = &cp
	}
	return f
}

func (f *fakePolicies) CreatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *p
	f.policies[p.ID] = &cp
	return nil
}

func (f *fakePolicies) GetPolicy(_ context.Context, id string) (*domain.BackupPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.policies[id]
	if !ok {
		return nil, fmt.Errorf("fake: policy %s: %w", id, domain.ErrNotFound)
	}
	cp := *p
	return &cp, nil
}

func (f *fakePolicies) UpdatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *p
	f.policies[p.ID] = &cp
	return nil
}

func (f *fakePolicies) DeletePolicy(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.policies, id)
	return nil
}

func (f *fakePolicies) ListPolicies(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []domain.BackupPolicy
	for _, p := range f.policies {
		all = append(all, *p)
	}
	return domain.ListResult[domain.BackupPolicy]{Items: all, Total: len(all)}, nil
}

func (f *fakePolicies) PublishVersion(_ context.Context, v *domain.BackupPolicyVersion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *v
	f.versions[v.PolicyID+":"+fmt.Sprint(v.Version)] = &cp
	return nil
}

func (f *fakePolicies) GetVersion(_ context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[policyID+":"+fmt.Sprint(version)]
	if !ok {
		return nil, fmt.Errorf("fake: version %s:%d: %w", policyID, version, domain.ErrNotFound)
	}
	cp := *v
	return &cp, nil
}

func (f *fakePolicies) ListVersions(_ context.Context, policyID string) ([]domain.BackupPolicyVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BackupPolicyVersion
	for _, v := range f.versions {
		if v.PolicyID == policyID {
			out = append(out, *v)
		}
	}
	return out, nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type fakeCancels struct {
	mu    sync.Mutex
	calls [][2]string
	err   error
}

func (f *fakeCancels) NotifyCancel(_ context.Context, agentID, jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2]string{agentID, jobID})
	return f.err
}

// ---- Harness ----

type harness struct {
	jobs     *fakeJobs
	agents   *fakeAgents
	policies *fakePolicies
	clock    *fakeClock
	cancels  *fakeCancels
	svc      *Service
	ids      int
}

func newHarness(t *testing.T, deps func(*Deps)) *harness {
	t.Helper()
	h := &harness{
		jobs:     newFakeJobs(),
		agents:   newFakeAgents(),
		policies: newFakePolicies(nil, nil),
		clock:    &fakeClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)},
		cancels:  &fakeCancels{},
	}
	d := Deps{
		Jobs:     h.jobs,
		Agents:   h.agents,
		Policies: h.policies,
		Clock:    h.clock,
		Cancels:  h.cancels,
		IDGenerator: func() string {
			h.ids++
			return fmt.Sprintf("job-%03d", h.ids)
		},
	}
	if deps != nil {
		deps(&d)
	}
	h.svc = NewService(d)
	return h
}

func (h *harness) addAgent(id string, disabled bool) {
	h.agents.agents[id] = &domain.Agent{ID: id, Hostname: id, Disabled: disabled}
}

func (h *harness) addPolicy(id string, enabled bool, targets ...domain.BackupPolicyTarget) {
	h.policies.policies[id] = &domain.BackupPolicy{ID: id, Name: id, AgentIDs: []string{"a1"}, Enabled: enabled, CurrentVersion: 3}
	h.policies.versions[id+":3"] = &domain.BackupPolicyVersion{PolicyID: id, Version: 3, Targets: targets, StorageID: "st1"}
}

func (h *harness) seedJob(j *domain.BackupJob) {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = h.clock.Now()
	}
	if j.UpdatedAt.IsZero() {
		j.UpdatedAt = h.clock.Now()
	}
	h.jobs.jobs[j.ID] = j
}

func fptr(f float64) *float64 { return &f }
func uptr(u uint64) *uint64   { return &u }
func bptr(b bool) *bool       { return &b }

// ---- Transition matrix ----

func TestTransitionMatrix(t *testing.T) {
	legal := map[domain.JobState][]domain.JobState{
		domain.JobQueued:    {domain.JobAssigned, domain.JobCancelled},
		domain.JobAssigned:  {domain.JobRunning, domain.JobCancelled, domain.JobQueued},
		domain.JobRunning:   {domain.JobUploading, domain.JobFailed, domain.JobCancelled},
		domain.JobUploading: {domain.JobVerifying, domain.JobFailed, domain.JobCancelled},
		domain.JobVerifying: {domain.JobSucceeded, domain.JobFailed, domain.JobCancelled},
		domain.JobFailed:    {domain.JobQueued},
	}
	states := []domain.JobState{
		domain.JobQueued, domain.JobAssigned, domain.JobRunning, domain.JobUploading,
		domain.JobVerifying, domain.JobSucceeded, domain.JobFailed, domain.JobCancelled,
	}
	ctx := context.Background()
	for _, from := range states {
		for _, to := range states {
			if from == to {
				continue
			}
			wantLegal := false
			for _, n := range legal[from] {
				if n == to {
					wantLegal = true
				}
			}
			name := fmt.Sprintf("%s->%s_legal=%v", from, to, wantLegal)
			t.Run(name, func(t *testing.T) {
				h := newHarness(t, nil)
				h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", PolicyID: "p1", PolicyVersion: 1, State: from})
				got, err := h.svc.Transition(ctx, "j1", to)
				if wantLegal {
					require.NoError(t, err)
					require.Equal(t, to, got.State)
					// Persisted: re-read shows the new state.
					stored, gerr := h.jobs.GetJob(ctx, "j1")
					require.NoError(t, gerr)
					require.Equal(t, to, stored.State)
				} else {
					require.ErrorIs(t, err, domain.ErrConflict)
					stored, gerr := h.jobs.GetJob(ctx, "j1")
					require.NoError(t, gerr)
					require.Equal(t, from, stored.State, "illegal transition must persist nothing")
				}
			})
		}
	}
}

func TestTransitionStampsTimestamps(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.seedJob(&domain.BackupJob{ID: "j1", State: domain.JobAssigned})
	got, err := h.svc.Transition(ctx, "j1", domain.JobRunning)
	require.NoError(t, err)
	require.NotNil(t, got.StartedAt)

	h.clock.now = h.clock.now.Add(time.Hour)
	got, err = h.svc.Transition(ctx, "j1", domain.JobUploading)
	require.NoError(t, err)
	require.NoError(t, func() error { _, err := h.svc.Transition(ctx, "j1", domain.JobVerifying); return err }())
	got, err = h.svc.Transition(ctx, "j1", domain.JobSucceeded)
	require.NoError(t, err)
	require.NotNil(t, got.FinishedAt)
	require.True(t, got.FinishedAt.After(*got.StartedAt))
}

// ---- Enqueue ----

func TestEnqueueManualPinsVersion(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.addAgent("a1", false)
	h.addPolicy("p1", true, domain.BackupPolicyTarget{Type: domain.TargetHostFilesystem, Selector: "/srv"})
	h.policies.versions["p1:3"].Encryption = domain.Encryption{Enabled: true, KeyRef: "key-7"}

	got, err := h.svc.EnqueueManual(ctx, "a1", "p1", "op-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobQueued, got.State)
	require.Equal(t, 3, got.PolicyVersion)
	require.Equal(t, domain.JobTriggerManual, got.Trigger)
	require.Equal(t, 1, got.Attempt)
	require.True(t, got.Encrypted)
	require.Equal(t, "key-7", got.KeyRef)
	require.Equal(t, "st1", got.StorageID)
	require.Equal(t, "op-1", got.CreatedBy)
}

func TestEnqueueScheduled(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.addAgent("a1", false)
	h.addPolicy("p1", true)

	got, err := h.svc.EnqueueScheduled(ctx, "a1", "p1", "sched-9")
	require.NoError(t, err)
	require.Equal(t, domain.JobTriggerSchedule, got.Trigger)
	require.Equal(t, "sched-9", got.ScheduleID)

	_, err = h.svc.EnqueueScheduled(ctx, "a1", "p1", "")
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestEnqueueRejects(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.addAgent("a1", false)
	h.addAgent("a-off", true)
	h.addPolicy("p1", true)
	h.addPolicy("p-off", false)

	_, err := h.svc.EnqueueManual(ctx, "missing", "p1", "op")
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = h.svc.EnqueueManual(ctx, "a-off", "p1", "op")
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = h.svc.EnqueueManual(ctx, "a1", "missing", "op")
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = h.svc.EnqueueManual(ctx, "a1", "p-off", "op")
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = h.svc.EnqueueManual(ctx, "", "p1", "op")
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestEnqueueConcurrencyLimits(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(d *Deps) {
		d.Limits = ConcurrencyLimits{MaxPerAgent: 1, MaxGlobal: 2}
	})
	h.addAgent("a1", false)
	h.addAgent("a2", false)
	h.addPolicy("p1", true)

	_, err := h.svc.EnqueueManual(ctx, "a1", "p1", "op")
	require.NoError(t, err)
	_, err = h.svc.EnqueueManual(ctx, "a1", "p1", "op")
	require.ErrorIs(t, err, domain.ErrConflict, "per-agent cap")

	_, err = h.svc.EnqueueManual(ctx, "a2", "p1", "op")
	require.NoError(t, err)
	_, err = h.svc.EnqueueManual(ctx, "a2", "p1", "op")
	require.ErrorIs(t, err, domain.ErrConflict, "global cap")
}

// ---- Retry ----

func TestRetryAfterFailureCreatesCleanAttempt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.seedJob(&domain.BackupJob{
		ID: "j1", AgentID: "a1", PolicyID: "p1", PolicyVersion: 3,
		State: domain.JobFailed, Progress: fptr(0.7),
		BytesRead: uptr(100), BytesCompressed: uptr(40), BytesUploaded: uptr(10),
		Encrypted: true, KeyRef: "key-7", StorageID: "st1",
		SnapshotID: "snap-1", ErrorCode: "E_UPLOAD", ErrorMessage: "boom",
		Attempt: 1, StartedAt: &[]time.Time{time.Now()}[0],
	})

	got, err := h.svc.Retry(ctx, "j1", "op-2")
	require.NoError(t, err)
	require.NotEqual(t, "j1", got.ID)
	require.Equal(t, domain.JobQueued, got.State)
	require.Equal(t, domain.JobTriggerRetry, got.Trigger)
	require.Equal(t, "j1", got.RetryOf)
	require.Equal(t, 2, got.Attempt)
	require.Equal(t, "a1", got.AgentID)
	require.Equal(t, "p1", got.PolicyID)
	require.Equal(t, 3, got.PolicyVersion)
	// Clean attempt: no progress, bytes, snapshot, or error carried over.
	require.Nil(t, got.Progress)
	require.Nil(t, got.BytesRead)
	require.Nil(t, got.BytesUploaded)
	require.Empty(t, got.SnapshotID)
	require.Empty(t, got.ErrorCode)
	require.Empty(t, got.ErrorMessage)
	require.Nil(t, got.StartedAt)
	require.Nil(t, got.FinishedAt)
	// Config travels with the pinned version.
	require.True(t, got.Encrypted)
	require.Equal(t, "key-7", got.KeyRef)

	// Old job untouched.
	old, err := h.jobs.GetJob(ctx, "j1")
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, old.State)
	require.Equal(t, "snap-1", old.SnapshotID)
	require.Equal(t, "E_UPLOAD", old.ErrorCode)
	require.NotNil(t, old.Progress)

	// History links them.
	hist, err := h.svc.RetryHistory(ctx, got.ID)
	require.NoError(t, err)
	require.Len(t, hist, 2)
	require.Equal(t, "j1", hist[0].ID)
	require.Equal(t, got.ID, hist[1].ID)
}

func TestRetryRequiresFailed(t *testing.T) {
	ctx := context.Background()
	for _, st := range []domain.JobState{domain.JobQueued, domain.JobRunning, domain.JobSucceeded, domain.JobCancelled} {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", State: st})
		_, err := h.svc.Retry(ctx, "j1", "op")
		require.ErrorIs(t, err, domain.ErrConflict, "state %s", st)
	}
}

// ---- Cancel ----

func TestCancelRules(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		state domain.JobState
		ok    bool
	}{
		{domain.JobQueued, true},
		{domain.JobRunning, true},
		{domain.JobAssigned, false},
		{domain.JobUploading, false},
		{domain.JobVerifying, false},
		{domain.JobSucceeded, false},
		{domain.JobFailed, false},
		{domain.JobCancelled, false},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			h := newHarness(t, nil)
			h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: tc.state})
			got, err := h.svc.Cancel(ctx, "j1")
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, domain.JobCancelled, got.State)
				require.NotNil(t, got.FinishedAt)
				require.Len(t, h.cancels.calls, 1)
				require.Equal(t, [2]string{"a1", "j1"}, h.cancels.calls[0])
			} else {
				require.ErrorIs(t, err, domain.ErrConflict)
				stored, _ := h.jobs.GetJob(ctx, "j1")
				require.Equal(t, tc.state, stored.State)
				require.Empty(t, h.cancels.calls)
			}
		})
	}
}

func TestCancelNotifierFailureIsBestEffort(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.cancels.err = fmt.Errorf("agent unreachable")
	h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning})
	got, err := h.svc.Cancel(ctx, "j1")
	require.NoError(t, err)
	require.Equal(t, domain.JobCancelled, got.State)
}

// ---- Poll ----

func TestPollAssignsOldestDueFirst(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.addAgent("a1", false)
	h.addPolicy("p1", true, domain.BackupPolicyTarget{Type: domain.TargetHostFilesystem, Selector: "/srv"})
	base := h.clock.Now()
	for i, id := range []string{"j-old", "j-mid", "j-new"} {
		h.seedJob(&domain.BackupJob{
			ID: id, AgentID: "a1", PolicyID: "p1", PolicyVersion: 3,
			State:     domain.JobQueued,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	got, err := h.svc.PollJobs(ctx, "a1", nil)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "j-old", got[0].ID)
	require.Equal(t, "j-mid", got[1].ID)
	require.Equal(t, "j-new", got[2].ID)
	for _, j := range got {
		require.Equal(t, domain.JobAssigned, j.State)
	}
	// Persisted.
	stored, _ := h.jobs.GetJob(ctx, "j-old")
	require.Equal(t, domain.JobAssigned, stored.State)
}

func TestPollSkips(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled agent", func(t *testing.T) {
		h := newHarness(t, nil)
		h.addAgent("a1", true)
		got, err := h.svc.PollJobs(ctx, "a1", nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Empty(t, got)
	})

	t.Run("unknown agent", func(t *testing.T) {
		h := newHarness(t, nil)
		got, err := h.svc.PollJobs(ctx, "ghost", nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("disabled policy jobs skipped, healthy assigned", func(t *testing.T) {
		h := newHarness(t, nil)
		h.addAgent("a1", false)
		h.addPolicy("p-good", true)
		h.addPolicy("p-off", false)
		h.seedJob(&domain.BackupJob{ID: "j-bad", AgentID: "a1", PolicyID: "p-off", PolicyVersion: 3, State: domain.JobQueued})
		h.seedJob(&domain.BackupJob{ID: "j-good", AgentID: "a1", PolicyID: "p-good", PolicyVersion: 3, State: domain.JobQueued})

		got, err := h.svc.PollJobs(ctx, "a1", nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, "j-good", got[0].ID)
		stored, _ := h.jobs.GetJob(ctx, "j-bad")
		require.Equal(t, domain.JobQueued, stored.State)
	})

	t.Run("missing policy version skipped", func(t *testing.T) {
		h := newHarness(t, nil)
		h.addAgent("a1", false)
		h.policies.policies["p1"] = &domain.BackupPolicy{ID: "p1", Enabled: true, CurrentVersion: 9}
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", PolicyID: "p1", PolicyVersion: 9, State: domain.JobQueued})
		got, err := h.svc.PollJobs(ctx, "a1", nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("docker job needs docker capability", func(t *testing.T) {
		newDockerHarness := func(t *testing.T) *harness {
			h := newHarness(t, nil)
			h.addAgent("a1", false)
			h.addPolicy("p1", true, domain.BackupPolicyTarget{Type: domain.TargetDockerVolume, Selector: "data"})
			h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", PolicyID: "p1", PolicyVersion: 3, State: domain.JobQueued})
			return h
		}
		h := newDockerHarness(t)
		got, err := h.svc.PollJobs(ctx, "a1", []string{"docker"})
		require.NoError(t, err)
		require.Len(t, got, 1)

		h = newDockerHarness(t)
		got, err = h.svc.PollJobs(ctx, "a1", []string{"postgres"})
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("per-agent cap", func(t *testing.T) {
		h := newHarness(t, func(d *Deps) { d.Limits = ConcurrencyLimits{MaxPerAgent: 1} })
		h.addAgent("a1", false)
		h.addPolicy("p1", true)
		h.seedJob(&domain.BackupJob{ID: "j-run", AgentID: "a1", PolicyID: "p1", PolicyVersion: 3, State: domain.JobRunning})
		h.seedJob(&domain.BackupJob{ID: "j-wait", AgentID: "a1", PolicyID: "p1", PolicyVersion: 3, State: domain.JobQueued})
		got, err := h.svc.PollJobs(ctx, "a1", nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("batch size", func(t *testing.T) {
		h := newHarness(t, func(d *Deps) { d.PollBatchSize = 2 })
		h.addAgent("a1", false)
		h.addPolicy("p1", true)
		for _, id := range []string{"j1", "j2", "j3"} {
			h.seedJob(&domain.BackupJob{ID: id, AgentID: "a1", PolicyID: "p1", PolicyVersion: 3, State: domain.JobQueued})
		}
		got, err := h.svc.PollJobs(ctx, "a1", nil)
		require.NoError(t, err)
		require.Len(t, got, 2)
	})
}

// ---- Report ----

func TestReportValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown job", func(t *testing.T) {
		h := newHarness(t, nil)
		_, err := h.svc.ReportResult(ctx, "a1", "ghost", Outcome{State: domain.JobRunning})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("agent mismatch", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobAssigned})
		_, err := h.svc.ReportResult(ctx, "a2", "j1", Outcome{State: domain.JobRunning})
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("terminal job", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobSucceeded})
		_, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{Progress: fptr(1)})
		require.ErrorIs(t, err, domain.ErrConflict)
	})

	t.Run("illegal transition persists nothing", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning})
		_, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{State: domain.JobSucceeded, Progress: fptr(1)})
		require.ErrorIs(t, err, domain.ErrConflict)
		stored, _ := h.jobs.GetJob(ctx, "j1")
		require.Equal(t, domain.JobRunning, stored.State)
		require.Nil(t, stored.Progress)
	})

	t.Run("bad progress", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning})
		_, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{Progress: fptr(2)})
		require.ErrorIs(t, err, domain.ErrValidation)
	})

	t.Run("encryption without key ref", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning})
		_, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{Encrypted: bptr(true)})
		require.ErrorIs(t, err, domain.ErrValidation)
	})
}

func TestReportLifecycle(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobAssigned})

	// Progress-only report: no transition.
	got, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{Progress: fptr(0.1), BytesRead: uptr(50)})
	require.NoError(t, err)
	require.Equal(t, domain.JobAssigned, got.State)
	require.Equal(t, 0.1, *got.Progress)
	require.Equal(t, uint64(50), *got.BytesRead)

	// Full lifecycle through reports.
	for _, st := range []domain.JobState{domain.JobRunning, domain.JobUploading, domain.JobVerifying} {
		got, err = h.svc.ReportResult(ctx, "a1", "j1", Outcome{State: st})
		require.NoError(t, err)
		require.Equal(t, st, got.State)
	}
	require.NotNil(t, got.StartedAt)

	// Snapshot registration alone never succeeds the job.
	got, err = h.svc.ReportResult(ctx, "a1", "j1", Outcome{SnapshotID: "snap-1"})
	require.NoError(t, err)
	require.Equal(t, domain.JobVerifying, got.State)
	require.Equal(t, "snap-1", got.SnapshotID)

	got, err = h.svc.ReportResult(ctx, "a1", "j1", Outcome{State: domain.JobSucceeded})
	require.NoError(t, err)
	require.Equal(t, domain.JobSucceeded, got.State)
	require.NotNil(t, got.FinishedAt)
}

// ---- Misc ----

func TestAssign(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.addAgent("a1", false)
	h.addAgent("a-off", true)
	h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobQueued})
	h.seedJob(&domain.BackupJob{ID: "j2", AgentID: "a1", State: domain.JobRunning})

	got, err := h.svc.Assign(ctx, "j1", "a1")
	require.NoError(t, err)
	require.Equal(t, domain.JobAssigned, got.State)

	_, err = h.svc.Assign(ctx, "j2", "a1")
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = h.svc.Assign(ctx, "j1", "a-off")
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = h.svc.Assign(ctx, "ghost", "a1")
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestUpdateProgressAndEncryption(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.seedJob(&domain.BackupJob{ID: "j1", State: domain.JobRunning})
	h.seedJob(&domain.BackupJob{ID: "j-done", State: domain.JobSucceeded})

	got, err := h.svc.UpdateProgress(ctx, "j1", ProgressUpdate{Progress: fptr(0.5), BytesRead: uptr(100), BytesCompressed: uptr(30), BytesUploaded: uptr(10)})
	require.NoError(t, err)
	require.Equal(t, domain.JobRunning, got.State, "progress never transitions")
	require.InEpsilon(t, 0.3, *got.CompressionRatio(), 1e-9)

	_, err = h.svc.UpdateProgress(ctx, "j1", ProgressUpdate{Progress: fptr(-1)})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = h.svc.UpdateProgress(ctx, "j-done", ProgressUpdate{Progress: fptr(1)})
	require.ErrorIs(t, err, domain.ErrConflict)

	got, err = h.svc.RecordEncryptionStatus(ctx, "j1", true, "key-1")
	require.NoError(t, err)
	require.True(t, got.Encrypted)
	_, err = h.svc.RecordEncryptionStatus(ctx, "j1", true, "")
	require.ErrorIs(t, err, domain.ErrValidation)

	got, err = h.svc.RecordError(ctx, "j1", "E_IO", "disk full")
	require.NoError(t, err)
	require.Equal(t, "E_IO", got.ErrorCode)
	require.Equal(t, domain.JobRunning, got.State, "recording error never transitions")
	_, err = h.svc.RecordError(ctx, "j1", "", "")
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestListDateFilter(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	h.seedJob(&domain.BackupJob{ID: "j-old", AgentID: "a1", State: domain.JobSucceeded, CreatedAt: base.Add(-48 * time.Hour)})
	h.seedJob(&domain.BackupJob{ID: "j-mid", AgentID: "a1", State: domain.JobFailed, CreatedAt: base.Add(-2 * time.Hour)})
	h.seedJob(&domain.BackupJob{ID: "j-new", AgentID: "a2", State: domain.JobQueued, CreatedAt: base.Add(time.Hour)})

	since := base.Add(-24 * time.Hour)
	until := base.Add(24 * time.Hour)
	res, err := h.svc.List(ctx, domain.ListOptions{}, Filter{Since: &since, Until: &until})
	require.NoError(t, err)
	require.Len(t, res.Items, 2)

	res, err = h.svc.List(ctx, domain.ListOptions{}, Filter{JobFilter: domain.JobFilter{AgentID: "a1"}})
	require.NoError(t, err)
	require.Len(t, res.Items, 2)
}

func TestCountActive(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning})
	h.seedJob(&domain.BackupJob{ID: "j2", AgentID: "a1", State: domain.JobSucceeded})
	h.seedJob(&domain.BackupJob{ID: "j3", AgentID: "a2", State: domain.JobQueued})

	n, err := h.svc.CountActive(ctx, "a1")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	n, err = h.svc.CountActive(ctx, "")
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("get missing", func(t *testing.T) {
		h := newHarness(t, nil)
		_, err := h.svc.Get(ctx, "ghost")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("missing job everywhere", func(t *testing.T) {
		h := newHarness(t, nil)
		_, err := h.svc.Transition(ctx, "ghost", domain.JobAssigned)
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.Assign(ctx, "ghost", "a1")
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.UpdateProgress(ctx, "ghost", ProgressUpdate{Progress: fptr(0.1)})
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.RecordEncryptionStatus(ctx, "ghost", false, "")
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.RecordError(ctx, "ghost", "E", "m")
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.Retry(ctx, "ghost", "op")
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.Cancel(ctx, "ghost")
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = h.svc.RetryHistory(ctx, "ghost")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("assign to unknown agent", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", State: domain.JobQueued})
		_, err := h.svc.Assign(ctx, "j1", "ghost")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("report disables encryption and clears key ref", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning, Encrypted: true, KeyRef: "key-1"})
		got, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{Encrypted: bptr(false)})
		require.NoError(t, err)
		require.False(t, got.Encrypted)
		require.Empty(t, got.KeyRef)
	})

	t.Run("report rotates key ref", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", AgentID: "a1", State: domain.JobRunning, Encrypted: true, KeyRef: "key-1"})
		got, err := h.svc.ReportResult(ctx, "a1", "j1", Outcome{Encrypted: bptr(true), KeyRef: "key-2"})
		require.NoError(t, err)
		require.Equal(t, "key-2", got.KeyRef)
	})

	t.Run("noop cancels", func(t *testing.T) {
		require.NoError(t, NoopCancelNotifier().NotifyCancel(ctx, "a1", "j1"))
	})

	t.Run("retry history cycle safe", func(t *testing.T) {
		h := newHarness(t, nil)
		h.seedJob(&domain.BackupJob{ID: "j1", State: domain.JobFailed, RetryOf: "j1"})
		hist, err := h.svc.RetryHistory(ctx, "j1")
		require.NoError(t, err)
		require.Len(t, hist, 1)
	})
}

func TestNewServiceDefaults(t *testing.T) {
	s := NewService(Deps{Jobs: newFakeJobs(), Agents: newFakeAgents(), Policies: newFakePolicies(nil, nil)})
	require.NotNil(t, s.clock)
	require.NotNil(t, s.cancels)
	require.NotNil(t, s.idgen)
	require.Equal(t, DefaultPollBatchSize, s.pollBatchSize)
	require.NotEmpty(t, s.idgen())
}
