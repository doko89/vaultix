package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	authhttp "github.com/vaultix/vaultix/internal/adapter/http/auth"
	jobshttp "github.com/vaultix/vaultix/internal/adapter/http/jobs"
	platformhttp "github.com/vaultix/vaultix/internal/adapter/http/platform"
	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	updateuc "github.com/vaultix/vaultix/internal/usecase/agentupdate"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
	discoveryuc "github.com/vaultix/vaultix/internal/usecase/discovery"
	jobuc "github.com/vaultix/vaultix/internal/usecase/job"
)

// ---------- fakes ----------

// matrixAuth serves canned sessions by cookie token. It embeds the full
// AuthService so only ValidateSession needs an override.
type matrixAuth struct {
	authhttp.AuthService
	sessions map[string]*authsvc.SessionInfo
}

func (f *matrixAuth) ValidateSession(_ context.Context, token string) (*authsvc.SessionInfo, error) {
	s, ok := f.sessions[token]
	if !ok {
		return nil, errors.New("bad session")
	}
	return s, nil
}

func sessionWithRoles(roles ...string) *authsvc.SessionInfo {
	var out []domain.Role
	for _, r := range roles {
		out = append(out, domain.Role{Name: r})
	}
	return &authsvc.SessionInfo{User: &domain.User{ID: "u1"}, Roles: out}
}

type matrixJobs struct{}

func (matrixJobs) EnqueueManual(_ context.Context, agentID, policyID, _ string) (*domain.BackupJob, error) {
	return &domain.BackupJob{ID: "j-new", AgentID: agentID, PolicyID: policyID, State: domain.JobQueued}, nil
}
func (matrixJobs) Get(_ context.Context, id string) (*domain.BackupJob, error) {
	return &domain.BackupJob{ID: id, State: domain.JobQueued}, nil
}
func (matrixJobs) List(_ context.Context, _ domain.ListOptions, _ jobuc.Filter) (domain.ListResult[domain.BackupJob], error) {
	return domain.ListResult[domain.BackupJob]{}, nil
}
func (matrixJobs) Retry(_ context.Context, id, _ string) (*domain.BackupJob, error) {
	return &domain.BackupJob{ID: "j-retry", RetryOf: id, State: domain.JobQueued}, nil
}
func (matrixJobs) Cancel(_ context.Context, id string) (*domain.BackupJob, error) {
	return &domain.BackupJob{ID: id, State: domain.JobCancelled}, nil
}
func (matrixJobs) RetryHistory(_ context.Context, id string) ([]*domain.BackupJob, error) {
	return []*domain.BackupJob{{ID: id, State: domain.JobQueued}}, nil
}

type matrixAgents struct{}

func (matrixAgents) IssueEnrollmentToken(_ context.Context, _ time.Duration) (string, *domain.AgentCredential, error) {
	return "tok", &domain.AgentCredential{ID: "c1"}, nil
}
func (matrixAgents) RedeemEnrollmentToken(_ context.Context, _ string, _ agentuc.Registration) (*domain.Agent, string, error) {
	return nil, "", agentuc.ErrInvalidToken
}
func (matrixAgents) Heartbeat(_ context.Context, _ agentuc.HeartbeatInput) (*agentuc.HeartbeatResult, error) {
	return &agentuc.HeartbeatResult{Status: domain.AgentStatusOnline}, nil
}
func (matrixAgents) GetAgent(_ context.Context, id string) (*agentuc.AgentView, error) {
	return &agentuc.AgentView{Agent: domain.Agent{ID: id}, Status: domain.AgentStatusOnline}, nil
}
func (matrixAgents) ListAgents(_ context.Context, _ domain.ListOptions, _ agentuc.AgentFilter) (*agentuc.AgentListResult, error) {
	return &agentuc.AgentListResult{}, nil
}
func (matrixAgents) RevokeAgent(_ context.Context, _ string) error         { return nil }
func (matrixAgents) SetDisabled(_ context.Context, _ string, _ bool) error { return nil }
func (matrixAgents) DeleteAgent(_ context.Context, _ string) error         { return nil }
func (matrixAgents) SyncReleases(_ context.Context) (*updateuc.SyncStatus, error) {
	return &updateuc.SyncStatus{}, nil
}
func (matrixAgents) ListArtifacts(_ context.Context) ([]domain.AgentArtifact, error) {
	return nil, nil
}
func (matrixAgents) DeleteArtifact(_ context.Context, _ string) error { return nil }
func (matrixAgents) RequestUpdate(_ context.Context, _, _, _ string) (*domain.AgentUpdate, error) {
	return &domain.AgentUpdate{Status: domain.AgentUpdatePending}, nil
}
func (matrixAgents) PendingDirective(_ context.Context, _ string) (*updateuc.Directive, error) {
	return nil, nil
}
func (matrixAgents) DownloadArtifact(_ context.Context, _ string) (*domain.AgentArtifact, error) {
	return nil, domain.ErrNotFound
}
func (matrixAgents) ReportUpdate(_ context.Context, _, _, _, _ string) error { return nil }
func (matrixAgents) Describe(_ context.Context, _ string) (*updateuc.Info, error) {
	return &updateuc.Info{}, nil
}
func (matrixAgents) AgentPolicies(_ context.Context, _ string) ([]agentuc.PolicySummary, error) {
	return nil, nil
}
func (matrixAgents) AuthenticateAgent(_ context.Context, bearer string) (*domain.AgentCredential, error) {
	if bearer == "agent-token" {
		return &domain.AgentCredential{ID: "c", AgentID: "ag-1"}, nil
	}
	return nil, agentuc.ErrUnauthorized
}

type matrixDiscovery struct{}

func (matrixDiscovery) Ingest(_ context.Context, _ string, r []discoveryuc.ResourceInput) (int, error) {
	return len(r), nil
}
func (matrixDiscovery) List(_ context.Context, _ string, _ domain.ResourceKind) ([]domain.DiscoveredResource, error) {
	return nil, nil
}

type matrixQueue struct{}

func (matrixQueue) Poll(_ context.Context, _ string, _ int) ([]agentshttp.QueuedJob, error) {
	return []agentshttp.QueuedJob{}, nil
}
func (matrixQueue) CancelledIDs(_ context.Context, _ string) ([]string, error) {
	return []string{}, nil
}
func (matrixQueue) Report(_ context.Context, _ string, _ agentshttp.JobReport) error {
	return nil
}
func (matrixQueue) CompleteWithManifest(_ context.Context, _ string, _ agentshttp.JobReport) (*agentshttp.CompletionOutcome, error) {
	return &agentshttp.CompletionOutcome{Verified: true}, nil
}

type matrixSettings struct{ s platformhttp.GlobalSettings }

func (f *matrixSettings) Get(_ context.Context) (platformhttp.GlobalSettings, error) {
	return f.s, nil
}
func (f *matrixSettings) Update(_ context.Context, s platformhttp.GlobalSettings) (platformhttp.GlobalSettings, error) {
	f.s = s
	return s, nil
}

// ---------- rig ----------

func matrixRouter() *Mux {
	return NewRouter(RouterDeps{
		Health:         HealthDeps{DB: stubDB{}, Version: "test"},
		RequestTimeout: 5 * time.Second,
		Logger:         discardLogger(),
		Slices: SliceDeps{
			Auth: authhttp.Deps{Auth: &matrixAuth{sessions: map[string]*authsvc.SessionInfo{
				"tok-admin": sessionWithRoles(domain.RoleAdministrator),
				"tok-op":    sessionWithRoles(domain.RoleOperator),
				"tok-ro":    sessionWithRoles(domain.RoleReadOnly),
			}}},
			Agents: agentshttp.Deps{Agents: matrixAgents{}, Discovery: matrixDiscovery{}, Jobs: matrixQueue{}, Updates: matrixAgents{}},
			Jobs:   jobshttp.Deps{Service: matrixJobs{}},
			Platform: platformhttp.Deps{
				Settings: &matrixSettings{},
			},
		},
	})
}

func matrixDo(t *testing.T, mux *Mux, method, target, body, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: cookie})
		// Cookie-authed mutations need the CSRF pass header (see middleware).
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// ---------- matrix ----------

func TestRBACMatrix(t *testing.T) {
	mux := matrixRouter()

	anon := matrixDo(t, mux, "GET", "/api/jobs/", "", "")
	require.Equal(t, http.StatusUnauthorized, anon.Code, "anon UI read -> 401")

	anonWrite := matrixDo(t, mux, "POST", "/api/jobs/j1/retry", "", "")
	require.Equal(t, http.StatusUnauthorized, anonWrite.Code, "anon UI write -> 401")

	// Reads: all authed roles pass.
	for _, tok := range []string{"tok-admin", "tok-op", "tok-ro"} {
		rec := matrixDo(t, mux, "GET", "/api/jobs/", "", tok)
		require.Equal(t, http.StatusOK, rec.Code, "read as %s", tok)
		rec = matrixDo(t, mux, "GET", "/api/agents", "", tok)
		require.Equal(t, http.StatusOK, rec.Code, "agents read as %s", tok)
		rec = matrixDo(t, mux, "GET", "/api/settings", "", tok)
		require.Equal(t, http.StatusOK, rec.Code, "settings read as %s", tok)
	}

	// Writes: readonly -> 403, operator/admin pass.
	writes := []struct{ method, target, body string }{
		{"POST", "/api/jobs/", `{"agent_id":"a","policy_id":"p"}`},
		{"POST", "/api/jobs/j1/retry", ""},
		{"POST", "/api/jobs/j1/cancel", ""},
		{"POST", "/api/agents/enroll-tokens", "{}"},
		{"PUT", "/api/settings", `{"session_lifetime_minutes":60}`},
	}
	for _, w := range writes {
		rec := matrixDo(t, mux, w.method, w.target, w.body, "tok-ro")
		require.Equal(t, http.StatusForbidden, rec.Code, "%s %s readonly -> 403", w.method, w.target)
		rec = matrixDo(t, mux, w.method, w.target, w.body, "tok-op")
		require.GreaterOrEqual(t, rec.Code, 200)
		require.Less(t, rec.Code, 300, "%s %s operator allowed, got %d (%s)", w.method, w.target, rec.Code, rec.Body.String())
	}

	// Agents admin-only writes refuse operators too.
	for _, w := range []struct{ method, target, body string }{
		{"PATCH", "/api/agents/ag-1", `{"disabled":true}`},
		{"POST", "/api/agents/ag-1/revoke", ""},
		{"DELETE", "/api/agents/ag-1", ""},
		{"POST", "/api/agents/ag-1/update", `{"artifact_id":"art-1"}`},
		{"DELETE", "/api/agents/updates/artifacts/art-1", ""},
	} {
		rec := matrixDo(t, mux, w.method, w.target, w.body, "tok-op")
		require.Equal(t, http.StatusForbidden, rec.Code, "%s %s operator -> 403", w.method, w.target)
		rec = matrixDo(t, mux, w.method, w.target, w.body, "tok-admin")
		require.Less(t, rec.Code, 300, "%s %s admin allowed, got %d", w.method, w.target, rec.Code)
	}
}

func TestProtocolStaysBearerAuthed(t *testing.T) {
	mux := matrixRouter()

	// Session cookies buy nothing on the protocol: no bearer -> 401 even
	// when a valid admin session cookie is present.
	req := httptest.NewRequest("GET", "/api/agent/poll", nil)
	req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: "tok-admin"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Agent bearer passes with no session cookie at all.
	req = httptest.NewRequest("GET", "/api/agent/poll", nil)
	req.Header.Set("Authorization", "Bearer agent-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// And the UI rejects bare bearer tokens (wrong credential surface).
	req = httptest.NewRequest("GET", "/api/jobs/", nil)
	req.Header.Set("Authorization", "Bearer agent-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUIRoleRulesTable(t *testing.T) {
	rules := uiRoleRules()
	byKey := map[string][]string{}
	for _, r := range rules {
		byKey[r.method+" "+r.pattern] = r.roles
	}
	op := []string{domain.RoleAdministrator, domain.RoleOperator}
	admin := []string{domain.RoleAdministrator}
	for key, want := range map[string][]string{
		"POST /api/agents/enroll-tokens":             op,
		"PATCH /api/agents/:id":                      admin,
		"POST /api/agents/:id/revoke":                admin,
		"DELETE /api/agents/:id":                     admin,
		"POST /api/agents/updates/check":             admin,
		"DELETE /api/agents/updates/artifacts/:id":   admin,
		"POST /api/agents/:id/update":                admin,
		"POST /api/restore/plan":                     op,
		"POST /api/restore/confirm":                  op,
		"POST /api/restore/execute":                  op,
		"POST /api/storage":                          op,
		"PUT /api/storage/{id}":                      op,
		"DELETE /api/storage/{id}":                   op,
		"POST /api/storage/{id}/test":                op,
		"POST /api/credentials":                      op,
		"PUT /api/credentials/{id}":                  op,
		"DELETE /api/credentials/{id}":               op,
		"PUT /api/settings":                          op,
		"POST /api/settings/oidc-providers":          op,
		"PUT /api/settings/oidc-providers/:id":       op,
		"DELETE /api/settings/oidc-providers/:id":    op,
		"POST /api/settings/oidc-providers/:id/test": op,
		"POST /api/notifications/rules":              op,
		"PUT /api/notifications/rules/:id":           op,
		"DELETE /api/notifications/rules/:id":        op,
		"POST /api/notifications/rules/:id/test":     op,
	} {
		require.ElementsMatch(t, want, byKey[key], "rule %s", key)
	}
	// Reads carry no rule (plain RequireAuth).
	for _, key := range []string{"GET /api/agents", "GET /api/storage", "GET /api/settings", "GET /api/dashboard"} {
		_, ok := byKey[key]
		require.False(t, ok, "read %s must not carry a role rule", key)
	}
}
