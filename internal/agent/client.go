package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Manager API paths (agent protocol, see internal/adapter/http/agents).
// Upload-grant, multipart, and restore endpoints are implemented by the
// phase-3 server track against the same contract (see CONTRACT NOTES).
const (
	pathHeartbeat      = "/api/agent/heartbeat"
	pathPoll           = "/api/agent/poll"
	pathReport         = "/api/agent/report"
	pathUploadGrants   = "/api/agent/jobs/%s/upload-grants"
	pathMultipartInit  = "/api/agent/jobs/%s/multipart/init"
	pathMultipartDone  = "/api/agent/jobs/%s/multipart/complete"
	pathMultipartAbort = "/api/agent/jobs/%s/multipart/abort"
	pathRestoresPoll   = "/api/agent/restores/poll"
	pathDownloadGrants = "/api/agent/restores/%s/download-grants"
	pathRestoreReport  = "/api/agent/restores/%s/report"

	maxErrorBodyByte = 4 << 10
	defaultPollLimit = 5
)

// HeartbeatRequest carries agent liveness and capacity information.
// Unknown fields are ignored by the manager; identity comes from the
// bearer credential, never from the body.
type HeartbeatRequest struct {
	AgentID         string    `json:"agent_id"`
	Hostname        string    `json:"hostname"`
	Version         string    `json:"version"`
	Capabilities    []string  `json:"capabilities,omitempty"`
	DiskTotal       uint64    `json:"disk_total_bytes,omitempty"`
	DiskFree        uint64    `json:"disk_free_bytes,omitempty"`
	IPAddress       string    `json:"ip_address,omitempty"`
	RunningJobs     int       `json:"running_jobs"`
	DockerAvailable bool      `json:"docker_available"`
	At              time.Time `json:"at"`
}

// HeartbeatResponse is the Manager's acknowledgement.
type HeartbeatResponse struct {
	OK          bool      `json:"ok"`
	Status      string    `json:"status,omitempty"`
	Outdated    bool      `json:"outdated,omitempty"`
	JobsPending int       `json:"jobs_pending,omitempty"`
	ServerTime  time.Time `json:"server_time,omitempty"`
}

// ManagerClient is the agent's view of the Manager control plane.
type ManagerClient interface {
	Heartbeat(ctx context.Context, req HeartbeatRequest) (HeartbeatResponse, error)
	PollJobs(ctx context.Context, limit int) (PollResponse, error)
	ReportJob(ctx context.Context, rep JobReportPayload) error
	UploadGrants(ctx context.Context, jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error)
	MultipartInit(ctx context.Context, jobID string, req MultipartInitRequest) (MultipartInitResponse, error)
	MultipartComplete(ctx context.Context, jobID string, req MultipartCompleteRequest) error
	MultipartAbort(ctx context.Context, jobID string, req MultipartAbortRequest) error
	PollRestore(ctx context.Context, agentID string) (*RestoreJob, error)
	DownloadGrants(ctx context.Context, restoreID string, keys []string) (DownloadGrantsResponse, error)
	ReportRestore(ctx context.Context, restoreID string, req RestoreReportRequest) error
}

// HTTPManagerClient is an HTTPS polling client. Every call carries an
// explicit timeout; callers may additionally bound calls with ctx.
type HTTPManagerClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	timeout    time.Duration
}

// NewHTTPManagerClient builds a client for managerURL. The token is attached
// as a Bearer credential and never logged.
func NewHTTPManagerClient(managerURL, token string, timeout time.Duration) *HTTPManagerClient {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return &HTTPManagerClient{
		baseURL:    strings.TrimSuffix(managerURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
	}
}

func (c *HTTPManagerClient) Heartbeat(ctx context.Context, req HeartbeatRequest) (HeartbeatResponse, error) {
	var out HeartbeatResponse
	if err := c.do(ctx, http.MethodPost, c.baseURL+pathHeartbeat, req, &out); err != nil {
		return HeartbeatResponse{}, err
	}
	return out, nil
}

func (c *HTTPManagerClient) PollJobs(ctx context.Context, limit int) (PollResponse, error) {
	var out PollResponse
	if limit <= 0 {
		limit = defaultPollLimit
	}
	u := c.baseURL + pathPoll + "?limit=" + url.QueryEscape(fmt.Sprint(limit))
	if err := c.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return PollResponse{}, err
	}
	return out, nil
}

func (c *HTTPManagerClient) ReportJob(ctx context.Context, rep JobReportPayload) error {
	body := map[string]any{"job": rep}
	return c.do(ctx, http.MethodPost, c.baseURL+pathReport, body, nil)
}

func (c *HTTPManagerClient) UploadGrants(ctx context.Context, jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
	var out UploadGrantsResponse
	u := fmt.Sprintf(c.baseURL+pathUploadGrants, url.PathEscape(jobID))
	if err := c.do(ctx, http.MethodPost, u, req, &out); err != nil {
		return UploadGrantsResponse{}, err
	}
	return out, nil
}

func (c *HTTPManagerClient) MultipartInit(ctx context.Context, jobID string, req MultipartInitRequest) (MultipartInitResponse, error) {
	var out MultipartInitResponse
	u := fmt.Sprintf(c.baseURL+pathMultipartInit, url.PathEscape(jobID))
	if err := c.do(ctx, http.MethodPost, u, req, &out); err != nil {
		return MultipartInitResponse{}, err
	}
	return out, nil
}

func (c *HTTPManagerClient) MultipartComplete(ctx context.Context, jobID string, req MultipartCompleteRequest) error {
	u := fmt.Sprintf(c.baseURL+pathMultipartDone, url.PathEscape(jobID))
	return c.do(ctx, http.MethodPost, u, req, nil)
}

func (c *HTTPManagerClient) MultipartAbort(ctx context.Context, jobID string, req MultipartAbortRequest) error {
	u := fmt.Sprintf(c.baseURL+pathMultipartAbort, url.PathEscape(jobID))
	return c.do(ctx, http.MethodPost, u, req, nil)
}

func (c *HTTPManagerClient) PollRestore(ctx context.Context, agentID string) (*RestoreJob, error) {
	var out RestorePollResponse
	if err := c.do(ctx, http.MethodPost, c.baseURL+pathRestoresPoll, RestorePollRequest{AgentID: agentID}, &out); err != nil {
		return nil, err
	}
	return out.Restore, nil
}

func (c *HTTPManagerClient) DownloadGrants(ctx context.Context, restoreID string, keys []string) (DownloadGrantsResponse, error) {
	var out DownloadGrantsResponse
	u := fmt.Sprintf(c.baseURL+pathDownloadGrants, url.PathEscape(restoreID))
	if err := c.do(ctx, http.MethodPost, u, DownloadGrantsRequest{Keys: keys}, &out); err != nil {
		return DownloadGrantsResponse{}, err
	}
	return out, nil
}

func (c *HTTPManagerClient) ReportRestore(ctx context.Context, restoreID string, req RestoreReportRequest) error {
	u := fmt.Sprintf(c.baseURL+pathRestoreReport, url.PathEscape(restoreID))
	return c.do(ctx, http.MethodPost, u, req, nil)
}

func (c *HTTPManagerClient) do(ctx context.Context, method, url string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agent: encode request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("agent: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent: manager %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyByte))
		return fmt.Errorf("agent: manager %s %s: status %s: %s",
			method, url, resp.Status, strings.TrimSpace(string(detail)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("agent: decode manager response: %w", err)
	}
	return nil
}

// StubManagerClient is an in-memory ManagerClient for tests and offline runs.
type StubManagerClient struct {
	OnHeartbeat      func(req HeartbeatRequest) (HeartbeatResponse, error)
	OnPollJobs       func(limit int) (PollResponse, error)
	OnReportJob      func(rep JobReportPayload) error
	OnUploadGrants   func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error)
	OnMultipartInit  func(jobID string, req MultipartInitRequest) (MultipartInitResponse, error)
	OnMultipartDone  func(jobID string, req MultipartCompleteRequest) error
	OnMultipartAbort func(jobID string, req MultipartAbortRequest) error
	OnPollRestore    func(agentID string) (*RestoreJob, error)
	OnDownloadGrants func(restoreID string, keys []string) (DownloadGrantsResponse, error)
	OnReportRestore  func(restoreID string, req RestoreReportRequest) error

	Heartbeats []HeartbeatRequest
	Reports    []JobReportPayload
	Restores   []RestoreReportRequest
}

func (s *StubManagerClient) Heartbeat(_ context.Context, req HeartbeatRequest) (HeartbeatResponse, error) {
	s.Heartbeats = append(s.Heartbeats, req)
	if s.OnHeartbeat != nil {
		return s.OnHeartbeat(req)
	}
	return HeartbeatResponse{OK: true}, nil
}

func (s *StubManagerClient) PollJobs(_ context.Context, limit int) (PollResponse, error) {
	if s.OnPollJobs != nil {
		return s.OnPollJobs(limit)
	}
	return PollResponse{Jobs: []PolledJob{}, CancelledJobIDs: []string{}}, nil
}

func (s *StubManagerClient) ReportJob(_ context.Context, rep JobReportPayload) error {
	s.Reports = append(s.Reports, rep)
	if s.OnReportJob != nil {
		return s.OnReportJob(rep)
	}
	return nil
}

func (s *StubManagerClient) UploadGrants(_ context.Context, jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
	if s.OnUploadGrants != nil {
		return s.OnUploadGrants(jobID, req)
	}
	return UploadGrantsResponse{}, fmt.Errorf("agent: stub: no UploadGrants handler")
}

func (s *StubManagerClient) MultipartInit(_ context.Context, jobID string, req MultipartInitRequest) (MultipartInitResponse, error) {
	if s.OnMultipartInit != nil {
		return s.OnMultipartInit(jobID, req)
	}
	return MultipartInitResponse{}, fmt.Errorf("agent: stub: no MultipartInit handler")
}

func (s *StubManagerClient) MultipartComplete(_ context.Context, jobID string, req MultipartCompleteRequest) error {
	if s.OnMultipartDone != nil {
		return s.OnMultipartDone(jobID, req)
	}
	return fmt.Errorf("agent: stub: no MultipartComplete handler")
}

func (s *StubManagerClient) MultipartAbort(_ context.Context, jobID string, req MultipartAbortRequest) error {
	if s.OnMultipartAbort != nil {
		return s.OnMultipartAbort(jobID, req)
	}
	return fmt.Errorf("agent: stub: no MultipartAbort handler")
}

func (s *StubManagerClient) PollRestore(_ context.Context, agentID string) (*RestoreJob, error) {
	if s.OnPollRestore != nil {
		return s.OnPollRestore(agentID)
	}
	return nil, nil
}

func (s *StubManagerClient) DownloadGrants(_ context.Context, restoreID string, keys []string) (DownloadGrantsResponse, error) {
	if s.OnDownloadGrants != nil {
		return s.OnDownloadGrants(restoreID, keys)
	}
	return DownloadGrantsResponse{}, fmt.Errorf("agent: stub: no DownloadGrants handler")
}

func (s *StubManagerClient) ReportRestore(_ context.Context, restoreID string, req RestoreReportRequest) error {
	s.Restores = append(s.Restores, req)
	if s.OnReportRestore != nil {
		return s.OnReportRestore(restoreID, req)
	}
	return nil
}
