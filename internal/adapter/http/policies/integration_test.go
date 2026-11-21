package policies

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

// ---- Minimal in-memory repos backing the REAL use-case services ----

type memPolicyRepo struct {
	policies map[string]*domain.BackupPolicy
	versions map[string][]domain.BackupPolicyVersion
}

func newMemPolicyRepo() *memPolicyRepo {
	return &memPolicyRepo{policies: map[string]*domain.BackupPolicy{}, versions: map[string][]domain.BackupPolicyVersion{}}
}

func (m *memPolicyRepo) CreatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	cp := *p
	m.policies[p.ID] = &cp
	return nil
}

func (m *memPolicyRepo) GetPolicy(_ context.Context, id string) (*domain.BackupPolicy, error) {
	p, ok := m.policies[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memPolicyRepo) UpdatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	if _, ok := m.policies[p.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *p
	m.policies[p.ID] = &cp
	return nil
}

func (m *memPolicyRepo) DeletePolicy(_ context.Context, id string) error {
	if _, ok := m.policies[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.policies, id)
	return nil
}

func (m *memPolicyRepo) ListPolicies(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	var out domain.ListResult[domain.BackupPolicy]
	for _, p := range m.policies {
		out.Items = append(out.Items, *p)
	}
	out.Total = len(out.Items)
	return out, nil
}

func (m *memPolicyRepo) PublishVersion(_ context.Context, v *domain.BackupPolicyVersion) error {
	m.versions[v.PolicyID] = append(m.versions[v.PolicyID], *v)
	return nil
}

func (m *memPolicyRepo) GetVersion(_ context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	for _, v := range m.versions[policyID] {
		if v.Version == version {
			cp := v
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *memPolicyRepo) ListVersions(_ context.Context, policyID string) ([]domain.BackupPolicyVersion, error) {
	return append([]domain.BackupPolicyVersion{}, m.versions[policyID]...), nil
}

type memScheduleRepo struct {
	items map[string]*domain.Schedule
}

func (m *memScheduleRepo) CreateSchedule(_ context.Context, s *domain.Schedule) error {
	cp := *s
	m.items[s.ID] = &cp
	return nil
}

func (m *memScheduleRepo) GetSchedule(_ context.Context, id string) (*domain.Schedule, error) {
	s, ok := m.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *memScheduleRepo) UpdateSchedule(_ context.Context, s *domain.Schedule) error {
	if _, ok := m.items[s.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *s
	m.items[s.ID] = &cp
	return nil
}

func (m *memScheduleRepo) DeleteSchedule(_ context.Context, id string) error {
	if _, ok := m.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

func (m *memScheduleRepo) ListSchedules(_ context.Context, policyID string) ([]domain.Schedule, error) {
	var out []domain.Schedule
	for _, s := range m.items {
		if s.PolicyID == policyID {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (m *memScheduleRepo) ListAllSchedules(_ context.Context) ([]domain.Schedule, error) {
	var out []domain.Schedule
	for _, s := range m.items {
		out = append(out, *s)
	}
	return out, nil
}

func (m *memScheduleRepo) DueSchedules(_ context.Context, _ time.Time, _ int) ([]domain.Schedule, error) {
	return nil, nil
}

type recordingEnqueuer struct {
	reqs []scheduleuc.EnqueueRequest
}

func (e *recordingEnqueuer) EnqueueJob(_ context.Context, req scheduleuc.EnqueueRequest) (*domain.BackupJob, error) {
	e.reqs = append(e.reqs, req)
	return &domain.BackupJob{ID: "job_" + req.AgentID, AgentID: req.AgentID, PolicyID: req.PolicyID,
		PolicyVersion: req.PolicyVersion, ScheduleID: req.ScheduleID, Trigger: domain.JobTriggerManual,
		State: domain.JobQueued, CreatedBy: req.CreatedBy}, nil
}

func wiredMux() (*http.ServeMux, *recordingEnqueuer) {
	// Both services share one policy repo, like production wiring.
	repo := newMemPolicyRepo()
	policies := policyuc.NewService(repo)
	schedules := scheduleuc.NewService(&memScheduleRepo{items: map[string]*domain.Schedule{}}, repo)
	q := &recordingEnqueuer{}
	mux := http.NewServeMux()
	Mount(mux, Deps{Policies: policies, Schedules: schedules, Enqueuer: q})
	return mux, q
}

// TestEndToEndPolicyScheduleRun exercises the real services through HTTP:
// create → version immutability across an edit → schedule → manual run.
func TestEndToEndPolicyScheduleRun(t *testing.T) {
	mux, q := wiredMux()

	// Service-level per-type validation surfaces through HTTP (db w/o creds).
	bad := validPolicyBody()
	bad["targets"] = []any{map[string]any{"type": "postgres", "selector": "appdb"}}
	if rec := doRequest(t, mux, "POST", "/api/policies", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("db w/o credential: want 400, got %d", rec.Code)
	}

	rec := doRequest(t, mux, "POST", "/api/policies", validPolicyBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var created policyVersionResponse
	decodeBody(t, rec, &created)

	// Edit retargets; v1 must be byte-identical afterwards.
	body := validPolicyBody()
	body["name"] = "daily-home-v2"
	body["targets"] = []any{map[string]any{"type": "host_filesystem", "selector": "/v2"}}
	rec = doRequest(t, mux, "PUT", "/api/policies/"+created.Policy.ID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, mux, "GET", "/api/policies/"+created.Policy.ID+"/versions/1", nil)
	var v1 versionDTO
	decodeBody(t, rec, &v1)
	if v1.Targets[0].Selector != "/data" {
		t.Fatalf("v1 mutated through HTTP edit: %+v", v1.Targets[0])
	}

	// Schedule + manual run pin the current version.
	rec = doRequest(t, mux, "POST", "/api/schedules", map[string]any{
		"policy_id": created.Policy.ID, "kind": "daily",
		"time_of_day": "02:30", "timezone": "UTC",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("schedule create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var sched scheduleDTO
	decodeBody(t, rec, &sched)
	if sched.NextRunAt == nil || sched.NextRunAt.Before(time.Now()) {
		t.Fatalf("next_run_at must be future: %+v", sched)
	}
	rec = doRequest(t, mux, "POST", "/api/schedules/"+sched.ID+"/run", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run: want 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(q.reqs) != 1 || q.reqs[0].PolicyVersion != 2 {
		t.Fatalf("manual run must pin v2: %+v", q.reqs)
	}

	// Pause/resume round trip.
	if rec := doRequest(t, mux, "POST", "/api/schedules/"+sched.ID+"/pause", nil); rec.Code != http.StatusOK {
		t.Fatalf("pause: want 200, got %d", rec.Code)
	}
	rec = doRequest(t, mux, "POST", "/api/schedules/"+sched.ID+"/resume", nil)
	var resumed scheduleDTO
	decodeBody(t, rec, &resumed)
	if resumed.Paused || resumed.NextRunAt == nil || resumed.NextRunAt.Before(time.Now()) {
		t.Fatalf("bad resume: %+v", resumed)
	}
}
