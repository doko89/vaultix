package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---- Fakes ----

type fakeSchedules struct {
	items map[string]*domain.Schedule
}

func newFakeSchedules() *fakeSchedules {
	return &fakeSchedules{items: map[string]*domain.Schedule{}}
}

func cloneSchedule(s *domain.Schedule) *domain.Schedule {
	cp := *s
	if s.Weekday != nil {
		wd := *s.Weekday
		cp.Weekday = &wd
	}
	if s.RunAt != nil {
		t := *s.RunAt
		cp.RunAt = &t
	}
	if s.NextRunAt != nil {
		t := *s.NextRunAt
		cp.NextRunAt = &t
	}
	return &cp
}

func (f *fakeSchedules) CreateSchedule(_ context.Context, s *domain.Schedule) error {
	f.items[s.ID] = cloneSchedule(s)
	return nil
}

func (f *fakeSchedules) GetSchedule(_ context.Context, id string) (*domain.Schedule, error) {
	s, ok := f.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return cloneSchedule(s), nil
}

func (f *fakeSchedules) UpdateSchedule(_ context.Context, s *domain.Schedule) error {
	if _, ok := f.items[s.ID]; !ok {
		return domain.ErrNotFound
	}
	f.items[s.ID] = cloneSchedule(s)
	return nil
}

func (f *fakeSchedules) DeleteSchedule(_ context.Context, id string) error {
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func (f *fakeSchedules) ListSchedules(_ context.Context, policyID string) ([]domain.Schedule, error) {
	var out []domain.Schedule
	for _, s := range f.items {
		if s.PolicyID == policyID {
			out = append(out, *cloneSchedule(s))
		}
	}
	return out, nil
}

func (f *fakeSchedules) ListAllSchedules(_ context.Context) ([]domain.Schedule, error) {
	var out []domain.Schedule
	for _, s := range f.items {
		out = append(out, *cloneSchedule(s))
	}
	return out, nil
}

func (f *fakeSchedules) DueSchedules(_ context.Context, now time.Time, limit int) ([]domain.Schedule, error) {
	var out []domain.Schedule
	for _, s := range f.items {
		if s.Active() && s.NextRunAt != nil && !s.NextRunAt.After(now) {
			out = append(out, *cloneSchedule(s))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type fakePolicies struct {
	items map[string]*domain.BackupPolicy
}

func (f *fakePolicies) CreatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	f.items[p.ID] = p
	return nil
}

func (f *fakePolicies) GetPolicy(_ context.Context, id string) (*domain.BackupPolicy, error) {
	p, ok := f.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	cp.AgentIDs = append([]string{}, p.AgentIDs...)
	return &cp, nil
}

func (f *fakePolicies) UpdatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	f.items[p.ID] = p
	return nil
}

func (f *fakePolicies) DeletePolicy(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

func (f *fakePolicies) ListPolicies(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	return domain.ListResult[domain.BackupPolicy]{}, nil
}

func (f *fakePolicies) PublishVersion(_ context.Context, _ *domain.BackupPolicyVersion) error {
	return nil
}

func (f *fakePolicies) GetVersion(_ context.Context, _ string, _ int) (*domain.BackupPolicyVersion, error) {
	return nil, domain.ErrNotFound
}

func (f *fakePolicies) ListVersions(_ context.Context, _ string) ([]domain.BackupPolicyVersion, error) {
	return nil, nil
}

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

type fakeEnqueuer struct {
	jobs []*domain.BackupJob
	err  error
}

func (f *fakeEnqueuer) EnqueueJob(_ context.Context, req EnqueueRequest) (*domain.BackupJob, error) {
	if f.err != nil {
		return nil, f.err
	}
	j := &domain.BackupJob{
		ID: "job_" + req.AgentID, AgentID: req.AgentID,
		PolicyID: req.PolicyID, PolicyVersion: req.PolicyVersion,
		ScheduleID: req.ScheduleID, Trigger: domain.JobTriggerManual,
		State: domain.JobQueued, CreatedBy: req.CreatedBy,
	}
	f.jobs = append(f.jobs, j)
	return j, nil
}

type fakeRunner struct {
	fires []Fire
	err   error
}

func (f *fakeRunner) RunScheduled(_ context.Context, fire Fire) error {
	if f.err != nil {
		return f.err
	}
	f.fires = append(f.fires, fire)
	return nil
}

type fakeGate struct {
	counts Counts
	err    error
}

func (f *fakeGate) ActiveCounts(_ context.Context) (Counts, error) {
	return f.counts, f.err
}

// ---- Helpers ----

func intptr(v int) *int { return &v }

func weekdayptr(wd time.Weekday) *time.Weekday { return &wd }

func testEnv(now time.Time) (*Service, *fakeSchedules, *fakePolicies) {
	fs := newFakeSchedules()
	fp := &fakePolicies{items: map[string]*domain.BackupPolicy{
		"pol_1": {ID: "pol_1", Name: "daily", AgentIDs: []string{"ag_1", "ag_2"}, Enabled: true, CurrentVersion: 3},
	}}
	s := NewService(fs, fp, WithClock(&fixedClock{now: now}))
	return s, fs, fp
}

func dailyInput() CreateInput {
	return CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleDaily, TimeOfDay: "02:30", Timezone: "UTC"}
}

// ---- Tests ----

func TestCreateScheduleKinds(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s, _, _ := testEnv(now)
	ctx := context.Background()
	runAt := now.Add(2 * time.Hour)
	inputs := []CreateInput{
		{PolicyID: "pol_1", Kind: domain.ScheduleOneTime, RunAt: &runAt, Timezone: "UTC"},
		{PolicyID: "pol_1", Kind: domain.ScheduleHourly, IntervalHours: 6, Timezone: "Asia/Jakarta"},
		dailyInput(),
		{PolicyID: "pol_1", Kind: domain.ScheduleWeekly, TimeOfDay: "03:00", Weekday: weekdayptr(time.Monday), Timezone: "UTC"},
		{PolicyID: "pol_1", Kind: domain.ScheduleCron, CronExpr: "0 2 * * *", Timezone: "UTC"},
	}
	for i, in := range inputs {
		sched, err := s.Create(ctx, in)
		if err != nil {
			t.Fatalf("create[%d] (%s): %v", i, in.Kind, err)
		}
		if sched.NextRunAt == nil || !sched.NextRunAt.After(now) {
			t.Fatalf("create[%d]: NextRunAt must be future, got %v", i, sched.NextRunAt)
		}
		if sched.MaxConcurrency != DefaultMaxConcurrency {
			t.Fatalf("create[%d]: default cap must be %d, got %d", i, DefaultMaxConcurrency, sched.MaxConcurrency)
		}
		if sched.Timezone != in.Timezone {
			t.Fatalf("create[%d]: timezone not stored: %q", i, sched.Timezone)
		}
	}
	// Explicit 0 = unlimited is preserved.
	zero := dailyInput()
	zero.MaxConcurrency = intptr(0)
	sched, err := s.Create(ctx, zero)
	if err != nil || sched.MaxConcurrency != 0 {
		t.Fatalf("explicit 0 cap: want unlimited, got %+v err=%v", sched, err)
	}
}

func TestCreateScheduleInvalid(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s, _, _ := testEnv(now)
	ctx := context.Background()
	past := now.Add(-time.Hour)
	cases := []struct {
		name string
		in   CreateInput
	}{
		{"unknown policy", CreateInput{PolicyID: "pol_missing", Kind: domain.ScheduleDaily, TimeOfDay: "01:00", Timezone: "UTC"}},
		{"hourly no interval", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleHourly, Timezone: "UTC"}},
		{"daily bad tod", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleDaily, TimeOfDay: "2:30pm", Timezone: "UTC"}},
		{"weekly no weekday", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleWeekly, TimeOfDay: "03:00", Timezone: "UTC"}},
		{"bad timezone", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleDaily, TimeOfDay: "01:00", Timezone: "Mars"}},
		{"negative cap", func() CreateInput { in := dailyInput(); in.MaxConcurrency = intptr(-1); return in }()},
		{"past one-time", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleOneTime, RunAt: &past, Timezone: "UTC"}},
		// Passes the domain 5-field check but fails semantic cron parsing.
		{"bad cron minute", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleCron, CronExpr: "61 * * * *", Timezone: "UTC"}},
		{"bad cron step", CreateInput{PolicyID: "pol_1", Kind: domain.ScheduleCron, CronExpr: "*/0 * * * *", Timezone: "UTC"}},
	}
	for _, tc := range cases {
		if _, err := s.Create(ctx, tc.in); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		} else if tc.name == "unknown policy" {
			if !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%s: want ErrNotFound, got %v", tc.name, err)
			}
		} else if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", tc.name, err)
		}
	}
}

func TestUpdateRecomputesNextRun(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s, _, _ := testEnv(now)
	ctx := context.Background()
	sched, err := s.Create(ctx, dailyInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := *sched.NextRunAt
	upd, err := s.Update(ctx, sched.ID, UpdateInput{Kind: domain.ScheduleDaily, TimeOfDay: "23:00", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	want := time.Date(2026, 5, 1, 23, 0, 0, 0, time.UTC)
	if !upd.NextRunAt.Equal(want) {
		t.Fatalf("want recomputed %v, got %v (was %v)", want, upd.NextRunAt, before)
	}
	if _, err := s.Update(ctx, "sch_missing", UpdateInput{Kind: domain.ScheduleDaily, TimeOfDay: "01:00", Timezone: "UTC"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
}

func TestPauseResume(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	fs := newFakeSchedules()
	fp := &fakePolicies{items: map[string]*domain.BackupPolicy{"pol_1": {ID: "pol_1", AgentIDs: []string{"ag_1"}, Enabled: true, CurrentVersion: 1}}}
	s := NewService(fs, fp, WithClock(clock))
	ctx := context.Background()
	sched, err := s.Create(ctx, dailyInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	paused, err := s.Pause(ctx, sched.ID)
	if err != nil || !paused.Paused || paused.Active() {
		t.Fatalf("pause: %+v err=%v", paused, err)
	}
	// Time passes while paused; resume must recompute from now (no backlog).
	clock.now = now.Add(48 * time.Hour)
	resumed, err := s.Resume(ctx, sched.ID)
	if err != nil || resumed.Paused || !resumed.Active() {
		t.Fatalf("resume: %+v err=%v", resumed, err)
	}
	if resumed.NextRunAt == nil || !resumed.NextRunAt.After(clock.now) {
		t.Fatalf("resume must set a future NextRunAt, got %v", resumed.NextRunAt)
	}
	want := time.Date(2026, 5, 4, 2, 30, 0, 0, time.UTC)
	if !resumed.NextRunAt.Equal(want) {
		t.Fatalf("resume: want %v, got %v", want, resumed.NextRunAt)
	}
	if _, err := s.Pause(ctx, "sch_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pause missing: want ErrNotFound, got %v", err)
	}
}

func TestAdmitMatrix(t *testing.T) {
	base := domain.Schedule{PolicyID: "pol_1", Kind: domain.ScheduleDaily, MaxConcurrency: 1}
	admitted := Admit(AdmitInput{Schedule: base, PolicyID: "pol_1", AgentIDs: []string{"ag_1"}})
	if !admitted.Admit {
		t.Fatalf("idle system must admit: %s", admitted.Reason)
	}
	paused := base
	paused.Pause()
	if d := Admit(AdmitInput{Schedule: paused, PolicyID: "pol_1"}); d.Admit {
		t.Fatal("paused schedule must be denied")
	}
	// Overlap prevention: same protected resource already running.
	overlap := Admit(AdmitInput{Schedule: base, PolicyID: "pol_1",
		Live: Counts{PerPolicy: map[string]int{"pol_1": 1}}})
	if overlap.Admit {
		t.Fatal("active job on same resource must be denied (overlap prevention)")
	}
	// Higher per-resource cap admits until reached.
	base2 := base
	base2.MaxConcurrency = 2
	if d := Admit(AdmitInput{Schedule: base2, PolicyID: "pol_1",
		Live: Counts{PerPolicy: map[string]int{"pol_1": 1}}}); !d.Admit {
		t.Fatalf("under per-resource cap must admit: %s", d.Reason)
	}
	// Unlimited per-resource cap (0) ignores policy load.
	base0 := base
	base0.MaxConcurrency = 0
	if d := Admit(AdmitInput{Schedule: base0, PolicyID: "pol_1",
		Live: Counts{PerPolicy: map[string]int{"pol_1": 99}}}); !d.Admit {
		t.Fatalf("unlimited cap must admit: %s", d.Reason)
	}
	// Per-agent cap.
	if d := Admit(AdmitInput{Schedule: base0, PolicyID: "pol_1", AgentIDs: []string{"ag_1", "ag_2"},
		PerAgentLimit: 2, Live: Counts{PerAgent: map[string]int{"ag_2": 2}}}); d.Admit {
		t.Fatal("agent at cap must deny the whole firing")
	}
	if d := Admit(AdmitInput{Schedule: base0, PolicyID: "pol_1", AgentIDs: []string{"ag_1"},
		PerAgentLimit: 2, Live: Counts{PerAgent: map[string]int{"ag_1": 1}}}); !d.Admit {
		t.Fatalf("agent under cap must admit: %s", d.Reason)
	}
	// Global cap.
	if d := Admit(AdmitInput{Schedule: base0, PolicyID: "pol_1", GlobalLimit: 10,
		Live: Counts{Global: 10}}); d.Admit {
		t.Fatal("global at cap must deny")
	}
	if d := Admit(AdmitInput{Schedule: base0, PolicyID: "pol_1", GlobalLimit: 10,
		Live: Counts{Global: 9}}); !d.Admit {
		t.Fatalf("global under cap must admit: %s", d.Reason)
	}
}

func TestTriggerManual(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s, _, _ := testEnv(now)
	s = NewService(newFakeSchedules(), &fakePolicies{items: map[string]*domain.BackupPolicy{
		"pol_1": {ID: "pol_1", Name: "daily", AgentIDs: []string{"ag_1", "ag_2"}, Enabled: true, CurrentVersion: 3},
	}}, WithClock(&fixedClock{now: now}), WithLimits(5, 50))
	ctx := context.Background()
	sched, err := s.Create(ctx, dailyInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Fan out to all agents, pinning the current version.
	q := &fakeEnqueuer{}
	jobs, err := s.TriggerManual(ctx, ManualTrigger{ScheduleID: sched.ID, CreatedBy: "op", Enqueuer: q})
	if err != nil {
		t.Fatalf("manual trigger: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs (one per agent), got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.PolicyVersion != 3 || j.ScheduleID != sched.ID || j.Trigger != domain.JobTriggerManual {
			t.Fatalf("job pins wrong version/schedule/trigger: %+v", j)
		}
	}
	// Single-agent filter.
	q2 := &fakeEnqueuer{}
	jobs, err = s.TriggerManual(ctx, ManualTrigger{ScheduleID: sched.ID, AgentID: "ag_2", Enqueuer: q2})
	if err != nil || len(jobs) != 1 || jobs[0].AgentID != "ag_2" {
		t.Fatalf("single-agent trigger: %+v err=%v", jobs, err)
	}
	// Unknown agent.
	if _, err := s.TriggerManual(ctx, ManualTrigger{ScheduleID: sched.ID, AgentID: "ag_x", Enqueuer: q2}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown agent: want ErrValidation, got %v", err)
	}
	// Nil enqueuer.
	if _, err := s.TriggerManual(ctx, ManualTrigger{ScheduleID: sched.ID}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nil enqueuer: want ErrValidation, got %v", err)
	}
	// Overlap denied through the gate.
	gate := &fakeGate{counts: Counts{PerPolicy: map[string]int{"pol_1": 1}}}
	if _, err := s.TriggerManual(ctx, ManualTrigger{ScheduleID: sched.ID, Enqueuer: q2, Gate: gate}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("overlap: want ErrConflict, got %v", err)
	}
	if len(q2.jobs) != 1 {
		t.Fatal("denied trigger must not enqueue")
	}
	// Paused schedules still allow explicit manual runs.
	if _, err := s.Pause(ctx, sched.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	q3 := &fakeEnqueuer{}
	if _, err := s.TriggerManual(ctx, ManualTrigger{ScheduleID: sched.ID, Enqueuer: q3}); err != nil {
		t.Fatalf("manual on paused schedule must be allowed: %v", err)
	}
	// Disabled-kind schedules refuse manual runs.
	dis := dailyInput()
	dis.Kind = domain.ScheduleDisabled
	dsched, err := s.Create(ctx, dis)
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	if _, err := s.TriggerManual(ctx, ManualTrigger{ScheduleID: dsched.ID, Enqueuer: q3}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("manual on disabled schedule: want ErrConflict, got %v", err)
	}
}

func TestDispatchDue(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fs := newFakeSchedules()
	past := now.Add(-time.Minute)
	dueID := "sch_due"
	fs.items[dueID] = &domain.Schedule{ID: dueID, PolicyID: "pol_1", Kind: domain.ScheduleHourly,
		IntervalHours: 1, Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &past}
	oneID := "sch_one"
	fs.items[oneID] = &domain.Schedule{ID: oneID, PolicyID: "pol_1", Kind: domain.ScheduleOneTime,
		RunAt: &past, Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &past}
	fp := &fakePolicies{items: map[string]*domain.BackupPolicy{
		"pol_1": {ID: "pol_1", AgentIDs: []string{"ag_1"}, Enabled: true, CurrentVersion: 7},
	}}
	s := NewService(fs, fp, WithClock(&fixedClock{now: now}))
	ctx := context.Background()
	r := &fakeRunner{}
	rep, err := s.DispatchDue(ctx, now, r, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(rep.Fired) != 2 || len(rep.Failed) != 0 {
		t.Fatalf("want 2 fired: %+v", rep)
	}
	if len(r.fires) != 2 || r.fires[0].PolicyVersion != 7 {
		t.Fatalf("runner must see pinned version 7: %+v", r.fires)
	}
	// Recurring advanced; one-time retired to disabled.
	adv, _ := s.Get(ctx, dueID)
	if adv.NextRunAt == nil || !adv.NextRunAt.After(now) {
		t.Fatalf("recurring NextRunAt must advance, got %v", adv.NextRunAt)
	}
	one, _ := s.Get(ctx, oneID)
	if one.Kind != domain.ScheduleDisabled || one.NextRunAt != nil {
		t.Fatalf("one-time must retire to disabled: %+v", one)
	}
	// Second tick: nothing due.
	rep2, _ := s.DispatchDue(ctx, now, r, nil)
	if len(rep2.Fired) != 0 {
		t.Fatalf("second tick must fire nothing: %+v", rep2)
	}
}

func TestDispatchSkipsAndFailures(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fs := newFakeSchedules()
	past := now.Add(-time.Minute)
	mk := func(id, pol string, paused bool) {
		fs.items[id] = &domain.Schedule{ID: id, PolicyID: pol, Kind: domain.ScheduleHourly,
			IntervalHours: 1, Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &past, Paused: paused}
	}
	mk("sch_busy", "pol_1", false)       // denied by gate (overlap)
	mk("sch_paused", "pol_1", true)      // inactive: DueSchedules filters, assert defensively
	mk("sch_disabled", "pol_off", false) // policy disabled
	fp := &fakePolicies{items: map[string]*domain.BackupPolicy{
		"pol_1":   {ID: "pol_1", AgentIDs: []string{"ag_1"}, Enabled: true, CurrentVersion: 1},
		"pol_off": {ID: "pol_off", AgentIDs: []string{"ag_1"}, Enabled: false, CurrentVersion: 1},
	}}
	s := NewService(fs, fp, WithClock(&fixedClock{now: now}))
	ctx := context.Background()
	gate := &fakeGate{counts: Counts{PerPolicy: map[string]int{"pol_1": 1}}}
	r := &fakeRunner{}
	rep, err := s.DispatchDue(ctx, now, r, gate)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(rep.Fired) != 0 || len(r.fires) != 0 {
		t.Fatalf("nothing may fire: %+v", rep)
	}
	if len(rep.Skipped) != 2 { // busy + policy-disabled (paused never surfaces as due)
		t.Fatalf("want 2 skipped, got %+v", rep.Skipped)
	}
	// Runner failure: reported, NextRunAt NOT advanced.
	s2 := NewService(fs, fp, WithClock(&fixedClock{now: now}))
	rFail := &fakeRunner{err: errors.New("runner down")}
	rep3, err := s2.DispatchDue(ctx, now, rFail, nil)
	if err != nil {
		t.Fatalf("dispatch with failing runner: %v", err)
	}
	if len(rep3.Failed) == 0 {
		t.Fatal("runner failure must be reported per schedule")
	}
	still, _ := s2.Get(ctx, "sch_busy")
	if !still.NextRunAt.Equal(past) {
		t.Fatalf("failed firing must not advance NextRunAt: %v", still.NextRunAt)
	}
	// Nil runner is a validation error.
	if _, err := s.DispatchDue(ctx, now, nil, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nil runner: want ErrValidation, got %v", err)
	}
}

func TestDeleteAndList(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s, _, _ := testEnv(now)
	ctx := context.Background()
	sched, err := s.Create(ctx, dailyInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := s.List(ctx, "pol_1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	all, err := s.List(ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("list all: %+v err=%v", all, err)
	}
	if err := s.Delete(ctx, sched.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(ctx, sched.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
}
