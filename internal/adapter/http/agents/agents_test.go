package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	discoveryuc "github.com/vaultix/vaultix/internal/usecase/discovery"
)

type route struct {
	method  string
	pattern string
	h       http.HandlerFunc
}

type stubRouter struct {
	routes []route
}

func (s *stubRouter) Get(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, route{"GET", pattern, h})
}

func (s *stubRouter) Post(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, route{"POST", pattern, h})
}

func (s *stubRouter) Put(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, route{"PUT", pattern, h})
}

func (s *stubRouter) Patch(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, route{"PATCH", pattern, h})
}

func (s *stubRouter) Delete(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, route{"DELETE", pattern, h})
}

func (s *stubRouter) find(method, pattern string) http.HandlerFunc {
	for _, r := range s.routes {
		if r.method == method && r.pattern == pattern {
			return r.h
		}
	}
	return nil
}

type fakeAgents struct {
	agents map[string]agentuc.AgentView
	tokens map[string]string // enrollment plaintext -> agent id (after redeem, deleted)

	issueTTL      time.Duration
	patched       map[string]bool
	revoked       []string
	heartbeatIn   []agentuc.HeartbeatInput
	authTokens    []string
	policies      map[string][]agentuc.PolicySummary
	err           error // injected unexpected failure
	revokedAgents map[string]bool
}

func newFakeAgents() *fakeAgents {
	return &fakeAgents{
		agents:        map[string]agentuc.AgentView{},
		tokens:        map[string]string{"enroll-plaintext": ""},
		patched:       map[string]bool{},
		policies:      map[string][]agentuc.PolicySummary{},
		revokedAgents: map[string]bool{},
	}
}

func (f *fakeAgents) IssueEnrollmentToken(_ context.Context, ttl time.Duration) (string, *domain.AgentCredential, error) {
	if f.err != nil {
		return "", nil, f.err
	}
	f.issueTTL = ttl
	exp := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f.tokens["enroll-plaintext"] = ""
	return "enroll-plaintext", &domain.AgentCredential{ID: "cred-1", Enrollment: true, ExpiresAt: &exp}, nil
}

func (f *fakeAgents) RedeemEnrollmentToken(_ context.Context, token string, reg agentuc.Registration) (*domain.Agent, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	if _, ok := f.tokens[token]; !ok {
		return nil, "", agentuc.ErrInvalidToken
	}
	delete(f.tokens, token)
	ag := &domain.Agent{ID: "ag-new", Hostname: reg.Hostname, EnrolledAt: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	f.agents[ag.ID] = agentuc.AgentView{Agent: *ag, Status: domain.AgentStatusOffline}
	return ag, "agent-plaintext", nil
}

func (f *fakeAgents) Heartbeat(_ context.Context, in agentuc.HeartbeatInput) (*agentuc.HeartbeatResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.heartbeatIn = append(f.heartbeatIn, in)
	v, ok := f.agents[in.AgentID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &agentuc.HeartbeatResult{Status: v.Status}, nil
}

func (f *fakeAgents) GetAgent(_ context.Context, id string) (*agentuc.AgentView, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.agents[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &v, nil
}

func (f *fakeAgents) ListAgents(_ context.Context, _ domain.ListOptions, filter agentuc.AgentFilter) (*agentuc.AgentListResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	switch filter.Status {
	case "", domain.AgentStatusOnline, domain.AgentStatusOffline, domain.AgentStatusStale, domain.AgentStatusRevoked:
	default:
		return nil, errors.Join(domain.ErrValidation, errors.New("unknown status"))
	}
	var items []agentuc.AgentView
	for _, v := range f.agents {
		if filter.Status != "" && v.Status != filter.Status {
			continue
		}
		items = append(items, v)
	}
	return &agentuc.AgentListResult{Items: items, Total: len(f.agents)}, nil
}

func (f *fakeAgents) RevokeAgent(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.agents[id]; !ok {
		return domain.ErrNotFound
	}
	f.revoked = append(f.revoked, id)
	f.revokedAgents[id] = true
	v := f.agents[id]
	v.Agent.Disabled = true
	v.Status = domain.AgentStatusRevoked
	f.agents[id] = v
	return nil
}

func (f *fakeAgents) DeleteAgent(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	v, ok := f.agents[id]
	if !ok {
		return domain.ErrNotFound
	}
	if !v.Agent.Disabled {
		return errors.Join(domain.ErrConflict, errors.New("only revoked agents can be deleted"))
	}
	delete(f.agents, id)
	return nil
}

func (f *fakeAgents) SetDisabled(_ context.Context, id string, disabled bool) error {
	if f.err != nil {
		return f.err
	}
	v, ok := f.agents[id]
	if !ok {
		return domain.ErrNotFound
	}
	v.Agent.Disabled = disabled
	f.agents[id] = v
	f.patched[id] = disabled
	return nil
}

func (f *fakeAgents) AgentPolicies(_ context.Context, agentID string) ([]agentuc.PolicySummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	if _, ok := f.agents[agentID]; !ok {
		return nil, domain.ErrNotFound
	}
	return f.policies[agentID], nil
}

func (f *fakeAgents) AuthenticateAgent(_ context.Context, bearer string) (*domain.AgentCredential, error) {
	f.authTokens = append(f.authTokens, bearer)
	switch bearer {
	case "":
		return nil, agentuc.ErrUnauthorized
	case "revoked-token":
		return nil, agentuc.ErrAgentRevoked
	case "agent-token":
		return &domain.AgentCredential{ID: "cred-agent", AgentID: "ag-1"}, nil
	default:
		return nil, agentuc.ErrUnauthorized
	}
}

type fakeDiscovery struct {
	items    map[string][]domain.DiscoveredResource
	ingested map[string][]discoveryuc.ResourceInput
	err      error
}

func (f *fakeDiscovery) Ingest(_ context.Context, agentID string, resources []discoveryuc.ResourceInput) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if f.ingested == nil {
		f.ingested = map[string][]discoveryuc.ResourceInput{}
	}
	f.ingested[agentID] = resources
	return len(resources), nil
}

func (f *fakeDiscovery) List(_ context.Context, agentID string, kind domain.ResourceKind) ([]domain.DiscoveredResource, error) {
	if f.err != nil {
		return nil, f.err
	}
	if kind != "" && !discoveryuc.ValidKind(kind) {
		return nil, errors.Join(domain.ErrValidation, errors.New("unknown kind"))
	}
	var out []domain.DiscoveredResource
	for _, r := range f.items[agentID] {
		if kind != "" && r.Kind != kind {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

type fakeJobs struct {
	jobs        []QueuedJob
	cancelled   []string
	polled      []string
	limits      []int
	reports     []JobReport
	completions []JobReport
	outcome     *CompletionOutcome
	pollErr     error
	cancelErr   error
	reportErr   error
	completeErr error
}

func (f *fakeJobs) Poll(_ context.Context, agentID string, limit int) ([]QueuedJob, error) {
	f.polled = append(f.polled, agentID)
	f.limits = append(f.limits, limit)
	if f.pollErr != nil {
		return nil, f.pollErr
	}
	return f.jobs, nil
}

func (f *fakeJobs) CancelledIDs(_ context.Context, _ string) ([]string, error) {
	if f.cancelErr != nil {
		return nil, f.cancelErr
	}
	return f.cancelled, nil
}

func (f *fakeJobs) Report(_ context.Context, agentID string, rep JobReport) error {
	if f.reportErr != nil {
		return f.reportErr
	}
	f.reports = append(f.reports, rep)
	return nil
}

func (f *fakeJobs) CompleteWithManifest(_ context.Context, _ string, rep JobReport) (*CompletionOutcome, error) {
	if f.completeErr != nil {
		return nil, f.completeErr
	}
	f.completions = append(f.completions, rep)
	if f.outcome != nil {
		return f.outcome, nil
	}
	return &CompletionOutcome{Verified: true, SnapshotID: "snap-1"}, nil
}

func testRouter() (*stubRouter, *fakeAgents, *fakeDiscovery, *fakeJobs) {
	agents := newFakeAgents()
	agents.agents["ag-1"] = agentuc.AgentView{
		Agent:  domain.Agent{ID: "ag-1", Hostname: "vm-01", Version: "1.2.0", EnrolledAt: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)},
		Status: domain.AgentStatusOnline,
	}
	agents.policies["ag-1"] = []agentuc.PolicySummary{{ID: "p1", Name: "daily", Enabled: true, CurrentVersion: 2}}
	disco := &fakeDiscovery{items: map[string][]domain.DiscoveredResource{
		"ag-1": {
			{ID: "r1", AgentID: "ag-1", Kind: domain.ResourceKindDockerContainer, Name: "web"},
			{ID: "r2", AgentID: "ag-1", Kind: domain.ResourceKindHostPath, Name: "/srv"},
		},
	}}
	jobs := &fakeJobs{}
	r := &stubRouter{}
	Mount(r, Deps{
		Agents:    agents,
		Discovery: disco,
		Jobs:      jobs,
		Now:       func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return r, agents, disco, jobs
}

func do(t *testing.T, h http.HandlerFunc, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %d response %q: %v", rec.Code, rec.Body.String(), err)
	}
}

func TestMountRegistersRoutes(t *testing.T) {
	r, _, _, _ := testRouter()
	want := []route{
		{"POST", "/api/agents/enroll-tokens", nil},
		{"GET", "/api/agents", nil},
		{"GET", "/api/agents/:id", nil},
		{"PATCH", "/api/agents/:id", nil},
		{"POST", "/api/agents/:id/revoke", nil},
		{"DELETE", "/api/agents/:id", nil},
		{"GET", "/api/agents/:id/policies", nil},
		{"GET", "/api/agents/:id/resources", nil},
		{"POST", "/api/agent/enroll", nil},
		{"POST", "/api/agent/heartbeat", nil},
		{"GET", "/api/agent/poll", nil},
		{"POST", "/api/agent/report", nil},
		{"POST", "/api/agent/jobs/:id/upload-grants", nil},
		{"POST", "/api/agent/jobs/:id/multipart/init", nil},
		{"POST", "/api/agent/jobs/:id/multipart/complete", nil},
		{"POST", "/api/agent/jobs/:id/multipart/abort", nil},
		{"POST", "/api/agent/restores/poll", nil},
		{"POST", "/api/agent/restores/:id/download-grants", nil},
		{"POST", "/api/agent/restores/:id/report", nil},
	}
	if len(r.routes) != len(want) {
		t.Fatalf("want %d routes, got %d", len(want), len(r.routes))
	}
	for i, w := range want {
		if r.routes[i].method != w.method || r.routes[i].pattern != w.pattern || r.routes[i].h == nil {
			t.Fatalf("route %d: want %s %s, got %s %s", i, w.method, w.pattern, r.routes[i].method, r.routes[i].pattern)
		}
	}
}

func TestMountPanicsOnNilDeps(t *testing.T) {
	base := Deps{Agents: newFakeAgents(), Discovery: &fakeDiscovery{}, Jobs: &fakeJobs{}}
	for name, mutate := range map[string]func(*Deps){
		"agents":    func(d *Deps) { d.Agents = nil },
		"discovery": func(d *Deps) { d.Discovery = nil },
		"jobs":      func(d *Deps) { d.Jobs = nil },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("nil %s: want panic", name)
				}
			}()
			deps := base
			mutate(&deps)
			Mount(&stubRouter{}, deps)
		}()
	}
}

func TestAgentAuthAllowDeny(t *testing.T) {
	r, agents, _, jobs := testRouter()
	jobs.jobs = []QueuedJob{{ID: "j1", PolicyID: "p1", Kind: "filesystem"}}
	poll := r.find("GET", "/api/agent/poll")

	// No token -> 401.
	rec := do(t, poll, "GET", "/api/agent/poll", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d (%s)", rec.Code, rec.Body.String())
	}
	// Malformed scheme is denied too (no crash).
	req := httptest.NewRequest("GET", "/api/agent/poll", nil)
	req.Header.Set("Authorization", "Token agent-token")
	bad := httptest.NewRecorder()
	poll(bad, req)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad scheme: want 401, got %d", bad.Code)
	}
	// Unknown token -> 401.
	rec = do(t, poll, "GET", "/api/agent/poll", "", "nope")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", rec.Code)
	}
	// Revoked agent -> 403.
	rec = do(t, poll, "GET", "/api/agent/poll", "", "revoked-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("revoked: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}
	// Valid token -> 200, polled as the credential's agent.
	rec = do(t, poll, "GET", "/api/agent/poll", "", "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(jobs.polled) != 1 || jobs.polled[0] != "ag-1" {
		t.Fatalf("poll agent: got %v", jobs.polled)
	}
	if len(agents.authTokens) == 0 || agents.authTokens[len(agents.authTokens)-1] != "agent-token" {
		t.Fatalf("auth not consulted: %v", agents.authTokens)
	}
	var out pollResponse
	decodeBody(t, rec, &out)
	if len(out.Jobs) != 1 || out.Jobs[0].ID != "j1" {
		t.Fatalf("jobs not passed through: %+v", out)
	}

	// Heartbeat and report share the gate.
	hb := r.find("POST", "/api/agent/heartbeat")
	if rec := do(t, hb, "POST", "/api/agent/heartbeat", `{}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("heartbeat no token: want 401, got %d", rec.Code)
	}
	rep := r.find("POST", "/api/agent/report")
	if rec := do(t, rep, "POST", "/api/agent/report", `{}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("report no token: want 401, got %d", rec.Code)
	}
}

func TestPollValidation(t *testing.T) {
	r, _, _, jobs := testRouter()
	poll := r.find("GET", "/api/agent/poll")
	for _, target := range []string{"/api/agent/poll?limit=0", "/api/agent/poll?limit=999", "/api/agent/poll?limit=-1", "/api/agent/poll?limit=abc"} {
		if rec := do(t, poll, "GET", target, "", "agent-token"); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", target, rec.Code)
		}
	}
	rec := do(t, poll, "GET", "/api/agent/poll?limit=3", "", "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(jobs.limits) == 0 || jobs.limits[len(jobs.limits)-1] != 3 {
		t.Fatalf("limit not honored: %v", jobs.limits)
	}
	rec = do(t, poll, "GET", "/api/agent/poll", "", "agent-token")
	if rec.Code != http.StatusOK || jobs.limits[len(jobs.limits)-1] != defaultPollLimit {
		t.Fatalf("default limit: code=%d limits=%v", rec.Code, jobs.limits)
	}
}

func TestReportValidation(t *testing.T) {
	r, _, disco, jobs := testRouter()
	rep := r.find("POST", "/api/agent/report")

	cases := map[string]string{
		"empty body":        "",
		"neither":           `{}`,
		"job w/o id":        `{"job":{"state":"running"}}`,
		"job w/o state":     `{"job":{"job_id":"j1"}}`,
		"job bad state":     `{"job":{"job_id":"j1","state":"teleporting"}}`,
		"resource w/o kind": `{"resources":[{"name":"x"}]}`,
		"resource w/o name": `{"resources":[{"kind":"host_path"}]}`,
		"resource bad kind": `{"resources":[{"kind":"nope","name":"x"}]}`,
		"malformed":         `{oops`,
	}
	for name, body := range cases {
		if rec := do(t, rep, "POST", "/api/agent/report", body, "agent-token"); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	if len(jobs.reports) != 0 || len(disco.ingested) != 0 {
		t.Fatal("rejected reports must not reach services")
	}

	// Job-only report maps fields onto the queue.
	rec := do(t, rep, "POST", "/api/agent/report",
		`{"job":{"job_id":"j1","state":"succeeded","bytes_read":10,"bytes_uploaded":8}}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("job report: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(jobs.reports) != 1 {
		t.Fatalf("want 1 report, got %d", len(jobs.reports))
	}
	got := jobs.reports[0]
	if got.JobID != "j1" || got.State != domain.JobSucceeded || got.BytesRead == nil || *got.BytesRead != 10 {
		t.Fatalf("report not mapped: %+v", got)
	}
	var out reportResponse
	decodeBody(t, rec, &out)
	if !out.JobAccepted || out.ResourcesIngested != 0 {
		t.Fatalf("bad ack: %+v", out)
	}

	// Resource-only report ingests discovery.
	rec = do(t, rep, "POST", "/api/agent/report",
		`{"resources":[{"kind":"docker_container","name":"web","detail":{"container_id":"abc"}}]}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("resource report: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	ing := disco.ingested["ag-1"]
	if len(ing) != 1 || ing[0].Kind != domain.ResourceKindDockerContainer || ing[0].Detail["container_id"] != "abc" {
		t.Fatalf("ingest not mapped: %+v", ing)
	}
}

func TestEnroll(t *testing.T) {
	r, _, _, _ := testRouter()
	enroll := r.find("POST", "/api/agent/enroll")
	for name, body := range map[string]string{
		"empty":    "",
		"no token": `{"hostname":"vm"}`,
		"no host":  `{"token":"enroll-plaintext"}`,
		"bad json": `{`,
	} {
		if rec := do(t, enroll, "POST", "/api/agent/enroll", body, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	// Unknown enrollment token -> 401, never 500.
	if rec := do(t, enroll, "POST", "/api/agent/enroll", `{"token":"bogus","hostname":"vm"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bogus token: want 401, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec := do(t, enroll, "POST", "/api/agent/enroll",
		`{"token":"enroll-plaintext","hostname":"vm-02","os":"linux","version":"1.2.0"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("enroll: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out enrollResponse
	decodeBody(t, rec, &out)
	if out.AgentID == "" || out.Token == "" {
		t.Fatalf("enroll response missing fields: %+v", out)
	}
}

func TestHeartbeatEndpoint(t *testing.T) {
	r, agents, _, _ := testRouter()
	hb := r.find("POST", "/api/agent/heartbeat")
	if rec := do(t, hb, "POST", "/api/agent/heartbeat", `{"running_jobs":-1}`, "agent-token"); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative jobs: want 400, got %d", rec.Code)
	}
	if rec := do(t, hb, "POST", "/api/agent/heartbeat", ``, "agent-token"); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: want 400, got %d", rec.Code)
	}
	rec := do(t, hb, "POST", "/api/agent/heartbeat",
		`{"version":"1.2.0","os":"linux","architecture":"amd64","ip_address":"10.0.0.2","disk_total_bytes":200,"disk_free_bytes":80,"running_jobs":2,"docker_available":true}`, "agent-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(agents.heartbeatIn) != 1 {
		t.Fatalf("heartbeat input: %+v", agents.heartbeatIn)
	}
	in := agents.heartbeatIn[0]
	if in.AgentID != "ag-1" || in.RunningJobs != 2 || in.OS != "linux" || in.Architecture != "amd64" ||
		in.IPAddress != "10.0.0.2" || in.DiskTotalBytes == nil || *in.DiskTotalBytes != 200 ||
		in.DiskFreeBytes == nil || *in.DiskFreeBytes != 80 {
		t.Fatalf("heartbeat input: %+v", agents.heartbeatIn)
	}
	var out heartbeatResponse
	decodeBody(t, rec, &out)
	if !out.OK || out.Status != "online" {
		t.Fatalf("heartbeat response: %+v", out)
	}
}

func TestUIIssueToken(t *testing.T) {
	r, agents, _, _ := testRouter()
	issue := r.find("POST", "/api/agents/enroll-tokens")
	rec := do(t, issue, "POST", "/api/agents/enroll-tokens", "", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("default ttl: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if agents.issueTTL != 0 {
		t.Fatalf("default ttl should pass 0 (usecase default), got %v", agents.issueTTL)
	}
	var out issueTokenResponse
	decodeBody(t, rec, &out)
	if out.Token == "" || out.CredentialID == "" {
		t.Fatalf("issue response: %+v", out)
	}
	rec = do(t, issue, "POST", "/api/agents/enroll-tokens", `{"ttl_hours":2}`, "")
	if rec.Code != http.StatusCreated || agents.issueTTL != 2*time.Hour {
		t.Fatalf("ttl=2: code=%d ttl=%v", rec.Code, agents.issueTTL)
	}
	if rec := do(t, issue, "POST", "/api/agents/enroll-tokens", `{"ttl_hours":999}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize ttl: want 400, got %d", rec.Code)
	}
}

func TestUIListGet(t *testing.T) {
	r, _, _, _ := testRouter()
	list := r.find("GET", "/api/agents")
	rec := do(t, list, "GET", "/api/agents", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out listAgentsResponse
	decodeBody(t, rec, &out)
	if out.Total != 1 || len(out.Items) != 1 || out.Items[0].Status != "online" {
		t.Fatalf("list body: %+v", out)
	}
	for _, target := range []string{"/api/agents?status=bogus", "/api/agents?limit=999", "/api/agents?offset=-1", "/api/agents?limit=x"} {
		if rec := do(t, list, "GET", target, "", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d (%s)", target, rec.Code, rec.Body.String())
		}
	}

	get := r.find("GET", "/api/agents/:id")
	rec = do(t, get, "GET", "/api/agents/ag-1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
	var one agentSummary
	decodeBody(t, rec, &one)
	if one.Hostname != "vm-01" || one.ID != "ag-1" {
		t.Fatalf("get body: %+v", one)
	}
	if rec := do(t, get, "GET", "/api/agents/missing", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", rec.Code)
	}
	if rec := do(t, get, "GET", "/api/agents/", "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank id: want 400, got %d", rec.Code)
	}
}

func TestUIPatchRevoke(t *testing.T) {
	r, agents, _, _ := testRouter()
	patch := r.find("PATCH", "/api/agents/:id")
	if rec := do(t, patch, "PATCH", "/api/agents/ag-1", "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: want 400, got %d", rec.Code)
	}
	if rec := do(t, patch, "PATCH", "/api/agents/ag-1", `{}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("no fields: want 400, got %d", rec.Code)
	}
	rec := do(t, patch, "PATCH", "/api/agents/ag-1", `{"disabled":true}`, "")
	if rec.Code != http.StatusOK || !agents.patched["ag-1"] {
		t.Fatalf("disable: code=%d patched=%v", rec.Code, agents.patched)
	}
	if rec := do(t, patch, "PATCH", "/api/agents/missing", `{"disabled":true}`, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", rec.Code)
	}

	revoke := r.find("POST", "/api/agents/:id/revoke")
	rec = do(t, revoke, "POST", "/api/agents/ag-1/revoke", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(agents.revoked) != 1 || agents.revoked[0] != "ag-1" {
		t.Fatalf("revoked: %v", agents.revoked)
	}
	var one agentSummary
	decodeBody(t, rec, &one)
	if one.Status != "revoked" || !one.Disabled {
		t.Fatalf("revoke body: %+v", one)
	}
	if rec := do(t, revoke, "POST", "/api/agents/missing/revoke", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing revoke: want 404, got %d", rec.Code)
	}
}

func TestUIDelete(t *testing.T) {
	r, agents, _, _ := testRouter()
	del := r.find("DELETE", "/api/agents/:id")
	if rec := do(t, del, "DELETE", "/api/agents/missing", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", rec.Code)
	}
	if rec := do(t, del, "DELETE", "/api/agents/ag-1", "", ""); rec.Code != http.StatusConflict {
		t.Fatalf("non-revoked: want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
	revoke := r.find("POST", "/api/agents/:id/revoke")
	if rec := do(t, revoke, "POST", "/api/agents/ag-1/revoke", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d", rec.Code)
	}
	if rec := do(t, del, "DELETE", "/api/agents/ag-1", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := agents.agents["ag-1"]; ok {
		t.Fatal("agent row still present after delete")
	}
	get := r.find("GET", "/api/agents/:id")
	if rec := do(t, get, "GET", "/api/agents/ag-1", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", rec.Code)
	}
}

func TestUIPoliciesResources(t *testing.T) {
	r, _, _, _ := testRouter()
	pol := r.find("GET", "/api/agents/:id/policies")
	rec := do(t, pol, "GET", "/api/agents/ag-1/policies", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("policies: want 200, got %d", rec.Code)
	}
	var p agentPoliciesResponse
	decodeBody(t, rec, &p)
	if len(p.Policies) != 1 || p.Policies[0].Name != "daily" {
		t.Fatalf("policies body: %+v", p)
	}
	if rec := do(t, pol, "GET", "/api/agents/missing/policies", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", rec.Code)
	}

	res := r.find("GET", "/api/agents/:id/resources")
	rec = do(t, res, "GET", "/api/agents/ag-1/resources", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resources: want 200, got %d", rec.Code)
	}
	var rr agentResourcesResponse
	decodeBody(t, rec, &rr)
	if len(rr.Items) != 2 {
		t.Fatalf("resources body: %+v", rr)
	}
	rec = do(t, res, "GET", "/api/agents/ag-1/resources?kind=host_path", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("kind filter: want 200, got %d", rec.Code)
	}
	decodeBody(t, rec, &rr)
	if len(rr.Items) != 1 || rr.Items[0].Name != "/srv" {
		t.Fatalf("kind body: %+v", rr)
	}
	if rec := do(t, res, "GET", "/api/agents/ag-1/resources?kind=bogus", "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: want 400, got %d", rec.Code)
	}
}

func TestInternalErrorMapping(t *testing.T) {
	r, agents, _, _ := testRouter()
	agents.err = errors.New("boom: connection reset")
	get := r.find("GET", "/api/agents/:id")
	rec := do(t, get, "GET", "/api/agents/ag-1", "", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	var body errorBody
	decodeBody(t, rec, &body)
	if body.Error.Code != "internal_error" || body.Error.Message != "internal error" {
		t.Fatalf("500 must be generic: %+v", body)
	}
}
