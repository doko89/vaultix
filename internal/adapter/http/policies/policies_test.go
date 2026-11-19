package policies

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

// ---- Stubs ----

type stubPolicyService struct {
	t                *testing.T
	onCreate         func(policyuc.CreateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error)
	onGet            func(id string) (*domain.BackupPolicy, error)
	onList           func(domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error)
	onUpdate         func(id string, in policyuc.UpdateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error)
	onDelete         func(id string) error
	onSetEnabled     func(id string, enabled bool, actor string) (*domain.BackupPolicy, error)
	onPublish        func(policyID string, in policyuc.PublishInput) (*domain.BackupPolicyVersion, error)
	onGetVersion     func(policyID string, v int) (*domain.BackupPolicyVersion, error)
	onListVersions   func(policyID string) ([]domain.BackupPolicyVersion, error)
	onCurrentVersion func(policyID string) (*domain.BackupPolicyVersion, error)
	onAddTarget      func(policyID string, in policyuc.TargetInput) (*domain.BackupPolicyVersion, error)
	onUpdateTarget   func(policyID, tid string, in policyuc.TargetInput) (*domain.BackupPolicyVersion, error)
	onRemoveTarget   func(policyID, tid string) (*domain.BackupPolicyVersion, error)
}

func (s *stubPolicyService) fail(op string) {
	s.t.Fatalf("unexpected Policies.%s call", op)
}

func (s *stubPolicyService) Create(_ context.Context, in policyuc.CreateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
	if s.onCreate == nil {
		s.fail("Create")
	}
	return s.onCreate(in)
}

func (s *stubPolicyService) Get(_ context.Context, id string) (*domain.BackupPolicy, error) {
	if s.onGet == nil {
		s.fail("Get")
	}
	return s.onGet(id)
}

func (s *stubPolicyService) List(_ context.Context, opts domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	if s.onList == nil {
		s.fail("List")
	}
	return s.onList(opts)
}

func (s *stubPolicyService) Update(_ context.Context, id string, in policyuc.UpdateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
	if s.onUpdate == nil {
		s.fail("Update")
	}
	return s.onUpdate(id, in)
}

func (s *stubPolicyService) Delete(_ context.Context, id string) error {
	if s.onDelete == nil {
		s.fail("Delete")
	}
	return s.onDelete(id)
}

func (s *stubPolicyService) SetEnabled(_ context.Context, id string, enabled bool, actor string) (*domain.BackupPolicy, error) {
	if s.onSetEnabled == nil {
		s.fail("SetEnabled")
	}
	return s.onSetEnabled(id, enabled, actor)
}

func (s *stubPolicyService) PublishVersion(_ context.Context, policyID string, in policyuc.PublishInput) (*domain.BackupPolicyVersion, error) {
	if s.onPublish == nil {
		s.fail("PublishVersion")
	}
	return s.onPublish(policyID, in)
}

func (s *stubPolicyService) GetVersion(_ context.Context, policyID string, v int) (*domain.BackupPolicyVersion, error) {
	if s.onGetVersion == nil {
		s.fail("GetVersion")
	}
	return s.onGetVersion(policyID, v)
}

func (s *stubPolicyService) ListVersions(_ context.Context, policyID string) ([]domain.BackupPolicyVersion, error) {
	if s.onListVersions == nil {
		s.fail("ListVersions")
	}
	return s.onListVersions(policyID)
}

func (s *stubPolicyService) CurrentVersion(_ context.Context, policyID string) (*domain.BackupPolicyVersion, error) {
	if s.onCurrentVersion == nil {
		s.fail("CurrentVersion")
	}
	return s.onCurrentVersion(policyID)
}

func (s *stubPolicyService) AddTarget(_ context.Context, policyID string, in policyuc.TargetInput, _ policyuc.ChangeMeta) (*domain.BackupPolicyVersion, error) {
	if s.onAddTarget == nil {
		s.fail("AddTarget")
	}
	return s.onAddTarget(policyID, in)
}

func (s *stubPolicyService) UpdateTarget(_ context.Context, policyID, tid string, in policyuc.TargetInput, _ policyuc.ChangeMeta) (*domain.BackupPolicyVersion, error) {
	if s.onUpdateTarget == nil {
		s.fail("UpdateTarget")
	}
	return s.onUpdateTarget(policyID, tid, in)
}

func (s *stubPolicyService) RemoveTarget(_ context.Context, policyID, tid string, _ policyuc.ChangeMeta) (*domain.BackupPolicyVersion, error) {
	if s.onRemoveTarget == nil {
		s.fail("RemoveTarget")
	}
	return s.onRemoveTarget(policyID, tid)
}

type stubScheduleService struct {
	t           *testing.T
	onCreate    func(scheduleuc.CreateInput) (*domain.Schedule, error)
	onGet       func(id string) (*domain.Schedule, error)
	onList      func(policyID string) ([]domain.Schedule, error)
	onUpdate    func(id string, in scheduleuc.UpdateInput) (*domain.Schedule, error)
	onDelete    func(id string) error
	onPause     func(id string) (*domain.Schedule, error)
	onResume    func(id string) (*domain.Schedule, error)
	onTrigger   func(scheduleuc.ManualTrigger) ([]*domain.BackupJob, error)
}

func (s *stubScheduleService) fail(op string) {
	s.t.Fatalf("unexpected Schedules.%s call", op)
}

func (s *stubScheduleService) Create(_ context.Context, in scheduleuc.CreateInput) (*domain.Schedule, error) {
	if s.onCreate == nil {
		s.fail("Create")
	}
	return s.onCreate(in)
}

func (s *stubScheduleService) Get(_ context.Context, id string) (*domain.Schedule, error) {
	if s.onGet == nil {
		s.fail("Get")
	}
	return s.onGet(id)
}

func (s *stubScheduleService) List(_ context.Context, policyID string) ([]domain.Schedule, error) {
	if s.onList == nil {
		s.fail("List")
	}
	return s.onList(policyID)
}

func (s *stubScheduleService) Update(_ context.Context, id string, in scheduleuc.UpdateInput) (*domain.Schedule, error) {
	if s.onUpdate == nil {
		s.fail("Update")
	}
	return s.onUpdate(id, in)
}

func (s *stubScheduleService) Delete(_ context.Context, id string) error {
	if s.onDelete == nil {
		s.fail("Delete")
	}
	return s.onDelete(id)
}

func (s *stubScheduleService) Pause(_ context.Context, id string) (*domain.Schedule, error) {
	if s.onPause == nil {
		s.fail("Pause")
	}
	return s.onPause(id)
}

func (s *stubScheduleService) Resume(_ context.Context, id string) (*domain.Schedule, error) {
	if s.onResume == nil {
		s.fail("Resume")
	}
	return s.onResume(id)
}

func (s *stubScheduleService) TriggerManual(_ context.Context, in scheduleuc.ManualTrigger) ([]*domain.BackupJob, error) {
	if s.onTrigger == nil {
		s.fail("TriggerManual")
	}
	return s.onTrigger(in)
}

type stubEnqueuer struct {
	err error
}

func (s *stubEnqueuer) EnqueueJob(_ context.Context, _ scheduleuc.EnqueueRequest) (*domain.BackupJob, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.BackupJob{}, nil
}

// ---- Test helpers ----

func testMux(t *testing.T, deps Deps) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, deps)
	return mux
}

func doRequest(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = bytes.NewBufferString(b)
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			rdr = bytes.NewReader(raw)
		}
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode response (status %d, body %q): %v", rec.Code, rec.Body.String(), err)
	}
}

func samplePolicy() *domain.BackupPolicy {
	return &domain.BackupPolicy{
		ID: "pol_1", Name: "daily-home", AgentIDs: []string{"ag_1"},
		Enabled: true, CurrentVersion: 1,
		CreatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
}

func sampleVersion(v int) *domain.BackupPolicyVersion {
	return &domain.BackupPolicyVersion{
		Version: v, PolicyID: "pol_1", CreatedBy: "admin",
		CreatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		Targets: []domain.BackupPolicyTarget{
			{ID: "tgt_1", PolicyID: "pol_1", Type: domain.TargetHostFilesystem, Method: domain.BackupMethodFull, Selector: "/data"},
		},
		Retention: domain.RetentionRule{KeepLastN: 7, KeepMinimum: 1},
		Note:      "v",
	}
}

func validPolicyBody() map[string]any {
	return map[string]any{
		"name":      "daily-home",
		"agent_ids": []string{"ag_1"},
		"retention": map[string]any{"keep_last_n": 7, "keep_minimum": 1},
		"targets": []any{
			map[string]any{"type": "host_filesystem", "selector": "/data"},
		},
	}
}

// ---- Policy endpoint tests ----

func TestCreatePolicyRoundTrip(t *testing.T) {
	stub := &stubPolicyService{t: t}
	var got policyuc.CreateInput
	stub.onCreate = func(in policyuc.CreateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
		got = in
		return samplePolicy(), sampleVersion(1), nil
	}
	mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})
	rec := doRequest(t, mux, "POST", "/api/policies", validPolicyBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp policyVersionResponse
	decodeBody(t, rec, &resp)
	if resp.Policy.ID != "pol_1" || resp.Version.Version != 1 {
		t.Fatalf("bad response: %+v", resp)
	}
	if got.Name != "daily-home" || len(got.AgentIDs) != 1 || len(got.Targets) != 1 {
		t.Fatalf("input not translated: %+v", got)
	}
	if got.Targets[0].Type != domain.TargetHostFilesystem || got.Retention.KeepLastN != 7 {
		t.Fatalf("target/retention not translated: %+v", got)
	}
}

func TestPolicyValidation400s(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing name", func(b map[string]any) { delete(b, "name") }},
		{"missing agents", func(b map[string]any) { delete(b, "agent_ids") }},
		{"blank agent", func(b map[string]any) { b["agent_ids"] = []string{" "} }},
		{"missing retention", func(b map[string]any) { delete(b, "retention") }},
		{"retention no bound", func(b map[string]any) { b["retention"] = map[string]any{} }},
		{"missing targets", func(b map[string]any) { delete(b, "targets") }},
		{"bad target type", func(b map[string]any) {
			b["targets"] = []any{map[string]any{"type": "tape", "selector": "/x"}}
		}},
		{"target no selector", func(b map[string]any) {
			b["targets"] = []any{map[string]any{"type": "postgres"}}
		}},
		{"encryption w/o key", func(b map[string]any) { b["encryption"] = map[string]any{"enabled": true} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubPolicyService{t: t} // any service call fails the test
			mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})
			body := validPolicyBody()
			tc.mutate(body)
			rec := doRequest(t, mux, "POST", "/api/policies", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
			}
		})
	}
	// Unknown JSON fields are rejected (strict DTOs).
	stub := &stubPolicyService{t: t}
	mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})
	rec := doRequest(t, mux, "POST", "/api/policies", `{"name":"x","bogus_field":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: want 400, got %d", rec.Code)
	}
	// Malformed JSON.
	rec = doRequest(t, mux, "POST", "/api/policies", `{"name":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: want 400, got %d", rec.Code)
	}
}

func TestPolicyErrorMapping(t *testing.T) {
	stub := &stubPolicyService{t: t}
	stub.onGet = func(id string) (*domain.BackupPolicy, error) { return nil, domain.ErrNotFound }
	stub.onDelete = func(id string) error { return domain.ErrNotFound }
	mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})
	if rec := doRequest(t, mux, "GET", "/api/policies/pol_x", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get missing: want 404, got %d", rec.Code)
	}
	if rec := doRequest(t, mux, "DELETE", "/api/policies/pol_x", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: want 404, got %d", rec.Code)
	}
	// Service validation errors surface as 400 with detail.
	stub2 := &stubPolicyService{t: t}
	stub2.onCreate = func(policyuc.CreateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
		return nil, nil, &validationWrap{msg: "domain: validation failed: relative path"}
	}
	mux2 := testMux(t, Deps{Policies: stub2, Schedules: &stubScheduleService{t: t}})
	rec := doRequest(t, mux2, "POST", "/api/policies", validPolicyBody())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("service validation: want 400, got %d", rec.Code)
	}
	// Unexpected errors become generic 500s (no leak).
	stub3 := &stubPolicyService{t: t}
	stub3.onGet = func(id string) (*domain.BackupPolicy, error) { return nil, errBoom }
	mux3 := testMux(t, Deps{Policies: stub3, Schedules: &stubScheduleService{t: t}})
	rec = doRequest(t, mux3, "GET", "/api/policies/pol_1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("boom: want 500, got %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("boom")) {
		t.Fatalf("500 must not leak internals: %s", rec.Body.String())
	}
}

// validationWrap simulates a wrapped domain.ErrValidation from the service.
type validationWrap struct{ msg string }

func (e *validationWrap) Error() string { return e.msg }
func (e *validationWrap) Unwrap() error { return domain.ErrValidation }

type boomError struct{}

func (boomError) Error() string { return "boom: connection reset" }

var errBoom error = boomError{}

func TestPolicyCRUDAndToggles(t *testing.T) {
	stub := &stubPolicyService{t: t}
	stub.onGet = func(id string) (*domain.BackupPolicy, error) { return samplePolicy(), nil }
	stub.onUpdate = func(id string, in policyuc.UpdateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error) {
		if id != "pol_1" || in.Name != "renamed" {
			t.Fatalf("update input: %s %+v", id, in)
		}
		p := samplePolicy()
		p.CurrentVersion = 2
		return p, sampleVersion(2), nil
	}
	stub.onDelete = func(id string) error { return nil }
	stub.onSetEnabled = func(id string, enabled bool, actor string) (*domain.BackupPolicy, error) {
		p := samplePolicy()
		p.Enabled = enabled
		return p, nil
	}
	mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})

	if rec := doRequest(t, mux, "GET", "/api/policies/pol_1", nil); rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
	body := validPolicyBody()
	body["name"] = "renamed"
	rec := doRequest(t, mux, "PUT", "/api/policies/pol_1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var uresp policyVersionResponse
	decodeBody(t, rec, &uresp)
	if uresp.Version.Version != 2 {
		t.Fatalf("update must return the new version: %+v", uresp)
	}
	if rec := doRequest(t, mux, "DELETE", "/api/policies/pol_1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", rec.Code)
	}
	rec = doRequest(t, mux, "POST", "/api/policies/pol_1/disable", nil)
	var dis policyDTO
	decodeBody(t, rec, &dis)
	if dis.Enabled {
		t.Fatal("disable must return disabled policy")
	}
	rec = doRequest(t, mux, "POST", "/api/policies/pol_1/enable", nil)
	var en policyDTO
	decodeBody(t, rec, &en)
	if !en.Enabled {
		t.Fatal("enable must return enabled policy")
	}
}

func TestPolicyListPagination(t *testing.T) {
	stub := &stubPolicyService{t: t}
	stub.onList = func(opts domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
		if opts.Limit != 10 || opts.Offset != 5 || opts.Query != "home" {
			t.Fatalf("pagination not passed: %+v", opts)
		}
		return domain.ListResult[domain.BackupPolicy]{Items: []domain.BackupPolicy{*samplePolicy()}, Total: 1}, nil
	}
	mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})
	rec := doRequest(t, mux, "GET", "/api/policies?limit=10&offset=5&q=home", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	var resp policyListResponse
	decodeBody(t, rec, &resp)
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("bad list response: %+v", resp)
	}
	for _, bad := range []string{"/api/policies?limit=0", "/api/policies?limit=999", "/api/policies?offset=-1", "/api/policies?limit=abc"} {
		if rec := doRequest(t, mux, "GET", bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", bad, rec.Code)
		}
	}
}

func TestPolicyVersionsAndTargets(t *testing.T) {
	stub := &stubPolicyService{t: t}
	stub.onListVersions = func(policyID string) ([]domain.BackupPolicyVersion, error) {
		return []domain.BackupPolicyVersion{*sampleVersion(1), *sampleVersion(2)}, nil
	}
	stub.onGetVersion = func(policyID string, v int) (*domain.BackupPolicyVersion, error) {
		if v != 2 {
			return nil, domain.ErrNotFound
		}
		return sampleVersion(2), nil
	}
	stub.onPublish = func(policyID string, in policyuc.PublishInput) (*domain.BackupPolicyVersion, error) {
		return sampleVersion(3), nil
	}
	stub.onCurrentVersion = func(policyID string) (*domain.BackupPolicyVersion, error) { return sampleVersion(2), nil }
	stub.onAddTarget = func(policyID string, in policyuc.TargetInput) (*domain.BackupPolicyVersion, error) {
		if in.Type != domain.TargetPostgres {
			t.Fatalf("add target type: %+v", in)
		}
		return sampleVersion(3), nil
	}
	stub.onUpdateTarget = func(policyID, tid string, in policyuc.TargetInput) (*domain.BackupPolicyVersion, error) {
		if tid != "tgt_1" {
			t.Fatalf("update target id: %s", tid)
		}
		return sampleVersion(4), nil
	}
	stub.onRemoveTarget = func(policyID, tid string) (*domain.BackupPolicyVersion, error) {
		if tid != "tgt_1" {
			return nil, domain.ErrNotFound
		}
		return sampleVersion(5), nil
	}
	mux := testMux(t, Deps{Policies: stub, Schedules: &stubScheduleService{t: t}})

	rec := doRequest(t, mux, "GET", "/api/policies/pol_1/versions", nil)
	var vl versionListResponse
	decodeBody(t, rec, &vl)
	if vl.Total != 2 {
		t.Fatalf("versions list: %+v", vl)
	}
	rec = doRequest(t, mux, "GET", "/api/policies/pol_1/versions/2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get version: want 200, got %d", rec.Code)
	}
	for _, bad := range []string{"/api/policies/pol_1/versions/0", "/api/policies/pol_1/versions/-3", "/api/policies/pol_1/versions/abc"} {
		if rec := doRequest(t, mux, "GET", bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", bad, rec.Code)
		}
	}
	pub := map[string]any{
		"targets":   []any{map[string]any{"type": "host_filesystem", "selector": "/v3"}},
		"retention": map[string]any{"keep_last_n": 7},
	}
	if rec := doRequest(t, mux, "POST", "/api/policies/pol_1/versions", pub); rec.Code != http.StatusCreated {
		t.Fatalf("publish: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, mux, "GET", "/api/policies/pol_1/targets", nil); rec.Code != http.StatusOK {
		t.Fatalf("list targets: want 200, got %d", rec.Code)
	}
	add := map[string]any{"type": "postgres", "selector": "appdb", "credential_ref": "sek_db"}
	if rec := doRequest(t, mux, "POST", "/api/policies/pol_1/targets", add); rec.Code != http.StatusCreated {
		t.Fatalf("add target: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	upd := map[string]any{"type": "host_filesystem", "selector": "/moved"}
	if rec := doRequest(t, mux, "PUT", "/api/policies/pol_1/targets/tgt_1", upd); rec.Code != http.StatusOK {
		t.Fatalf("update target: want 200, got %d", rec.Code)
	}
	if rec := doRequest(t, mux, "DELETE", "/api/policies/pol_1/targets/tgt_1", nil); rec.Code != http.StatusOK {
		t.Fatalf("remove target: want 200, got %d", rec.Code)
	}
	if rec := doRequest(t, mux, "DELETE", "/api/policies/pol_1/targets/tgt_missing", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("remove missing target: want 404, got %d", rec.Code)
	}
}

func TestMountPanicsOnNilDeps(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Mount with nil deps must panic")
		}
	}()
	Mount(http.NewServeMux(), Deps{})
}
