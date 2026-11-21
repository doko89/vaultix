package restore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
)

// ---------- fakes ----------

type fakeSnapshots struct {
	mu       sync.Mutex
	snaps    map[string]*domain.Snapshot
	manifest map[string]*domain.SnapshotManifest
	objects  map[string][]domain.SnapshotObject
	lastOpts domain.ListOptions
	lastFilt domain.SnapshotFilter
}

func (f *fakeSnapshots) seed(s *domain.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snaps == nil {
		f.snaps = map[string]*domain.Snapshot{}
		f.manifest = map[string]*domain.SnapshotManifest{}
		f.objects = map[string][]domain.SnapshotObject{}
	}
	f.snaps[s.ID] = s
}
func (f *fakeSnapshots) CreateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.seed(s)
	return nil
}
func (f *fakeSnapshots) GetSnapshot(_ context.Context, id string) (*domain.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.snaps[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}
func (f *fakeSnapshots) UpdateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.seed(s)
	return nil
}
func (f *fakeSnapshots) DeleteSnapshot(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snaps, id)
	return nil
}
func (f *fakeSnapshots) ListSnapshots(_ context.Context, opts domain.ListOptions, filter domain.SnapshotFilter) (domain.ListResult[domain.Snapshot], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastOpts, f.lastFilt = opts, filter
	var items []domain.Snapshot
	for _, s := range f.snaps {
		if filter.PolicyID != "" && s.PolicyID != filter.PolicyID {
			continue
		}
		items = append(items, *s)
	}
	return domain.ListResult[domain.Snapshot]{Items: items, Total: len(items)}, nil
}
func (f *fakeSnapshots) PutManifest(_ context.Context, m *domain.SnapshotManifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.manifest[m.SnapshotID] = m
	return nil
}
func (f *fakeSnapshots) GetManifest(_ context.Context, snapshotID string) (*domain.SnapshotManifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.manifest[snapshotID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return m, nil
}
func (f *fakeSnapshots) PutObjects(_ context.Context, _ []domain.SnapshotObject) error {
	return nil
}
func (f *fakeSnapshots) ListObjects(_ context.Context, manifestID string) ([]domain.SnapshotObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SnapshotObject(nil), f.objects[manifestID]...), nil
}

type fakeRestores struct {
	planErr    error
	confirmErr error
	execErr    error
	statusErr  error
	plan       *restoreuc.Plan
	job        *domain.RestoreJob
	lastPlan   restoreuc.PlanRequest
}

func (f *fakeRestores) Plan(_ context.Context, req restoreuc.PlanRequest) (*restoreuc.Plan, error) {
	f.lastPlan = req
	if f.planErr != nil {
		return nil, f.planErr
	}
	return f.plan, nil
}
func (f *fakeRestores) Confirm(_ context.Context, planID, _ string) (*restoreuc.Confirmation, error) {
	if f.confirmErr != nil {
		return nil, f.confirmErr
	}
	return &restoreuc.Confirmation{Token: "tok-1", PlanID: planID, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (f *fakeRestores) Execute(_ context.Context, _, _ string) (*domain.RestoreJob, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	return f.job, nil
}
func (f *fakeRestores) Status(_ context.Context, _ string) (*domain.RestoreJob, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	return f.job, nil
}

// ---------- helpers ----------

// testRouter adapts the stdlib mux to the local Router interface the same
// way a chi router satisfies it in production.
type testRouter struct {
	mux *http.ServeMux
}

func (t *testRouter) Get(pattern string, h http.HandlerFunc) {
	t.mux.HandleFunc("GET "+pattern, h)
}

func (t *testRouter) Post(pattern string, h http.HandlerFunc) {
	t.mux.HandleFunc("POST "+pattern, h)
}

func testServer(snaps *fakeSnapshots, restores *fakeRestores) *httptest.Server {
	if snaps == nil {
		snaps = &fakeSnapshots{}
	}
	if restores == nil {
		restores = &fakeRestores{}
	}
	r := &testRouter{mux: http.NewServeMux()}
	Mount(r, Deps{Snapshots: snaps, Restores: restores})
	return httptest.NewServer(r.mux)
}

func decodeData(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	if resp.StatusCode >= 400 {
		t.Logf("error body: code=%s msg=%s", env.Error.Code, env.Error.Message)
		return
	}
	require.NoError(t, json.Unmarshal(env.Data, v))
}

func errorCode(t *testing.T, resp *http.Response) (int, string) {
	t.Helper()
	defer resp.Body.Close()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	return resp.StatusCode, env.Error.Code
}

// ---------- catalog ----------

func TestCatalog_LabelsEncryptionAndRestorable(t *testing.T) {
	snaps := &fakeSnapshots{}
	snaps.seed(&domain.Snapshot{ID: "enc", AgentID: "a", PolicyID: "p",
		TargetType: domain.TargetHostFilesystem, Status: domain.SnapshotVerified,
		ManifestID: "m1", Encrypted: true, KeyRef: "k1"})
	snaps.seed(&domain.Snapshot{ID: "plain", AgentID: "a", PolicyID: "p",
		TargetType: domain.TargetPostgres, Status: domain.SnapshotVerifying})
	srv := testServer(snaps, nil)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/catalog?policy_id=p&limit=10")
	require.NoError(t, err)
	var page CatalogPageDTO
	decodeData(t, resp, &page)
	require.Equal(t, 2, page.Total)
	byID := map[string]SnapshotDTO{}
	for _, it := range page.Items {
		byID[it.ID] = it
	}
	require.Equal(t, EncryptionLabelEncrypted, byID["enc"].Encryption)
	require.True(t, byID["enc"].Restorable)
	require.Equal(t, EncryptionLabelUnencrypted, byID["plain"].Encryption)
	require.False(t, byID["plain"].Restorable, "unverified snapshots must not be restorable")
	require.Equal(t, 10, snaps.lastOpts.Limit)
	require.Equal(t, "p", snaps.lastFilt.PolicyID)
}

func TestCatalog_RejectsBadFilters(t *testing.T) {
	srv := testServer(nil, nil)
	defer srv.Close()
	for _, url := range []string{
		"/api/catalog?target=nope",
		"/api/catalog?limit=0",
		"/api/catalog?limit=101",
		"/api/catalog?offset=-1",
		"/api/catalog?since=not-a-time",
		"/api/catalog?restorable_only=maybe",
	} {
		resp, err := http.Get(srv.URL + url)
		require.NoError(t, err)
		status, code := errorCode(t, resp)
		require.Equal(t, http.StatusBadRequest, status, url)
		require.Equal(t, "validation_error", code, url)
	}
}

func TestCatalog_DetailIncludesManifest(t *testing.T) {
	snaps := &fakeSnapshots{}
	snaps.seed(&domain.Snapshot{ID: "s-1", AgentID: "a", Status: domain.SnapshotVerified, ManifestID: "m-1"})
	require.NoError(t, snaps.PutManifest(context.Background(), &domain.SnapshotManifest{
		ID: "m-1", SnapshotID: "s-1", FormatVersion: 1, ObjectKey: "m", Checksum: "sha256:00",
	}))
	snaps.objects["m-1"] = []domain.SnapshotObject{
		{ID: "o-1", ObjectKey: "k", Kind: "chunk", Size: 5, Checksum: "sha256:00"},
	}
	srv := testServer(snaps, nil)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/catalog/snapshot?id=s-1")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var detail SnapshotDetailDTO
	decodeData(t, resp, &detail)
	require.Equal(t, "s-1", detail.Snapshot.ID)
	require.NotNil(t, detail.Manifest)
	require.Len(t, detail.Manifest.Objects, 1)

	resp, err = http.Get(srv.URL + "/api/catalog/manifest?id=s-1")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var manifest ManifestDTO
	decodeData(t, resp, &manifest)
	require.Equal(t, "m-1", manifest.ID)

	resp, err = http.Get(srv.URL + "/api/catalog/snapshot?id=missing")
	require.NoError(t, err)
	status, code := errorCode(t, resp)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "not_found", code)

	resp, err = http.Get(srv.URL + "/api/catalog/snapshot")
	require.NoError(t, err)
	status, _ = errorCode(t, resp)
	require.Equal(t, http.StatusBadRequest, status)
}

// ---------- restore flow ----------

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	return resp
}

func TestRestore_PlanValidatesAndMapsGating(t *testing.T) {
	restores := &fakeRestores{
		plan: &restoreuc.Plan{ID: "plan-1", Kind: restoreuc.KindFile, AgentID: "a",
			SnapshotIDs: []string{"s-1"}, Warnings: []string{"OVERWRITE"}},
	}
	srv := testServer(nil, restores)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/restore/plan", `{"kind":"file","snapshot_id":"s-1","requested_by":"op"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var plan PlanDTO
	decodeData(t, resp, &plan)
	require.Equal(t, "plan-1", plan.ID)
	require.Equal(t, "s-1", restores.lastPlan.SnapshotID)

	resp = postJSON(t, srv.URL+"/api/restore/plan", `{"kind":"file","snapshot_id":"s-1","target_agent_id":"agent-2","requested_by":"op"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, "agent-2", restores.lastPlan.TargetAgentID)

	for _, body := range []string{
		`{"kind":"nope","snapshot_id":"s-1","requested_by":"op"}`,
		`{"kind":"file","requested_by":"op"}`,
		`{"kind":"file","snapshot_id":"s-1"}`,
		`{"kind":"stack","snapshot_ids":["a","b"],"requested_by":"op"}`,
		`not json`,
	} {
		resp := postJSON(t, srv.URL+"/api/restore/plan", body)
		status, _ := errorCode(t, resp)
		require.Equal(t, http.StatusBadRequest, status, body)
	}

	restores.planErr = restoreuc.ErrNotRestorable
	resp = postJSON(t, srv.URL+"/api/restore/plan", `{"kind":"file","snapshot_id":"s-1","requested_by":"op"}`)
	status, code := errorCode(t, resp)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "conflict", code)
}

func TestRestore_ConfirmExecuteStatus(t *testing.T) {
	restores := &fakeRestores{
		job: &domain.RestoreJob{ID: "job-1", AgentID: "a", SnapshotID: "s-1", State: domain.JobQueued},
	}
	srv := testServer(nil, restores)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/restore/confirm", `{"plan_id":"plan-1","actor":"admin"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var conf ConfirmationDTO
	decodeData(t, resp, &conf)
	require.Equal(t, "tok-1", conf.Token)

	resp = postJSON(t, srv.URL+"/api/restore/confirm", `{"plan_id":"plan-1"}`)
	status, _ := errorCode(t, resp)
	require.Equal(t, http.StatusBadRequest, status)

	resp = postJSON(t, srv.URL+"/api/restore/execute", `{"token":"tok-1","actor":"admin"}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var job RestoreJobDTO
	decodeData(t, resp, &job)
	require.Equal(t, "job-1", job.ID)

	restores.execErr = restoreuc.ErrTokenReused
	resp = postJSON(t, srv.URL+"/api/restore/execute", `{"token":"tok-1","actor":"admin"}`)
	status, _ = errorCode(t, resp)
	require.Equal(t, http.StatusConflict, status)

	restores.execErr = restoreuc.ErrTokenExpired
	resp = postJSON(t, srv.URL+"/api/restore/execute", `{"token":"tok-1","actor":"admin"}`)
	status, code := errorCode(t, resp)
	require.Equal(t, http.StatusGone, status)
	require.Equal(t, "gone", code)

	restores.execErr = restoreuc.ErrTokenInvalid
	resp = postJSON(t, srv.URL+"/api/restore/execute", `{"token":"bogus","actor":"admin"}`)
	status, code = errorCode(t, resp)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "invalid_token", code)

	resp, err := http.Get(srv.URL + "/api/restore/status?id=job-1")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get(srv.URL + "/api/restore/status")
	require.NoError(t, err)
	status, _ = errorCode(t, resp)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMount_PanicsOnMissingDeps(t *testing.T) {
	require.Panics(t, func() { Mount(&testRouter{mux: http.NewServeMux()}, Deps{}) })
	require.Panics(t, func() { Mount(nil, Deps{Snapshots: &fakeSnapshots{}, Restores: &fakeRestores{}}) })
}
