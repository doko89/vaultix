package policies

import (
	"net/http"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

func sampleSchedule() *domain.Schedule {
	next := time.Date(2026, 5, 2, 2, 30, 0, 0, time.UTC)
	return &domain.Schedule{
		ID: "sch_1", PolicyID: "pol_1", Kind: domain.ScheduleDaily,
		TimeOfDay: "02:30", Timezone: "UTC", MaxConcurrency: 1,
		NextRunAt: &next,
		CreatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
}

func validScheduleBody() map[string]any {
	return map[string]any{
		"policy_id": "pol_1", "kind": "daily",
		"time_of_day": "02:30", "timezone": "UTC",
	}
}

func TestCreateScheduleKinds(t *testing.T) {
	bodies := []map[string]any{
		{"policy_id": "pol_1", "kind": "one_time", "run_at": "2026-06-01T02:00:00Z", "timezone": "UTC"},
		{"policy_id": "pol_1", "kind": "hourly", "interval_hours": 6, "timezone": "Asia/Jakarta"},
		validScheduleBody(),
		{"policy_id": "pol_1", "kind": "weekly", "time_of_day": "03:00", "weekday": 1, "timezone": "UTC"},
		{"policy_id": "pol_1", "kind": "cron", "cron_expr": "0 2 * * *", "timezone": "UTC"},
		{"policy_id": "pol_1", "kind": "cron", "cron_expr": "*/15 9-17 * * mon-fri", "timezone": "UTC"},
	}
	for i, body := range bodies {
		stub := &stubScheduleService{t: t}
		stub.onCreate = func(in scheduleuc.CreateInput) (*domain.Schedule, error) {
			s := sampleSchedule()
			s.Kind = in.Kind
			return s, nil
		}
		mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub})
		rec := doRequest(t, mux, "POST", "/api/schedules", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("body[%d] (%v): want 201, got %d (%s)", i, body["kind"], rec.Code, rec.Body.String())
		}
	}
}

func TestScheduleValidation400s(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing policy", func(b map[string]any) { delete(b, "policy_id") }},
		{"bad kind", func(b map[string]any) { b["kind"] = "minutely" }},
		{"disabled kind", func(b map[string]any) { b["kind"] = "disabled" }},
		{"missing timezone", func(b map[string]any) { delete(b, "timezone") }},
		{"bad timezone", func(b map[string]any) { b["timezone"] = "Mars/Olympus" }},
		{"daily bad tod", func(b map[string]any) { b["time_of_day"] = "25:99" }},
		{"daily missing tod", func(b map[string]any) { delete(b, "time_of_day") }},
		{"weekly no weekday", func(b map[string]any) { b["kind"] = "weekly"; b["time_of_day"] = "03:00" }},
		{"weekly bad weekday", func(b map[string]any) { b["kind"] = "weekly"; b["weekday"] = 7 }},
		{"hourly no interval", func(b map[string]any) { b["kind"] = "hourly" }},
		{"hourly zero interval", func(b map[string]any) { b["kind"] = "hourly"; b["interval_hours"] = 0 }},
		{"one-time no runat", func(b map[string]any) { b["kind"] = "one_time" }},
		{"cron no expr", func(b map[string]any) { b["kind"] = "cron" }},
		{"cron 4 fields", func(b map[string]any) { b["kind"] = "cron"; b["cron_expr"] = "0 2 *" }},
		{"cron bad minute", func(b map[string]any) { b["kind"] = "cron"; b["cron_expr"] = "61 * * * *" }},
		{"cron bad step", func(b map[string]any) { b["kind"] = "cron"; b["cron_expr"] = "*/0 * * * *" }},
		{"negative cap", func(b map[string]any) { b["max_concurrency"] = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubScheduleService{t: t} // any service call fails
			mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub})
			body := validScheduleBody()
			tc.mutate(body)
			rec := doRequest(t, mux, "POST", "/api/schedules", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestScheduleCRUDPauseResume(t *testing.T) {
	stub := &stubScheduleService{t: t}
	stub.onGet = func(id string) (*domain.Schedule, error) { return sampleSchedule(), nil }
	stub.onList = func(policyID string) ([]domain.Schedule, error) {
		if policyID != "pol_1" {
			t.Fatalf("list policy: %q", policyID)
		}
		return []domain.Schedule{*sampleSchedule()}, nil
	}
	stub.onUpdate = func(id string, in scheduleuc.UpdateInput) (*domain.Schedule, error) {
		s := sampleSchedule()
		s.TimeOfDay = in.TimeOfDay
		return s, nil
	}
	stub.onDelete = func(id string) error { return nil }
	stub.onPause = func(id string) (*domain.Schedule, error) {
		s := sampleSchedule()
		s.Paused = true
		return s, nil
	}
	stub.onResume = func(id string) (*domain.Schedule, error) { return sampleSchedule(), nil }
	mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub})

	rec := doRequest(t, mux, "GET", "/api/schedules?policy_id=pol_1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	var list scheduleListResponse
	decodeBody(t, rec, &list)
	if list.Total != 1 {
		t.Fatalf("list total: %+v", list)
	}
	if rec := doRequest(t, mux, "GET", "/api/schedules", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("list w/o policy_id: want 400, got %d", rec.Code)
	}
	if rec := doRequest(t, mux, "GET", "/api/schedules/sch_1", nil); rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
	upd := map[string]any{"kind": "daily", "time_of_day": "23:00", "timezone": "UTC"}
	rec = doRequest(t, mux, "PUT", "/api/schedules/sch_1", upd)
	var got scheduleDTO
	decodeBody(t, rec, &got)
	if got.TimeOfDay != "23:00" {
		t.Fatalf("update not applied: %+v", got)
	}
	if rec := doRequest(t, mux, "DELETE", "/api/schedules/sch_1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", rec.Code)
	}
	rec = doRequest(t, mux, "POST", "/api/schedules/sch_1/pause", nil)
	var paused scheduleDTO
	decodeBody(t, rec, &paused)
	if !paused.Paused {
		t.Fatal("pause must return paused schedule")
	}
	rec = doRequest(t, mux, "POST", "/api/schedules/sch_1/resume", nil)
	var resumed scheduleDTO
	decodeBody(t, rec, &resumed)
	if resumed.Paused {
		t.Fatal("resume must return unpaused schedule")
	}
}

func TestManualRun(t *testing.T) {
	newJobs := func() []*domain.BackupJob {
		return []*domain.BackupJob{
			{ID: "job_1", AgentID: "ag_1", PolicyID: "pol_1", PolicyVersion: 3, ScheduleID: "sch_1", Trigger: domain.JobTriggerManual, State: domain.JobQueued},
			{ID: "job_2", AgentID: "ag_2", PolicyID: "pol_1", PolicyVersion: 3, ScheduleID: "sch_1", Trigger: domain.JobTriggerManual, State: domain.JobQueued},
		}
	}
	stub := &stubScheduleService{t: t}
	stub.onTrigger = func(in scheduleuc.ManualTrigger) ([]*domain.BackupJob, error) {
		if in.ScheduleID != "sch_1" || in.Enqueuer == nil {
			t.Fatalf("trigger input: %+v", in)
		}
		jobs := newJobs()
		if in.AgentID != "" {
			jobs = jobs[:1]
			jobs[0].AgentID = in.AgentID
		}
		return jobs, nil
	}
	mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub, Enqueuer: &stubEnqueuer{}})

	rec := doRequest(t, mux, "POST", "/api/schedules/sch_1/run", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run: want 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp runResponse
	decodeBody(t, rec, &resp)
	if len(resp.Jobs) != 2 || resp.Jobs[0].PolicyVersion != 3 {
		t.Fatalf("run response: %+v", resp)
	}
	rec = doRequest(t, mux, "POST", "/api/schedules/sch_1/run", map[string]any{"agent_id": "ag_9"})
	decodeBody(t, rec, &resp)
	if len(resp.Jobs) != 1 || resp.Jobs[0].AgentID != "ag_9" {
		t.Fatalf("single-agent run: %+v", resp)
	}
}

func TestManualRunNotWired(t *testing.T) {
	stub := &stubScheduleService{t: t} // must not be called
	mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub, Enqueuer: nil})
	rec := doRequest(t, mux, "POST", "/api/schedules/sch_1/run", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired enqueuer: want 501, got %d", rec.Code)
	}
}

func TestManualRunConflict(t *testing.T) {
	stub := &stubScheduleService{t: t}
	stub.onTrigger = func(scheduleuc.ManualTrigger) ([]*domain.BackupJob, error) {
		return nil, &conflictWrap{msg: "domain: conflict: protected resource at capacity"}
	}
	mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub, Enqueuer: &stubEnqueuer{}})
	rec := doRequest(t, mux, "POST", "/api/schedules/sch_1/run", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("overlap denial: want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
}

type conflictWrap struct{ msg string }

func (e *conflictWrap) Error() string { return e.msg }
func (e *conflictWrap) Unwrap() error { return domain.ErrConflict }

func TestScheduleNotFound(t *testing.T) {
	stub := &stubScheduleService{t: t}
	stub.onGet = func(id string) (*domain.Schedule, error) { return nil, domain.ErrNotFound }
	mux := testMux(t, Deps{Policies: &stubPolicyService{t: t}, Schedules: stub})
	if rec := doRequest(t, mux, "GET", "/api/schedules/sch_x", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}
