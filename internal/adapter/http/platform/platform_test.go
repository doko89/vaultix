package platform_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterhttp "github.com/vaultix/vaultix/internal/adapter/http"
	"github.com/vaultix/vaultix/internal/adapter/http/platform"
	"github.com/vaultix/vaultix/internal/domain"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
	notificationuc "github.com/vaultix/vaultix/internal/usecase/notification"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- fakes ----

type fakeDashboard struct {
	counts    platform.AgentCounts
	byStatus  map[domain.JobState]int64
	recent    []platform.JobSummary
	perAgent  []platform.LastBackup
	perPolicy []platform.LastBackup
	gaps      []platform.PolicyGap
	trend     []platform.DayPoint
	storage   platform.StorageUsage
	err       error
	failOn    string // fail only this method ("" = fail all when err set)
}

func (f *fakeDashboard) fail(method string) error {
	if f.err == nil {
		return nil
	}
	if f.failOn == "" || f.failOn == method {
		return f.err
	}
	return nil
}

func (f *fakeDashboard) AgentCounts(context.Context) (platform.AgentCounts, error) {
	return f.counts, f.fail("counts")
}
func (f *fakeDashboard) JobsByStatus(context.Context) (map[domain.JobState]int64, error) {
	if f.byStatus == nil {
		f.byStatus = map[domain.JobState]int64{}
	}
	return f.byStatus, f.fail("status")
}
func (f *fakeDashboard) RecentJobs(_ context.Context, limit int) ([]platform.JobSummary, error) {
	if len(f.recent) > limit {
		return f.recent[:limit], f.fail("recent")
	}
	return f.recent, f.fail("recent")
}
func (f *fakeDashboard) LastBackupPerAgent(context.Context) ([]platform.LastBackup, error) {
	return f.perAgent, f.fail("perAgent")
}
func (f *fakeDashboard) LastBackupPerPolicy(context.Context) ([]platform.LastBackup, error) {
	return f.perPolicy, f.fail("perPolicy")
}
func (f *fakeDashboard) PoliciesWithoutRecentSuccess(context.Context, time.Duration) ([]platform.PolicyGap, error) {
	return f.gaps, f.fail("gaps")
}
func (f *fakeDashboard) JobTrend(context.Context, int) ([]platform.DayPoint, error) {
	return f.trend, f.fail("trend")
}
func (f *fakeDashboard) StorageUsage(context.Context) (platform.StorageUsage, error) {
	return f.storage, f.fail("storage")
}

type fakeAudit struct {
	items []domain.AuditLog
	err   error
}

func (f *fakeAudit) Query(_ context.Context, _ domain.ListOptions, _ audituc.Filter) (domain.ListResult[domain.AuditLog], error) {
	if f.err != nil {
		return domain.ListResult[domain.AuditLog]{}, f.err
	}
	return domain.ListResult[domain.AuditLog]{Items: f.items, Total: len(f.items)}, nil
}

type fakeOIDC struct {
	providers map[string]domain.OIDCProvider
	testErr   map[string]error
}

func (f *fakeOIDC) CreateProvider(_ context.Context, in platform.OIDCProviderInput) (domain.OIDCProvider, error) {
	p := domain.OIDCProvider{
		ID: "p-new", Name: in.Name, Issuer: in.Issuer, Enabled: in.Enabled,
		ClientID: in.ClientID, Scopes: in.Scopes,
		GroupClaimNames: in.GroupClaimNames, GroupRoleMapping: in.GroupRoleMapping,
		DefaultRole: in.DefaultRole,
	}
	if in.ClientSecret != "" {
		p.ClientSecretRef = "ref-encrypted"
	}
	if in.JITProvisioning != nil {
		p.JITProvisioning = *in.JITProvisioning
	}
	f.providers[p.ID] = p
	return p, nil
}
func (f *fakeOIDC) GetProvider(_ context.Context, id string) (domain.OIDCProvider, error) {
	p, ok := f.providers[id]
	if !ok {
		return domain.OIDCProvider{}, fmt.Errorf("%w: provider %s", domain.ErrNotFound, id)
	}
	return p, nil
}
func (f *fakeOIDC) ListProviders(context.Context) ([]domain.OIDCProvider, error) {
	var out []domain.OIDCProvider
	for _, p := range f.providers {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeOIDC) UpdateProvider(_ context.Context, id string, in platform.OIDCProviderInput) (domain.OIDCProvider, error) {
	p, ok := f.providers[id]
	if !ok {
		return domain.OIDCProvider{}, fmt.Errorf("%w: provider %s", domain.ErrNotFound, id)
	}
	p.Name, p.Issuer, p.Enabled = in.Name, in.Issuer, in.Enabled
	f.providers[id] = p
	return p, nil
}
func (f *fakeOIDC) DeleteProvider(_ context.Context, id string) error {
	if _, ok := f.providers[id]; !ok {
		return fmt.Errorf("%w: provider %s", domain.ErrNotFound, id)
	}
	delete(f.providers, id)
	return nil
}
func (f *fakeOIDC) TestProvider(_ context.Context, id string) error {
	if err, ok := f.testErr[id]; ok {
		return err
	}
	if _, ok := f.providers[id]; !ok {
		return fmt.Errorf("%w: provider %s", domain.ErrNotFound, id)
	}
	return nil
}

type stubDB struct{}

func (stubDB) Ping(context.Context) error { return nil }

// testMux builds the REAL root mux with the platform slice mounted, proving
// the Mount/ChIPattern interop end to end.
func testMux(t *testing.T, deps platform.Deps) *adapterhttp.Mux {
	t.Helper()
	deps.Logger = discardLogger()
	m := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Health: adapterhttp.HealthDeps{DB: stubDB{}, Version: "test"},
		Logger: discardLogger(),
	})
	platform.Mount(m, deps)
	return m
}

func testNotifService(t *testing.T) *notificationuc.Service {
	t.Helper()
	store, err := notificationuc.NewStaticRuleStore(nil)
	require.NoError(t, err)
	svc, err := notificationuc.New(notificationuc.Deps{
		Rules: store, Outbox: &fakeOutbox{}, Senders: []notificationuc.Sender{&stubSender{}},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	return svc
}

type fakeOutbox struct{}

func (fakeOutbox) CreateNotification(_ context.Context, n *domain.Notification) error {
	n.ID = "n-1"
	return nil
}
func (fakeOutbox) UpdateNotification(context.Context, *domain.Notification) error  { return nil }
func (fakeOutbox) ListPending(context.Context, int) ([]domain.Notification, error) { return nil, nil }

type stubSender struct{}

func (stubSender) Channel() domain.NotificationChannel { return domain.NotificationEmail }
func (stubSender) Send(context.Context, *domain.Notification) error {
	return fmt.Errorf("no email transport in tests")
}

func TestDashboard(t *testing.T) {
	m := testMux(t, platform.Deps{Dashboard: &fakeDashboard{
		counts:   platform.AgentCounts{Total: 3, Online: 2, Offline: 1},
		byStatus: map[domain.JobState]int64{domain.JobSucceeded: 10, domain.JobFailed: 2},
		recent: []platform.JobSummary{
			{ID: "j-1", AgentID: "a-1", PolicyID: "p-1", State: domain.JobSucceeded},
			{ID: "j-2", AgentID: "a-2", PolicyID: "p-1", State: domain.JobFailed, Error: "boom"},
		},
		perAgent:  []platform.LastBackup{{AgentID: "a-1", HasSucceeded: true}},
		perPolicy: []platform.LastBackup{{PolicyID: "p-1", HasSucceeded: true}},
		gaps:      []platform.PolicyGap{{PolicyID: "p-9", PolicyName: "stale"}},
		trend:     []platform.DayPoint{{Date: "2026-11-19", Succeeded: 5, Failed: 1}},
		storage:   platform.StorageUsage{BytesUsed: 1024, Available: true},
	}})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"total":3`)
	assert.Contains(t, body, `"succeeded":10`)
	assert.Contains(t, body, `"j-1"`)
	assert.Contains(t, body, `"policies_without_recent_success"`)
	assert.Contains(t, body, `"p-9"`)
	assert.Contains(t, body, `"trend"`)
	assert.Contains(t, body, `"storage"`)

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard?recent_limit=1", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"j-1"`)
	assert.NotContains(t, rec.Body.String(), `"j-2"`)

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard?gap_days=nope", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationRulesCRUD(t *testing.T) {
	m := testMux(t, platform.Deps{Notifications: testNotifService(t)})

	// Create.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/rules",
		strings.NewReader(`{"name":"ops","events":["job_failed"],"channel":"email","recipient":"ops@example.com","enabled":true}`))
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"ops@example.com"`)

	// Invalid channel rejected.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/notifications/rules",
		strings.NewReader(`{"name":"bad","events":["job_failed"],"channel":"sms","recipient":"x","enabled":true}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// List.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/rules", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ops"`)

	// Get one (ID parsed from path).
	ruleID := extractID(t, rec.Body.String())
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/rules/"+ruleID, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	// Update.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/notifications/rules/"+ruleID,
		strings.NewReader(`{"name":"ops2","events":["job_failed"],"channel":"email","recipient":"b@example.com","enabled":true}`))
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ops2"`)

	// Test (stub transport fails -> 500, proving the call reaches the sender).
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/notifications/rules/"+ruleID+"/test", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Delete.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/notifications/rules/"+ruleID, nil))
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Gone.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/rules/"+ruleID, nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func extractID(t *testing.T, body string) string {
	t.Helper()
	idx := strings.Index(body, `"id":"`)
	require.NotEqual(t, -1, idx, "expected an id in %s", body)
	rest := body[idx+len(`"id":"`):]
	end := strings.Index(rest, `"`)
	require.NotEqual(t, -1, end)
	return rest[:end]
}

func TestAuditLogs(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	m := testMux(t, platform.Deps{Audit: &fakeAudit{items: []domain.AuditLog{
		{ID: "e-1", ActorID: "u-1", ActorType: "user", Action: domain.AuditLogin, Outcome: "ok", CreatedAt: now},
	}}})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit-logs", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"total":1`)
	assert.Contains(t, rec.Body.String(), `"login"`)

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit-logs?since=not-a-time", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSettings(t *testing.T) {
	store := platform.NewMemorySettingsStore(platform.DefaultGlobalSettings())
	m := testMux(t, platform.Deps{Settings: store})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"local_login_enabled":true`)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"local_login_enabled":false,"default_role":"readonly","session_lifetime_minutes":60,"session_idle_minutes":10,"heartbeat_timeout_seconds":120,"global_concurrency":5}`))
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "\"global_concurrency\":5")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"local_login_enabled":true,"session_lifetime_minutes":60,"session_idle_minutes":600}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "idle > lifetime must be rejected")
}

func TestOIDCProviders(t *testing.T) {
	oidc := &fakeOIDC{providers: map[string]domain.OIDCProvider{}, testErr: map[string]error{}}
	m := testMux(t, platform.Deps{OIDC: oidc})

	// Create with a secret.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/oidc-providers",
		strings.NewReader(`{"name":"Okta","issuer":"https://okta.example","enabled":true,"client_id":"cid","client_secret":"super-secret-value"}`))
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"has_client_secret":true`)
	assert.NotContains(t, rec.Body.String(), "super-secret-value", "secrets must never appear in read shapes")
	assert.NotContains(t, rec.Body.String(), `"client_secret":`, "no value-bearing secret key in read shapes")
	assert.NotContains(t, rec.Body.String(), "ClientSecretRef")

	id := extractID(t, rec.Body.String())

	// Get + list are secret-free.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/oidc-providers/"+id, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "super-secret-value")

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/oidc-providers", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"Okta"`)

	// Missing create fields rejected.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings/oidc-providers",
		strings.NewReader(`{"name":"x"}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Test ok.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/settings/oidc-providers/"+id+"/test", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// Test failure surfaces actionably.
	oidc.testErr[id] = fmt.Errorf("discovery unreachable")
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/settings/oidc-providers/"+id+"/test", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "discovery unreachable")

	// Delete.
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/oidc-providers/"+id, nil))
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDashboardErrorPropagates(t *testing.T) {
	m := testMux(t, platform.Deps{Dashboard: &fakeDashboard{err: fmt.Errorf("db down")}})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Each aggregation call failing must 500 (covers every error branch).
	for _, method := range []string{"counts", "status", "recent", "perAgent", "perPolicy", "gaps", "trend", "storage"} {
		m := testMux(t, platform.Deps{Dashboard: &fakeDashboard{err: fmt.Errorf("db down"), failOn: method}})
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code, "failOn=%s", method)
	}
}

func TestDashboardBadParams(t *testing.T) {
	m := testMux(t, platform.Deps{Dashboard: &fakeDashboard{}})
	for _, target := range []string{
		"/api/dashboard?recent_limit=-1",
		"/api/dashboard?trend_days=0",
		"/api/dashboard?trend_days=abc",
		"/api/dashboard?gap_days=0",
	} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code, target)
	}
	// recent_limit=0 means default, not empty.
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard?recent_limit=0", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRuleNotFoundAndBadBody(t *testing.T) {
	m := testMux(t, platform.Deps{Notifications: testNotifService(t)})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/rules/missing", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/rules", strings.NewReader(`{oops`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/notifications/rules",
		strings.NewReader(`{"name":"x","events":[],"channel":"email","recipient":"a@example.com"}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "empty events rejected by validator")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/notifications/rules/missing",
		strings.NewReader(`{"name":"x","events":["job_failed"],"channel":"email","recipient":"a@example.com"}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAuditErrorAndPaging(t *testing.T) {
	m := testMux(t, platform.Deps{Audit: &fakeAudit{err: fmt.Errorf("db down")}})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit-logs", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	m2 := testMux(t, platform.Deps{Audit: &fakeAudit{}})
	rec = httptest.NewRecorder()
	m2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit-logs?limit=200&offset=5&until=bad", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = httptest.NewRecorder()
	m2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit-logs?limit=200&offset=5", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "limit clamps to 100 without error")
}

func TestSettingsBadBody(t *testing.T) {
	store := platform.NewMemorySettingsStore(platform.DefaultGlobalSettings())
	m := testMux(t, platform.Deps{Settings: store})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{bad`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"default_role":"superuser"}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOIDCUpdateDeleteNotFound(t *testing.T) {
	oidc := &fakeOIDC{providers: map[string]domain.OIDCProvider{}, testErr: map[string]error{}}
	m := testMux(t, platform.Deps{OIDC: oidc})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/oidc-providers/missing", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/oidc-providers/missing",
		strings.NewReader(`{"name":"x","issuer":"https://x.example","client_id":"c"}`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/oidc-providers/missing", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings/oidc-providers",
		strings.NewReader(`{bad`))
	m.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Update round-trip preserves secret-backed flag.
	created, err := oidc.CreateProvider(context.Background(), platform.OIDCProviderInput{
		Name: "A", Issuer: "https://a.example", ClientID: "c", ClientSecret: "s",
	})
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings/oidc-providers/"+created.ID,
		strings.NewReader(`{"name":"B","issuer":"https://a.example","client_id":"c"}`))
	m.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"B"`)
	assert.NotContains(t, rec.Body.String(), `"client_secret":`)
}

// ---- failing-dependency stubs: exercise every handler error branch ----

type failingNotifications struct{ err error }

func (f failingNotifications) CreateRule(context.Context, notificationuc.Rule) (notificationuc.Rule, error) {
	return notificationuc.Rule{}, f.err
}
func (f failingNotifications) GetRule(context.Context, string) (notificationuc.Rule, error) {
	return notificationuc.Rule{}, f.err
}
func (f failingNotifications) ListRules(context.Context) ([]notificationuc.Rule, error) {
	return nil, f.err
}
func (f failingNotifications) UpdateRule(context.Context, string, notificationuc.Rule) (notificationuc.Rule, error) {
	return notificationuc.Rule{}, f.err
}
func (f failingNotifications) DeleteRule(context.Context, string) error { return f.err }
func (f failingNotifications) TestRule(context.Context, string) error   { return f.err }

type failingSettings struct{ err error }

func (f failingSettings) Get(context.Context) (platform.GlobalSettings, error) {
	return platform.GlobalSettings{}, f.err
}
func (f failingSettings) Update(_ context.Context, s platform.GlobalSettings) (platform.GlobalSettings, error) {
	return platform.GlobalSettings{}, f.err
}

type failingOIDC struct{ err error }

func (f failingOIDC) CreateProvider(context.Context, platform.OIDCProviderInput) (domain.OIDCProvider, error) {
	return domain.OIDCProvider{}, f.err
}
func (f failingOIDC) GetProvider(context.Context, string) (domain.OIDCProvider, error) {
	return domain.OIDCProvider{}, f.err
}
func (f failingOIDC) ListProviders(context.Context) ([]domain.OIDCProvider, error) {
	return nil, f.err
}
func (f failingOIDC) UpdateProvider(context.Context, string, platform.OIDCProviderInput) (domain.OIDCProvider, error) {
	return domain.OIDCProvider{}, f.err
}
func (f failingOIDC) DeleteProvider(context.Context, string) error { return f.err }
func (f failingOIDC) TestProvider(context.Context, string) error   { return f.err }

func TestHandlerErrorBranches(t *testing.T) {
	boom := fmt.Errorf("db down")
	m := testMux(t, platform.Deps{
		Notifications: failingNotifications{err: boom},
		Audit:         &fakeAudit{err: boom},
		Settings:      failingSettings{err: boom},
		OIDC:          failingOIDC{err: boom},
	})
	ruleBody := func() *strings.Reader {
		return strings.NewReader(`{"name":"x","events":["job_failed"],"channel":"email","recipient":"a@example.com"}`)
	}
	provBody := func() *strings.Reader {
		return strings.NewReader(`{"name":"x","issuer":"https://x.example","client_id":"c"}`)
	}
	cases := []struct {
		method, target string
		body           *strings.Reader
		want           int
	}{
		{http.MethodGet, "/api/notifications/rules", nil, 500},
		{http.MethodPost, "/api/notifications/rules", ruleBody(), 500},
		{http.MethodGet, "/api/notifications/rules/r1", nil, 500},
		{http.MethodPut, "/api/notifications/rules/r1", ruleBody(), 500},
		{http.MethodDelete, "/api/notifications/rules/r1", nil, 500},
		{http.MethodPost, "/api/notifications/rules/r1/test", nil, 500},
		{http.MethodGet, "/api/settings", nil, 500},
		{http.MethodGet, "/api/settings/oidc-providers", nil, 500},
		{http.MethodPost, "/api/settings/oidc-providers", provBody(), 500},
		{http.MethodGet, "/api/settings/oidc-providers/p1", nil, 500},
		{http.MethodPut, "/api/settings/oidc-providers/p1", provBody(), 500},
		{http.MethodDelete, "/api/settings/oidc-providers/p1", nil, 500},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		var body *strings.Reader
		if c.body != nil {
			body = c.body
		}
		var req *http.Request
		if body == nil {
			req = httptest.NewRequest(c.method, c.target, nil)
		} else {
			req = httptest.NewRequest(c.method, c.target, body)
		}
		m.ServeHTTP(rec, req)
		assert.Equal(t, c.want, rec.Code, "%s %s", c.method, c.target)
		assert.Contains(t, rec.Body.String(), "internal server error")
	}
}

func TestDomainErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: x", domain.ErrConflict), http.StatusConflict},
		{fmt.Errorf("%w: x", domain.ErrForbidden), http.StatusForbidden},
	} {
		m := testMux(t, platform.Deps{Notifications: failingNotifications{err: tc.err}})
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/rules", nil))
		assert.Equal(t, tc.want, rec.Code)
	}
}

func TestGlobalSettingsValidationBranches(t *testing.T) {
	assert.Error(t, platform.GlobalSettings{SessionLifetimeMinutes: -1}.Validate())
	assert.Error(t, platform.GlobalSettings{HeartbeatTimeoutSeconds: -1}.Validate())
	assert.Error(t, platform.GlobalSettings{GlobalConcurrency: -1}.Validate())
	assert.NoError(t, platform.GlobalSettings{SessionLifetimeMinutes: 0, SessionIdleMinutes: 5}.Validate())
}

func TestPartialMountSkipsNilSections(t *testing.T) {
	m := testMux(t, platform.Deps{Settings: platform.NewMemorySettingsStore(platform.DefaultGlobalSettings())})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "unmounted section falls to the root JSON 404")
}
