package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
	"github.com/vaultix/vaultix/internal/usecase/job"
)

type fakeService struct {
	jobs    map[string]*domain.BackupJob
	enqueue func(agentID, policyID string) (*domain.BackupJob, error)
	retry   func(jobID string) (*domain.BackupJob, error)
	cancel  func(jobID string) (*domain.BackupJob, error)

	lastListOpts   domain.ListOptions
	lastListFilter job.Filter
}

func (f *fakeService) EnqueueManual(_ context.Context, agentID, policyID, _ string) (*domain.BackupJob, error) {
	return f.enqueue(agentID, policyID)
}

func (f *fakeService) Get(_ context.Context, jobID string) (*domain.BackupJob, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("fake: job %s: %w", jobID, domain.ErrNotFound)
	}
	return j, nil
}

func (f *fakeService) List(_ context.Context, opts domain.ListOptions, filter job.Filter) (domain.ListResult[domain.BackupJob], error) {
	f.lastListOpts = opts
	f.lastListFilter = filter
	var items []domain.BackupJob
	for _, j := range f.jobs {
		if filter.AgentID != "" && j.AgentID != filter.AgentID {
			continue
		}
		if filter.State != "" && j.State != filter.State {
			continue
		}
		items = append(items, *j)
	}
	return domain.ListResult[domain.BackupJob]{Items: items, Total: len(items)}, nil
}

func (f *fakeService) Retry(_ context.Context, jobID, _ string) (*domain.BackupJob, error) {
	return f.retry(jobID)
}

func (f *fakeService) Cancel(_ context.Context, jobID string) (*domain.BackupJob, error) {
	return f.cancel(jobID)
}

func (f *fakeService) RetryHistory(_ context.Context, jobID string) ([]*domain.BackupJob, error) {
	j, ok := f.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("fake: job %s: %w", jobID, domain.ErrNotFound)
	}
	return []*domain.BackupJob{j}, nil
}

func testRouter(svc Service) *chi.Mux {
	r := chi.NewRouter()
	Mount(r, Deps{Service: svc})
	return r
}

func doRequest(t *testing.T, r *chi.Mux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func sampleJob() *domain.BackupJob {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return &domain.BackupJob{
		ID: "j1", AgentID: "a1", PolicyID: "p1", PolicyVersion: 3,
		Trigger: domain.JobTriggerManual, State: domain.JobRunning,
		Progress:        &[]float64{0.5}[0],
		BytesRead:       &[]uint64{100}[0],
		BytesCompressed: &[]uint64{30}[0],
		BytesUploaded:   &[]uint64{10}[0],
		Encrypted:       true, KeyRef: "key-1",
		StorageID: "st1", Attempt: 1, CreatedBy: "op-1",
		StartedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
}

func TestList(t *testing.T) {
	svc := &fakeService{jobs: map[string]*domain.BackupJob{"j1": sampleJob()}}
	r := testRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/api/jobs/?agent_id=a1&state=running&since=2026-04-01T00:00:00Z&until=2026-06-01T00:00:00Z&limit=10&offset=2", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "a1", svc.lastListFilter.AgentID)
	require.Equal(t, domain.JobRunning, svc.lastListFilter.State)
	require.NotNil(t, svc.lastListFilter.Since)
	require.NotNil(t, svc.lastListFilter.Until)
	require.Equal(t, 10, svc.lastListOpts.Limit)
	require.Equal(t, 2, svc.lastListOpts.Offset)

	var list ListDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Equal(t, 1, list.Total)
	require.Len(t, list.Items, 1)
	require.Equal(t, "j1", list.Items[0].ID)
	require.Equal(t, "running", list.Items[0].State)
	require.InEpsilon(t, 0.3, *list.Items[0].CompressionRatio, 1e-9)
}

func TestListBadQuery(t *testing.T) {
	svc := &fakeService{jobs: map[string]*domain.BackupJob{}}
	r := testRouter(svc)
	for _, path := range []string{
		"/api/jobs/?state=bogus",
		"/api/jobs/?since=not-a-time",
		"/api/jobs/?limit=-1",
		"/api/jobs/?offset=nope",
	} {
		rec := doRequest(t, r, http.MethodGet, path, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
		var errDTO ErrorDTO
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errDTO))
		require.Equal(t, "validation", errDTO.Error.Code)
	}
}

func TestDetail(t *testing.T) {
	svc := &fakeService{jobs: map[string]*domain.BackupJob{"j1": sampleJob()}}
	r := testRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/api/jobs/j1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var detail DetailDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	require.Equal(t, "j1", detail.ID)
	require.Equal(t, "a1", detail.AgentID)
	require.Equal(t, 3, detail.PolicyVersion)
	require.Equal(t, 0.5, *detail.Progress)
	require.Equal(t, uint64(100), *detail.BytesRead)
	require.True(t, detail.Encrypted)
	require.Equal(t, "key-1", detail.KeyRef)
	require.NotNil(t, detail.StartedAt)
	require.Len(t, detail.RetryHistory, 1)

	rec = doRequest(t, r, http.MethodGet, "/api/jobs/ghost", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreate(t *testing.T) {
	svc := &fakeService{
		enqueue: func(agentID, policyID string) (*domain.BackupJob, error) {
			if agentID == "off" {
				return nil, fmt.Errorf("agent disabled: %w", domain.ErrConflict)
			}
			return &domain.BackupJob{ID: "j9", AgentID: agentID, PolicyID: policyID, State: domain.JobQueued}, nil
		},
	}
	r := testRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/api/jobs/", `{"agent_id":"a1","policy_id":"p1"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var dto JobDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Equal(t, "j9", dto.ID)
	require.Equal(t, "queued", dto.State)

	rec = doRequest(t, r, http.MethodPost, "/api/jobs/", `{"agent_id":"off","policy_id":"p1"}`)
	require.Equal(t, http.StatusConflict, rec.Code)

	for _, body := range []string{
		`{"policy_id":"p1"}`,
		`{"agent_id":"a1"}`,
		`{}`,
		`not json`,
		`{"agent_id":"a1","policy_id":"p1","bogus":1}`,
	} {
		rec = doRequest(t, r, http.MethodPost, "/api/jobs/", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
}

func TestRetryAndCancel(t *testing.T) {
	newJob := func(id string, st domain.JobState) *domain.BackupJob {
		return &domain.BackupJob{ID: id, AgentID: "a1", PolicyID: "p1", State: st}
	}
	svc := &fakeService{
		retry: func(jobID string) (*domain.BackupJob, error) {
			if jobID == "j-ok" {
				return newJob("j-ok-2", domain.JobQueued), nil
			}
			return nil, fmt.Errorf("retry requires failed: %w", domain.ErrConflict)
		},
		cancel: func(jobID string) (*domain.BackupJob, error) {
			if jobID == "j-run" {
				return newJob("j-run", domain.JobCancelled), nil
			}
			return nil, fmt.Errorf("cancel requires queued or running: %w", domain.ErrConflict)
		},
	}
	r := testRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/api/jobs/j-ok/retry", "")
	require.Equal(t, http.StatusCreated, rec.Code)
	rec = doRequest(t, r, http.MethodPost, "/api/jobs/j-done/retry", "")
	require.Equal(t, http.StatusConflict, rec.Code)

	rec = doRequest(t, r, http.MethodPost, "/api/jobs/j-run/cancel", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var dto JobDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Equal(t, "cancelled", dto.State)
	rec = doRequest(t, r, http.MethodPost, "/api/jobs/j-done/cancel", "")
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestInternalErrorMapping(t *testing.T) {
	svc := &fakeService{
		retry: func(jobID string) (*domain.BackupJob, error) {
			return nil, fmt.Errorf("db exploded")
		},
	}
	r := testRouter(svc)
	rec := doRequest(t, r, http.MethodPost, "/api/jobs/j1/retry", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var errDTO ErrorDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errDTO))
	require.Equal(t, "internal", errDTO.Error.Code)
	require.NotContains(t, errDTO.Error.Message, "exploded")
}
